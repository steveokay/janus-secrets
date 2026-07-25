package store

import (
	"context"
	"testing"
	"time"
)

// TestMigration041CreatesVerifyTables asserts the drift-detection tables exist.
func TestMigration041CreatesVerifyTables(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()
	for _, table := range []string{"sync_verify_state", "sync_verify_runs"} {
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("%s table missing after migrate", table)
		}
	}
}

// TestSyncTargetProviderConstraintAcceptsAllProviders is the regression test for
// the 000041 bug fix: migration 000011 pinned the provider CHECK to
// ('github','k8s'), which silently made the six later providers unusable.
func TestSyncTargetProviderConstraintAcceptsAllProviders(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)
	projectID, _, configID := mkConfig(t, s, "prod")

	for _, provider := range []string{
		"github", "k8s", "gitlab", "aws_ssm", "cloudflare", "aws_secrets", "vercel", "netlify",
	} {
		tgt := newSyncTarget(t, s, projectID, configID, []byte(`{"owner":"`+provider+`","repo":"r"}`))
		tgt.Provider = provider
		if _, err := r.Create(ctx, tgt); err != nil {
			t.Fatalf("Create target with provider %q: %v", provider, err)
		}
	}
}

// TestSyncVerifyStateAndRuns covers the lazy default, the due-claim predicate,
// run recording + history cap, and the schedule knobs.
func TestSyncVerifyStateAndRuns(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	r := NewSyncTargetRepo(s)
	projectID, _, configID := mkConfig(t, s, "prod")

	tgt, err := r.Create(ctx, newSyncTarget(t, s, projectID, configID, nil))
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	// No row yet → lazy default (enabled, default pace, zero next → due now).
	st, err := r.GetVerifyState(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetVerifyState: %v", err)
	}
	if !st.Enabled || st.IntervalSeconds != DefaultVerifyIntervalSeconds || st.LastStatus != nil {
		t.Fatalf("lazy default = %+v", st)
	}

	now := time.Now().UTC()
	due, err := r.ClaimVerifyDueIDs(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimVerifyDueIDs: %v", err)
	}
	if len(due) != 1 || due[0] != tgt.ID {
		t.Fatalf("due = %v, want [%s]", due, tgt.ID)
	}

	// Record a drift run; state advances and summarizes.
	next := now.Add(time.Hour)
	if err := r.RecordVerifyRun(ctx, SyncVerifyRunInput{
		TargetID: tgt.ID, StartedAt: now, EndedAt: now.Add(time.Second),
		Status: "drift", Capability: "values", ValuesCompared: true,
		MissingKeys: []string{"GONE"}, ModifiedKeys: []string{"CHANGED"},
		MissingCount: 1, ModifiedCount: 1, CheckedCount: 4,
	}, next); err != nil {
		t.Fatalf("RecordVerifyRun: %v", err)
	}
	st, err = r.GetVerifyState(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetVerifyState (2): %v", err)
	}
	if st.LastStatus == nil || *st.LastStatus != "drift" || st.LastDriftCount != 2 {
		t.Fatalf("state after run = %+v", st)
	}
	if !st.NextVerifyAt.After(now) {
		t.Fatalf("next_verify_at = %v, want after %v", st.NextVerifyAt, now)
	}
	if due, err = r.ClaimVerifyDueIDs(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("due after reschedule = %v (err %v), want none", due, err)
	}

	runs, err := r.ListVerifyRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListVerifyRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].MissingCount != 1 || runs[0].ModifiedKeys[0] != "CHANGED" {
		t.Fatalf("runs = %+v", runs)
	}
	// Unset name arrays must round-trip as empty, never NULL.
	if runs[0].ExtraKeys == nil || len(runs[0].ExtraKeys) != 0 {
		t.Fatalf("extra_keys = %v, want empty", runs[0].ExtraKeys)
	}

	// Schedule knobs: disabling removes the target from the due set even far in
	// the future; changing the interval re-bases next_verify_at.
	off := false
	if err := r.SetVerifySchedule(ctx, tgt.ID, &off, nil, now); err != nil {
		t.Fatalf("SetVerifySchedule(off): %v", err)
	}
	if due, err = r.ClaimVerifyDueIDs(ctx, now.Add(72*time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("disabled target due = %v (err %v)", due, err)
	}
	on, iv := true, int64(120)
	if err := r.SetVerifySchedule(ctx, tgt.ID, &on, &iv, now); err != nil {
		t.Fatalf("SetVerifySchedule(on): %v", err)
	}
	st, err = r.GetVerifyState(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetVerifyState (3): %v", err)
	}
	if !st.Enabled || st.IntervalSeconds != 120 {
		t.Fatalf("state after knobs = %+v", st)
	}

	// History cap.
	for i := 0; i < VerifyRunHistoryCap+5; i++ {
		if err := r.RecordVerifyRun(ctx, SyncVerifyRunInput{
			TargetID: tgt.ID, StartedAt: now, EndedAt: now,
			Status: "clean", Capability: "values", ValuesCompared: true, CheckedCount: 1,
		}, next); err != nil {
			t.Fatalf("RecordVerifyRun #%d: %v", i, err)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sync_verify_runs WHERE target_id = $1::uuid`, tgt.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != VerifyRunHistoryCap {
		t.Fatalf("retained %d runs, want cap %d", count, VerifyRunHistoryCap)
	}

	// Project-wide batch load returns the materialized row.
	states, err := r.VerifyStatesByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("VerifyStatesByProject: %v", err)
	}
	if _, ok := states[tgt.ID]; !ok {
		t.Fatalf("VerifyStatesByProject missing target: %+v", states)
	}
}
