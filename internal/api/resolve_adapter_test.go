package api

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/resolve"
)

func TestWriteServiceErrorResolveSentinels(t *testing.T) {
	s := &Server{}
	cases := []struct {
		err  error
		code int
	}{
		{resolve.ErrForbiddenReference, 403},
		{resolve.ErrInheritanceCycle, 409},
		{resolve.ErrBrokenInheritance, 409},
		{resolve.ErrReferenceCycle, 409},
		{resolve.ErrUnresolvedReference, 422},
		{resolve.ErrReferenceDepth, 422},
		{resolve.ErrBadReferenceSyntax, 400},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.writeServiceError(rec, tc.err)
		if rec.Code != tc.code {
			t.Fatalf("%v → %d, want %d", tc.err, rec.Code, tc.code)
		}
	}
}

func TestWriteServiceErrorWrappedResolve(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.writeServiceError(rec, errors.New("x")) // default
	if rec.Code != 500 {
		t.Fatalf("default → %d", rec.Code)
	}
}
