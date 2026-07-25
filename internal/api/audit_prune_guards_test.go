package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestCombinePruneCeilings is the pure unit test over the ceiling fold that
// backs both audit-prune guards. -1 means "this constraint imposes no ceiling";
// any non-negative value is a hard "do not prune past this seq". Every active
// constraint must hold, so the fold is a min over the active ones.
func TestCombinePruneCeilings(t *testing.T) {
	tests := []struct {
		name     string
		ceilings []int64
		want     int64
	}{
		{"no constraints at all", nil, -1},
		{"all inactive", []int64{-1, -1}, -1},
		{"only ship active", []int64{7, -1}, 7},
		{"only retention active", []int64{-1, 7}, 7},
		{"ship stricter", []int64{3, 9}, 3},
		{"retention stricter", []int64{9, 3}, 3},
		{"equal", []int64{5, 5}, 5},
		{"zero is active and wins", []int64{0, 9}, 0},
		{"zero beats unbounded", []int64{-1, 0}, 0},
		{"more negative sentinels are still inactive", []int64{-5, 4}, 4},
		{"three constraints", []int64{8, 4, 6}, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := combinePruneCeilings(tc.ceilings...); got != tc.want {
				t.Fatalf("combinePruneCeilings(%v) = %d, want %d", tc.ceilings, got, tc.want)
			}
		})
	}
}

// mintEvents generates n auditable events (token mints) to populate the chain.
func mintEvents(t *testing.T, baseURL, cookie, configID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`{"name":"g%d","scope":{"kind":"config","id":%q},"access":"read"}`, i, configID)
		if code := doAuthed(t, "POST", baseURL+"/v1/tokens", cookie, "", body, nil); code != 200 {
			t.Fatalf("mint %d: %d", i, code)
		}
	}
}

// createCheckpoint anchors the current chain head and returns its through_seq.
func createCheckpoint(t *testing.T, baseURL, cookie string) int64 {
	t.Helper()
	var cp checkpointResp
	if code := doAuthed(t, "POST", baseURL+"/v1/audit/checkpoint", cookie, "", "", &cp); code != 200 {
		t.Fatalf("create checkpoint: %d", code)
	}
	if cp.Checkpoint == nil || !cp.Checkpoint.MACValid || cp.Checkpoint.ThroughSeq == 0 {
		t.Fatalf("checkpoint = %+v", cp.Checkpoint)
	}
	return cp.Checkpoint.ThroughSeq
}

type pruneResult struct {
	PrunedThrough int64 `json:"pruned_through"`
	Deleted       int64 `json:"deleted"`
}

// TestAuditPruneShipHWMGuardSurvivesUnwiredShipper is the L-8 regression test.
//
// The durable ship high-water mark (audit_ship_state, migration 000034) records
// what has already been streamed to the external SIEM. Previously the guard was
// consulted ONLY when an auditship.Service happened to be wired in the current
// process, so an instance that shipped for months and then had its
// JANUS_AUDIT_SHIP_* configuration removed silently lost the "never prune
// un-shipped events" protection on its next boot.
//
// Here the server is booted with NO shipper (srv.auditShip == nil) but the
// persisted mark is set below the checkpoint anchor: the prune point MUST still
// be clamped to the mark.
func TestAuditPruneShipHWMGuardSurvivesUnwiredShipper(t *testing.T) {
	ts, srv, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	if srv.auditShip != nil {
		t.Fatal("precondition: this stack must boot with NO shipper wired")
	}

	mintEvents(t, ts.URL, cookie, configID, 4)
	anchorSeq := createCheckpoint(t, ts.URL, cookie)
	if anchorSeq < 3 {
		t.Fatalf("need a few events before the anchor, got seq %d", anchorSeq)
	}

	// Simulate shipping history: everything up to anchorSeq-2 has been shipped.
	hwm := anchorSeq - 2
	if err := store.NewAuditShipRepo(srv.st).AdvanceHighWater(ctx, hwm); err != nil {
		t.Fatal(err)
	}

	var pr pruneResult
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", &pr); code != 200 {
		t.Fatalf("prune: %d", code)
	}
	if pr.PrunedThrough != hwm {
		t.Fatalf("prune clamped to %d, want the persisted ship high-water mark %d "+
			"(the guard must not depend on the shipper being wired)", pr.PrunedThrough, hwm)
	}

	// The un-shipped events between the mark and the anchor must survive.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var surviving int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_events WHERE seq > $1 AND seq <= $2`, hwm, anchorSeq).Scan(&surviving); err != nil {
		t.Fatal(err)
	}
	if surviving == 0 {
		t.Fatal("un-shipped events between the ship mark and the checkpoint anchor were deleted")
	}
}

