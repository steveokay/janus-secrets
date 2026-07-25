package auth

import (
	"encoding/json"
	"testing"
)

// FuzzStringClaims feeds arbitrary JSON claim sets (the payload of a CI OIDC
// token, which the CI provider controls) through the projection that federation
// bindings match on. Non-string claims must be dropped rather than coerced: if a
// numeric or boolean claim could become a string, a binding pinning
// `repository_id: "42"` could be satisfied by the number 42, weakening an exact
// claim match into a type-confused one.
func FuzzStringClaims(f *testing.F) {
	for _, seed := range []string{
		`{"sub":"repo:acme/app:ref:refs/heads/main","repository":"acme/app"}`,
		`{"iat":1700000000,"exp":1700003600,"repository":"acme/app"}`,
		`{"repository_id":42}`, `{"repository_id":"42"}`,
		`{"nested":{"a":"b"}}`, `{"arr":["a","b"]}`, `{"null":null}`,
		`{"bool":true}`, `{"":""}`, `{}`, `[]`, `null`, `"str"`, `not json`,
		"{\"ws\":\" \n\t\"}", `{"dup":"1","dup":"2"}`, "{\"nul\":\"a\u0000b\"}",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload string) {
		var raw map[string]any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil || raw == nil {
			return // not a JSON object: never reaches stringClaims
		}

		got := stringClaims(raw)

		// Every projected entry must correspond to a genuinely string-valued
		// claim with the identical value — no coercion, no invention.
		for k, v := range got {
			rv, ok := raw[k]
			if !ok {
				t.Fatalf("stringClaims invented key %q (payload %q)", k, payload)
			}
			s, isString := rv.(string)
			if !isString {
				t.Fatalf("stringClaims kept non-string claim %q (%T) — type confusion risk", k, rv)
			}
			if s != v {
				t.Fatalf("stringClaims altered %q: %q → %q", k, s, v)
			}
		}
		// Conversely, every string claim must survive: silently dropping one
		// could make a binding's required claim look absent.
		for k, rv := range raw {
			if s, isString := rv.(string); isString {
				if got[k] != s {
					t.Fatalf("stringClaims dropped string claim %q (%q)", k, s)
				}
			}
		}
	})
}

// FuzzClaimsSatisfyFailClosed pins the matching rule that decides whether a CI
// token is allowed to mint a Janus service token. Two properties matter:
// an EMPTY requirement set must never match (a binding that pins nothing must
// not accept every token), and a match must require every wanted claim to be
// present and exactly equal.
func FuzzClaimsSatisfyFailClosed(f *testing.F) {
	f.Add("repository", "acme/app", "acme/app", true)
	f.Add("repository", "acme/app", "evil/app", true)
	f.Add("sub", "", "", true)
	f.Add("repository", "acme/app", "acme/app", false)
	f.Add("", "", "", false)

	f.Fuzz(func(t *testing.T, key, wantVal, tokenVal string, present bool) {
		tokenClaims := map[string]string{}
		if present {
			tokenClaims[key] = tokenVal
		}

		// An empty requirement set must always fail closed.
		if claimsSatisfy(tokenClaims, map[string]string{}) {
			t.Fatal("claimsSatisfy accepted an EMPTY requirement set — a binding pinning nothing would match every token")
		}
		if claimsSatisfy(tokenClaims, nil) {
			t.Fatal("claimsSatisfy accepted a nil requirement set")
		}

		want := map[string]string{key: wantVal}
		ok := claimsSatisfy(tokenClaims, want)

		// A positive result is only legitimate when the claim is present and equal.
		if ok {
			if !present {
				t.Fatalf("matched requirement %q=%q against a token missing that claim", key, wantVal)
			}
			if tokenVal != wantVal {
				t.Fatalf("matched %q: token %q != required %q", key, tokenVal, wantVal)
			}
		}
		// And when it is present and equal, it must match (no false negatives).
		if present && tokenVal == wantVal && !ok {
			t.Fatalf("failed to match an exactly-equal claim %q=%q", key, wantVal)
		}
	})
}
