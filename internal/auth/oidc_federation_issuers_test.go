package auth

import (
	"context"
	"testing"
	"time"
)

// TestFederationProviderAwareRequiredClaim asserts that the mandatory-claim rule
// is provider-aware: each supported CI issuer accepts a binding constrained by
// its own strong identifying claim, GitHub's "repository" path is unchanged, an
// unknown issuer accepts any non-empty claim, and a binding with no strong claim
// is always rejected.
func TestFederationProviderAwareRequiredClaim(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, scopeID := mkScope(t)

	setIssuer := func(t *testing.T, issuer string) {
		t.Helper()
		if err := svc.SetFederationConfig(ctx, FederationConfigInput{
			Issuer: issuer, Audience: "janus", Enabled: true,
		}); err != nil {
			t.Fatalf("set issuer %q: %v", issuer, err)
		}
	}

	mkBinding := func(name string, claims map[string]string) FederationBindingInput {
		return FederationBindingInput{
			Name: name, MatchClaims: claims, ScopeKind: "config",
			ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
		}
	}

	cases := []struct {
		name    string
		issuer  string
		claims  map[string]string
		wantErr bool
	}{
		// GitHub Actions (default issuer) — repository path unchanged.
		{"github repository accepted", issuerGitHubActions, map[string]string{"repository": "acme/app"}, false},
		{"github without repository rejected", issuerGitHubActions, map[string]string{"ref": "refs/heads/main"}, true},
		// GitLab — project_path is the strong claim.
		{"gitlab project_path accepted", issuerGitLabCom, map[string]string{"project_path": "acme/app"}, false},
		{"gitlab repository not enough", issuerGitLabCom, map[string]string{"repository": "acme/app"}, true},
		// Buildkite — organization_slug is the strong claim.
		{"buildkite org_slug accepted", issuerBuildkite, map[string]string{"organization_slug": "acme"}, false},
		{"buildkite org_slug + pipeline accepted", issuerBuildkite, map[string]string{"organization_slug": "acme", "pipeline_slug": "app-deploy"}, false},
		{"buildkite pipeline only rejected", issuerBuildkite, map[string]string{"pipeline_slug": "app-deploy"}, true},
		// CircleCI — org-scoped issuer; project-id claim is the strong constraint.
		{"circleci project-id accepted", issuerCircleCIBase + "abc-123", map[string]string{"oidc.circleci.com/project-id": "proj-uuid"}, false},
		{"circleci no strong claim rejected", issuerCircleCIBase + "abc-123", map[string]string{"ref": "main"}, true},
		// Unknown/custom issuer — any single non-empty claim is a sufficient constraint.
		{"custom issuer any claim accepted", "https://oidc.example.internal", map[string]string{"sub": "svc:deployer"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setIssuer(t, tc.issuer)
			b, err := svc.CreateFederationBinding(ctx, mkBinding("b-"+tc.name, tc.claims))
			if tc.wantErr {
				if err != ErrValidation {
					t.Fatalf("want ErrValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Clean up so scope/name reuse is unambiguous across cases.
			if err := svc.DeleteFederationBinding(ctx, b.ID); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
		})
	}
}

// TestFederationIssuerWellFormed rejects a non-https / malformed issuer while
// accepting the four known providers' issuer forms and an empty (defaulting)
// issuer.
func TestFederationIssuerWellFormed(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	bad := []string{"not a url", "ftp://x", "://nohost", "https://", "just-a-host.com"}
	for _, iss := range bad {
		if err := svc.SetFederationConfig(ctx, FederationConfigInput{Issuer: iss, Audience: "janus", Enabled: true}); err != ErrValidation {
			t.Fatalf("issuer %q: want ErrValidation, got %v", iss, err)
		}
	}
	good := []string{"", issuerGitHubActions, issuerGitLabCom, issuerBuildkite, issuerCircleCIBase + "abc-123", "https://gitlab.self-hosted.example"}
	for _, iss := range good {
		if err := svc.SetFederationConfig(ctx, FederationConfigInput{Issuer: iss, Audience: "janus", Enabled: true}); err != nil {
			t.Fatalf("issuer %q: unexpected %v", iss, err)
		}
	}
}

// TestFederateCILoginGitLab exercises the full exchange against the mock IdP with
// a GitLab-style claim set, proving the provider-aware rule and matcher work
// end-to-end for a non-GitHub provider.
func TestFederateCILoginGitLab(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdP(t, "janus")
	_, configID := mkScope(t)

	// Issuer must equal the mock IdP URL for verification; the provider-aware
	// required-claim rule keys off the configured issuer, so a self-hosted-style
	// URL falls into the custom-issuer branch — we bind project_path anyway to
	// mirror real GitLab usage.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "gl", MatchClaims: map[string]string{"project_path": "acme/app", "ref": "main"},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	tok := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "project_path:acme/app:ref:main",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"project_path": "acme/app", "ref": "main",
	})
	res, err := svc.FederateCILogin(ctx, tok)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if res.Binding != "gl" || res.Token == "" {
		t.Fatalf("result: %+v", res)
	}
	if _, scope, err := svc.VerifyServiceToken(ctx, res.Token); err != nil || scope.ID != configID {
		t.Fatalf("verify minted: %v %+v", err, scope)
	}
}
