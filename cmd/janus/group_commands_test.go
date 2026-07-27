package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubGroups serves the routes the group commands touch, and records what was
// PUT so the tests can assert the request the CLI actually built.
func stubGroups(t *testing.T) (*httptest.Server, *map[string]string) {
	t.Helper()
	seen := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("GET /v1/groups", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"groups": []map[string]any{
			{"id": "g1", "name": "payments", "kind": "local", "claim_value": nil, "member_count": 3, "binding_count": 1},
			{"id": "g2", "name": "platform", "kind": "oidc", "claim_value": "grp-platform", "member_count": 9, "binding_count": 2},
		}})
	})
	mux.HandleFunc("POST /v1/groups", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		out, _ := json.Marshal(body)
		seen["create"] = string(out)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "g9", "name": body["name"], "kind": body["kind"]})
	})
	for _, path := range []string{
		"PUT /v1/instance/group-members/g1",
		"PUT /v1/projects/p1/group-members/g1",
		"PUT /v1/projects/p1/environments/e1/group-members/g1",
	} {
		p := path
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			seen["bind"] = p + " role=" + body["role"].(string)
			w.WriteHeader(204)
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &seen
}

func runGroupCmd(t *testing.T, ts *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(args, "--address", ts.URL, "--token", "janus_svc_test"))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestGroupListRendersKindAndClaim(t *testing.T) {
	ts, _ := stubGroups(t)
	stdout, _, err := runGroupCmd(t, ts, "group", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "payments") || !strings.Contains(stdout, "grp-platform") {
		t.Fatalf("list output = %q", stdout)
	}
	// A local group has no claim value; it must render as "-", not "<nil>".
	if strings.Contains(stdout, "<nil>") {
		t.Fatalf("nil claim rendered raw: %q", stdout)
	}
}

// The scope flags must select the same three routes the members commands use.
func TestGroupBindScopeRouting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"instance", []string{"group", "bind", "payments", "--role", "admin"},
			"PUT /v1/instance/group-members/g1 role=admin"},
		{"project", []string{"group", "bind", "payments", "--role", "developer", "--project", "acme"},
			"PUT /v1/projects/p1/group-members/g1 role=developer"},
		{"environment", []string{"group", "bind", "payments", "--role", "viewer", "--project", "acme", "--env", "prod"},
			"PUT /v1/projects/p1/environments/e1/group-members/g1 role=viewer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, seen := stubGroups(t)
			if _, _, err := runGroupCmd(t, ts, tc.args...); err != nil {
				t.Fatalf("bind: %v", err)
			}
			if (*seen)["bind"] != tc.want {
				t.Fatalf("bind = %q, want %q", (*seen)["bind"], tc.want)
			}
		})
	}
}

// Owner is refused locally, so the mistake is caught at the keyboard rather
// than round-tripping to a server 400.
func TestGroupBindRejectsOwnerLocally(t *testing.T) {
	ts, seen := stubGroups(t)
	_, _, err := runGroupCmd(t, ts, "group", "bind", "payments", "--role", "owner")
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("err = %v, want a local rejection mentioning owner", err)
	}
	if (*seen)["bind"] != "" {
		t.Fatal("an owner bind must never reach the server")
	}
}

func TestGroupCreateValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"oidc without a claim", []string{"group", "create", "a", "--kind", "oidc"}, "--claim is required"},
		{"local with a claim", []string{"group", "create", "b", "--kind", "local", "--claim", "x"}, "only meaningful"},
		{"unknown kind", []string{"group", "create", "c", "--kind", "ldap"}, "oidc|local"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, seen := stubGroups(t)
			_, _, err := runGroupCmd(t, ts, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if (*seen)["create"] != "" {
				t.Fatal("an invalid group must never reach the server")
			}
		})
	}
}

func TestGroupCreateSendsClaimValue(t *testing.T) {
	ts, seen := stubGroups(t)
	if _, _, err := runGroupCmd(t, ts, "group", "create", "payments", "--kind", "oidc", "--claim", "grp-payments"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains((*seen)["create"], `"claim_value":"grp-payments"`) {
		t.Fatalf("create body = %s", (*seen)["create"])
	}
}

// A group can be addressed by its unique name; an unknown one fails with a
// clear message instead of a 404 from a URL built out of the typo.
func TestGroupResolveByNameOrID(t *testing.T) {
	ts, _ := stubGroups(t)
	stdout, _, err := runGroupCmd(t, ts, "group", "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var groups []groupSummary
	if err := json.Unmarshal([]byte(stdout), &groups); err != nil {
		t.Fatalf("json output: %v", err)
	}
	if len(groups) != 2 || groups[0].Name != "payments" {
		t.Fatalf("groups = %+v", groups)
	}

	_, _, err = runGroupCmd(t, ts, "group", "bind", "nope", "--role", "viewer")
	if err == nil || !strings.Contains(err.Error(), "no group named") {
		t.Fatalf("err = %v, want a clear unknown-group error", err)
	}
}
