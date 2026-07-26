package main

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// runAdminCmd executes the admin command tree with a scripted stdin (unseal
// shares, one per line). A strings.Reader is not an *os.File, so isTerminalCmd
// reports false and the piped branches are exercised.
func runAdminCmd(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errb strings.Builder
	c := newAdminCmd()
	c.SetArgs(args)
	c.SetIn(strings.NewReader(stdin))
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SilenceUsage = true
	c.SilenceErrors = true
	err := c.Execute()
	return out.String(), errb.String(), err
}

func TestValidateAdminEmail(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "root@corp.io", "root@corp.io", false},
		{"trims surrounding space", "  root@corp.io\t", "root@corp.io", false},
		{"plus addressing", "root+dr@corp.io", "root+dr@corp.io", false},
		{"empty", "", "", true},
		{"only whitespace", "   ", "", true},
		{"no at sign", "root.corp.io", "", true},
		{"two at signs", "root@a@corp.io", "", true},
		{"leading at", "@corp.io", "", true},
		{"trailing at", "root@", "", true},
		{"inner space", "ro ot@corp.io", "", true},
		{"embedded newline", "root@corp.io\nx@y.z", "", true},
		{"control character", "root\x01@corp.io", "", true},
		{"too long", strings.Repeat("a", 320) + "@corp.io", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateAdminEmail(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateAdminEmail(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateAdminEmail(%q) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("validateAdminEmail(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAdminReadSharesPiped covers the piped multi-share path. A fresh
// bufio.Reader per share would buffer ahead and swallow the later lines, so the
// shared reader is load-bearing for any threshold > 1.
func TestAdminReadSharesPiped(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		n       int
		want    []string
		wantErr bool
	}{
		{"single share", "a1b2\n", 1, []string{"a1b2"}, false},
		{"three shares in one write", "aa\nbb\ncc\n", 3, []string{"aa", "bb", "cc"}, false},
		{"tolerates a missing final newline", "aa\nbb", 2, []string{"aa", "bb"}, false},
		{"trims carriage returns", "aa\r\nbb\r\n", 2, []string{"aa", "bb"}, false},
		{"rejects non-hex", "not-hex\n", 1, nil, true},
		{"rejects a short stream", "aa\n", 2, nil, true},
		{"rejects a non-positive threshold", "aa\n", 0, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(tc.stdin))
			cmd.SetOut(&strings.Builder{})
			cmd.SetErr(&strings.Builder{})
			got, err := adminReadShares(cmd, tc.n)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("adminReadShares(%q, %d) = %x, want error", tc.stdin, tc.n, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d shares, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if hex.EncodeToString(got[i]) != w {
					t.Fatalf("share %d = %x, want %s", i, got[i], w)
				}
			}
		})
	}
}

// TestAdminReadSharesNeverEchoesInput asserts a rejected share is not quoted
// back at the operator — the rejected bytes are key material.
func TestAdminReadSharesNeverEchoesInput(t *testing.T) {
	const bad = "notavalidsharebutstillsecret"
	cmd := &cobra.Command{}
	var errb strings.Builder
	cmd.SetIn(strings.NewReader(bad + "\n"))
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&errb)
	_, err := adminReadShares(cmd, 1)
	if err == nil {
		t.Fatal("expected an error for a non-hex share")
	}
	if strings.Contains(err.Error(), bad) || strings.Contains(errb.String(), bad) {
		t.Fatalf("the rejected share leaked into diagnostics")
	}
}

