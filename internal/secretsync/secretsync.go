// Package secretsync is Janus's outbound sync engine: scheduled one-way
// replication of a config's resolved secrets to external stores (GitHub
// Actions secrets, Kubernetes Secrets).
package secretsync

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	failureThreshold = 5  // consecutive failures → status='failed'
	defaultBatch     = 50 // targets claimed per tick
)

// keyring is the subset of *crypto.Keyring the engine needs (fakeable in tests).
type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	SyncFingerprint(data []byte) []byte
	Sealed() bool
}

// Service is the sync engine.
type Service struct {
	kr       keyring
	repo     *store.SyncTargetRepo
	projects *store.ProjectRepo
	secrets  *secrets.Service
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	hc       *http.Client
	now      func() time.Time // injectable clock (tests)
	tickHook func()           // optional; called at the top of each RunDue (metrics/health)

	githubBaseURL string // GitHub API base; overridden in tests to point at a fake
}

// SetTickHook installs a callback invoked at the top of every RunDue pass. Used
// to stamp the shared scheduler "last tick" time for metrics + /v1/sys/status.
// nil is a no-op.
func (s *Service) SetTickHook(h func()) { s.tickHook = h }

// New wires the engine.
func New(kr *crypto.Keyring, st *store.Store, sec *secrets.Service, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, repo: store.NewSyncTargetRepo(st), projects: store.NewProjectRepo(st),
		secrets: sec, audit: aud, logger: logger, st: st,
		hc:            &http.Client{Timeout: 20 * time.Second},
		now:           time.Now,
		githubBaseURL: "https://api.github.com",
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// sealCreds encrypts a Creds blob under proj's KEK, bound to targetID.
func (s *Service) sealCreds(proj *store.Project, targetID string, c Creds) (ct, nonce, wrapped []byte, kekVer int, err error) {
	return s.sealBlob(proj, crypto.SyncCredsAAD(targetID), mustJSON(c))
}

// openCreds decrypts the stored Creds blob for target t.
func (s *Service) openCreds(proj *store.Project, t *store.SyncTarget) (Creds, error) {
	pt, err := s.openBlob(proj, crypto.SyncCredsAAD(t.ID), t.CredsWrappedDEK, t.CredsNonce, t.CredsCT)
	if err != nil {
		return Creds{}, err
	}
	defer zero(pt)
	var c Creds
	if err := json.Unmarshal(pt, &c); err != nil {
		return Creds{}, ErrInvalidConfig
	}
	return c, nil
}

// sealBlob mints a DEK under proj's KEK and GCM-encrypts plaintext with aad.
func (s *Service) sealBlob(proj *store.Project, aad, plaintext []byte) (ct, nonce, wrapped []byte, kekVer int, err error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(kek)
	dek, wrappedDEK, err := s.kr.NewDEK(kek, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(dek)
	c, err := crypto.Encrypt(dek, plaintext, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	// Wipe the freshly-allocated plaintext blob (JSON creds) now that it's
	// encrypted; the ciphertext carries it from here.
	zero(plaintext)
	return c.Data, c.Nonce, wrappedDEK.Marshal(), proj.KEKVersion, nil
}

// openBlob unwraps the DEK and GCM-decrypts the stored ciphertext.
func (s *Service) openBlob(proj *store.Project, aad, wrappedDEK, nonce, ct []byte) ([]byte, error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, err
	}
	defer zero(kek)
	dekCT, err := crypto.ParseCiphertext(wrappedDEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return crypto.Decrypt(dek, crypto.Ciphertext{Nonce: nonce, Data: ct}, aad)
}

func (s *Service) unwrapProjectKEK(proj *store.Project) ([]byte, error) {
	if s.kr.Sealed() {
		return nil, ErrSealed
	}
	ct, err := crypto.ParseCiphertext(proj.WrappedKEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return s.kr.UnwrapProjectKEK(ct, proj.ID)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// mapStoreErr translates store sentinels to sync sentinels.
func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return ErrExists
	default:
		return err
	}
}