// TestAuditPruneUnguardedWhenNothingEverShipped proves the historical behavior
// is preserved: a deployment that has never shipped a single event (mark still
// at its seeded 0) prunes the whole checkpointed prefix, and so does one whose
// mark row does not exist at all.
func TestAuditPruneUnguardedWhenNothingEverShipped(t *testing.T) {
	ts, srv, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	// Sanity: the seeded mark is 0 on a fresh install (nothing ever shipped).
	if hwm, err := store.NewAuditShipRepo(srv.st).GetHighWater(ctx); err != nil || hwm != 0 {
		t.Fatalf("seeded high-water = %d, err = %v; want 0, nil", hwm, err)
	}

	mintEvents(t, ts.URL, cookie, configID, 3)
	anchorSeq := createCheckpoint(t, ts.URL, cookie)

	var pr pruneResult
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", &pr); code != 200 {
		t.Fatalf("prune: %d", code)
	}
	if pr.PrunedThrough != anchorSeq {
		t.Fatalf("prune through %d, want the full checkpointed prefix %d", pr.PrunedThrough, anchorSeq)
	}

	// Now delete the mark row entirely ("no mark was ever written") and prove a
	// second prune is likewise unguarded rather than fail-closed.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DELETE FROM audit_ship_state`); err != nil {
		t.Fatal(err)
	}
	mintEvents(t, ts.URL, cookie, configID, 2)
	anchor2 := createCheckpoint(t, ts.URL, cookie)
	var pr2 pruneResult
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", &pr2); code != 200 {
		t.Fatalf("prune with no mark row: %d", code)
	}
	if pr2.PrunedThrough != anchor2 {
		t.Fatalf("prune through %d, want %d", pr2.PrunedThrough, anchor2)
	}
}

// TestAuditPruneFailsClosedOnHighWaterReadError proves the guard is fail-closed:
// if the persisted mark cannot be read, prune 500s and deletes nothing rather
// than proceeding without the guard.
func TestAuditPruneFailsClosedOnHighWaterReadError(t *testing.T) {
	ts, _, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	mintEvents(t, ts.URL, cookie, configID, 3)
	anchorSeq := createCheckpoint(t, ts.URL, cookie)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Break the read (undefined table is neither ErrNotFound nor a clean value).
	// The container is per-test, so no restore is needed.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_ship_state RENAME TO audit_ship_state_broken`); err != nil {
		t.Fatal(err)
	}

	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", nil); code != 500 {
		t.Fatalf("prune with unreadable high-water mark = %d, want 500", code)
	}
	var remaining int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_events WHERE seq <= $1`, anchorSeq).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining == 0 {
		t.Fatal("events were pruned despite the fail-closed high-water read error")
	}
}

// TestAuditPruneRetentionFloorEvents proves JANUS_AUDIT_RETAIN_MIN_EVENTS clamps
// the prune point so the newest N events always survive.
func TestAuditPruneRetentionFloorEvents(t *testing.T) {
	ts, srv, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	mintEvents(t, ts.URL, cookie, configID, 6)
	anchorSeq := createCheckpoint(t, ts.URL, cookie)

	// Retain the newest 3 events. The prune point must fall strictly below the
	// 3rd-newest seq, i.e. exactly 3 events at/below the anchor survive.
	srv.cfg.AuditRetainMinEvents = 3

	var pr pruneResult
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", &pr); code != 200 {
		t.Fatalf("prune: %d", code)
	}
	if pr.PrunedThrough >= anchorSeq {
		t.Fatalf("prune through %d was not clamped below the anchor %d", pr.PrunedThrough, anchorSeq)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var remaining int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	// 3 retained by the floor, plus the audit.prune event the request itself
	// chained on afterwards.
	if remaining < 3 {
		t.Fatalf("only %d events survived; the floor must retain at least 3", remaining)
	}
}

// TestAuditPruneRetentionFloorBlocksEverything proves a floor that covers the
// whole log refuses the prune with a distinguishable 409 that names the floor
// (rather than the engine's shipping-oriented message).
func TestAuditPruneRetentionFloorBlocksEverything(t *testing.T) {
	ts, srv, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	mintEvents(t, ts.URL, cookie, configID, 3)
	anchorSeq := createCheckpoint(t, ts.URL, cookie)

	// Every event in this test was written seconds ago, so a 1-day floor retains
	// all of them.
	srv.cfg.AuditRetainMinDays = 1

	body, code := doAuthedBody(t, "POST", ts.URL+"/v1/audit/prune", cookie, "")
	if code != 409 {
		t.Fatalf("prune under a total retention floor = %d, want 409; body=%s", code, body)
	}
	if !strings.Contains(body, "retention") {
		t.Fatalf("409 body must name the retention floor, got %s", body)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var remaining int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_events WHERE seq <= $1`, anchorSeq).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining == 0 {
		t.Fatal("events were pruned despite a retention floor covering the whole log")
	}

	// Clearing the floor restores the historical behavior.
	srv.cfg.AuditRetainMinDays = 0
	var pr pruneResult
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", cookie, "", "", &pr); code != 200 {
		t.Fatalf("prune with the floor off: %d", code)
	}
	if pr.PrunedThrough != anchorSeq {
		t.Fatalf("prune through %d, want the full prefix %d", pr.PrunedThrough, anchorSeq)
	}
}
