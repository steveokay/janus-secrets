package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFederationKubernetesRequiredClaim asserts the Kubernetes preset forces a
// binding to pin service-account identity: the exact `sub`, or the namespace AND
// the service-account name from the flattened kubernetes.io object. A binding
// that pins only `aud` — which every workload in the cluster can request — is
// rejected, in the same spirit as GitHub's `repository` rule.
func TestFederationKubernetesRequiredClaim(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, scopeID := mkScope(t)

	const clusterIssuer = "https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE"
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: clusterIssuer, Audience: "janus", Preset: PresetKubernetes, Enabled: true,
	}); err != nil {
		t.Fatalf("trust cluster issuer: %v", err)
	}

	cases := []struct {
		name    string
		claims  map[string]string
		wantErr bool
	}{
		{"exact sub accepted", map[string]string{
			"sub": "system:serviceaccount:prod:api"}, false},
		{"namespace + service account name accepted", map[string]string{
			"kubernetes.io.namespace":           "prod",
			"kubernetes.io.serviceaccount.name": "api"}, false},
		{"namespace alone rejected", map[string]string{
			"kubernetes.io.namespace": "prod"}, true},
		{"service account name alone rejected", map[string]string{
			"kubernetes.io.serviceaccount.name": "api"}, true},
		{"audience alone rejected", map[string]string{"aud": "janus"}, true},
		{"unrelated claim rejected", map[string]string{"iss": clusterIssuer}, true},
		{"pod name is not identity enough", map[string]string{
			"kubernetes.io.pod.name": "api-7c9"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
				Name: "k8s-" + tc.name, Issuer: clusterIssuer, MatchClaims: tc.claims,
				ScopeKind: "config", ScopeID: scopeID, Access: "read",
				TTLSeconds: 900, Enabled: true,
			})
			if tc.wantErr {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("want ErrValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if b.Issuer != clusterIssuer {
				t.Fatalf("binding issuer = %q, want %q", b.Issuer, clusterIssuer)
			}
		})
	}
}

