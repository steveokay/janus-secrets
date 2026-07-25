package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// syncVerifyStateResp mirrors syncVerifyStateDTO for decoding.
type syncVerifyStateResp struct {
	Enabled         bool    `json:"enabled"`
	IntervalSeconds int64   `json:"interval_seconds"`
	LastStatus      *string `json:"last_status"`
	LastDriftCount  int     `json:"last_drift_count"`
	Capability      string  `json:"capability"`
}

// TestSyncVerifyEndpointsViaAPI drives the drift-detection endpoints as admin:
// the target view carries verification state with the provider's honest
// capability, the schedule can be paced/disabled, verify-runs is empty until a
// pass happens, and a github (write-only) target reports names_only — never a
// value comparison.
//
// It deliberately does not assert a successful verification network call: a
// booted server's github base URL points at api.github.com with no setter, so a
// real pass would need live GitHub (mirrors TestSyncCRUDViaAPI's reasoning).
// The engine-level verification behavior is covered in internal/secretsync.
func TestSyncVerifyEndpointsViaAPI(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "API_KEY", Value: []byte("v1")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	createBody := `{"config_id":"` + cid + `","provider":"github","interval_seconds":3600,` +
		`"addr":{"owner":"o","repo":"r"},"creds":{"pat":"ghp_x"}}`
	var created struct {
		ID        string               `json:"id"`
		ProjectID string               `json:"project_id"`
		Verify    *syncVerifyStateResp `json:"verify"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", admin, "", createBody, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}

	// GET carries the verify block with the provider's declared capability.
	var got struct {
		Verify *syncVerifyStateResp `json:"verify"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+created.ID, admin, "", "", &got); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if got.Verify == nil {
		t.Fatal("get response carried no verify block")
	}
	if got.Verify.Capability != "names_only" {
		t.Fatalf("github capability = %q, want names_only", got.Verify.Capability)
	}
	if !got.Verify.Enabled || got.Verify.IntervalSeconds != 3600 {
		t.Fatalf("default verify state = %+v", got.Verify)
	}

	// LIST carries it too (batch-loaded).
	var list struct {
		Targets []struct {
			ID     string               `json:"id"`
			Verify *syncVerifyStateResp `json:"verify"`
		} `json:"targets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets?project_id="+created.ProjectID, admin, "", "", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	found := false
	for _, tg := range list.Targets {
		if tg.ID == created.ID {
			found = true
			if tg.Verify == nil || tg.Verify.Capability != "names_only" {
				t.Fatalf("list verify block = %+v", tg.Verify)
			}
		}
	}
	if !found {
		t.Fatal("list missing the created target")
	}

	// Schedule knobs.
	var sched syncVerifyStateResp
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/sync/targets/"+created.ID+"/verify-schedule", admin, "",
		`{"enabled":false,"interval_seconds":7200}`, &sched); code != http.StatusOK {
		t.Fatalf("patch verify-schedule: %d", code)
	}
	if sched.Enabled || sched.IntervalSeconds != 7200 {
		t.Fatalf("verify schedule = %+v", sched)
	}
	// Validation: sub-minute pacing is rejected.
	if code := doAuthed(t, "PATCH", ts.URL+"/v1/sync/targets/"+created.ID+"/verify-schedule", admin, "",
		`{"interval_seconds":5}`, nil); code != http.StatusBadRequest {
		t.Fatalf("patch with a 5s interval: want 400, got %d", code)
	}

	// History is empty until a pass runs.
	var runs struct {
		Runs       []map[string]any `json:"runs"`
		NextCursor *int64           `json:"next_cursor"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+created.ID+"/verify-runs", admin, "", "", &runs); code != http.StatusOK {
		t.Fatalf("verify-runs: %d", code)
	}
	if len(runs.Runs) != 0 {
		t.Fatalf("verify-runs = %+v, want empty", runs.Runs)
	}
}

// TestSyncVerifyForbiddenForNonAdmin confirms the drift endpoints reuse
// sync:manage and are denied by default to lower instance roles.
func TestSyncVerifyForbiddenForNonAdmin(t *testing.T) {
	ts, _, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	createBody := `{"config_id":"` + cid + `","provider":"github","interval_seconds":3600,` +
		`"addr":{"owner":"o","repo":"r"},"creds":{"pat":"ghp_x"}}`
	var created struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", admin, "", createBody, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}

	_, devPass := makeUser(t, ts.URL, admin, "syncverifydev@corp.io", "developer")
	dev := login(t, ts.URL, "syncverifydev@corp.io", devPass)

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/v1/sync/targets/" + created.ID + "/verify", ""},
		{"GET", "/v1/sync/targets/" + created.ID + "/verify-runs", ""},
		{"PATCH", "/v1/sync/targets/" + created.ID + "/verify-schedule", `{"enabled":false}`},
	} {
		if code := doAuthed(t, tc.method, ts.URL+tc.path, dev, "", tc.body, nil); code != http.StatusForbidden {
			t.Errorf("%s %s as developer: want 403, got %d", tc.method, tc.path, code)
		}
	}
}

// TestSyncVerifyResponseIsValueFree asserts the verify DTOs never carry a
// creds field or a secret value, using the same masking contract as the CRUD
// endpoints.
func TestSyncVerifyResponseIsValueFree(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	const secretValue = "VALUE-fixture-must-not-appear-11ff"
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "API_KEY", Value: []byte(secretValue)},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	const rawPAT = "ghp_verify_should_not_leak"
	createBody := `{"config_id":"` + cid + `","provider":"github","interval_seconds":3600,` +
		`"addr":{"owner":"o","repo":"r"},"creds":{"pat":"` + rawPAT + `"}}`
	var created struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", admin, "", createBody, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}

	for _, path := range []string{
		"/v1/sync/targets/" + created.ID,
		"/v1/sync/targets/" + created.ID + "/verify-runs",
	} {
		req, err := http.NewRequest("GET", ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := readAllString(t, resp)
		for _, needle := range []string{secretValue, rawPAT, `"pat"`, "creds"} {
			if strings.Contains(body, needle) {
				t.Fatalf("GET %s leaked %q: %s", path, needle, body)
			}
		}
	}
}
