// Package secrets orchestrates internal/crypto and internal/store: it is the
// only component that holds an unsealed key or sees plaintext, and only
// transiently within a call. The store stays crypto-blind; crypto stays
// storage-blind.
package secrets

import (
	"runtime"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// DefaultUnusedSecretDays is the advisory "not read in N days" threshold used
// when JANUS_UNUSED_SECRET_DAYS is unset or non-positive.
const DefaultUnusedSecretDays = 90

// Service is the secrets façade over the store repositories and an injected,
// already-unsealed keyring.
type Service struct {
	st       *store.Store
	projects *store.ProjectRepo
	envs     *store.EnvironmentRepo
	configs  *store.ConfigRepo
	secrets  *store.SecretRepo
	kekVers  *store.ProjectKEKVersionRepo
	maxAge   *store.MaxAgeRepo
	annots   *store.AnnotationRepo
	lastRead *store.LastReadRepo
	readIns  *store.ReadInsightsRepo
	keyring  *crypto.Keyring

	// unusedDays is the advisory unused-secret threshold in days (a key with no
	// per-key reveal within this window is flagged "unused"). Server config, not
	// per-config state; defaults to DefaultUnusedSecretDays.
	unusedDays int
}

// NewService retains st and builds the repositories from it. kr must be an
// already-unsealed keyring (bootstrap/unseal is a later milestone).
func NewService(st *store.Store, kr *crypto.Keyring) *Service {
	return &Service{
		st:       st,
		projects: store.NewProjectRepo(st),
		envs:     store.NewEnvironmentRepo(st),
		configs:  store.NewConfigRepo(st),
		secrets:  store.NewSecretRepo(st),
		kekVers:  store.NewProjectKEKVersionRepo(st),
		maxAge:   store.NewMaxAgeRepo(st),
		annots:   store.NewAnnotationRepo(st),
		lastRead: store.NewLastReadRepo(st),
		readIns:  store.NewReadInsightsRepo(st),
		keyring:  kr,

		unusedDays: DefaultUnusedSecretDays,
	}
}

// SetUnusedSecretDays overrides the advisory unused-secret threshold (days). A
// non-positive value resets to DefaultUnusedSecretDays. Server config, applied
// at boot from JANUS_UNUSED_SECRET_DAYS.
func (s *Service) SetUnusedSecretDays(days int) {
	if days <= 0 {
		days = DefaultUnusedSecretDays
	}
	s.unusedDays = days
}

// UnusedSecretDays returns the effective advisory unused-secret threshold (days).
func (s *Service) UnusedSecretDays() int { return s.unusedDays }

// unusedWindow is the threshold as a duration.
func (s *Service) unusedWindow() time.Duration {
	return time.Duration(s.unusedDays) * 24 * time.Hour
}

// zeroize overwrites b with zeros. Best-effort defense-in-depth: Go's GC may
// have already copied the bytes; this narrows the exposure window, it does not
// guarantee erasure.
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}
