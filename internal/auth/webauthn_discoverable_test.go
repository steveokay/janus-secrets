package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
)

// Passwordless (client-side discoverable) login at the service layer.
//
// The identified flow resolves the account from the CHALLENGE. This one cannot
// — at begin there is no account — so identity comes from the assertion. These
// tests pin the safety of that inversion: the account is whatever OUR credential
// row says owns the presented credential id, and the claimed userHandle only
// ever gets to agree or be rejected.

// handleOf restates auth.userHandle independently, so a bug in the production
// helper cannot make these tests agree with themselves.
func handleOf(t *testing.T, userID string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(userID, "-", ""))
	if err != nil || len(b) != 16 {
		t.Fatalf("user id %q is not a UUID: %v", userID, err)
	}
	return b
}

// discoverableLogin runs a full begin→finish passwordless ceremony, letting the
// caller bend exactly one binding.
func discoverableLogin(t *testing.T, svc *Service, a *virtAuthenticator, mutate func(*virtOpts)) (cookie, userID, email string, err error) {
	t.Helper()
	ctx := context.Background()
	opts, bErr := svc.BeginWebAuthnDiscoverableLogin(ctx, false)
	if bErr != nil {
		t.Fatalf("begin discoverable login: %v", bErr)
	}
	o := virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	}
	if mutate != nil {
		mutate(&o)
	}
	body := a.assertionResponse(t, o)
	cookie, userID, email, _, err = svc.FinishWebAuthnDiscoverableLogin(ctx, bytes.NewReader(body))
	return cookie, userID, email, err
}

// TestWebAuthnDiscoverableIdentityBinding is the security core: the presented
// user handle must agree with the credential's stored owner, or nothing happens.
func TestWebAuthnDiscoverableIdentityBinding(t *testing.T) {
	svc, aliceID, aliceEmail, _ := newWebAuthnService(t)
	ctx := context.Background()

	bobID, _, err := svc.CreateUser(ctx, "bob-passwordless@example.com")
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	alice, _ := enrollPasskey(t, svc, aliceID, aliceEmail, "Alice key")
	bob, _ := enrollPasskey(t, svc, bobID, "bob-passwordless@example.com", "Bob key")

	aliceHandle := handleOf(t, aliceID)
	bobHandle := handleOf(t, bobID)

	tests := []struct {
		name string
		// who signs the assertion
		signer *virtAuthenticator
		mutate func(*virtOpts)
		wantOK bool
	}{
		{
			name:   "alice's credential with alice's handle signs in",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle },
			wantOK: true,
		},
		{
			// THE substitution attack. Resolving the user from the handle alone
			// would authenticate Bob using Alice's credential.
			name:   "alice's credential with bob's handle is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = bobHandle },
		},
		{
			name:   "bob's credential with alice's handle is rejected",
			signer: bob,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle },
		},
		{
			// Alice's credential id, but signed by Bob's private key: the
			// signature is checked against ALICE's stored public key.
			name:   "alice's credential id signed by bob's key is rejected",
			signer: bob,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle; o.rawID = alice.credID },
		},
		{
			name:   "an unregistered credential is rejected",
			signer: newVirtAuthenticator(t),
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle },
		},
		{
			name:   "a handle for no account at all is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = bytes.Repeat([]byte{0xAB}, 16) },
		},
		{
			name:   "a truncated handle is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle[:8] },
		},
		{
			name:   "an absent handle is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = nil },
		},
		{
			// User verification is REQUIRED on every Janus ceremony: a passkey
			// login is single-step, so the credential must be two factors itself.
			name:   "user presence without user verification is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle; o.flags = flagUserPresent },
		},
		{
			name:   "a foreign origin is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle; o.origin = "https://evil.example.com" },
		},
		{
			name:   "a foreign relying party is rejected",
			signer: alice,
			mutate: func(o *virtOpts) { o.userHandle = aliceHandle; o.rpID = "evil.example.com" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cookie, gotID, gotEmail, err := discoverableLogin(t, svc, tc.signer, tc.mutate)
			if !tc.wantOK {
				if err == nil {
					t.Fatalf("ceremony succeeded as %s (%s) — it must not", gotID, gotEmail)
				}
				if cookie != "" {
					t.Fatal("a rejected ceremony still minted a session cookie")
				}
				// Every rejection is the SAME error, so the endpoint is not an
				// oracle for which credentials or accounts exist.
				if !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("error = %v, want ErrInvalidCredentials", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ceremony failed: %v", err)
			}
			if cookie == "" {
				t.Fatal("no session cookie")
			}
			if gotID != aliceID || gotEmail != aliceEmail {
				t.Fatalf("signed in as %s/%s, want %s/%s", gotID, gotEmail, aliceID, aliceEmail)
			}
		})
	}
}

