package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/transit"
)

// writeServiceError maps internal/secrets and internal/store sentinels to the
// HTTP error envelope. Messages never carry internals, key material, or secret
// values.
func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secrets.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
	case errors.Is(err, secrets.ErrNotFound), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrParentNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, secrets.ErrConflict), errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, secrets.ErrValidation):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid input")
	case errors.Is(err, transit.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
	case errors.Is(err, transit.ErrKeyNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, transit.ErrKeyExists):
		writeError(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, transit.ErrDeletionNotAllowed):
		writeError(w, http.StatusConflict, "conflict", "deletion not allowed for this key")
	case errors.Is(err, transit.ErrWrongKeyType), errors.Is(err, transit.ErrVersionTooOld),
		errors.Is(err, transit.ErrBadCiphertext), errors.Is(err, transit.ErrValidation):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid input")
	case errors.Is(err, resolve.ErrForbiddenReference):
		writeError(w, http.StatusForbidden, CodeForbidden, "forbidden reference")
	case errors.Is(err, resolve.ErrInheritanceCycle), errors.Is(err, resolve.ErrBrokenInheritance),
		errors.Is(err, resolve.ErrReferenceCycle):
		writeError(w, http.StatusConflict, "conflict", "unresolvable configuration")
	case errors.Is(err, resolve.ErrUnresolvedReference), errors.Is(err, resolve.ErrReferenceDepth):
		writeError(w, http.StatusUnprocessableEntity, "unresolved_reference", "unresolved reference")
	case errors.Is(err, resolve.ErrBadReferenceSyntax):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid reference syntax")
	default:
		// ErrDecrypt (integrity) and any unexpected error: generic 500, no leak.
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
