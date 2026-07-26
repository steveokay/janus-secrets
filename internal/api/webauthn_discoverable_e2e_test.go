package api

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Passwordless (client-side discoverable) passkey login over the real HTTP
// surface, with the software authenticator from webauthn_e2e_test.go.
//
// The identified flow takes the account from the CHALLENGE. A discoverable
// ceremony cannot: at begin there is no account, so identity comes from the
// assertion. What is proved HERE is the wiring — routes, status codes, cookie,
// audit, and the headline substitution rejection. The exhaustive behavioural
// matrix (user-verification flags, origin/RP-ID, counter regression, challenge
// pool crossing, disabled accounts) lives in internal/auth, which runs against
// the service directly and is not competing with the per-IP passkey rate
// limiter that guards these routes.

// waHandle derives the 16-byte WebAuthn user handle from an account UUID —
// exactly what auth.userHandle does, restated here so the test does not simply
// mirror the implementation's own helper.
func waHandle(t *testing.T, userID string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(userID, "-", ""))
	if err != nil || len(b) != 16 {
		t.Fatalf("bad user id %q: %v", userID, err)
	}
	return b
}

// waWhoami returns the user id and email behind a session cookie.
func waWhoami(t *testing.T, ts *httptest.Server, cookie string) (id, email string) {
	t.Helper()
	var me struct{ ID, Name string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", &me); code != 200 {
		t.Fatalf("me: %d", code)
	}
	return me.ID, me.Name
}

// waDiscoverableBegin starts a passwordless ceremony and returns the challenge
// plus the raw options, which must carry no account information at all.
func waDiscoverableBegin(t *testing.T, ts *httptest.Server, body string) (string, map[string]any) {
	t.Helper()
	var opts map[string]any
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/discoverable/begin", body, &opts); code != 200 {
		t.Fatalf("discoverable/begin: %d", code)
	}
	challenge, _ := opts["challenge"].(string)
	if challenge == "" {
		t.Fatalf("no challenge in discoverable request options: %v", opts)
	}
	return challenge, opts
}

// waDiscoverableFinish posts an assertion and returns status, raw body, and the
// session cookie if one was set.
func waDiscoverableFinish(t *testing.T, ts *httptest.Server, assertion string) (int, string, string) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/v1/auth/webauthn/login/discoverable/finish",
		"application/json", strings.NewReader(assertion))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	cookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}
	return resp.StatusCode, string(raw), cookie
}

