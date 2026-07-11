package dynamic

import "errors"

var (
	ErrNotFound      = errors.New("dynamic: role or lease not found")
	ErrExists        = errors.New("dynamic: role already exists for this config/name")
	ErrSealed        = errors.New("dynamic: server is sealed")
	ErrInvalidConfig = errors.New("dynamic: invalid role config")
	ErrNotRenewable  = errors.New("dynamic: lease is not active")
	ErrApplyFailed   = errors.New("dynamic: postgres statement failed")
)
