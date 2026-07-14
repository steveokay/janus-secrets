package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordedReq captures the method/path/body a handler received on the wire.
type recordedReq struct {
	method string
	path   string
	body   map[string]any
}

// stubPromotion scripts the resolution routes (projects/environments/configs) plus
// the pipeline / locked-keys / promote routes for CLI tests, recording every request.
func stubPromotion(t *testing.T) (*httptest.Server, *[]recordedReq) {
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

	// Pipeline.
	mux.HandleFunc("GET /v1/projects/{pid}/pipeline", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"environment_ids": []string{"e-dev", "e-stg"},
		})
	})
	mux.HandleFunc("PUT /v1/projects/{pid}/pipeline", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"environment_ids": []string{"e-dev", "e-stg"}})
	})

	// Locked keys.
	mux.HandleFunc("POST /v1/configs/{cid}/locked-keys", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []string{"DATABASE_URL"}})
	})
	mux.HandleFunc("DELETE /v1/configs/{cid}/locked-keys/{key}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []string{}})
	})

	// Promote preview + apply.
	mux.HandleFunc("GET /v1/promote/preview", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"source_version": 3,
			"target_exists":  true,
			"entries": []map[string]any{
				{"key": "A", "status": "same", "source_value": "x", "target_value": "x", "locked": false},
				{"key": "B", "status": "change", "source_value": "new", "target_value": "old", "locked": false},
				{"key": "C", "status": "add", "source_value": "z", "target_value": "", "locked": true},
			},
		})
	})
	mux.HandleFunc("POST /v1/promote", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"target_version": 4,
			"applied":        []string{"B"},
			"skipped":        []string{},
		})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &reqs
}

func findReq(reqs []recordedReq, method, path string) *recordedReq {
	for i := range reqs {
		if reqs[i].method == method && reqs[i].path == path {
			return &reqs[i]
		}
	}
	return nil
}

func TestPromotionCmdsRegistered(t *testing.T) {
	root := newRootCmd()
	want := map[string]bool{"promote": false, "pipeline": false}
	for _, sub := range root.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root missing subcommand %q", name)
		}
	}
}

