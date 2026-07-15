package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// doAuthedIdem is doAuthed with an Idempotency-Key header. It returns the status
// code and the raw response body so tests can assert replay identity.
func doAuthedIdem(t *testing.T, method, url, cookie, idemKey, body string, out any) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, string(raw)
}

// TestPromoteApplyIdempotencyE2E verifies that Idempotency-Key on POST /v1/promote
// (1) replays the original result without creating a second target version,
// (2) 409s when the same key is reused with a different body, and
// (3) releases the claim when apply fails so a retry with the same key can proceed.
func TestPromoteApplyIdempotencyE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "promoidem", "Promote Idempotency Project")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := srv.service.CreateEnvironment(ctx, p.ID, "dev", "Dev")
	if err != nil {
		t.Fatal(err)
	}
	stg, err := srv.service.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewPipelineRepo(srv.st).Set(ctx, p.ID, []string{dev.ID, stg.ID}); err != nil {
		t.Fatal(err)
	}
	devCfg, err := srv.service.CreateConfig(ctx, dev.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	stgCfg, err := srv.service.CreateConfig(ctx, stg.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	cv, err := srv.service.SetSecrets(ctx, devCfg.ID, []secrets.SecretChange{
		{Key: "A", Value: []byte("aval")},
		{Key: "B", Value: []byte("bval")},
	}, "seed", "test")
	if err != nil {
		t.Fatal(err)
	}
	srcVersion := cv.Version

	applyBody := `{"from_config":"` + devCfg.ID + `","to_config":"` + stgCfg.ID +
		`","source_version":` + strconv.Itoa(srcVersion) + `,"selections":[{"key":"B","action":"set"}]}`

	// --- Replay: first apply with Idempotency-Key k1 -> 200 ---
	var first struct {
		TargetVersion int      `json:"target_version"`
		Applied       []string `json:"applied"`
	}
	code, firstBody := doAuthedIdem(t, "POST", ts.URL+"/v1/promote", ownerCookie, "k1", applyBody, &first)
	if code != http.StatusOK {
		t.Fatalf("first apply: want 200, got %d (%s)", code, firstBody)
	}
	if len(first.Applied) != 1 || first.Applied[0] != "B" {
		t.Fatalf("first apply: want applied [B], got %+v", first.Applied)
	}
	firstVer := first.TargetVersion

	stgVerAfterFirst, err := srv.service.LatestVersion(ctx, stgCfg.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Retry the IDENTICAL request + key -> 200, SAME body, NO new version.
	var second struct {
		TargetVersion int `json:"target_version"`
	}
	code, secondBody := doAuthedIdem(t, "POST", ts.URL+"/v1/promote", ownerCookie, "k1", applyBody, &second)
	if code != http.StatusOK {
		t.Fatalf("replay apply: want 200, got %d (%s)", code, secondBody)
	}
	if second.TargetVersion != firstVer {
		t.Fatalf("replay apply: want same target_version %d, got %d", firstVer, second.TargetVersion)
	}
	// The replayed body is served from stored jsonb (keys may be reordered /
	// whitespace normalized by Postgres), so compare semantically.
	var firstJSON, secondJSON map[string]any
	if err := json.Unmarshal([]byte(firstBody), &firstJSON); err != nil {
		t.Fatalf("unmarshal first body: %v (%s)", err, firstBody)
	}
	if err := json.Unmarshal([]byte(secondBody), &secondJSON); err != nil {
		t.Fatalf("unmarshal replay body: %v (%s)", err, secondBody)
	}
	if !reflect.DeepEqual(firstJSON, secondJSON) {
		t.Fatalf("replay apply: body mismatch\n first: %s\nsecond: %s", firstBody, secondBody)
	}
	stgVerAfterReplay, err := srv.service.LatestVersion(ctx, stgCfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stgVerAfterReplay != stgVerAfterFirst {
		t.Fatalf("replay created a new version: before=%d after=%d", stgVerAfterFirst, stgVerAfterReplay)
	}

	// --- Conflict: same key k1, different body -> 409 idempotency_key_conflict ---
	conflictBody := `{"from_config":"` + devCfg.ID + `","to_config":"` + stgCfg.ID +
		`","source_version":` + strconv.Itoa(srcVersion) + `,"selections":[{"key":"A","action":"set"}]}`
	var conflictErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	code, cBody := doAuthedIdem(t, "POST", ts.URL+"/v1/promote", ownerCookie, "k1", conflictBody, &conflictErr)
	if code != http.StatusConflict {
		t.Fatalf("conflict apply: want 409, got %d (%s)", code, cBody)
	}
	if conflictErr.Error.Code != "idempotency_key_conflict" {
		t.Fatalf("conflict apply: want code idempotency_key_conflict, got %q (%s)", conflictErr.Error.Code, cBody)
	}

	// --- Release on failure: key k2 with a LOCKED key fails (409), then the same
	// key succeeds after unlock (proves the claim was released, not stuck). ---
	if err := store.NewLockedKeyRepo(srv.st).Lock(ctx, stgCfg.ID, "A", ""); err != nil {
		t.Fatal(err)
	}
	lockedBody := `{"from_config":"` + devCfg.ID + `","to_config":"` + stgCfg.ID +
		`","source_version":` + strconv.Itoa(srcVersion) + `,"selections":[{"key":"A","action":"set"}]}`
	code, lBody := doAuthedIdem(t, "POST", ts.URL+"/v1/promote", ownerCookie, "k2", lockedBody, nil)
	if code != http.StatusConflict {
		t.Fatalf("locked apply: want 409, got %d (%s)", code, lBody)
	}
	// Unlock and retry with the SAME key k2 -> 200 (claim was released).
	if err := store.NewLockedKeyRepo(srv.st).Unlock(ctx, stgCfg.ID, "A"); err != nil {
		t.Fatal(err)
	}
	var retry struct {
		Applied []string `json:"applied"`
	}
	code, rBody := doAuthedIdem(t, "POST", ts.URL+"/v1/promote", ownerCookie, "k2", lockedBody, &retry)
	if code != http.StatusOK {
		t.Fatalf("post-release retry apply: want 200, got %d (%s)", code, rBody)
	}
	if len(retry.Applied) != 1 || retry.Applied[0] != "A" {
		t.Fatalf("post-release retry apply: want applied [A], got %+v", retry.Applied)
	}
}
