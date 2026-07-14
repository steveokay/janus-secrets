package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Sentinels pushed through the failure paths. If any of these ever surfaces in a
// rotation_runs / sync_runs column, a raw driver/apply error (or a synced secret
// value) leaked into the durable run history — a security bug, not a test bug.
const (
	// Embedded in a rotation policy's admin_dsn. The postgres rotator cannot
	// connect (port 1) so the raw pgx error may echo host/port/DSN before the
	// engine sanitizes it to the "apply failed" category.
	sentinelRunDSN = "SENTINEL-RUN-CANARY-DSN-7b2a"
	// A real secret VALUE written into the synced config. The strongest check:
	// a synced secret's plaintext must never reach a run row.
	sentinelRunValue = "SENTINEL-RUN-CANARY-VALUE-9f3a"
	// Embedded in a sync target's github address (owner). A raw apply error could
	// echo the target URL/owner.
	sentinelRunAddr = "SENTINEL-RUN-CANARY-ADDR"
)

// rotationErrorWhitelist / syncErrorWhitelist are the ONLY values the engines'
// sanitize() may ever persist in the run row's error column.
var (
	rotationErrorWhitelist = map[string]bool{
		"sealed": true, "apply failed": true, "invalid config": true, "rotation error": true,
	}
	syncErrorWhitelist = map[string]bool{
		"sealed": true, "apply failed": true, "invalid config": true,
		"forbidden reference": true, "sync error": true,
	}
)

