package rotation

import "errors"

var (
	ErrNotFound      = errors.New("rotation: policy not found")
	ErrExists        = errors.New("rotation: policy already exists for this config/key")
	ErrSealed        = errors.New("rotation: server is sealed")
	ErrInvalidType   = errors.New("rotation: unknown rotator type")
	ErrInvalidConfig = errors.New("rotation: invalid rotator config")
	ErrApplyFailed   = errors.New("rotation: rotator apply failed")
)