func TestPipelineSet(t *testing.T) {
	ts, reqs := stubPromotion(t)
	out, err := runCLI(t, "", "pipeline", "set", "proj", "dev", "staging",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	put := findReq(*reqs, "PUT", "/v1/projects/p1/pipeline")
	if put == nil {
		t.Fatalf("no PUT /v1/projects/p1/pipeline; reqs=%v", *reqs)
	}
	ids, _ := put.body["environment_ids"].([]any)
	if len(ids) != 2 || ids[0] != "e-dev" || ids[1] != "e-stg" {
		t.Fatalf("environment_ids = %v, want [e-dev e-stg]", put.body["environment_ids"])
	}
	if !strings.Contains(out, "dev") || !strings.Contains(out, "staging") {
		t.Fatalf("output missing env slugs: %q", out)
	}
}

func TestPipelineSetUnknownEnv(t *testing.T) {
	ts, _ := stubPromotion(t)
	_, err := runCLI(t, "", "pipeline", "set", "proj", "dev", "nope",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err == nil {
		t.Fatal("expected error for unknown env slug, got nil")
	}
}

func TestPipelineGet(t *testing.T) {
	ts, _ := stubPromotion(t)
	out, err := runCLI(t, "", "pipeline", "get", "proj",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dev") || !strings.Contains(out, "staging") {
		t.Fatalf("output missing pipeline env slugs: %q", out)
	}
}

func TestSecretsLock(t *testing.T) {
	ts, reqs := stubPromotion(t)
	out, err := runCLI(t, "", "secrets", "lock", "DATABASE_URL",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/configs/c-e-dev/locked-keys")
	if post == nil {
		t.Fatalf("no POST locked-keys; reqs=%v", *reqs)
	}
	if post.body["key"] != "DATABASE_URL" {
		t.Fatalf("body key = %v, want DATABASE_URL", post.body["key"])
	}
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("output missing key: %q", out)
	}
}

func TestSecretsUnlock(t *testing.T) {
	ts, reqs := stubPromotion(t)
	out, err := runCLI(t, "", "secrets", "unlock", "DATABASE_URL",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err != nil {
		t.Fatal(err)
	}
	del := findReq(*reqs, "DELETE", "/v1/configs/c-e-dev/locked-keys/DATABASE_URL")
	if del == nil {
		t.Fatalf("no DELETE locked-keys/DATABASE_URL; reqs=%v", *reqs)
	}
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("output missing key: %q", out)
	}
}

func TestPromoteDryRun(t *testing.T) {
	ts, reqs := stubPromotion(t)
	out, err := runCLI(t, "", "promote", "--to", "staging", "--dry-run",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err != nil {
		t.Fatal(err)
	}
	if findReq(*reqs, "GET", "/v1/promote/preview") == nil {
		t.Fatalf("no preview GET; reqs=%v", *reqs)
	}
	if findReq(*reqs, "POST", "/v1/promote") != nil {
		t.Fatalf("dry-run must not POST /v1/promote; reqs=%v", *reqs)
	}
	// Table shows keys + statuses, never secret values.
	if !strings.Contains(out, "B") || !strings.Contains(out, "change") {
		t.Fatalf("dry-run table missing key/status: %q", out)
	}
	for _, val := range []string{"new", "old", "z"} {
		if strings.Contains(out, val) {
			t.Fatalf("dry-run leaked secret value %q: %q", val, out)
		}
	}
}

func TestPromoteApplyKey(t *testing.T) {
	ts, reqs := stubPromotion(t)
	out, err := runCLI(t, "", "promote", "--to", "staging", "--key", "B",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err != nil {
		t.Fatal(err)
	}
	post := findReq(*reqs, "POST", "/v1/promote")
	if post == nil {
		t.Fatalf("no POST /v1/promote; reqs=%v", *reqs)
	}
	if post.body["from_config"] != "c-e-dev" || post.body["to_config"] != "c-e-stg" {
		t.Fatalf("from/to config = %v/%v, want c-e-dev/c-e-stg",
			post.body["from_config"], post.body["to_config"])
	}
	sels, _ := post.body["selections"].([]any)
	if len(sels) != 1 {
		t.Fatalf("selections = %v, want exactly [B set]", post.body["selections"])
	}
	sel := sels[0].(map[string]any)
	if sel["key"] != "B" || sel["action"] != "set" {
		t.Fatalf("selection = %v, want {key:B action:set}", sel)
	}
	if sv, ok := post.body["source_version"].(float64); !ok || int(sv) != 3 {
		t.Fatalf("source_version = %v, want 3", post.body["source_version"])
	}
	if !strings.Contains(out, "B") {
		t.Fatalf("output missing applied key: %q", out)
	}
}

func TestPromoteLockedKeySkipped(t *testing.T) {
	ts, reqs := stubPromotion(t)
	// C is add+locked; selecting it should be skipped with a warning, leaving no selections.
	_, err := runCLI(t, "", "promote", "--to", "staging", "--key", "C",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err == nil {
		// No selectable keys -> should error rather than POST an empty promotion.
		if findReq(*reqs, "POST", "/v1/promote") != nil {
			t.Fatalf("locked-only selection must not POST /v1/promote; reqs=%v", *reqs)
		}
	}
}

func TestPromoteAllAndKeyMutuallyExclusive(t *testing.T) {
	ts, _ := stubPromotion(t)
	_, err := runCLI(t, "", "promote", "--to", "staging", "--all", "--key", "B",
		"--address", ts.URL, "--token", "janus_svc_test",
		"--project", "proj", "--env", "dev", "--config", "default")
	if err == nil {
		t.Fatal("expected error when --all and --key both set")
	}
}