// TestWebAuthnDiscoverableE2E is the passwordless story over HTTP, in one test
// because each e2e test boots its own Postgres container — and deliberately
// frugal with ceremony calls, which are per-IP rate limited in production.
func TestWebAuthnDiscoverableE2E(t *testing.T) {
	ts, adminEmail, adminPassword := webauthnStack(t, true)
	adminCookie := login(t, ts.URL, adminEmail, adminPassword)
	adminID, _ := waWhoami(t, ts, adminCookie)

	// A second account with its OWN passkey — the other half of the credential
	// substitution test.
	var created struct{ ID, Email, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", adminCookie, "",
		`{"email":"passwordless-b@corp.io"}`, &created); code != 200 {
		t.Fatalf("create second user: %d", code)
	}
	bobCookie := login(t, ts.URL, "passwordless-b@corp.io", created.Password)
	bobID, _ := waWhoami(t, ts, bobCookie)
	if adminID == bobID {
		t.Fatal("the two accounts collapsed into one")
	}

	// Alice's client reports credProps.rk=true; Bob's client reports nothing.
	yes := true
	alice, aliceCred := registerPasskeyRK(t, ts, adminCookie, "Alice key", &yes)
	_, _ = registerPasskeyRK(t, ts, bobCookie, "Bob key", nil)

	aliceHandle := waHandle(t, adminID)
	bobHandle := waHandle(t, bobID)

	// ── the begin endpoint carries no account information at all ────────────
	challenge, opts := waDiscoverableBegin(t, ts, `{"conditional":true}`)
	// A discoverable ceremony must NOT name credentials: doing so would both
	// break the flow and turn the endpoint into an enumeration oracle.
	if ac, ok := opts["allowCredentials"]; ok {
		if list, isList := ac.([]any); !isList || len(list) != 0 {
			t.Fatalf("discoverable options carry allowCredentials: %v", ac)
		}
	}
	// User verification stays REQUIRED — a passkey login is single-step, so the
	// credential must itself be two factors.
	if uv, _ := opts["userVerification"].(string); uv != "required" {
		t.Fatalf("userVerification = %q, want \"required\"", uv)
	}
	if rp, _ := opts["rpId"].(string); rp != waRPID {
		t.Fatalf("rpId = %q, want %q", rp, waRPID)
	}

	// ── THE substitution test ───────────────────────────────────────────────
	// Alice's credential, presented with Bob's user handle. If the server
	// resolved identity from the handle alone, this would sign somebody in.
	codeSub, bodySub, cookieSub := waDiscoverableFinish(t, ts,
		alice.assertionWith(t, challenge, waOrigin, waAssertOpts{handle: bobHandle}))
	if codeSub != 401 {
		t.Fatalf("credential of A with user handle of B returned %d, want 401\n%s", codeSub, bodySub)
	}
	if cookieSub != "" {
		t.Fatal("a substituted user handle minted a session cookie")
	}

	// ── every rejection looks the same (no enumeration) ─────────────────────
	// An entirely unknown credential must be indistinguishable from a real one
	// presented with the wrong handle.
	unknown := newTestAuthenticator(t)
	challenge, _ = waDiscoverableBegin(t, ts, `{}`)
	codeUnknown, bodyUnknown, _ := waDiscoverableFinish(t, ts,
		unknown.assertionWith(t, challenge, waOrigin, waAssertOpts{handle: aliceHandle}))
	if codeUnknown != codeSub || bodyUnknown != bodySub {
		t.Fatalf("unknown vs known-bad differ — this is an enumeration oracle:\n"+
			"  unknown: %d %s\n  known:   %d %s", codeUnknown, bodyUnknown, codeSub, bodySub)
	}

	// ── the happy path ──────────────────────────────────────────────────────
	challenge, _ = waDiscoverableBegin(t, ts, `{}`)
	assertion := alice.assertionWith(t, challenge, waOrigin, waAssertOpts{handle: aliceHandle})
	code, body, cookie := waDiscoverableFinish(t, ts, assertion)
	if code != 200 {
		t.Fatalf("discoverable/finish: %d\n%s", code, body)
	}
	if cookie == "" {
		t.Fatal("no session cookie was set")
	}
	// The cookie really is Alice's session — and no email was ever supplied.
	gotID, gotEmail := waWhoami(t, ts, cookie)
	if gotID != adminID || gotEmail != adminEmail {
		t.Fatalf("passwordless session resolved to %s/%s, want %s/%s", gotID, gotEmail, adminID, adminEmail)
	}

	// A replayed assertion is refused: the challenge is consumed, and the
	// signature counter has already moved past it.
	codeReplay, _, replayCookie := waDiscoverableFinish(t, ts, assertion)
	if codeReplay != 401 || replayCookie != "" {
		t.Fatalf("replayed assertion returned %d (cookie %q), want 401", codeReplay, replayCookie)
	}

	// ── audit: recorded, and value-free ─────────────────────────────────────
	auditCode, auditBody := rawGet(t, ts.URL+"/v1/audit/events?limit=200", cookie)
	if auditCode != 200 {
		t.Fatalf("audit events: %d %s", auditCode, auditBody)
	}
	if !strings.Contains(auditBody, "webauthn.login") {
		t.Fatalf("no webauthn.login event in the ledger:\n%s", auditBody)
	}
	// The ledger distinguishes the two ceremonies, so an operator can see that a
	// sign-in happened with no address supplied.
	if !strings.Contains(auditBody, "ceremony=discoverable") {
		t.Fatalf("the passwordless ceremony is not identifiable in the ledger:\n%s", auditBody)
	}
	// Nothing derived from a private key may appear anywhere in the trail.
	if strings.Contains(auditBody, waB64(alice.key.D.Bytes())) {
		t.Fatal("audit trail leaked private key material")
	}

	// ── discoverability is surfaced, never guessed ──────────────────────────
	var list waList
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(list.Credentials) != 1 {
		t.Fatalf("expected exactly one credential, got %+v", list.Credentials)
	}
	// Alice's client reported credProps.rk=true AND she has now signed in
	// passwordlessly — either alone is enough.
	if c := list.Credentials[0]; c.ID != aliceCred.ID || c.Discoverable == nil || !*c.Discoverable {
		t.Fatalf("alice's credential = %+v, want discoverable=true", c)
	}
	// Bob's client reported nothing and he has never signed in passwordlessly,
	// so his credential is UNKNOWN — never silently reported as either.
	var bobList waList
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/webauthn", bobCookie, "", "", &bobList); code != 200 {
		t.Fatalf("bob list: %d", code)
	}
	if len(bobList.Credentials) != 1 || bobList.Credentials[0].Discoverable != nil {
		t.Fatalf("bob's credential = %+v, want discoverable=null (unknown)", bobList.Credentials)
	}
	// The JSON carries an explicit null, so the UI can tell "unknown" apart from
	// "this build of the server does not report it".
	_, raw := rawGet(t, ts.URL+"/v1/auth/webauthn", bobCookie)
	if !strings.Contains(raw, `"discoverable":null`) {
		t.Fatalf("unknown discoverability is not an explicit null: %s", raw)
	}
}

// With passkeys unconfigured the passwordless routes refuse rather than
// half-working, exactly like the identified ones. A garbage or absent body on
// begin is also not an error: there is no user input on that route beyond an
// optional mediation hint.
func TestWebAuthnDiscoverableConfigAndBody(t *testing.T) {
	ts, _, _ := webauthnStack(t, false)

	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/discoverable/begin", `{}`, &env); code != 409 {
		t.Fatalf("discoverable/begin while disabled: %d", code)
	}
	if env.Error.Code != "webauthn_not_configured" {
		t.Fatalf("code = %q", env.Error.Code)
	}
	env = errEnvelope{}
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/discoverable/finish", `{}`, &env); code != 409 {
		t.Fatalf("discoverable/finish while disabled: %d", code)
	}
	if env.Error.Code != "webauthn_not_configured" {
		t.Fatalf("finish code = %q", env.Error.Code)
	}
}

// Begin tolerates any body shape: absent, empty, a mediation hint, or junk.
func TestWebAuthnDiscoverableBeginTolerantBody(t *testing.T) {
	ts, _, _ := webauthnStack(t, true)
	seen := map[string]bool{}
	for _, body := range []string{"", "{}", `{"conditional":true}`, "not json at all"} {
		var opts map[string]any
		if code := doJSON(t, "POST", ts.URL+"/v1/auth/webauthn/login/discoverable/begin", body, &opts); code != 200 {
			t.Fatalf("begin with body %q: %d", body, code)
		}
		c, _ := opts["challenge"].(string)
		if c == "" {
			b, _ := json.Marshal(opts)
			t.Fatalf("no challenge for body %q: %s", body, b)
		}
		if seen[c] {
			t.Fatalf("a challenge was reused across ceremonies (body %q)", body)
		}
		seen[c] = true
	}
}
