package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubSys scripts the sys API for CLI tests.
func stubSys(t *testing.T, sealType string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/init", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var req struct{ Shares, Threshold int }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if sealType == "shamir" {
			shares := []string{"aa01", "bb02", "cc03"}
			if req.Shares == 1 {
				shares = []string{"dd04"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "shamir", "shares": shares})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "awskms"})
	})
	mux.HandleFunc("GET /v1/sys/seal-status", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"initialized": true, "sealed": true, "type": sealType,
			"threshold": 3, "shares": 5,
			"progress": map[string]int{"submitted": 1, "required": 3},
		})
	})
	mux.HandleFunc("POST /v1/sys/unseal", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var req struct{ Share string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if sealType == "shamir" && req.Share == "" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "validation", "message": "share is required"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": false})
	})
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": true})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
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
	ts, paths := stubSys(t, "shamir")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL, "--share", "aa01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
	found := false
	for _, p := range *paths {
		if p == "/v1/sys/unseal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unseal endpoint not called: %v", *paths)
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
	ts, paths := stubSys(t, "shamir")
	if _, err := runCLI(t, "", "seal", "--address", ts.URL); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range *paths {
		if p == "/v1/sys/seal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("seal endpoint not called: %v", *paths)
	}
}