// TestAdminResetPasswordFlagRefusals covers the argument-level refusals that
// need no database at all.
func TestAdminResetPasswordFlagRefusals(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		args []string
		want string
	}{
		{"email required", "postgres://unused", []string{"reset-password", "--yes"}, "--email is required"},
		{"email validated", "postgres://unused", []string{"reset-password", "--email", "bogus", "--yes"}, "not a valid email"},
		{"database url required", "", []string{"reset-password", "--email", "root@corp.io", "--yes"}, "JANUS_DATABASE_URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JANUS_DATABASE_URL", tc.dsn)
			_, _, err := runAdminCmd(t, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// adminTestStack boots Postgres + the API server (migrated, NOT initialized)
// and points JANUS_DATABASE_URL at it.
func adminTestStack(t *testing.T) (base string, st *store.Store) {
	t.Helper()
	dsn := bootPostgres(t)
	srv, s, err := api.Boot(context.Background(), api.BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Setenv("JANUS_DATABASE_URL", dsn)
	return ts.URL, s
}

type adminInitResult struct {
	Shares []string `json:"shares"`
	Admin  *struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	} `json:"admin"`
}

// adminInit runs the 1-of-1 seal init + unseal and returns the seal share and
// the bootstrap admin credential.
func adminInit(t *testing.T, base, email string) adminInitResult {
	t.Helper()
	var ir adminInitResult
	post(t, base+"/v1/sys/init", "",
		fmt.Sprintf(`{"shares":1,"threshold":1,"admin_email":%q}`, email), &ir)
	post(t, base+"/v1/sys/unseal", "", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil)
	if ir.Admin == nil || ir.Admin.Password == "" {
		t.Fatal("init did not return a bootstrap admin credential")
	}
	return ir
}

// loginStatus reports the HTTP status of a password login attempt.
func loginStatus(t *testing.T, base, email, pw string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": pw})
	resp, err := http.Post(base+"/v1/auth/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// parseResetPassword pulls the one-time credential out of the command's stdout.
func parseResetPassword(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if _, pw, ok := strings.Cut(strings.TrimSpace(line), "Password:"); ok {
			return strings.TrimSpace(pw)
		}
	}
	t.Fatalf("no password line in output:\n%s", out)
	return ""
}

// latestAudit returns the most recent audit event.
func latestAudit(t *testing.T, st *store.Store) store.AuditRow {
	t.Helper()
	rows, err := store.NewAuditRepo(st).ListPage(context.Background(), store.AuditFilter{}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("audit log is empty")
	}
	return rows[0]
}

// auditByAction returns the most recent event with the given action, or false
// if the action never appears.
func auditByAction(t *testing.T, st *store.Store, action string) (store.AuditRow, bool) {
	t.Helper()
	rows, err := store.NewAuditRepo(st).ListPage(context.Background(),
		store.AuditFilter{Action: action}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return store.AuditRow{}, false
	}
	return rows[0], true
}

// mustAuditByAction is auditByAction with a fatal on absence.
func mustAuditByAction(t *testing.T, st *store.Store, action string) store.AuditRow {
	t.Helper()
	row, ok := auditByAction(t, st, action)
	if !ok {
		t.Fatalf("no %s event in the audit log", action)
	}
	return row
}

// TestAdminResetPasswordE2E is the full disaster-recovery ceremony against a
// real Postgres and a real API server: refuse on an uninitialized instance,
// refuse on an unknown account, refuse without confirmation or seal material,
// then reset, prove the new credential works and the old one does not, and
// prove the audit chain is still valid.
func TestAdminResetPasswordE2E(t *testing.T) {
	base, st := adminTestStack(t)
	ctx := context.Background()
	const email = "root@corp.io"

	// 1. Uninitialized instance: the schema exists but no seal config does.
	if _, _, err := runAdminCmd(t, "", "reset-password", "--email", email, "--yes"); err == nil ||
		!strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("uninitialized refusal: err = %v", err)
	}

	ir := adminInit(t, base, email)
	oldPassword := ir.Admin.Password
	oldCookie := loginForCookie(t, base, email, oldPassword)

	// 2. Unknown account — refused before any share is asked for.
	if _, _, err := runAdminCmd(t, "", "reset-password", "--email", "ghost@corp.io", "--yes"); err == nil ||
		!strings.Contains(err.Error(), "no user with email") {
		t.Fatalf("unknown-email refusal: err = %v", err)
	}

	// 3. No confirmation and no terminal.
	if _, _, err := runAdminCmd(t, ir.Shares[0]+"\n", "reset-password", "--email", email); err == nil ||
		!strings.Contains(err.Error(), "--yes") {
		t.Fatalf("confirmation refusal: err = %v", err)
	}

	// 4. Malformed share.
	if _, _, err := runAdminCmd(t, "zzzz\n", "reset-password", "--email", email, "--yes"); err == nil ||
		!strings.Contains(err.Error(), "not valid hex") {
		t.Fatalf("bad-hex refusal: err = %v", err)
	}

	// 5. Well-formed but WRONG share — the key check value rejects it, so seal
	//    material really is a control and not a formality.
	wrong := strings.Repeat("ab", 32)
	if _, _, err := runAdminCmd(t, wrong+"\n", "reset-password", "--email", email, "--yes"); err == nil ||
		!strings.Contains(err.Error(), "could not obtain the master key") {
		t.Fatalf("wrong-share refusal: err = %v", err)
	}

	// Nothing above may have changed the credential.
	if code := loginStatus(t, base, email, oldPassword); code != 200 {
		t.Fatalf("a refused reset changed the password: login = %d", code)
	}

	// 6. The real ceremony.
	before := latestAudit(t, st).Seq
	out, errOut, err := runAdminCmd(t, ir.Shares[0]+"\n", "reset-password", "--email", email, "--yes")
	if err != nil {
		t.Fatalf("reset failed: %v (stderr: %s)", err, errOut)
	}
	newPassword := parseResetPassword(t, out)
	if newPassword == "" || newPassword == oldPassword {
		t.Fatal("the reset did not mint a fresh password")
	}
	if !strings.Contains(out, "WILL NOT BE SHOWN AGAIN") {
		t.Fatalf("missing the one-time credential banner:\n%s", out)
	}

	// The new credential works; the old one and the old session do not. Login
	// is rate limited (burst 5), so the working credential is exercised once and
	// the resulting cookie is reused for the audit check below.
	newCookie := loginForCookie(t, base, email, newPassword)
	if code := loginStatus(t, base, email, oldPassword); code == 200 {
		t.Fatal("the old password still works")
	}
	if code := getStatus(t, base+"/v1/projects", oldCookie); code != 401 {
		t.Fatalf("the pre-reset session survived: %d", code)
	}

	// 7. The audit event.
	ev := mustAuditByAction(t, st, adminActionResetPassword)
	if ev.Seq <= before {
		t.Fatal("no audit event was appended")
	}
	if ev.ActorKind != adminActorKind || ev.ActorName != adminActorName {
		t.Fatalf("actor = %s/%s, want %s/%s", ev.ActorKind, ev.ActorName, adminActorKind, adminActorName)
	}
	if ev.ActorID != nil {
		t.Fatalf("actor_id = %v, want NULL (no Janus principal ran this)", *ev.ActorID)
	}
	if ev.Result != "success" || ev.IP != adminActorIP {
		t.Fatalf("result/ip = %s/%s", ev.Result, ev.IP)
	}
	if !strings.HasPrefix(ev.Resource, "users/") {
		t.Fatalf("resource = %q", ev.Resource)
	}
	if ev.Detail == nil || !strings.Contains(*ev.Detail, "sessions_revoked=") ||
		!strings.Contains(*ev.Detail, "seal=shamir") {
		t.Fatalf("detail = %v", ev.Detail)
	}

	// 8. The chain is still valid, both directly and through the API the UI uses.
	res, err := audit.New(store.NewAuditRepo(st)).Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("audit chain broken at seq %d (%s)", res.BrokenAtSeq, res.Reason)
	}
	var verify struct {
		Valid bool  `json:"valid"`
		Count int64 `json:"count"`
	}
	getJSON(t, base+"/v1/audit/verify", newCookie, &verify)
	if !verify.Valid {
		t.Fatal("GET /v1/audit/verify reports the chain invalid after a reset")
	}

	// 9. The password must not appear anywhere but stdout: not on stderr, not in
	//    any audit column.
	if strings.Contains(errOut, newPassword) {
		t.Fatal("the new password leaked to stderr")
	}
	assertNoPasswordInAudit(t, st, newPassword)
}

// TestAdminResetPasswordClearMFA covers the louder --clear-mfa step: it is
// opt-in, audited under its own action, and a no-op note when nothing is
// enrolled. Without the flag an existing enrolment survives the reset.
func TestAdminResetPasswordClearMFA(t *testing.T) {
	base, st := adminTestStack(t)
	ctx := context.Background()
	const email = "mfa@corp.io"
	ir := adminInit(t, base, email)

	// Login is rate limited (burst 5), so enrolment state is read from the store
	// rather than by logging in repeatedly.
	totps := store.NewTOTPRepo(st)
	user, err := store.NewUserRepo(st).GetByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	enrolled := func() bool {
		row, err := totps.GetTOTP(ctx, user.ID)
		if errors.Is(err, store.ErrNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return row.ActivatedAt != nil
	}

	cookie := loginForCookie(t, base, email, ir.Admin.Password) // login 1
	enrollTOTP(t, base, cookie)
	if !enrolled() {
		t.Fatal("TOTP enrolment did not activate")
	}

	// A plain reset must NOT touch the second factor.
	if _, _, err := runAdminCmd(t, ir.Shares[0]+"\n",
		"reset-password", "--email", email, "--yes"); err != nil {
		t.Fatal(err)
	}
	if !enrolled() {
		t.Fatal("a plain reset silently cleared the second factor")
	}
	if _, ok := auditByAction(t, st, adminActionClearMFA); ok {
		t.Fatal("a reset without --clear-mfa emitted admin.clear_mfa")
	}

	// --clear-mfa strips the factor and audits it under its own action.
	out, _, err := runAdminCmd(t, ir.Shares[0]+"\n",
		"reset-password", "--email", email, "--yes", "--clear-mfa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "TOTP enrolment and recovery codes were removed") {
		t.Fatalf("missing the MFA-cleared notice:\n%s", out)
	}
	if enrolled() {
		t.Fatal("--clear-mfa left the enrolment in place")
	}
	ev := mustAuditByAction(t, st, adminActionClearMFA)
	if ev.ActorKind != adminActorKind || ev.Result != "success" || ev.ActorID != nil {
		t.Fatalf("clear-mfa audit = %+v", ev)
	}
	if ev.Resource != "users/"+user.ID {
		t.Fatalf("clear-mfa resource = %q, want users/%s", ev.Resource, user.ID)
	}
	// Password alone now logs in — no second-factor challenge. (login 2)
	if code := loginStatus(t, base, email, parseResetPassword(t, out)); code != 200 {
		t.Fatalf("login after --clear-mfa = %d, want 200", code)
	}

	// --clear-mfa again, with nothing enrolled: a note, not an error, and no
	// second admin.clear_mfa event.
	cleared := ev.Seq
	_, errOut, err := runAdminCmd(t, ir.Shares[0]+"\n",
		"reset-password", "--email", email, "--yes", "--clear-mfa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "no TOTP enrolment") {
		t.Fatalf("expected a no-enrolment note, got: %s", errOut)
	}
	if again := mustAuditByAction(t, st, adminActionClearMFA); again.Seq != cleared {
		t.Fatal("a no-op --clear-mfa emitted a second admin.clear_mfa event")
	}

	res, err := audit.New(store.NewAuditRepo(st)).Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("audit chain broken at seq %d (%s)", res.BrokenAtSeq, res.Reason)
	}
}

// TestAdminResetPasswordNoLeak is the leak guard for the recovery path: the
// generated password may appear on stdout (that is the point) and nowhere else
// — not on stderr, not in an error string, and not in the audit log.
func TestAdminResetPasswordNoLeak(t *testing.T) {
	base, st := adminTestStack(t)
	const email = "leak@corp.io"
	ir := adminInit(t, base, email)

	out, errOut, err := runAdminCmd(t, ir.Shares[0]+"\n", "reset-password", "--email", email, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	pw := parseResetPassword(t, out)
	if strings.Contains(errOut, pw) {
		t.Fatal("the password leaked to stderr")
	}
	assertNoPasswordInAudit(t, st, pw)

	// A forced failure after the credential exists must not echo it either.
	_, errOut2, err2 := runAdminCmd(t, "zzzz\n", "reset-password", "--email", email, "--yes")
	if err2 == nil {
		t.Fatal("expected the bad-share run to fail")
	}
	if strings.Contains(err2.Error(), pw) || strings.Contains(errOut2, pw) {
		t.Fatal("an error path echoed a password")
	}
	// The unseal share is credential material too and must never be echoed.
	if strings.Contains(errOut, ir.Shares[0]) || strings.Contains(out, ir.Shares[0]) {
		t.Fatal("the unseal share was echoed back")
	}
}

// assertNoPasswordInAudit walks every audit column looking for the plaintext.
func assertNoPasswordInAudit(t *testing.T, st *store.Store, pw string) {
	t.Helper()
	if pw == "" {
		t.Fatal("refusing to search for an empty password")
	}
	err := store.NewAuditRepo(st).Iterate(context.Background(), func(row store.AuditRow) error {
		fields := []string{row.ActorKind, row.ActorName, row.Action, row.Resource, row.Result, row.IP}
		for _, p := range []*string{row.ActorID, row.Detail, row.ResultCode} {
			if p != nil {
				fields = append(fields, *p)
			}
		}
		for _, f := range fields {
			if strings.Contains(f, pw) {
				return fmt.Errorf("audit seq %d contains the password", row.Seq)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- small API helpers used by the tests above ---

func getStatus(t *testing.T, url, cookie string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "janus_session", Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func getJSON(t *testing.T, url, cookie string, out any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "janus_session", Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s -> %d", url, resp.StatusCode)
	}
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
}

var totpB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// enrollTOTP runs the enroll + confirm ceremony for the session's user.
func enrollTOTP(t *testing.T, base, cookie string) {
	t.Helper()
	var er struct {
		Secret string `json:"secret"`
	}
	post(t, base+"/v1/auth/totp/enroll", cookie, "{}", &er)
	secret, err := totpB32.DecodeString(er.Secret)
	if err != nil {
		t.Fatal(err)
	}
	code := crypto.TOTPCodeAt(secret, time.Now())
	post(t, base+"/v1/auth/totp/confirm", cookie, fmt.Sprintf(`{"code":%q}`, code), nil)
}
