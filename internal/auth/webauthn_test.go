package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	testRPID   = "localhost"
	testOrigin = "http://localhost:5173"
)

// newWebAuthnService returns a service with passkeys configured plus the
// bootstrap admin's id and email.
func newWebAuthnService(t *testing.T) (svc *Service, userID, email, password string) {
	t.Helper()
	svc, email, password = newTestService(t)
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{
		RPID: testRPID, RPDisplayName: "Janus Test", Origins: []string{testOrigin},
	}); err != nil {
		t.Fatalf("configure webauthn: %v", err)
	}
	uid, err := svc.userByEmailForTest(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return svc, uid, email, password
}

// enrollPasskey runs a full begin→finish registration with a fresh virtual
// authenticator and returns it plus the stored credential summary.
func enrollPasskey(t *testing.T, svc *Service, uid, email, nickname string) (*virtAuthenticator, *WebAuthnCredentialInfo) {
	t.Helper()
	ctx := context.Background()
	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	a := newVirtAuthenticator(t)
	body := a.attestationResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	info, err := svc.FinishWebAuthnRegistration(ctx, uid, email, nickname, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("finish registration: %v", err)
	}
	return a, info
}

// assertLogin drives a begin→finish assertion and returns the result.
func assertLogin(t *testing.T, svc *Service, a *virtAuthenticator, email string, mutate func(*virtOpts)) (string, error) {
	t.Helper()
	ctx := context.Background()
	opts, err := svc.BeginWebAuthnLogin(ctx, email)
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	o := virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	}
	if mutate != nil {
		mutate(&o)
	}
	body := a.assertionResponse(t, o)
	cookie, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(body))
	return cookie, err
}

func TestWebAuthnDisabledRejectsEverything(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	if err := svc.SetWebAuthnConfig(WebAuthnConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.BeginWebAuthnRegistration(ctx, uid, email); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("begin registration: got %v", err)
	}
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "x", strings.NewReader("{}")); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("finish registration: got %v", err)
	}
	if _, err := svc.BeginWebAuthnLogin(ctx, email); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("begin login: got %v", err)
	}
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, strings.NewReader("{}")); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("finish login: got %v", err)
	}
	if _, err := svc.ListWebAuthnCredentials(ctx, uid); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("list: got %v", err)
	}
	if err := svc.RenameWebAuthnCredential(ctx, uid, uid, "x"); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("rename: got %v", err)
	}
	if _, err := svc.DeleteWebAuthnCredential(ctx, uid, uid); !errors.Is(err, ErrWebAuthnNotConfigured) {
		t.Errorf("delete: got %v", err)
	}
}

func TestWebAuthnRegisterThenLogin(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	a, info := enrollPasskey(t, svc, uid, email, "Work laptop")
	if info.Nickname != "Work laptop" {
		t.Fatalf("nickname = %q", info.Nickname)
	}
	if info.CredentialID == "" {
		t.Fatal("credential id not reported")
	}
	if info.LastUsedAt != nil {
		t.Fatal("a fresh credential must have no last-used stamp")
	}

	cookie, err := assertLogin(t, svc, a, email, nil)
	if err != nil {
		t.Fatalf("passkey login: %v", err)
	}
	p, err := svc.VerifySession(ctx, cookie)
	if err != nil {
		t.Fatalf("minted session does not verify: %v", err)
	}
	if p.ID != uid || p.Kind != KindUser {
		t.Fatalf("session resolved to %+v, want user %s", p, uid)
	}

	creds, err := svc.ListWebAuthnCredentials(ctx, uid)
	if err != nil || len(creds) != 1 {
		t.Fatalf("list: %v (%d creds)", err, len(creds))
	}
	if creds[0].LastUsedAt == nil {
		t.Fatal("a successful assertion must stamp last_used_at")
	}
}