// TestNoSecretValueInRunHistoryRows proves the value-free guarantee of the
// durable run history end-to-end against a real recorder. It drives a genuine
// FAILED rotation and a genuine FAILED sync through the HTTP rotate-now /
// sync-now endpoints (each embedding a sentinel in a credential/address field,
// with a known sentinel SECRET VALUE also present in the synced config), then:
//
//  1. asserts each of rotation_runs and sync_runs actually recorded a row (else
//     the test would be vacuous);
//  2. dumps every column of every recorded row and asserts none of the
//     sentinels appears anywhere;
//  3. positively whitelists the error column: every distinct non-null error
//     must be one of the fixed sanitized categories — any other value means a
//     raw error leaked into the column.
func TestNoSecretValueInRunHistoryRows(t *testing.T) {
	ts, srv, adminEmail, adminPass, cid := authStackFull(t)
	admin := login(t, ts.URL, adminEmail, adminPass)
	ctx := context.Background()

	// Seed the secret the rotation policy targets AND a sentinel SECRET VALUE
	// that the sync target will attempt to mirror. The sentinel value's plaintext
	// must never reach any run row.
	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "RUN_KEY", Value: []byte("rotate-me")},
		{Key: "SYNCED_KEY", Value: []byte(sentinelRunValue)},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	// --- Rotation failure path ------------------------------------------------
	// A postgres policy whose admin_dsn embeds the sentinel and points at an
	// unreachable port: the rotator's pgx.Connect fails -> ErrApplyFailed ->
	// category "apply failed". The raw connect error can echo host/port/DSN.
	rawDSN := "postgres://u:" + sentinelRunDSN + "@127.0.0.1:1/nope?sslmode=disable"
	createPolicy := fmt.Sprintf(
		`{"config_id":%q,"secret_key":"RUN_KEY","type":"postgres","interval_seconds":3600,`+
			`"config":{"admin_dsn":%q,"role":"app"}}`, cid, rawDSN)
	var policy struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/rotation/policies", admin, "", createPolicy, &policy); code != http.StatusCreated || policy.ID == "" {
		t.Fatalf("create rotation policy: %d %+v", code, policy)
	}
	// rotate-now must fail (the DSN can't connect).
	if code := doAuthed(t, "POST", ts.URL+"/v1/rotation/policies/"+policy.ID+"/rotate", admin, "", "", nil); code >= 200 && code < 300 {
		t.Fatalf("rotate-now unexpectedly succeeded (code %d); a failure run row is required for this test", code)
	}

	// --- Sync failure path ----------------------------------------------------
	// A github target whose owner embeds the sentinel. Apply first fetches the
	// repo public key from api.github.com with a bogus PAT: a 401 (or, if github
	// is unreachable, a transport error) both map to ErrApplyFailed -> category
	// "apply failed". The synced config carries the sentinel SECRET VALUE.
	createTarget := fmt.Sprintf(
		`{"config_id":%q,"provider":"github","interval_seconds":3600,`+
			`"addr":{"owner":%q,"repo":"r"},"creds":{"pat":"ghp_bogus_should_fail"}}`,
		cid, sentinelRunAddr)
	var target struct {
		ID string `json:"id"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets", admin, "", createTarget, &target); code != http.StatusCreated || target.ID == "" {
		t.Fatalf("create sync target: %d %+v", code, target)
	}
	// sync-now must fail.
	if code := doAuthed(t, "POST", ts.URL+"/v1/sync/targets/"+target.ID+"/sync", admin, "", "", nil); code >= 200 && code < 300 {
		t.Fatalf("sync-now unexpectedly succeeded (code %d); a failure run row is required for this test", code)
	}

	sentinels := []string{sentinelRunDSN, sentinelRunValue, sentinelRunAddr}

	// --- Assertions: rotation_runs -------------------------------------------
	rotRepo := store.NewRotationRepo(srv.st)
	rotRuns, err := rotRepo.ListRuns(ctx, policy.ID, 0, 100)
	if err != nil {
		t.Fatalf("list rotation runs: %v", err)
	}
	if len(rotRuns) == 0 {
		t.Fatal("rotation_runs recorded no rows; the rotate-now flow did not exercise the recorder (test would be vacuous)")
	}
	var rotDump strings.Builder
	sawRotFailure := false
	for _, r := range rotRuns {
		// Dump every column of the row (mirrors the audit leak test's full-column dump).
		fmt.Fprintf(&rotDump, "%d|%s|%s|%s|%s|%s|%s|%s\n",
			r.ID, r.PolicyID, r.StartedAt, r.EndedAt, r.Status,
			derefStr(r.Error), intPtrStr(r.ConfigVersion), fmt.Sprint(r.AttemptNum))
		if r.Status == "failure" {
			sawRotFailure = true
		}
		// Positive whitelist on the error column.
		if r.Error != nil && !rotationErrorWhitelist[*r.Error] {
			t.Fatalf("rotation_runs.error is off-whitelist: %q (a raw error leaked into the column)", *r.Error)
		}
	}
	if !sawRotFailure {
		t.Fatal("rotation_runs has no failure row; the forced apply failure was not recorded")
	}
	for _, s := range sentinels {
		if strings.Contains(rotDump.String(), s) {
			t.Fatalf("sentinel %q leaked into a rotation_runs row", s)
		}
	}

	// --- Assertions: sync_runs -----------------------------------------------
	syncRepo := store.NewSyncTargetRepo(srv.st)
	syncRuns, err := syncRepo.ListRuns(ctx, target.ID, 0, 100)
	if err != nil {
		t.Fatalf("list sync runs: %v", err)
	}
	if len(syncRuns) == 0 {
		t.Fatal("sync_runs recorded no rows; the sync-now flow did not exercise the recorder (test would be vacuous)")
	}
	var syncDump strings.Builder
	sawSyncFailure := false
	for _, r := range syncRuns {
		fmt.Fprintf(&syncDump, "%d|%s|%s|%s|%s|%s|%s|%s|%s\n",
			r.ID, r.TargetID, r.StartedAt, r.EndedAt, r.Status,
			derefStr(r.Error), intPtrStr(r.ConfigVersion), fmt.Sprint(r.KeysCount), fmt.Sprint(r.AttemptNum))
		if r.Status == "failure" {
			sawSyncFailure = true
		}
		if r.Error != nil && !syncErrorWhitelist[*r.Error] {
			t.Fatalf("sync_runs.error is off-whitelist: %q (a raw error leaked into the column)", *r.Error)
		}
	}
	if !sawSyncFailure {
		t.Fatal("sync_runs has no failure row; the forced apply failure was not recorded")
	}
	for _, s := range sentinels {
		if strings.Contains(syncDump.String(), s) {
			t.Fatalf("sentinel %q leaked into a sync_runs row", s)
		}
	}
}

// intPtrStr renders an optional int column for the leak dump.
func intPtrStr(p *int) string {
	if p == nil {
		return ""
	}
	return fmt.Sprint(*p)
}
