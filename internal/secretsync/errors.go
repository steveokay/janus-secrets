package secretsync

import "errors"

var (
	ErrNotFound      = errors.New("sync: target not found")
	ErrExists        = errors.New("sync: target already exists for this config/provider/destination")
	ErrSealed        = errors.New("sync: server is sealed")
	ErrInvalidType   = errors.New("sync: unknown provider")
	ErrInvalidConfig = errors.New("sync: invalid target configuration")
	ErrApplyFailed   = errors.New("sync: provider apply failed")
)
