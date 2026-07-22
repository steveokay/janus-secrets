package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// lockoutStack boots the full stack with a low-threshold, long-window lockout
// policy so an e2e test can trip a lock within the shared per-IP login limiter
// budget (10/min sustained, burst 5). Threshold 2 means two wrong logins lock,
// so a full lock-trip + reveal costs only three login POSTs.
func lockoutStack(t *testing.T) (*httptest.Server, *Server, string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir,
		Lockout: auth.LockoutPolicy{Enabled: true, Threshold: 2, Base: time.Hour, Max: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}
	return ts, srv, ir.Admin.Email, ir.Admin.Password
}

func postLogin(t *testing.T, base, email, password string) *http.Response {
	t.Helper()
	resp, err := http.Post(base+"/v1/auth/login", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestLockoutTripsAndRevealsOnlyForCorrectPassword drives the account past the
// threshold and asserts: wrong password while locked stays 401
// invalid_credentials, while the correct password yields 429 account_locked with
// a Retry-After header.
func TestLockoutTripsAndRevealsOnlyForCorrectPassword(t *testing.T) {
	ts, _, email, password := lockoutStack(t)

	// Two wrong logins trip the lock (threshold 2). Budget: 2 of ~5.
	for i := 0; i < 2; i++ {
		resp := postLogin(t, ts.URL, email, "wrong")
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("wrong login %d: status %d (want 401)", i, resp.StatusCode)
		}
	}

	// Wrong password while locked → still byte-identical 401 invalid_credentials,
	// no Retry-After, no lock reveal.
	resp := postLogin(t, ts.URL, email, "still wrong")
	var env errEnvelope
	decodeResp(t, resp, &env)
	if resp.StatusCode != 401 || env.Error.Code != "invalid_credentials" {
		t.Fatalf("wrong pw while locked: %d %+v", resp.StatusCode, env)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		t.Fatalf("wrong pw while locked leaked Retry-After: %q", ra)
	}

	// Correct password while locked → 429 account_locked + Retry-After > 0.
	resp = postLogin(t, ts.URL, email, password)
	var env2 errEnvelope
	ra := resp.Header.Get("Retry-After")
	decodeResp(t, resp, &env2)
	if resp.StatusCode != http.StatusTooManyRequests || env2.Error.Code != "account_locked" {
		t.Fatalf("correct pw while locked: %d %+v", resp.StatusCode, env2)
	}
	if ra == "" || ra == "0" {
		t.Fatalf("missing/zero Retry-After: %q", ra)
	}
}

// TestLockStateInUserList proves the admin user list surfaces locked/locked_until.
func TestLockStateInUserList(t *testing.T) {
	ts, srv, email, password := lockoutStack(t)
	ctx := context.Background()

	// Create a second user to lock (locking the admin would complicate the
	// session login used to read the list).
	victimID, _, err := srv.auth.CreateUser(ctx, "victim@corp.io")
	if err != nil {
		t.Fatal(err)
	}

	// Lock the victim (threshold 2). Budget so far: 2 login POSTs.
	for i := 0; i < 2; i++ {
		resp := postLogin(t, ts.URL, "victim@corp.io", "wrong")
		resp.Body.Close()
	}

	// Admin session (login POST #3) reads the list.
	cookie := login(t, ts.URL, email, password)
	var listResp struct {
		Users []struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			Locked      bool   `json:"locked"`
			LockedUntil string `json:"locked_until"`
		} `json:"users"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/users", cookie, "", "", &listResp); code != 200 {
		t.Fatalf("list users: %d", code)
	}
	var found bool
	for _, u := range listResp.Users {
		if u.ID == victimID {
			found = true
			if !u.Locked || u.LockedUntil == "" {
				t.Fatalf("victim not shown locked: %+v", u)
			}
		}
	}
	if !found {
		t.Fatal("victim not in user list")
	}
}

// TestUnlockAuthzAndSelf covers the unlock endpoint's authz: admin-only, and a
// self-unlock is rejected.
func TestUnlockAuthzAndSelf(t *testing.T) {
	ts, srv, email, password := lockoutStack(t)
	ctx := context.Background()

	victimID, _, err := srv.auth.CreateUser(ctx, "victim2@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	// Lock the victim (2 login POSTs).
	for i := 0; i < 2; i++ {
		resp := postLogin(t, ts.URL, "victim2@corp.io", "wrong")
		resp.Body.Close()
	}

	// Admin session (login POST #3).
	cookie := login(t, ts.URL, email, password)
	var me struct{ ID string }
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", cookie, "", "", &me); code != 200 || me.ID == "" {
		t.Fatalf("me: %d %+v", code, me)
	}
	adminID := me.ID

	// Unauthenticated unlock → 401.
	if code := doAuthed(t, "POST", ts.URL+"/v1/users/"+victimID+"/unlock", "", "", "", nil); code != 401 {
		t.Fatalf("unauth unlock: %d", code)
	}

	// Self-unlock → 409.
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/users/"+adminID+"/unlock", cookie, "", "", &env); code != 409 {
		t.Fatalf("self unlock: %d %+v", code, env)
	}

	// Admin unlock of the victim → 204.
	if code := doAuthed(t, "POST", ts.URL+"/v1/users/"+victimID+"/unlock", cookie, "", "", nil); code != 204 {
		t.Fatalf("admin unlock: %d", code)
	}

	// The victim's lock is cleared server-side.
	if srv.auth.IsEmailLocked(ctx, "victim2@corp.io") {
		t.Fatal("victim still locked after admin unlock")
	}
}

// TestUnlockDeniedForNonAdmin verifies a developer-role user cannot unlock.
func TestUnlockDeniedForNonAdmin(t *testing.T) {
	ts, srv, _, _ := lockoutStack(t)
	ctx := context.Background()

	// A plain user with no instance role.
	uid, pw, err := srv.auth.CreateUser(ctx, "dev@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	// Log in as the non-admin (login POST #1) and attempt to unlock someone.
	cookie := login(t, ts.URL, "dev@corp.io", pw)
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/users/"+uid+"/unlock", cookie, "", "", &env); code != 403 {
		t.Fatalf("non-admin unlock: %d %+v", code, env)
	}
	_ = ctx
}
