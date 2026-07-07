package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"runtime"
	"sync"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// transitKeys is the subset of *store.TransitRepo the token-minting path needs
// to validate a transit scope's optional key restriction (fakeable in tests).
// The restriction is keyed by the transit key's NAME, matching how enforcement
// compares scope.ID against Resource.TransitKey (the /{name} route param).
type transitKeys interface {
	GetByName(ctx context.Context, name string) (*store.TransitKey, error)
}

// Service is the identity layer: it verifies passwords, mints and verifies
// sessions and service tokens, and owns the master-key-wrapped HMAC key.
type Service struct {
	users    *store.UserRepo
	sessions *store.SessionRepo
	tokens   *store.ServiceTokenRepo
	authcfg  *store.AuthConfigRepo
	configs  *store.ConfigRepo
	envs     *store.EnvironmentRepo
	transit  transitKeys
	keyring  *crypto.Keyring

	oidcProviders  *store.OIDCProviderRepo
	oidcIdentities *store.OIDCIdentityRepo
	oidcAuthReqs   *store.OIDCAuthRequestRepo

	oidcMu    sync.Mutex
	oidcCache *oidcVerifier
}

// NewService builds the repositories from st. kr is the (possibly still
// sealed) keyring; operations needing the HMAC key surface crypto.ErrSealed
// until unsealed.
func NewService(st *store.Store, kr *crypto.Keyring) *Service {
	return &Service{
		users:    store.NewUserRepo(st),
		sessions: store.NewSessionRepo(st),
		tokens:   store.NewServiceTokenRepo(st),
		authcfg:  store.NewAuthConfigRepo(st),
		configs:  store.NewConfigRepo(st),
		envs:     store.NewEnvironmentRepo(st),
		transit:  store.NewTransitRepo(st),
		keyring:  kr,

		oidcProviders:  store.NewOIDCProviderRepo(st),
		oidcIdentities: store.NewOIDCIdentityRepo(st),
		oidcAuthReqs:   store.NewOIDCAuthRequestRepo(st),
	}
}

// zeroize overwrites b with zeros (best-effort, GC may have copied).
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// randToken returns n random bytes base64url-encoded (no padding).
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	zeroize(b)
	return s, nil
}

// hmacKey loads and unwraps the token-HMAC key. The caller must zeroize it.
// Returns crypto.ErrSealed while sealed; ErrNotFound before bootstrap.
func (s *Service) hmacKey(ctx context.Context) ([]byte, error) {
	wrapped, err := s.authcfg.GetWrappedHMACKey(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	ct, err := crypto.ParseCiphertext(wrapped)
	if err != nil {
		return nil, err
	}
	return s.keyring.UnwrapAuthKey(ct)
}

// mac computes HMAC-SHA256(key, data).
func mac(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// EnsureHMACKey generates, wraps, and stores the token-HMAC key if absent.
// Called at the first-unseal transition; concurrent racers converge on the
// first writer's key. Idempotent.
func (s *Service) EnsureHMACKey(ctx context.Context) error {
	if _, err := s.authcfg.GetWrappedHMACKey(ctx); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	defer zeroize(key)
	wrapped, err := s.keyring.WrapAuthKey(key)
	if err != nil {
		return err
	}
	return s.authcfg.PutWrappedHMACKeyIfAbsent(ctx, wrapped.Marshal())
}

// WrappedHMACKeyForTest exposes the stored wrapped key for integration tests.
func (s *Service) WrappedHMACKeyForTest(ctx context.Context) ([]byte, error) {
	return s.authcfg.GetWrappedHMACKey(ctx)
}

// CreateInitialAdmin creates the bootstrap admin with a generated one-time
// password (returned exactly once; only its Argon2id hash is stored). Returns
// (userID, password). Returns ErrValidation if any user exists. Called from the
// init ceremony only.
func (s *Service) CreateInitialAdmin(ctx context.Context, email string) (string, string, error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return "", "", err
	}
	if n > 0 {
		return "", "", ErrValidation
	}
	return s.createUser(ctx, email)
}

// CreateUser creates a user with a generated one-time password (returned once).
// Unlike CreateInitialAdmin it does not enforce the zero-users guard.
func (s *Service) CreateUser(ctx context.Context, email string) (string, string, error) {
	return s.createUser(ctx, email)
}

func (s *Service) createUser(ctx context.Context, email string) (string, string, error) {
	password, err := randToken(24) // 32 chars base64url
	if err != nil {
		return "", "", err
	}
	pw := []byte(password)
	hash, err := HashPassword(pw)
	zeroize(pw)
	if err != nil {
		return "", "", err
	}
	u, err := s.users.Create(ctx, email, &hash)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return "", "", ErrValidation
		}
		return "", "", err
	}
	return u.ID, password, nil
}

// UserInfo is a non-secret user summary.
type UserInfo struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
}

// ListUsers returns all users (no secrets).
func (s *Service) ListUsers(ctx context.Context) ([]UserInfo, error) {
	list, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserInfo, 0, len(list))
	for _, u := range list {
		out = append(out, UserInfo{ID: u.ID, Email: u.Email, Disabled: u.DisabledAt != nil})
	}
	return out, nil
}

// DisableUser marks a user disabled (sessions/logins stop working).
func (s *Service) DisableUser(ctx context.Context, id string) error {
	return s.users.SetDisabled(ctx, id, true)
}

// userByEmailForTest exposes a user id lookup for integration tests.
func (s *Service) userByEmailForTest(ctx context.Context, email string) (string, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}
