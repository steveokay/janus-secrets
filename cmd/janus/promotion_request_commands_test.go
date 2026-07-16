package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubPromoteRequests scripts the resolution routes (projects/environments/configs)
// plus the promote-request routes for CLI tests, recording every request.
func stubPromoteRequests(t *testing.T) (*httptest.Server, *[]recordedReq) {
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

	// Resolution: project "proj" (pid p1), envs dev(e-dev)/staging(e-stg), config "default".
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects": []map[string]any{{"id": "p1", "slug": "proj"}},
		})
	})
	mux.HandleFunc("GET /v1/projects/{pid}/environments", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environments": []map[string]any{
				{"id": "e-dev", "slug": "dev"},
				{"id": "e-stg", "slug": "staging"},
			},
		})
	})
	mux.HandleFunc("GET /v1/projects/{pid}/environments/{eid}/configs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configs": []map[string]any{{"id": "c-" + r.PathValue("eid"), "name": "default"}},
		})
	})

	// Promote requests.
	mux.HandleFunc("POST /v1/promote/requests", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "rq1", "status": "pending"})
	})
	mux.HandleFunc("GET /v1/promote/requests", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requests": []map[string]any{
				{
					"id":            "rq1",
					"status":        "pending",
					"target_env_id": "e-stg",
					"target_name":   "default",
					"keys":          []string{"DB_URL"},
					"note":          "ship",
					"requested_by":  "alice",
					"created_at":    "2026-07-16T00:00:00Z",
				},
			},
		})
	})
	mux.HandleFunc("POST /v1/promote/requests/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"target_version": 4,
			"applied":        []string{"DB_URL"},
			"skipped":        []string{},
		})
	})
	mux.HandleFunc("POST /v1/promote/requests/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "rejected"})
	})
	mux.HandleFunc("POST /v1/promote/requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "cancelled"})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &reqs
}

func TestPromoteRequestSubcommandsRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "promote" {
			found = true
			want := map[string]bool{"request": false, "requests": false, "approve": false, "reject": false, "cancel": false}
			for _, s2 := range sub.Commands() {
				if _, ok := want[s2.Name()]; ok {
					want[s2.Name()] = true
				}
			}
			for name, ok := range want {
				if !ok {
					t.Errorf("promote missing subcommand %q", name)
				}
			}
		}
	}
	if !found {
		t.Fatal("root missing promote command")
	}
}

func TestPromoteRequestCreate(t *testing.T) {
	ts, reqs := stubPromoteRequests(t)
	out, err := runCLI(t, "", "promote", "request", "--to", "staging", "--key", "DB_URL", "--note", "ship",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/promote/requests")
	if post == nil {
		t.Fatalf("no POST /v1/promote/requests; reqs=%v", *reqs)
	}
	if post.body["from_config"] != "c-e-dev" {
		t.Fatalf("from_config = %v, want c-e-dev", post.body["from_config"])
	}
	if post.body["to_env"] != "staging" {
		t.Fatalf("to_env = %v, want staging", post.body["to_env"])
	}
	if post.body["note"] != "ship" {
		t.Fatalf("note = %v, want ship", post.body["note"])
	}
	sels, _ := post.body["selections"].([]any)
	if len(sels) != 1 {
		t.Fatalf("selections = %v, want exactly [DB_URL set]", post.body["selections"])
	}
	sel := sels[0].(map[string]any)
	if sel["key"] != "DB_URL" || sel["action"] != "set" {
		t.Fatalf("selection = %v, want {key:DB_URL action:set}", sel)
	}
	if !strings.Contains(out, "rq1") {
		t.Fatalf("output missing request id: %q", out)
	}
}

func TestPromoteRequestList(t *testing.T) {
	ts, reqs := stubPromoteRequests(t)
	out, err := runCLI(t, "", "promote", "requests", "--project", "acme",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	get := findReq(*reqs, "GET", "/v1/promote/requests")
	if get == nil {
		t.Fatalf("no GET /v1/promote/requests; reqs=%v", *reqs)
	}
	if !strings.Contains(out, "rq1") {
		t.Fatalf("output missing request id: %q", out)
	}
}

func TestPromoteRequestApprove(t *testing.T) {
	ts, reqs := stubPromoteRequests(t)
	out, err := runCLI(t, "", "promote", "approve", "rq1",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/promote/requests/rq1/approve")
	if post == nil {
		t.Fatalf("no POST .../rq1/approve; reqs=%v", *reqs)
	}
	if !strings.Contains(out, "4") || !strings.Contains(out, "DB_URL") {
		t.Fatalf("output missing target_version/applied: %q", out)
	}
}

func TestPromoteRequestReject(t *testing.T) {
	ts, reqs := stubPromoteRequests(t)
	_, err := runCLI(t, "", "promote", "reject", "rq1", "--yes", "--note", "not now",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/promote/requests/rq1/reject")
	if post == nil {
		t.Fatalf("no POST .../rq1/reject; reqs=%v", *reqs)
	}
	if post.body["note"] != "not now" {
		t.Fatalf("note = %v, want 'not now'", post.body["note"])
	}
}

func TestPromoteRequestCancel(t *testing.T) {
	ts, reqs := stubPromoteRequests(t)
	_, err := runCLI(t, "", "promote", "cancel", "rq1", "--yes",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/promote/requests/rq1/cancel")
	if post == nil {
		t.Fatalf("no POST .../rq1/cancel; reqs=%v", *reqs)
	}
}
