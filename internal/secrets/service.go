// Package secrets orchestrates internal/crypto and internal/store: it is the
// only component that holds an unsealed key or sees plaintext, and only
// transiently within a call. The store stays crypto-blind; crypto stays
// storage-blind.
package secrets

import (
	"runtime"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Service is the secrets façade over the store repositories and an injected,
// already-unsealed keyring.
type Service struct {
	st       *store.Store
	projects *store.ProjectRepo
	envs     *store.EnvironmentRepo
	configs  *store.ConfigRepo
	secrets  *store.SecretRepo
	keyring  *crypto.Keyring
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
		keyring:  kr,
	}
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
