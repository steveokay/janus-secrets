package transit

import "errors"

var (
	ErrKeyNotFound        = errors.New("transit: key not found")
	ErrKeyExists          = errors.New("transit: key already exists")
	ErrWrongKeyType       = errors.New("transit: operation not valid for key type")
	ErrVersionTooOld      = errors.New("transit: ciphertext version below min_decryption_version")
	ErrBadCiphertext      = errors.New("transit: malformed or unverifiable ciphertext")
	ErrDeletionNotAllowed = errors.New("transit: deletion not allowed for this key")
	ErrValidation         = errors.New("transit: invalid input")
	ErrSealed             = errors.New("transit: server is sealed")
)
