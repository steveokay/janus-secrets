package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
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
	default:
		// ErrDecrypt (integrity) and any unexpected error: generic 500, no leak.
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
