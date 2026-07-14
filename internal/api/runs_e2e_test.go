package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// intPtr / strPtr are small helpers for the optional run fields.
func intPtr(n int) *int            { return &n }
func strPtrLocal(s string) *string { return &s }

// createRotationPolicy seeds a secret + a webhook rotation policy as admin and
// returns the created policy id and its project id.
func createRotationPolicy(t *testing.T, ts, admin, cid string) (id, projectID string) {
	t.Helper()
	createBody := `{"config_id":"` + cid + `","secret_key":"RUN_KEY","type":"webhook","interval_seconds":3600,` +
		`"config":{"url":"https://example.invalid/rotate"}}`
	var created struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
	}
	if code := doAuthed(t, "POST", ts+"/v1/rotation/policies", admin, "", createBody, &created); code != http.StatusCreated {
		t.Fatalf("create policy: %d", code)
	}
	if created.ID == "" {
		t.Fatalf("created policy id empty")
	}
	return created.ID, created.ProjectID
}

// TestRotationRunsEndpoint records two runs for a policy directly via the store,
// then exercises GET /v1/rotation/policies/{id}/runs: 200 with newest-first
// ordering + correct wire shape, limit=1 paging (non-nil next_cursor), 403 for a
// principal lacking rotation:manage, and 400 for out-of-range limit.
func TestRotationRunsEndpoint(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)

	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "RUN_KEY", Value: []byte("v1")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	pid, projectID := createRotationPolicy(t, ts.URL, admin, cid)

	// Record two runs directly (newest = the second inserted, higher id).
	repo := store.NewRotationRepo(srv.st)
	base := time.Now().UTC().Truncate(time.Second)
	if err := repo.InsertRun(context.Background(), store.RotationRunInput{
		PolicyID: pid, StartedAt: base, EndedAt: base.Add(time.Second),
		Status: "failure", Error: strPtrLocal("boom"), AttemptNum: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertRun(context.Background(), store.RotationRunInput{
		PolicyID: pid, StartedAt: base.Add(2 * time.Second), EndedAt: base.Add(3 * time.Second),
		Status: "success", ConfigVersion: intPtr(7), AttemptNum: 0,
	}); err != nil {
		t.Fatal(err)
	}

	// Full list: newest-first, correct shape.
	var resp struct {
		Runs []struct {
			ID            int64   `json:"id"`
			StartedAt     string  `json:"started_at"`
			EndedAt       string  `json:"ended_at"`
			Status        string  `json:"status"`
			Error         *string `json:"error,omitempty"`
			ConfigVersion *int    `json:"config_version,omitempty"`
			AttemptNum    int     `json:"attempt_num"`
		} `json:"runs"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+pid+"/runs", admin, "", "", &resp); code != http.StatusOK {
		t.Fatalf("runs: %d", code)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("want 2 runs, got %d: %+v", len(resp.Runs), resp.Runs)
	}
	if resp.Runs[0].ID <= resp.Runs[1].ID {
		t.Fatalf("runs not newest-first by id DESC: %d then %d", resp.Runs[0].ID, resp.Runs[1].ID)
	}
	if resp.Runs[0].Status != "success" || resp.Runs[0].ConfigVersion == nil || *resp.Runs[0].ConfigVersion != 7 {
		t.Fatalf("newest run shape = %+v", resp.Runs[0])
	}
	if resp.Runs[1].Status != "failure" || resp.Runs[1].Error == nil || *resp.Runs[1].Error != "boom" {
		t.Fatalf("older run shape = %+v", resp.Runs[1])
	}
	if resp.NextCursor != nil {
		t.Fatalf("full list should have nil next_cursor, got %v", *resp.NextCursor)
	}

	// limit=1 → one run + non-nil next_cursor equal to that run's id.
	var page struct {
		Runs []struct {
			ID int64 `json:"id"`
		} `json:"runs"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+pid+"/runs?limit=1", admin, "", "", &page); code != http.StatusOK {
		t.Fatalf("runs limit=1: %d", code)
	}
	if len(page.Runs) != 1 {
		t.Fatalf("limit=1 want 1 run, got %d", len(page.Runs))
	}
	if page.NextCursor == nil || *page.NextCursor != page.Runs[0].ID {
		t.Fatalf("limit=1 next_cursor = %v, want %d", page.NextCursor, page.Runs[0].ID)
	}

	// A developer lacks rotation:manage → 403 (mirrors the GET-one authz path).
	_, devPass := makeUser(t, ts.URL, admin, "runsdev@corp.io", "developer")
	dev := login(t, ts.URL, "runsdev@corp.io", devPass)
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+pid+"/runs", dev, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("developer runs: want 403, got %d", code)
	}
	_ = projectID

	// Bad paging → 400.
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+pid+"/runs?limit=0", admin, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("limit=0: want 400, got %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/rotation/policies/"+pid+"/runs?limit=101", admin, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("limit=101: want 400, got %d", code)
	}
}

