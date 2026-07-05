package transit

import (
	"errors"
	"regexp"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// KeyType values.
const (
	TypeAES     = "aes256-gcm"
	TypeEd25519 = "ed25519"
)

// keyring is the subset of *crypto.Keyring the engine needs (fakeable in tests).
type keyring interface {
	WrapTransitKey(material []byte, name string, version int) (crypto.Ciphertext, error)
	UnwrapTransitKey(ct crypto.Ciphertext, name string, version int) ([]byte, error)
	Sealed() bool
}

// Service is the transit engine.
type Service struct {
	kr   keyring
	repo *store.TransitRepo
	st   *store.Store // for NewID
}

// New wires the engine over a keyring and store.
func New(kr keyring, st *store.Store) *Service {
	return &Service{kr: kr, repo: store.NewTransitRepo(st), st: st}
}

var keyNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validKeyName(name string) bool { return keyNameRe.MatchString(name) }

// zeroize overwrites b with zeros to clear key material from memory.
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// mapStoreErr translates store sentinels to transit sentinels.
func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrKeyNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return ErrKeyExists
	default:
		return err
	}
}
