package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubState records what the scripted sys API received on the wire.
type stubState struct {
	paths         []string
	share         string // last share submitted to /v1/sys/unseal
	initShares    int    // shares param received by /v1/sys/init
	initThreshold int    // threshold param received by /v1/sys/init
	adminEmail    string // admin_email param received by /v1/sys/init
}

// stubSys scripts the sys API for CLI tests.
func stubSys(t *testing.T, sealType string) (*httptest.Server, *stubState) {
	t.Helper()
	st := &stubState{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/init", func(w http.ResponseWriter, r *http.Request) {
		st.paths = append(st.paths, r.URL.Path)
		var req struct {
			Shares, Threshold int
			AdminEmail        string `json:"admin_email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		st.initShares = req.Shares
		st.initThreshold = req.Threshold
		st.adminEmail = req.AdminEmail
		admin := map[string]string{"email": "admin@localhost", "password": "generated-one-time-pw"}
		if sealType == "shamir" {
			shares := []string{"aa01", "bb02", "cc03"}
			if req.Shares == 1 {
				shares = []string{"dd04"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "shamir", "shares": shares, "admin": admin})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "awskms", "admin": admin})
	})
	mux.HandleFunc("GET /v1/sys/seal-status", func(w http.ResponseWriter, r *http.Request) {
		st.paths = append(st.paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"initialized": true, "sealed": true, "type": sealType,
			"threshold": 3, "shares": 5,
			"progress": map[string]int{"submitted": 1, "required": 3},
		})
	})
	mux.HandleFunc("POST /v1/sys/unseal", func(w http.ResponseWriter, r *http.Request) {
		st.paths = append(st.paths, r.URL.Path)
		var req struct{ Share string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		st.share = req.Share
		if sealType == "shamir" && req.Share == "" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "validation", "message": "share is required"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": false})
	})
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		st.paths = append(st.paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": true})
	})
	mux.HandleFunc("POST /v1/sys/unseal/reset", func(w http.ResponseWriter, r *http.Request) {
		st.paths = append(st.paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": true, "progress": map[string]int{"submitted": 0, "required": 3}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st
}

// runCLI executes the root command with args, returning stdout.
func runCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if stdin != "" {
		root.SetIn(strings.NewReader(stdin))
	}
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestInitCommandPrintsSharesWithWarning(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--shares", "5", "--threshold", "3")
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"aa01", "bb02", "cc03"} {
		if !strings.Contains(out, sh) {
			t.Fatalf("output missing share %s: %q", sh, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "will not be shown again") {
		t.Fatalf("output missing warning: %q", out)
	}
}

func TestInitCommandJSON(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Type   string   `json:"type"`
		Shares []string `json:"shares"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("--json output not JSON: %q (%v)", out, err)
	}
	if resp.Type != "shamir" || len(resp.Shares) != 3 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUnsealCommandWithFlag(t *testing.T) {
	ts, st := stubSys(t, "shamir")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL, "--share", "aa01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
	found := false
	for _, p := range st.paths {
		if p == "/v1/sys/unseal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unseal endpoint not called: %v", st.paths)
	}
	if st.share != "aa01" {
		t.Fatalf("share on the wire = %q, want %q", st.share, "aa01")
	}
}

func TestUnsealCommandReadsStdinWhenPiped(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "aa01\n", "unseal", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
}

func TestUnsealCommandKMSNeedsNoShare(t *testing.T) {
	ts, _ := stubSys(t, "awskms")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
}

func TestSealStatusCommand(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "seal-status", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"initialized", "sealed", "shamir", "1/3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q: %q", want, out)
		}
	}
}

func TestSealCommand(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir()) // isolate stored auth
	t.Setenv("JANUS_TOKEN", "")
	ts, st := stubSys(t, "shamir")
	if _, err := runCLI(t, "", "seal", "--address", ts.URL, "--token", "janus_svc_test"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range st.paths {
		if p == "/v1/sys/seal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("seal endpoint not called: %v", st.paths)
	}
}

func TestSealSendsBearerToken(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir()) // isolate stored auth
	t.Setenv("JANUS_TOKEN", "")
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "janus_svc_abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer janus_svc_abc" {
		t.Fatalf("Authorization = %q, want Bearer janus_svc_abc", gotAuth)
	}
}

func TestSealSendsStoredSession(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	var gotCookie string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("janus_session"); err == nil {
			gotCookie = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	if err := saveAuth(&authState{Address: ts.URL, Session: "sess123"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotCookie != "sess123" {
		t.Fatalf("session cookie = %q, want sess123", gotCookie)
	}
}

func TestSealAuthErrorIsActionable(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthenticated","message":"authentication required"}}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "janus login") {
		t.Fatalf("want a 'janus login' hint, got %v", err)
	}
}

func TestInitCommandSendsParams(t *testing.T) {
	ts, st := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--shares", "1", "--threshold", "1")
	if err != nil {
		t.Fatal(err)
	}
	if st.initShares != 1 || st.initThreshold != 1 {
		t.Fatalf("init params on the wire = shares %d, threshold %d, want 1/1", st.initShares, st.initThreshold)
	}
	if !strings.Contains(out, "dd04") {
		t.Fatalf("output missing single share dd04: %q", out)
	}
}

func TestInitCommandPrintsAdminCredential(t *testing.T) {
	ts, st := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--admin-email", "root@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if st.adminEmail != "root@corp.io" {
		t.Fatalf("admin_email on the wire = %q", st.adminEmail)
	}
	for _, want := range []string{"admin@localhost", "generated-one-time-pw", "WILL NOT BE SHOWN AGAIN"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
}

func TestAPIErrorRendering(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/init", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
			"code": "already_initialized", "message": "seal is already initialized",
		}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	_, err := runCLI(t, "", "init", "--address", ts.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"already_initialized", "409"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestUnsealResetFlag(t *testing.T) {
	ts, st := stubSys(t, "shamir")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL, "--reset")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "discarded") {
		t.Fatalf("output = %q", out)
	}
	found := false
	for _, p := range st.paths {
		if p == "/v1/sys/unseal/reset" {
			found = true
		}
		if p == "/v1/sys/unseal" {
			t.Fatalf("--reset must not submit a share: %v", st.paths)
		}
	}
	if !found {
		t.Fatalf("reset endpoint not called: %v", st.paths)
	}
}
