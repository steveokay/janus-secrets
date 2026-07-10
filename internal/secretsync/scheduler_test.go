package secretsync

import (
	"context"
	"testing"
	"time"
)

// TestRunDueSyncsDueTargets seeds two github sync targets against one fake —
// one due (next_sync_at in the past), one not due (next_sync_at in the
// future) — and asserts RunDue syncs only the due one.
func TestRunDueSyncsDueTargets(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "rundue-due-vs-notdue")
	ctx := context.Background()

	dueTgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"DUE_KEY": "due-val"})
	// seedGHTarget hardcodes Addr={"owner":"o","repo":"r"}; sync_targets_dest is
	// unique on (config_id, provider, md5(addr::text)), so the second target
	// needs its own project+config to avoid colliding with dueTgt's destination.
	proj2, cfg2 := mkChain(t, sec, "rundue-due-vs-notdue-2")
	notDueTgt := seedGHTarget(t, svc, sec, proj2, cfg2, map[string]string{"NOTDUE_KEY": "notdue-val"})

	// dueTgt already defaults to next_sync_at = now() (due) via seedGHTarget.
	// Push the not-due target's next_sync_at into the future.
	if err := svc.repo.PrepareSyncNow(ctx, notDueTgt.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PrepareSyncNow(notDue): %v", err)
	}
	if err := svc.repo.PrepareSyncNow(ctx, dueTgt.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("PrepareSyncNow(due): %v", err)
	}

	svc.RunDue(ctx)

	gotDue, err := svc.repo.Get(ctx, dueTgt.ID)
	if err != nil {
		t.Fatalf("repo.Get(due): %v", err)
	}
	if gotDue.SyncedFingerprint == nil {
		t.Error("due target SyncedFingerprint is nil, want set (synced)")
	}
	if !fake.puts["DUE_KEY"] {
		t.Errorf("fake did not receive PUT for due target: %v", fake.puts)
	}

	gotNotDue, err := svc.repo.Get(ctx, notDueTgt.ID)
	if err != nil {
		t.Fatalf("repo.Get(notDue): %v", err)
	}
	if gotNotDue.SyncedFingerprint != nil {
		t.Error("not-due target SyncedFingerprint is set, want nil (not synced)")
	}
	if fake.puts["NOTDUE_KEY"] {
		t.Errorf("fake received PUT for not-due target, want none: %v", fake.puts)
	}
}

// TestRunDueSealedNoop asserts a sealed keyring makes RunDue a clean no-op,
// even with a due target queued.
func TestRunDueSealedNoop(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "rundue-sealed-noop")
	ctx := context.Background()

	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"SEALED_KEY": "val"})
	if err := svc.repo.PrepareSyncNow(ctx, tgt.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("PrepareSyncNow: %v", err)
	}

	svc.kr.(interface{ Seal() }).Seal()

	svc.RunDue(ctx)

	got, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if got.SyncedFingerprint != nil {
		t.Error("SyncedFingerprint is set, want nil (sealed no-op)")
	}
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (sealed is not a failure)", got.FailureCount)
	}
	if fake.pubKeyHits != 0 || len(fake.puts) != 0 {
		t.Errorf("fake received calls while sealed: pk=%d puts=%v (want none)", fake.pubKeyHits, fake.puts)
	}
}

// TestRunSchedulerDisabled asserts tick<=0 disables the scheduler: it
// returns immediately without ticking.
func TestRunSchedulerDisabled(t *testing.T) {
	svc, sec := newTestService(t)
	_, _ = mkChain(t, sec, "runscheduler-disabled")

	done := make(chan struct{})
	go func() {
		svc.RunScheduler(context.Background(), 0)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScheduler(tick=0) did not return immediately")
	}
}

// TestRunSchedulerStopsOnContextCancel asserts RunScheduler exits promptly
// once its context is canceled.
func TestRunSchedulerStopsOnContextCancel(t *testing.T) {
	svc, sec := newTestService(t)
	_, _ = mkChain(t, sec, "runscheduler-cancel")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunScheduler(ctx, time.Hour)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScheduler did not stop after context cancel")
	}
}
