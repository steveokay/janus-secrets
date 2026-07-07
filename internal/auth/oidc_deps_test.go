package auth

import (
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// TestOIDCDepsImportable guards that the OIDC libraries resolve and link.
func TestOIDCDepsImportable(t *testing.T) {
	_ = oidc.Config{}
	_ = oauth2.Config{}
}
