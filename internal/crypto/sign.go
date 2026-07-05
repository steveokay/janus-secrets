package crypto

import (
	"crypto/ed25519"
	"encoding/binary"
)

// GenerateEd25519Key returns a new (public, private) Ed25519 key pair. The private
// key is the 64-byte expanded form; the public key is 32 bytes. It draws from the
// package randReader seam (rand.Reader in production) so the failure path is testable.
func GenerateEd25519Key() (pub, priv []byte, err error) {
	pk, sk, err := ed25519.GenerateKey(randReader)
	if err != nil {
		return nil, nil, err
	}
	return pk, sk, nil
}

// Sign signs msg with an Ed25519 private key.
func Sign(priv, msg []byte) []byte {
	return ed25519.Sign(ed25519.PrivateKey(priv), msg)
}

// Verify reports whether sig is a valid Ed25519 signature of msg under pub. A
// malformed public key or signature returns false (never panics).
func Verify(pub, msg, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}

// WrapTransitKey wraps a transit key version's material under the master key,
// bound to (name, version). Refuses to run while sealed. Mirrors WrapProjectKEK;
// the master key never leaves the keyring.
func (k *Keyring) WrapTransitKey(material []byte, name string, version int) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return WrapKey(k.master, material, TransitKeyAAD(name, version))
}

// UnwrapTransitKey unwraps material previously wrapped by WrapTransitKey for the
// same (name, version); a mismatch fails the AEAD.
func (k *Keyring) UnwrapTransitKey(ct Ciphertext, name string, version int) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return UnwrapKey(k.master, ct, TransitKeyAAD(name, version))
}

// TransitKeyAAD binds a transit key version's wrapped material to its (name,
// version) so a version row copied elsewhere fails to unwrap. Domain-tagged and
// length-prefixed so (name,version) is injective.
func TransitKeyAAD(name string, version int) []byte {
	out := []byte("janus:transit:v1")
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(name)))
	out = append(out, n[:]...)
	out = append(out, name...)
	var v [8]byte
	binary.BigEndian.PutUint64(v[:], uint64(version)) // #nosec G115 -- version is a small positive int
	out = append(out, v[:]...)
	return out
}
