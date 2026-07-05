package authz

import "errors"

// ErrForbidden is returned by Can when the principal lacks the permission.
var ErrForbidden = errors.New("authz: forbidden")
