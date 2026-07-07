package auth

import (
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

func TestMatchFederationBinding(t *testing.T) {
	mk := func(name string, claims map[string]string, enabled bool) store.OIDCFederationBinding {
		return store.OIDCFederationBinding{ID: name, Name: name, MatchClaims: claims, Enabled: enabled}
	}
	prod := mk("prod", map[string]string{"repository": "org/app", "environment": "prod"}, true)
	anyRef := mk("any", map[string]string{"repository": "org/app"}, true)
	disabled := mk("dis", map[string]string{"repository": "org/app"}, false)

	tok := map[string]string{"repository": "org/app", "environment": "prod", "ref": "refs/heads/main"}

	tests := []struct {
		name     string
		bindings []store.OIDCFederationBinding
		want     string
		wantErr  error
	}{
		{"single match", []store.OIDCFederationBinding{prod}, "prod", nil},
		{"extra token claims ignored", []store.OIDCFederationBinding{anyRef}, "any", nil},
		{"no match", []store.OIDCFederationBinding{mk("x", map[string]string{"repository": "org/other"}, true)}, "", ErrFederationNoMatch},
		{"ambiguous", []store.OIDCFederationBinding{prod, anyRef}, "", ErrFederationAmbiguous},
		{"disabled skipped", []store.OIDCFederationBinding{disabled}, "", ErrFederationNoMatch},
		{"empty claims never matches", []store.OIDCFederationBinding{mk("empty", map[string]string{}, true)}, "", ErrFederationNoMatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := matchFederationBinding(tok, tc.bindings)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || b.Name != tc.want {
				t.Fatalf("got (%v, %v), want %s", b, err, tc.want)
			}
		})
	}
}
