package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubRetention scripts the resolution routes plus the retention/prune routes,
// recording every request so the tests can assert the exact wire body.
func stubRetention(t *testing.T) (*httptest.Server, *[]recordedReq) {
	t.Helper()
	var reqs []recordedReq
	record := func(r *http.Request) {
		rr := recordedReq{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				_ = json.Unmarshal(b, &rr.body)
			}
		}
		reqs = append(reqs, rr)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects": []map[string]any{{"id": "p1", "slug": "proj"}},
		})
	})
	mux.HandleFunc("GET /v1/projects/{pid}/environments", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environments": []map[string]any{{"id": "e-dev", "slug": "dev"}},
		})
	})
	mux.HandleFunc("GET /v1/projects/{pid}/environments/{eid}/configs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configs": []map[string]any{{"id": "c1", "name": "default"}},
		})
	})
	mux.HandleFunc("GET /v1/configs/{cid}/versions/retention", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instance_min_versions": 5, "instance_min_days": 0,
			"config_min_versions": 12, "config_min_days": nil,
			"effective_min_versions": 12, "effective_min_days": 0,
		})
	})
	mux.HandleFunc("PUT /v1/configs/{cid}/versions/retention", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instance_min_versions": 5, "instance_min_days": 0,
			"config_min_versions": nil, "config_min_days": nil,
			"effective_min_versions": 5, "effective_min_days": 0,
		})
	})
	mux.HandleFunc("POST /v1/configs/{cid}/versions/prune", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dry_run": true, "latest_version": 9, "keep_versions": 3, "keep_days": 0,
			"pruned_versions": []int{1, 2, 3}, "pinned_versions": []int{4},
			"versions_deleted": 3, "values_deleted": 5, "versions_retained": 6,
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &reqs
}

func retentionArgs(ts *httptest.Server, args ...string) []string {
	return append(args, "--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
}

// TestPruneCLIDefaultsToDryRun is the important one: the destructive command
// must never destroy without --apply.
func TestPruneCLIDefaultsToDryRun(t *testing.T) {
	ts, reqs := stubRetention(t)
	out, err := runCLI(t, "", retentionArgs(ts, "secrets", "prune", "--keep-versions", "3")...)
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/configs/c1/versions/prune")
	if post == nil {
		t.Fatalf("no prune request; reqs=%v", *reqs)
	}
	if post.body["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true (no --apply given)", post.body["dry_run"])
	}
	if !strings.Contains(out, "would prune") || !strings.Contains(out, "dry run") {
		t.Fatalf("preview output does not read as a preview: %q", out)
	}
	// Value-free: only version numbers and counts.
	for _, want := range []string{"v1, v2, v3", "3 config version(s)", "5 unreferenced value version(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
	if !strings.Contains(out, "pending promotion request") {
		t.Fatalf("output does not explain the pinned version: %q", out)
	}
}

func TestPruneCLIApplySendsDryRunFalse(t *testing.T) {
	ts, reqs := stubRetention(t)
	out, err := runCLI(t, "", retentionArgs(ts, "secrets", "prune", "--keep-days", "180", "--apply")...)
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/configs/c1/versions/prune")
	if post == nil {
		t.Fatalf("no prune request; reqs=%v", *reqs)
	}
	if post.body["dry_run"] != false {
		t.Fatalf("dry_run = %v, want false", post.body["dry_run"])
	}
	if post.body["keep_days"] != float64(180) {
		t.Fatalf("keep_days = %v, want 180", post.body["keep_days"])
	}
	if strings.Contains(out, "dry run") || !strings.Contains(out, "pruned") {
		t.Fatalf("apply output still reads as a preview: %q", out)
	}
}

func TestPruneCLIRequiresAKeepFlag(t *testing.T) {
	ts, reqs := stubRetention(t)
	if _, err := runCLI(t, "", retentionArgs(ts, "secrets", "prune")...); err == nil {
		t.Fatal("prune with no keep flags succeeded; want an error")
	}
	if findReq(*reqs, "POST", "/v1/configs/c1/versions/prune") != nil {
		t.Fatal("prune with no keep flags still hit the server")
	}
}

func TestRetentionCLIGetShowsAllThreeScopes(t *testing.T) {
	ts, _ := stubRetention(t)
	out, err := runCLI(t, "", retentionArgs(ts, "secrets", "retention", "get")...)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"instance", "config", "effective", "off"} {
		if !strings.Contains(out, want) {
			t.Fatalf("retention get output missing %q: %q", want, out)
		}
	}
}

func TestRetentionCLISetAndClear(t *testing.T) {
	ts, reqs := stubRetention(t)
	if _, err := runCLI(t, "", retentionArgs(ts, "secrets", "retention", "set", "--min-versions", "12")...); err != nil {
		t.Fatal(err)
	}
	put := findReq(*reqs, "PUT", "/v1/configs/c1/versions/retention")
	if put == nil {
		t.Fatalf("no retention PUT; reqs=%v", *reqs)
	}
	if put.body["min_versions"] != float64(12) || put.body["min_days"] != nil {
		t.Fatalf("set body = %v", put.body)
	}

	// set with no flags is refused client-side (it would otherwise CLEAR).
	if _, err := runCLI(t, "", retentionArgs(ts, "secrets", "retention", "set")...); err == nil {
		t.Fatal("retention set with no flags succeeded; want an error")
	}

	ts2, reqs2 := stubRetention(t)
	if _, err := runCLI(t, "", retentionArgs(ts2, "secrets", "retention", "clear")...); err != nil {
		t.Fatal(err)
	}
	put2 := findReq(*reqs2, "PUT", "/v1/configs/c1/versions/retention")
	if put2 == nil {
		t.Fatalf("no retention PUT for clear; reqs=%v", *reqs2)
	}
	if put2.body["min_versions"] != nil || put2.body["min_days"] != nil {
		t.Fatalf("clear body = %v, want both null", put2.body)
	}
}

func TestJoinInts(t *testing.T) {
	for _, tc := range []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{1}, "v1"},
		{[]int{1, 2, 10}, "v1, v2, v10"},
	} {
		if got := joinInts(tc.in); got != tc.want {
			t.Errorf("joinInts(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
