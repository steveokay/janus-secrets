package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Claim projection for federated machine-identity tokens.
//
// Bindings match on exact string equality, so the verified JWT payload is
// projected to a flat map[string]string first. Two properties are load-bearing:
//
//  1. No coercion. Numbers, booleans, arrays and null are DROPPED, never
//     stringified: if the number 42 became "42", a binding pinning
//     `repository_id: "42"` could be satisfied by a type-confused token.
//  2. No shadowing. Kubernetes puts service-account identity in a nested object
//     ({"kubernetes.io":{"serviceaccount":{"name":"api"}}}), so nested objects
//     are flattened to dotted paths ("kubernetes.io.serviceaccount.name").
//     Literal claim keys may themselves contain dots (CircleCI emits
//     "oidc.circleci.com/project-id"), so a flattened path can in principle
//     collide with a literal key — {"a.b":"x"} vs {"a":{"b":"y"}}. Rather than
//     pick a winner (which an attacker who controls one side could exploit to
//     shadow the other), ANY collision rejects the whole token.

const (
	// maxClaimDepth bounds nested-object recursion. Real machine-identity
	// tokens nest 2–3 levels; anything deeper is rejected rather than walked.
	maxClaimDepth = 6
	// maxFlatClaims bounds the projected claim count (a hostile issuer could
	// otherwise send a huge object). Rejected, not truncated: silently dropping
	// claims could make a binding's required claim look absent.
	maxFlatClaims = 512
	// maxJWTPayloadB64 bounds the base64 payload we decode purely to read the
	// unverified `iss` for verifier routing.
	maxJWTPayloadB64 = 16 << 10
)

// flattenClaims projects a verified claim set to the flat, dotted-path map that
// bindings match on. Top-level string claims keep their literal keys; nested
// objects contribute dotted paths. Returns ErrFederationClaims when any dotted
// path could be produced by more than one construction, when nesting exceeds
// maxClaimDepth, or when the projection exceeds maxFlatClaims entries.
func flattenClaims(raw map[string]any) (map[string]string, error) {
	out := stringClaims(raw)
	// Seed the path set with EVERY literal top-level key (not only the string
	// ones): a nested path colliding with a dropped non-string literal key is
	// still ambiguous, and rejecting is the fail-closed answer.
	seen := make(map[string]struct{}, len(raw))
	for k := range raw {
		seen[k] = struct{}{}
	}
	for k, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if err := flattenInto(out, seen, m, k, 1); err != nil {
			return nil, err
		}
	}
	// `aud` is verified by the OIDC verifier itself, but a binding may also pin
	// it. Kubernetes projected tokens carry aud as a one-element array, so an
	// array of exactly one string is unwrapped (an unwrap, not a coercion).
	// Multi-valued aud stays dropped: which element would a binding mean?
	if v, ok := singleStringAudience(raw); ok {
		out["aud"] = v
	}
	return out, nil
}

// flattenInto walks one nested object, emitting prefix+"."+key paths.
func flattenInto(out map[string]string, seen map[string]struct{}, m map[string]any, prefix string, depth int) error {
	if depth > maxClaimDepth {
		return ErrFederationClaims
	}
	for k, v := range m {
		path := prefix + "." + k
		if _, dup := seen[path]; dup {
			// Another construction already owns this dotted path. Detection is
			// order-independent: every literal top-level key is seeded before
			// any walk, and two nested walks that meet on the same path make
			// whichever arrives second fail.
			return ErrFederationClaims
		}
		seen[path] = struct{}{}
		switch t := v.(type) {
		case string:
			out[path] = t
		case map[string]any:
			if err := flattenInto(out, seen, t, path, depth+1); err != nil {
				return err
			}
		default:
			// numbers, booleans, arrays, null: dropped, never coerced.
		}
		if len(out) > maxFlatClaims {
			return ErrFederationClaims
		}
	}
	return nil
}

// singleStringAudience returns the sole audience when `aud` is an array holding
// exactly one string (the Kubernetes projected-token shape).
func singleStringAudience(raw map[string]any) (string, bool) {
	arr, ok := raw["aud"].([]any)
	if !ok || len(arr) != 1 {
		return "", false
	}
	s, ok := arr[0].(string)
	return s, ok
}

// stringClaims projects a raw claim set to its top-level string-valued entries
// (the only kind bindings match on). Non-string claims (iat/exp numbers, arrays,
// objects) are dropped rather than coerced.
func stringClaims(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// unverifiedIssuer reads the `iss` claim from an UNVERIFIED compact JWT payload.
// It is used only to route the token to the verifier for that issuer; that
// verifier then re-checks `iss` against its own configured value and validates
// the signature against that issuer's JWKS, so a forged `iss` can at most pick a
// verifier that will reject the token.
func unverifiedIssuer(rawJWT string) (string, error) {
	parts := strings.Split(rawJWT, ".")
	if len(parts) != 3 || len(parts[1]) == 0 || len(parts[1]) > maxJWTPayloadB64 {
		return "", ErrFederationVerify
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrFederationVerify
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ErrFederationVerify
	}
	if strings.TrimSpace(claims.Iss) == "" {
		return "", ErrFederationVerify
	}
	return claims.Iss, nil
}