// A challenge is good for exactly one finish; the second attempt must fail even
// though the response is otherwise perfectly valid.
func TestWebAuthnChallengeIsSingleUse(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatal(err)
	}
	a := newVirtAuthenticator(t)
	body := a.attestationResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "first", bytes.NewReader(body)); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "replay", bytes.NewReader(body)); !errors.Is(err, ErrWebAuthnVerification) {
		t.Fatalf("replayed registration challenge accepted (err=%v)", err)
	}

	// Same rule on the login side.
	a2, _ := enrollPasskey(t, svc, uid, email, "second")
	loginOpts, err := svc.BeginWebAuthnLogin(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	loginBody := a2.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, loginOpts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(loginBody)); err != nil {
		t.Fatalf("first login: %v", err)
	}
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(loginBody)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed assertion accepted (err=%v)", err)
	}
}

// An expired challenge must be refused even though it was never used.
func TestWebAuthnChallengeExpires(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatal(err)
	}
	challenge := challengeFrom(t, opts)

	// Re-date the stored row into the past (the ceremony itself has no clock we
	// can move), then confirm the claim refuses it.
	repo := store.NewWebAuthnRepo(testStore)
	if _, err := repo.ClaimChallenge(ctx, challenge, webauthnPurposeRegister); err != nil {
		t.Fatalf("prepare: challenge missing: %v", err)
	}
	sessionJSON, err := json.Marshal(map[string]any{"challenge": challenge})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertChallenge(ctx, challenge, webauthnPurposeRegister, &uid, sessionJSON,
		time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("re-insert expired: %v", err)
	}

	a := newVirtAuthenticator(t)
	body := a.attestationResponse(t, virtOpts{
		challenge: challenge, origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "expired", bytes.NewReader(body)); !errors.Is(err, ErrWebAuthnVerification) {
		t.Fatalf("expired challenge accepted (err=%v)", err)
	}
}

// A registration challenge issued to one user must not be finishable by another.
func TestWebAuthnRegistrationChallengeIsBoundToTheSession(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	otherID, _, err := svc.CreateUser(ctx, "other@example.com")
	if err != nil {
		t.Fatal(err)
	}

	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatal(err)
	}
	a := newVirtAuthenticator(t)
	body := a.attestationResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, err := svc.FinishWebAuthnRegistration(ctx, otherID, "other@example.com", "stolen", bytes.NewReader(body)); !errors.Is(err, ErrWebAuthnVerification) {
		t.Fatalf("cross-user registration challenge accepted (err=%v)", err)
	}
}

func TestWebAuthnRejectsWrongOriginAndRPID(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*virtOpts)
	}{
		{"wrong origin", func(o *virtOpts) { o.origin = "https://evil.example.com" }},
		{"wrong rp id", func(o *virtOpts) { o.rpID = "evil.example.com" }},
		{"no user verification", func(o *virtOpts) { o.flags = flagUserPresent }},
		{"no user presence", func(o *virtOpts) { o.flags = 0 }},
	}
	for _, tc := range tests {
		t.Run("registration/"+tc.name, func(t *testing.T) {
			opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
			if err != nil {
				t.Fatal(err)
			}
			o := virtOpts{
				challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
				flags: flagUserPresent | flagUserVerified,
			}
			tc.mutate(&o)
			a := newVirtAuthenticator(t)
			body := a.attestationResponse(t, o)
			if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, tc.name, bytes.NewReader(body)); err == nil {
				t.Fatal("registration accepted a broken binding")
			}
		})
	}

	a, _ := enrollPasskey(t, svc, uid, email, "login-binding")
	for _, tc := range tests {
		t.Run("assertion/"+tc.name, func(t *testing.T) {
			if _, err := assertLogin(t, svc, a, email, tc.mutate); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("assertion accepted a broken binding (err=%v)", err)
			}
		})
	}
}