// A discoverable challenge is single-use, and the two login pools do not mix.
func TestWebAuthnDiscoverableChallengeHygiene(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, _ := enrollPasskey(t, svc, uid, email, "key")
	handle := handleOf(t, uid)

	// Replay: the same assertion twice.
	opts, err := svc.BeginWebAuthnDiscoverableLogin(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	body := a.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified, userHandle: handle,
	})
	if _, _, _, _, err := svc.FinishWebAuthnDiscoverableLogin(ctx, bytes.NewReader(body)); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if _, _, _, _, err := svc.FinishWebAuthnDiscoverableLogin(ctx, bytes.NewReader(body)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed assertion: %v, want ErrInvalidCredentials", err)
	}

	// A signature counter that fails to advance is fatal even on a fresh
	// challenge — that is a cloned authenticator, not a replay.
	stale := a.signCount
	_, _, _, err = discoverableLogin(t, svc, a, func(o *virtOpts) {
		o.userHandle = handle
		o.signCount = &stale
	})
	if !errors.Is(err, ErrWebAuthnCloned) {
		t.Fatalf("counter regression: %v, want ErrWebAuthnCloned", err)
	}

	// An IDENTIFIED challenge cannot be spent on the discoverable endpoint.
	idOpts, err := svc.BeginWebAuthnLogin(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	crossed := a.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, idOpts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified, userHandle: handle,
	})
	if _, _, _, _, err := svc.FinishWebAuthnDiscoverableLogin(ctx, bytes.NewReader(crossed)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("identified challenge spent as discoverable: %v", err)
	}

	// ...and the reverse: a discoverable challenge on the identified endpoint.
	discOpts, err := svc.BeginWebAuthnDiscoverableLogin(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	crossed = a.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, discOpts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified, userHandle: handle,
	})
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(crossed)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("discoverable challenge spent as identified: %v", err)
	}
}

// A disabled account cannot sign in passwordlessly, and the refusal is the same
// invalid-credentials answer as everything else.
func TestWebAuthnDiscoverableRefusesDisabledAccount(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, _ := enrollPasskey(t, svc, uid, email, "key")
	handle := handleOf(t, uid)

	if _, _, _, err := discoverableLogin(t, svc, a, func(o *virtOpts) { o.userHandle = handle }); err != nil {
		t.Fatalf("baseline sign-in: %v", err)
	}
	if err := svc.DisableUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := discoverableLogin(t, svc, a, func(o *virtOpts) { o.userHandle = handle })
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled account: %v, want ErrInvalidCredentials", err)
	}
}

