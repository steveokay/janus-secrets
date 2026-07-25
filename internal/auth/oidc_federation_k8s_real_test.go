package auth

import (
	"encoding/json"
	"testing"
)

// realDockerDesktopSAToken is the VERBATIM payload of a projected service-account
// token minted by a real Kubernetes cluster (Docker Desktop, k8s v1.36) with:
//
//	kubectl -n janus-test create token api --audience=janus --duration=1h
//
// It is captured here as a fixture because the rest of the Kubernetes federation
// tests drive a mock IdP: a mock proves the code is self-consistent, but only a
// real token proves we modelled the claim shape the cluster actually emits —
// notably `aud` as a one-element ARRAY and service-account identity nested two
// levels deep under a key that itself contains dots ("kubernetes.io").
const realDockerDesktopSAToken = `{
  "aud": ["janus"],
  "exp": 1785007063,
  "iat": 1785003463,
  "iss": "https://kubernetes.default.svc.cluster.local",
  "jti": "1c3ba121-dfb3-4dde-89dc-6d12177477cd",
  "kubernetes.io": {
    "namespace": "janus-test",
    "serviceaccount": { "name": "api", "uid": "daef7c06-9d55-4ba1-a3ac-cec8b2649291" }
  },
  "nbf": 1785003463,
  "sub": "system:serviceaccount:janus-test:api"
}`

// TestFlattenRealKubernetesSAToken asserts the claim projection produces exactly
// the keys a Kubernetes binding is documented to pin, given a genuine cluster
// token rather than a hand-written approximation.
func TestFlattenRealKubernetesSAToken(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(realDockerDesktopSAToken), &raw); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}

	claims, err := flattenClaims(raw)
	if err != nil {
		t.Fatalf("flattenClaims rejected a real cluster token: %v", err)
	}

	for k, want := range map[string]string{
		"sub":                               "system:serviceaccount:janus-test:api",
		"iss":                               "https://kubernetes.default.svc.cluster.local",
		"kubernetes.io.namespace":           "janus-test",
		"kubernetes.io.serviceaccount.name": "api",
		"kubernetes.io.serviceaccount.uid":  "daef7c06-9d55-4ba1-a3ac-cec8b2649291",
		// Kubernetes emits `aud` as a one-element array; a binding must still be
		// able to pin it, otherwise an audience-scoped token can't be constrained.
		"aud": "janus",
	} {
		if got := claims[k]; got != want {
			t.Errorf("claim %q = %q, want %q", k, got, want)
		}
	}

	// Numeric claims must NOT be coerced to strings: a binding pinning
	// `exp: "1785007063"` must not be satisfiable by the number.
	for _, numeric := range []string{"exp", "iat", "nbf"} {
		if v, ok := claims[numeric]; ok {
			t.Errorf("numeric claim %q was projected as %q — type coercion reintroduced", numeric, v)
		}
	}
}

// TestRealKubernetesTokenSatisfiesPresetBinding proves the end-to-end matching
// rule against the real token: a binding pinning namespace + service-account
// name is accepted, a binding pinning only `aud` is rejected as too weak, and a
// binding for a DIFFERENT service account does not match.
func TestRealKubernetesTokenSatisfiesPresetBinding(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(realDockerDesktopSAToken), &raw); err != nil {
		t.Fatal(err)
	}
	claims, err := flattenClaims(raw)
	if err != nil {
		t.Fatal(err)
	}

	const iss = "https://kubernetes.default.svc.cluster.local"

	strong := map[string]string{
		"kubernetes.io.namespace":           "janus-test",
		"kubernetes.io.serviceaccount.name": "api",
	}
	if !bindingHasStrongClaim(iss, PresetKubernetes, strong) {
		t.Error("namespace + service-account name should satisfy the kubernetes preset")
	}
	if !claimsSatisfy(claims, strong) {
		t.Error("the real token should match a binding pinning its own namespace + SA name")
	}

	// `aud` alone does not identify a workload — any SA in any namespace holding
	// a token for this audience would match.
	if bindingHasStrongClaim(iss, PresetKubernetes, map[string]string{"aud": "janus"}) {
		t.Error("a binding pinning only aud must be rejected as too weak")
	}

	// A binding for a different service account must not match.
	if claimsSatisfy(claims, map[string]string{
		"kubernetes.io.namespace":           "janus-test",
		"kubernetes.io.serviceaccount.name": "other",
	}) {
		t.Error("a token for service account 'api' matched a binding for 'other'")
	}
	// Same SA name in a different namespace must not match either.
	if claimsSatisfy(claims, map[string]string{
		"kubernetes.io.namespace":           "prod",
		"kubernetes.io.serviceaccount.name": "api",
	}) {
		t.Error("a token from namespace 'janus-test' matched a binding for 'prod'")
	}

	// `sub` alone is also a valid strong claim for the preset.
	if !bindingHasStrongClaim(iss, PresetKubernetes, map[string]string{"sub": claims["sub"]}) {
		t.Error("a binding pinning the full sub should satisfy the kubernetes preset")
	}
}