// A signature counter that fails to advance means a cloned authenticator (or a
// replayed assertion) and must be rejected.
func TestWebAuthnRejectsSignCounterRegression(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)

	a, _ := enrollPasskey(t, svc, uid, email, "counter")
	// Registration recorded count 1; a legitimate use advances it.
	a.signCount = 5
	if _, err := assertLogin(t, svc, a, email, nil); err != nil { // asserts as 6
		t.Fatalf("advancing counter rejected: %v", err)
	}
	// A clone replays an older (or equal) counter.
	for _, stale := range []uint32{6, 5, 0} {
		stale := stale
		if _, err := assertLogin(t, svc, a, email, func(o *virtOpts) { o.signCount = &stale }); !errors.Is(err, ErrWebAuthnCloned) {
			t.Fatalf("counter %d accepted (err=%v)", stale, err)
		}
	}
	// The rejected assertions must not have moved the stored counter.
	a.signCount = 6
	if _, err := assertLogin(t, svc, a, email, nil); err != nil { // asserts as 7
		t.Fatalf("post-clone legitimate assertion rejected: %v", err)
	}
}

// An authenticator with no counter support always reports 0; that must keep
// working rather than being read as a permanent clone signal.
func TestWebAuthnZeroCounterAuthenticatorKeepsWorking(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatal(err)
	}
	a := newVirtAuthenticator(t)
	a.signCount = 0
	body := a.attestationResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "counterless", bytes.NewReader(body)); err != nil {
		t.Fatalf("register counterless authenticator: %v", err)
	}
	zero := uint32(0)
	for i := 0; i < 3; i++ {
		if _, err := assertLogin(t, svc, a, email, func(o *virtOpts) { o.signCount = &zero }); err != nil {
			t.Fatalf("counterless assertion %d rejected: %v", i, err)
		}
	}
}

func TestWebAuthnCredentialManagement(t *testing.T) {
	svc, uid, email, password := newWebAuthnService(t)
	ctx := context.Background()

	_, first := enrollPasskey(t, svc, uid, email, "Laptop")
	_, second := enrollPasskey(t, svc, uid, email, "Phone")

	creds, err := svc.ListWebAuthnCredentials(ctx, uid)
	if err != nil || len(creds) != 2 {
		t.Fatalf("list: %v (%d)", err, len(creds))
	}

	// Rename.
	if err := svc.RenameWebAuthnCredential(ctx, uid, first.ID, "  Desk laptop  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	creds, _ = svc.ListWebAuthnCredentials(ctx, uid)
	if creds[0].Nickname != "Desk laptop" {
		t.Fatalf("rename did not take: %q", creds[0].Nickname)
	}
	// Empty nickname is a validation error, not a silent no-op.
	if err := svc.RenameWebAuthnCredential(ctx, uid, first.ID, "   "); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty rename: %v", err)
	}
	// Colliding nickname is refused.
	if err := svc.RenameWebAuthnCredential(ctx, uid, first.ID, "Phone"); !errors.Is(err, ErrValidation) {
		t.Fatalf("colliding rename: %v", err)
	}
	// Another user's credential is simply not found.
	otherID, _, err := svc.CreateUser(ctx, "other2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RenameWebAuthnCredential(ctx, otherID, first.ID, "mine now"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user rename: %v", err)
	}
	if _, err := svc.DeleteWebAuthnCredential(ctx, otherID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete: %v", err)
	}

	// Delete both, including the last one: the password remains, so this cannot
	// lock the user out.
	if _, err := svc.DeleteWebAuthnCredential(ctx, uid, first.ID); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	name, err := svc.DeleteWebAuthnCredential(ctx, uid, second.ID)
	if err != nil {
		t.Fatalf("delete last: %v", err)
	}
	if name != "Phone" {
		t.Fatalf("delete returned nickname %q", name)
	}
	creds, _ = svc.ListWebAuthnCredentials(ctx, uid)
	if len(creds) != 0 {
		t.Fatalf("expected no credentials left, got %d", len(creds))
	}
	// The password still works after the last passkey is gone: deleting the last
	// passkey can never lock a user out.
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("password login broken after removing the last passkey: %v", err)
	}
}