// TestSyncRunsEndpoint records two runs for a sync target and exercises
// GET /v1/sync/targets/{id}/runs, asserting keys_count is present in the DTO,
// newest-first ordering, limit=1 paging, 403 for a non-manager, and 400 on bad limit.
func TestSyncRunsEndpoint(t *testing.T) {
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
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", admin, "", createBody, &created); code != http.StatusCreated {
		t.Fatalf("create target: %d", code)
	}
	tid := created.ID

	repo := store.NewSyncTargetRepo(srv.st)
	base := time.Now().UTC().Truncate(time.Second)
	if err := repo.InsertRun(context.Background(), store.SyncRunInput{
		TargetID: tid, StartedAt: base, EndedAt: base.Add(time.Second),
		Status: "failure", Error: strPtrLocal("net"), AttemptNum: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertRun(context.Background(), store.SyncRunInput{
		TargetID: tid, StartedAt: base.Add(2 * time.Second), EndedAt: base.Add(3 * time.Second),
		Status: "success", ConfigVersion: intPtr(4), KeysCount: 3, AttemptNum: 0,
	}); err != nil {
		t.Fatal(err)
	}

	rawResp, raw := rawGet(t, ts.URL+"/v1/sync/targets/"+tid+"/runs", admin)
	if rawResp != http.StatusOK {
		t.Fatalf("runs: %d %s", rawResp, raw)
	}
	// keys_count must be present in the wire shape.
	if !strings.Contains(raw, `"keys_count"`) {
		t.Fatalf("runs response missing keys_count: %s", raw)
	}
	var resp struct {
		Runs []struct {
			ID            int64   `json:"id"`
			Status        string  `json:"status"`
			ConfigVersion *int    `json:"config_version,omitempty"`
			KeysCount     int     `json:"keys_count"`
			Error         *string `json:"error,omitempty"`
			AttemptNum    int     `json:"attempt_num"`
		} `json:"runs"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if err := decodeInto(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(resp.Runs))
	}
	if resp.Runs[0].ID <= resp.Runs[1].ID {
		t.Fatalf("runs not newest-first: %d then %d", resp.Runs[0].ID, resp.Runs[1].ID)
	}
	if resp.Runs[0].Status != "success" || resp.Runs[0].KeysCount != 3 {
		t.Fatalf("newest run shape = %+v", resp.Runs[0])
	}

	// limit=1 → non-nil next_cursor.
	var page struct {
		Runs []struct {
			ID int64 `json:"id"`
		} `json:"runs"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+tid+"/runs?limit=1", admin, "", "", &page); code != http.StatusOK {
		t.Fatalf("runs limit=1: %d", code)
	}
	if len(page.Runs) != 1 || page.NextCursor == nil || *page.NextCursor != page.Runs[0].ID {
		t.Fatalf("limit=1 = %+v cursor=%v", page.Runs, page.NextCursor)
	}

	// Developer lacks sync:manage → 403.
	_, devPass := makeUser(t, ts.URL, admin, "syncrunsdev@corp.io", "developer")
	dev := login(t, ts.URL, "syncrunsdev@corp.io", devPass)
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+tid+"/runs", dev, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("developer sync runs: want 403, got %d", code)
	}

	// Bad limit → 400.
	if code := doAuthed(t, "GET", ts.URL+"/v1/sync/targets/"+tid+"/runs?limit=101", admin, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("limit=101: want 400, got %d", code)
	}
}