// The begin endpoint is identical for every caller: no email is taken, so
// nothing about it can vary by account. Only the challenge differs.
func TestWebAuthnDiscoverableBeginIsAccountFree(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	_, _ = enrollPasskey(t, svc, uid, email, "key")

	seen := map[string]bool{}
	var shape string
	for i := 0; i < 4; i++ {
		raw, err := svc.BeginWebAuthnDiscoverableLogin(ctx, i%2 == 0)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Challenge        string `json:"challenge"`
			RPID             string `json:"rpId"`
			UserVerification string `json:"userVerification"`
			Allow            []any  `json:"allowCredentials"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Allow) != 0 {
			t.Fatalf("discoverable options named %d credentials — they must name none", len(v.Allow))
		}
		if v.RPID != testRPID {
			t.Fatalf("rpId = %q", v.RPID)
		}
		if v.UserVerification != string(protocol.VerificationRequired) {
			t.Fatalf("userVerification = %q, want %q", v.UserVerification, protocol.VerificationRequired)
		}
		if seen[v.Challenge] {
			t.Fatal("a challenge was reused across ceremonies")
		}
		seen[v.Challenge] = true
		// Everything except the challenge must be byte-identical between calls,
		// including between conditional and non-conditional mediation.
		normalized := strings.Replace(string(raw), v.Challenge, "<challenge>", 1)
		if shape == "" {
			shape = normalized
		} else if normalized != shape {
			t.Fatalf("begin responses differ beyond the challenge:\n  %s\n  %s", shape, normalized)
		}
	}
}

// Passkeys unconfigured: the passwordless endpoints refuse rather than
// half-working.
func TestWebAuthnDiscoverableRequiresConfiguration(t *testing.T) {
	svc, _, _, _ := newWebAuthnService(t)
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.BeginWebAuthnDiscoverableLogin(ctx, false); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Fatalf("begin: %v", err)
	}
	if _, _, _, _, err := svc.FinishWebAuthnDiscoverableLogin(ctx, strings.NewReader("{}")); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Fatalf("finish: %v", err)
	}
}

// credProps is a client-supplied hint. It must round-trip faithfully, and an
// absent or malformed one must read as UNKNOWN (nil) rather than false — the UI
// has to be able to say "we don't know".
func TestCredPropsResidentKey(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		ext  protocol.AuthenticationExtensionsClientOutputs
		want *bool
	}{
		{"absent extension", protocol.AuthenticationExtensionsClientOutputs{}, nil},
		{"nil map", nil, nil},
		{"other extensions only", protocol.AuthenticationExtensionsClientOutputs{"appid": true}, nil},
		{"rk true", protocol.AuthenticationExtensionsClientOutputs{"credProps": map[string]any{"rk": true}}, &yes},
		{"rk false", protocol.AuthenticationExtensionsClientOutputs{"credProps": map[string]any{"rk": false}}, &no},
		{"credProps not an object", protocol.AuthenticationExtensionsClientOutputs{"credProps": true}, nil},
		{"credProps without rk", protocol.AuthenticationExtensionsClientOutputs{"credProps": map[string]any{}}, nil},
		{"rk not a bool", protocol.AuthenticationExtensionsClientOutputs{"credProps": map[string]any{"rk": "true"}}, nil},
		{"rk null", protocol.AuthenticationExtensionsClientOutputs{"credProps": map[string]any{"rk": nil}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := credPropsResidentKey(tc.ext)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %v, want nil (unknown)", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("got nil, want %v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

// A successful passwordless assertion is proof the credential is discoverable,
// and is recorded so a passkey enrolled before Janus asked stops reading
// "unknown".
func TestWebAuthnDiscoverableRecordsDiscoverability(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, info := enrollPasskey(t, svc, uid, email, "key")
	// The test authenticator reports no credProps, so discoverability starts out
	// unknown — exactly like a credential enrolled before this feature.
	if info.Discoverable != nil {
		t.Fatalf("fresh credential discoverable = %v, want nil", *info.Discoverable)
	}
	if _, _, _, err := discoverableLogin(t, svc, a, func(o *virtOpts) { o.userHandle = handleOf(t, uid) }); err != nil {
		t.Fatalf("passwordless sign-in: %v", err)
	}
	list, err := svc.ListWebAuthnCredentials(ctx, uid)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (%d)", err, len(list))
	}
	if list[0].Discoverable == nil || !*list[0].Discoverable {
		t.Fatalf("discoverability was not learned from a successful passwordless login: %+v", list[0])
	}
}

// Enrollment REQUIRES a discoverable credential, and says so in the options the
// browser receives — otherwise a user could end up with a passkey that silently
// cannot do passwordless sign-in.
func TestWebAuthnRegistrationDemandsResidentKey(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	raw, err := svc.BeginWebAuthnRegistration(context.Background(), uid, email)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AuthenticatorSelection struct {
			ResidentKey        string `json:"residentKey"`
			RequireResidentKey *bool  `json:"requireResidentKey"`
			UserVerification   string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Extensions map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.AuthenticatorSelection.ResidentKey != string(protocol.ResidentKeyRequirementRequired) {
		t.Fatalf("residentKey = %q, want %q", v.AuthenticatorSelection.ResidentKey, protocol.ResidentKeyRequirementRequired)
	}
	// The L1 spelling must agree with the L3 one, or an older client would be
	// told something different from a newer one.
	if v.AuthenticatorSelection.RequireResidentKey == nil || !*v.AuthenticatorSelection.RequireResidentKey {
		t.Fatalf("requireResidentKey = %v, want true", v.AuthenticatorSelection.RequireResidentKey)
	}
	if v.AuthenticatorSelection.UserVerification != string(protocol.VerificationRequired) {
		t.Fatalf("userVerification = %q", v.AuthenticatorSelection.UserVerification)
	}
	if rk, _ := v.Extensions["credProps"].(bool); !rk {
		t.Fatalf("credProps was not requested: %v", v.Extensions)
	}
}