// TestFederationBindingIssuerResolution covers which issuer a new binding is
// pinned to: explicit issuers must be trusted, an omitted issuer only resolves
// while exactly one issuer is trusted, and an omitted issuer is a validation
// error once several are (guessing is how a binding lands on the wrong anchor).
func TestFederationBindingIssuerResolution(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, scopeID := mkScope(t)

	mk := func(name, issuer string, claims map[string]string) (*FederationBindingView, error) {
		return svc.CreateFederationBinding(ctx, FederationBindingInput{
			Name: name, Issuer: issuer, MatchClaims: claims, ScopeKind: "config",
			ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
		})
	}

	// No issuer configured at all → historical GitHub Actions default.
	b, err := mk("legacy-default", "", map[string]string{"repository": "org/app"})
	if err != nil || b.Issuer != issuerGitHubActions {
		t.Fatalf("default issuer: %v %+v", err, b)
	}

	// Exactly one trusted issuer → an omitted issuer resolves to it.
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: issuerGitLabCom, Audience: "janus", Preset: PresetGitLab, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	b, err = mk("sole-issuer", "", map[string]string{"project_path": "acme/app"})
	if err != nil || b.Issuer != issuerGitLabCom {
		t.Fatalf("sole issuer: %v %+v", err, b)
	}

	// A second trusted issuer → the binding must name one.
	const clusterIssuer = "https://kubernetes.default.svc"
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: clusterIssuer, Audience: "janus", Preset: PresetKubernetes, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mk("ambiguous", "", map[string]string{"project_path": "acme/app"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("omitted issuer with several trusted: want ErrValidation, got %v", err)
	}
	// An issuer that is not trusted cannot be pinned.
	if _, err := mk("untrusted", "https://evil.example", map[string]string{"sub": "x"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("untrusted issuer: want ErrValidation, got %v", err)
	}
	// Naming a trusted issuer applies THAT issuer's required-claim rule.
	if _, err := mk("k8s-needs-identity", clusterIssuer, map[string]string{"aud": "janus"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("k8s rule not applied: %v", err)
	}
	b, err = mk("k8s-ok", clusterIssuer, map[string]string{
		"kubernetes.io.namespace": "prod", "kubernetes.io.serviceaccount.name": "api"})
	if err != nil || b.Issuer != clusterIssuer {
		t.Fatalf("k8s binding: %v %+v", err, b)
	}
	// Trailing-slash forms address the same trusted issuer.
	if _, err := mk("k8s-slash", clusterIssuer+"/", map[string]string{
		"kubernetes.io.namespace": "staging", "kubernetes.io.serviceaccount.name": "api"}); err != nil {
		t.Fatalf("trailing-slash issuer: %v", err)
	}

	// The legacy single-issuer endpoint refuses to silently drop the other
	// trusted issuers.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: issuerGitHubActions, Audience: "janus", Enabled: true,
	}); !errors.Is(err, ErrFederationIssuerConflict) {
		t.Fatalf("legacy PUT with several issuers: want conflict, got %v", err)
	}
}

// TestFederateMultiIssuer drives real exchanges against two independent mock
// issuers. The security property under test: a token signed by issuer A can
// never satisfy a binding pinned to issuer B, however well its claims line up.
func TestFederateMultiIssuer(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	ci := newMockIdP(t, "janus")      // stands in for a CI provider
	cluster := newMockIdP(t, "janus") // stands in for a Kubernetes cluster
	rogue := newMockIdP(t, "janus")   // never trusted
	_, configID := mkScope(t)

	for _, in := range []FederationIssuerInput{
		{Issuer: ci.srv.URL, Audience: "janus", Preset: PresetCustom, Enabled: true},
		{Issuer: cluster.srv.URL, Audience: "janus", Preset: PresetKubernetes, Enabled: true},
	} {
		if _, err := svc.PutFederationIssuer(ctx, in); err != nil {
			t.Fatalf("trust %s: %v", in.Issuer, err)
		}
	}
	if list, err := svc.ListFederationIssuers(ctx); err != nil || len(list) != 2 {
		t.Fatalf("issuers: %v len=%d", err, len(list))
	}

	// Identical match_claims on both issuers: only the issuer tells them apart.
	shared := map[string]string{"sub": "system:serviceaccount:prod:api"}
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "ci-binding", Issuer: ci.srv.URL, MatchClaims: shared,
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "k8s-binding", Issuer: cluster.srv.URL, MatchClaims: shared,
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	k8sClaims := func(iss string) map[string]any {
		return map[string]any{
			"iss": iss, "aud": []string{"janus"}, "sub": "system:serviceaccount:prod:api",
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"kubernetes.io": map[string]any{
				"namespace":      "prod",
				"serviceaccount": map[string]any{"name": "api", "uid": "sa-uid"},
				"pod":            map[string]any{"name": "api-7c9", "uid": "pod-uid"},
			},
		}
	}

	// A cluster-signed token matches the cluster binding — not the CI one, even
	// though both bindings pin exactly the same claims.
	res, err := svc.FederateCILogin(ctx, cluster.signClaims(t, k8sClaims(cluster.srv.URL)))
	if err != nil {
		t.Fatalf("cluster exchange: %v", err)
	}
	if res.Binding != "k8s-binding" {
		t.Fatalf("matched %q, want k8s-binding", res.Binding)
	}
	if _, scope, err := svc.VerifyServiceToken(ctx, res.Token); err != nil || scope.ID != configID {
		t.Fatalf("verify minted: %v %+v", err, scope)
	}

	// The CI issuer signing the very same claim set reaches only its own
	// binding — cross-issuer matching would have made this ambiguous instead.
	res, err = svc.FederateCILogin(ctx, ci.signClaims(t, k8sClaims(ci.srv.URL)))
	if err != nil {
		t.Fatalf("ci exchange: %v", err)
	}
	if res.Binding != "ci-binding" {
		t.Fatalf("matched %q, want ci-binding", res.Binding)
	}

	// A token from a trusted issuer that CLAIMS to come from the other one is
	// rejected: the verifier is picked by `iss` and validates the signature
	// against that issuer's JWKS.
	forged := cluster.signClaims(t, k8sClaims(ci.srv.URL))
	if _, err := svc.FederateCILogin(ctx, forged); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("cross-signed token: want ErrFederationVerify, got %v", err)
	}

	// An issuer that is not trusted at all.
	if _, err := svc.FederateCILogin(ctx, rogue.signClaims(t, k8sClaims(rogue.srv.URL))); !errors.Is(err, ErrFederationIssuerUntrusted) {
		t.Fatalf("untrusted issuer: want ErrFederationIssuerUntrusted, got %v", err)
	}

	// Audience is enforced per issuer: a projected SA token minted for another
	// audience never federates, even with a perfect claim match.
	wrongAud := k8sClaims(cluster.srv.URL)
	wrongAud["aud"] = []string{"vault"}
	if _, err := svc.FederateCILogin(ctx, cluster.signClaims(t, wrongAud)); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("wrong audience: want ErrFederationVerify, got %v", err)
	}

	// Ambiguous claim projection (a literal dotted key shadowing a nested path)
	// fails closed rather than matching on a guessed winner.
	ambiguous := k8sClaims(cluster.srv.URL)
	ambiguous["kubernetes.io.namespace"] = "attacker"
	if _, err := svc.FederateCILogin(ctx, cluster.signClaims(t, ambiguous)); !errors.Is(err, ErrFederationClaims) {
		t.Fatalf("ambiguous claims: want ErrFederationClaims, got %v", err)
	}

	// Disabling an issuer stops exchanges for it without touching the other.
	if _, err := svc.PutFederationIssuer(ctx, FederationIssuerInput{
		Issuer: cluster.srv.URL, Audience: "janus", Preset: PresetKubernetes, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FederateCILogin(ctx, cluster.signClaims(t, k8sClaims(cluster.srv.URL))); !errors.Is(err, ErrFederationIssuerUntrusted) {
		t.Fatalf("disabled issuer: want ErrFederationIssuerUntrusted, got %v", err)
	}
	if _, err := svc.FederateCILogin(ctx, ci.signClaims(t, k8sClaims(ci.srv.URL))); err != nil {
		t.Fatalf("other issuer still live: %v", err)
	}

	// Removing an issuer leaves its bindings inert.
	list, err := svc.ListFederationIssuers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range list {
		if iss.Issuer == cluster.srv.URL {
			if err := svc.DeleteFederationIssuer(ctx, iss.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := svc.FederateCILogin(ctx, cluster.signClaims(t, k8sClaims(cluster.srv.URL))); !errors.Is(err, ErrFederationIssuerUntrusted) {
		t.Fatalf("deleted issuer: want ErrFederationIssuerUntrusted, got %v", err)
	}
}

// TestFederationNoIssuersConfigured keeps the "nothing configured" signal
// distinct from "issuer not trusted".
func TestFederationNoIssuersConfigured(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdP(t, "janus")

	tok := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := svc.FederateCILogin(ctx, tok); !errors.Is(err, ErrFederationNotConfigured) {
		t.Fatalf("want ErrFederationNotConfigured, got %v", err)
	}
	// A token that isn't even a JWT is rejected before any store lookup.
	if _, err := svc.FederateCILogin(ctx, "not-a-jwt"); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("malformed token: want ErrFederationVerify, got %v", err)
	}
}