// A duplicate nickname at enrollment must not discard a completed ceremony.
func TestWebAuthnDuplicateNicknameIsUniquified(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	_, first := enrollPasskey(t, svc, uid, email, "Key")
	_, second := enrollPasskey(t, svc, uid, email, "Key")
	if first.Nickname != "Key" || second.Nickname == "Key" {
		t.Fatalf("nicknames %q / %q", first.Nickname, second.Nickname)
	}
	_, blank := enrollPasskey(t, svc, uid, email, "   ")
	if blank.Nickname == "" {
		t.Fatal("a blank nickname must get a generated label")
	}
}

// Re-registering the same authenticator is refused rather than creating a
// duplicate credential row.
func TestWebAuthnDuplicateCredentialRejected(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, _ := enrollPasskey(t, svc, uid, email, "Key")

	opts, err := svc.BeginWebAuthnRegistration(ctx, uid, email)
	if err != nil {
		t.Fatal(err)
	}
	body := a.attestationResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, err := svc.FinishWebAuthnRegistration(ctx, uid, email, "again", bytes.NewReader(body)); err == nil {
		t.Fatal("re-registering the same authenticator was accepted")
	}
}

// BeginWebAuthnLogin must not be an account-existence oracle: unknown, disabled,
// and passkey-less accounts get the same shape of response as a real one, and it
// is stable across calls.
func TestWebAuthnLoginBeginIsNotAnEnumerationOracle(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()

	shape := func(raw json.RawMessage) (int, string) {
		t.Helper()
		var v struct {
			AllowCredentials []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
			UserVerification string `json:"userVerification"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ids := make([]string, 0, len(v.AllowCredentials))
		for _, c := range v.AllowCredentials {
			ids = append(ids, c.ID)
		}
		return len(v.AllowCredentials), strings.Join(ids, ",") + "|" + v.UserVerification
	}

	// Before any passkey exists, a real account and a nonexistent one look alike.
	realBefore, err := svc.BeginWebAuthnLogin(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := svc.BeginWebAuthnLogin(ctx, "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	nReal, _ := shape(realBefore)
	nUnknown, uvUnknown := shape(unknown)
	if nReal != nUnknown || nReal != 1 {
		t.Fatalf("passkey-less real account (%d) and unknown account (%d) differ", nReal, nUnknown)
	}

	// The decoy is stable: probing the same unknown email twice yields the same
	// credential id, so a changing id is not itself a tell.
	unknown2, err := svc.BeginWebAuthnLogin(ctx, "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, uv2 := shape(unknown2); uv2 != uvUnknown {
		t.Fatal("decoy credential id is not stable across probes")
	}

	// A different unknown email gets a different decoy.
	other, err := svc.BeginWebAuthnLogin(ctx, "someone-else@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, uvOther := shape(other); uvOther == uvUnknown {
		t.Fatal("two different unknown emails share a decoy credential id")
	}

	// A decoy challenge can never be finished.
	a, _ := enrollPasskey(t, svc, uid, email, "real")
	decoyOpts, err := svc.BeginWebAuthnLogin(ctx, "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	body := a.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, decoyOpts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(body)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a decoy challenge completed a login (err=%v)", err)
	}
}

// A challenge minted for one account must never be spendable on another, even
// with a valid assertion from the attacker's own authenticator.
func TestWebAuthnLoginChallengeIsBoundToTheAccount(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	victim, _ := enrollPasskey(t, svc, uid, email, "victim key")
	_ = victim

	attackerID, _, err := svc.CreateUser(ctx, "attacker@example.com")
	if err != nil {
		t.Fatal(err)
	}
	attackerKey, _ := enrollPasskey(t, svc, attackerID, "attacker@example.com", "attacker key")

	// Challenge issued for the victim, answered by the attacker's authenticator.
	opts, err := svc.BeginWebAuthnLogin(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	body := attackerKey.assertionResponse(t, virtOpts{
		challenge: challengeFrom(t, opts), origin: testOrigin, rpID: testRPID,
		flags: flagUserPresent | flagUserVerified,
	})
	if _, _, _, _, err := svc.FinishWebAuthnLogin(ctx, bytes.NewReader(body)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a cross-account assertion was accepted (err=%v)", err)
	}
}

// Disabling the account must stop passkey logins immediately.
func TestWebAuthnDisabledUserCannotLogIn(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, _ := enrollPasskey(t, svc, uid, email, "key")
	if err := svc.DisableUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := assertLogin(t, svc, a, email, nil); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user logged in with a passkey (err=%v)", err)
	}
}

// A locked account stays locked for passkeys, and the lock is revealed only
// AFTER a valid assertion — mirroring the password path's "reveal only to the
// credential holder" rule.
func TestWebAuthnHonoursAccountLockout(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	svc.SetLockoutPolicy(LockoutPolicy{Enabled: true, Threshold: 1, Base: time.Hour, Max: time.Hour})
	a, _ := enrollPasskey(t, svc, uid, email, "key")

	// One wrong password trips the lock at threshold 1.
	if _, err := svc.Login(ctx, email, []byte("definitely-wrong"), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
	_, err := assertLogin(t, svc, a, email, nil)
	if locked, ok := AsAccountLocked(err); !ok {
		t.Fatalf("passkey login during lockout: got %v, want AccountLockedError", err)
	} else if locked.RetryAfter <= 0 {
		t.Fatal("lock error carried no remaining window")
	}

	// An INVALID assertion against the same locked account must not reveal the
	// lock — it looks like any other bad credential.
	if _, err := assertLogin(t, svc, a, email, func(o *virtOpts) { o.origin = "https://evil.example.com" }); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad assertion against a locked account leaked the lock: %v", err)
	}
}

// No part of a failed or successful ceremony may put credential/key material
// into an error string.
func TestWebAuthnErrorsAreValueFree(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, info := enrollPasskey(t, svc, uid, email, "leak-probe")

	var msgs []string
	collect := func(err error) {
		if err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	_, err := assertLogin(t, svc, a, email, func(o *virtOpts) { o.origin = "https://evil.example.com" })
	collect(err)
	_, err = assertLogin(t, svc, a, email, func(o *virtOpts) { o.rpID = "evil.example.com" })
	collect(err)
	stale := uint32(0)
	_, err = assertLogin(t, svc, a, email, func(o *virtOpts) { o.signCount = &stale })
	collect(err)
	_, _, _, _, err = svc.FinishWebAuthnLogin(ctx, strings.NewReader(`{"bogus":true}`))
	collect(err)
	_, err = svc.FinishWebAuthnRegistration(ctx, uid, email, "x", strings.NewReader(`{"bogus":true}`))
	collect(err)

	joined := strings.Join(msgs, "\n")
	if joined == "" {
		t.Fatal("expected some errors to inspect")
	}
	// The credential id is a public handle, but it still has no business in an
	// error message, and neither does anything derived from the private key.
	for _, needle := range []string{info.CredentialID, b64u(a.credID), email} {
		if needle != "" && strings.Contains(joined, needle) {
			t.Fatalf("error text leaked %q:\n%s", needle, joined)
		}
	}
}

// The stored row must contain public credential material only — no private key
// bytes ever reach the database.
func TestWebAuthnStoredCredentialHasNoPrivateMaterial(t *testing.T) {
	svc, uid, email, _ := newWebAuthnService(t)
	ctx := context.Background()
	a, _ := enrollPasskey(t, svc, uid, email, "stored")

	rows, err := store.NewWebAuthnRepo(testStore).ListCredentials(ctx, uid, testRPID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list rows: %v (%d)", err, len(rows))
	}
	priv := a.key.D.Bytes()
	if bytes.Contains(rows[0].Credential, priv) {
		t.Fatal("the stored credential record contains private key bytes")
	}
	if rows[0].RPID != testRPID {
		t.Fatalf("rp id not recorded: %q", rows[0].RPID)
	}
}
