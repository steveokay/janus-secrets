package rotation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestRunDueRotatesDuePolicies seeds two webhook policies — one due, one not —
// and asserts RunDue rotates only the due one.
func TestRunDueRotatesDuePolicies(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rundue-due-vs-notdue")
	ctx := context.Background()

	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Query().Get("policy")]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dueKey := "DUE_SECRET"
	notDueKey := "NOT_DUE_SECRET"
	dueStartVer := seedSecret(t, sec, storeCfg, dueKey, "due-initial")
	seedSecret(t, sec, storeCfg, notDueKey, "not-due-initial")

	duePol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, dueKey,
		PolicyConfig{URL: srv.URL + "?policy=due"}, time.Now().Add(-time.Hour))
	notDuePol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, notDueKey,
		PolicyConfig{URL: srv.URL + "?policy=notdue"}, time.Now().Add(time.Hour))

	svc.RunDue(ctx)

	mu.Lock()
	dueHits := hits["due"]
	notDueHits := hits["notdue"]
	mu.Unlock()
	if dueHits != 1 {
		t.Errorf("due policy webhook hits = %d, want 1", dueHits)
	}
	if notDueHits != 0 {
		t.Errorf("not-due policy webhook hits = %d, want 0", notDueHits)
	}

	// Due policy committed a new config version.
	gotDue, err := sec.GetSecret(ctx, storeCfg.ID, dueKey)
	if err != nil {
		t.Fatalf("GetSecret(due): %v", err)
	}
	if string(gotDue.Value) == "due-initial" {
		t.Error("due policy secret was not rotated")
	}
	reloadedDue, err := svc.repo.Get(ctx, duePol.ID)
	if err != nil {
		t.Fatalf("reload due: %v", err)
	}
	if reloadedDue.LastConfigVersion == nil || *reloadedDue.LastConfigVersion <= dueStartVer {
		t.Errorf("due policy LastConfigVersion = %v, want > %d", reloadedDue.LastConfigVersion, dueStartVer)
	}

	// Not-due policy: secret unchanged, no LastConfigVersion recorded.
	gotNotDue, err := sec.GetSecret(ctx, storeCfg.ID, notDueKey)
	if err != nil {
		t.Fatalf("GetSecret(notDue): %v", err)
	}
	if string(gotNotDue.Value) != "not-due-initial" {
		t.Errorf("not-due policy secret changed to %q, want unchanged", string(gotNotDue.Value))
	}
	reloadedNotDue, err := svc.repo.Get(ctx, notDuePol.ID)
	if err != nil {
		t.Fatalf("reload notDue: %v", err)
	}
	if reloadedNotDue.LastConfigVersion != nil {
		t.Errorf("not-due policy LastConfigVersion = %v, want nil", *reloadedNotDue.LastConfigVersion)
	}
}

// TestRunDueSealedNoop asserts a sealed keyring makes RunDue a clean no-op,
// even with a due policy queued.
func TestRunDueSealedNoop(t *testing.T) {
	svc, kr, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rundue-sealed-noop")
	ctx := context.Background()

	var hit bool
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := "SEALED_SECRET"
	seedSecret(t, sec, storeCfg, key, "initial")
	pol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: srv.URL}, time.Now().Add(-time.Hour))

	kr.Seal()

	svc.RunDue(ctx)

	mu.Lock()
	gotHit := hit
	mu.Unlock()
	if gotHit {
		t.Error("webhook was hit while sealed, want no-op")
	}

	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.LastConfigVersion != nil {
		t.Errorf("LastConfigVersion = %v, want nil (sealed no-op)", *reloaded.LastConfigVersion)
	}
	if reloaded.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (sealed is not a failure)", reloaded.FailureCount)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active", reloaded.Status)
	}
}

// TestRunDueRecoversPending simulates a policy that crashed mid-apply
// (pending_state='applying', next_rotation_at still in the past — a crashed
// policy never got its due time advanced) and asserts RunDue picks it up on
// the next tick and commits using the reused pending value.
func TestRunDueRecoversPending(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rundue-recovers-pending")
	ctx := context.Background()

	var mu sync.Mutex
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			NewValue string `json:"new_value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = body.NewValue
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := "CRASHED_SECRET"
	seedSecret(t, sec, storeCfg, key, "initial")
	pol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: srv.URL}, time.Now().Add(-time.Hour))

	// Simulate a crash mid-apply: pending value persisted, next_rotation_at
	// left untouched (in the past), matching real crash semantics (SetPending
	// does not move next_rotation_at).
	pendingVal := "crash-recovered-value"
	ct, nonce, wrapped, err := svc.sealPending(proj, pol.ID, pendingVal)
	if err != nil {
		t.Fatalf("sealPending: %v", err)
	}
	if err := svc.repo.SetPending(ctx, pol.ID, ct, nonce, wrapped); err != nil {
		t.Fatalf("SetPending: %v", err)
	}
	preCheck, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("pre-check reload: %v", err)
	}
	if preCheck.PendingState == nil || *preCheck.PendingState != "applying" {
		t.Fatalf("pending_state = %v, want applying", preCheck.PendingState)
	}
	if !preCheck.NextRotationAt.Before(time.Now()) {
		t.Fatalf("NextRotationAt = %v, want in the past", preCheck.NextRotationAt)
	}

	svc.RunDue(ctx)

	mu.Lock()
	gotRecv := received
	mu.Unlock()
	if gotRecv != pendingVal {
		t.Fatalf("webhook received %q, want reused pending %q", gotRecv, pendingVal)
	}

	got, err := sec.GetSecret(ctx, storeCfg.ID, key)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(got.Value) != pendingVal {
		t.Fatalf("committed value = %q, want %q", string(got.Value), pendingVal)
	}

	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PendingState != nil {
		t.Errorf("PendingState = %v, want nil (cleared on commit)", *reloaded.PendingState)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active", reloaded.Status)
	}
}
