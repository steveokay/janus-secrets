package api

import (
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

func TestWriteServiceError(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{secrets.ErrSealed, 503},
		{secrets.ErrNotFound, 404},
		{store.ErrNotFound, 404},
		{store.ErrParentNotFound, 404},
		{secrets.ErrConflict, 409},
		{store.ErrConflict, 409},
		{store.ErrAlreadyExists, 409},
		{secrets.ErrValidation, 400},
		{secrets.ErrDecrypt, 500},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		(&Server{}).writeServiceError(rec, c.err)
		if rec.Code != c.code {
			t.Errorf("%v -> %d, want %d", c.err, rec.Code, c.code)
		}
	}
}
