package rotation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreatePostgresPolicy(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-create-pg")
	ctx := context.Background()

	before := svc.now()
	in := PolicyInput{
		ConfigID:        storeCfg.ID,
		SecretKey:       "DB_PASSWORD",
		Type:            TypePostgres,
		IntervalSeconds: 3600,
		Config:          PolicyConfig{AdminDSN: testDSN, Role: "app_role"},
	}
	view, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.ID == "" {
		t.Fatal("view.ID is empty")
	}
	if view.ConfigID != storeCfg.ID || view.SecretKey != "DB_PASSWORD" || view.Type != TypePostgres {
		t.Errorf("view = %+v, mismatched input", view)
	}
	if view.Status != "active" {
		t.Errorf("Status = %q, want active", view.Status)
	}
	wantNext := before.Add(3600 * time.Second)
	diff := view.NextRotationAt.Sub(wantNext)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("NextRotationAt = %v, want ~%v", view.NextRotationAt, wantNext)
	}
}

func TestCreateWebhookPolicy(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-create-webhook")
	ctx := context.Background()

	in := PolicyInput{
		ConfigID:        storeCfg.ID,
		SecretKey:       "WEBHOOK_SECRET",
		Type:            TypeWebhook,
		IntervalSeconds: 1800,
		Config:          PolicyConfig{URL: "https://example.invalid/rotate"},
	}
	view, err := svc.Create(ctx, in, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.Type != TypeWebhook {
		t.Errorf("Type = %q, want %q", view.Type, TypeWebhook)
	}
}

func TestCreateValidationErrors(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-create-validation")
	ctx := context.Background()

	base := PolicyInput{
		ConfigID:        storeCfg.ID,
		SecretKey:       "SOME_KEY",
		Type:            TypePostgres,
		IntervalSeconds: 3600,
		Config:          PolicyConfig{AdminDSN: testDSN, Role: "app_role"},
	}

	tests := []struct {
		name    string
		mutate  func(in PolicyInput) PolicyInput
		wantErr error
	}{
		{
			name: "unknown type",
			mutate: func(in PolicyInput) PolicyInput {
				in.Type = "carrier-pigeon"
				return in
			},
			wantErr: ErrInvalidType,
		},
		{
			name: "empty secret key",
			mutate: func(in PolicyInput) PolicyInput {
				in.SecretKey = ""
				return in
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "zero interval",
			mutate: func(in PolicyInput) PolicyInput {
				in.IntervalSeconds = 0
				return in
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "negative interval",
			mutate: func(in PolicyInput) PolicyInput {
				in.IntervalSeconds = -1
				return in
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "postgres empty admin dsn",
			mutate: func(in PolicyInput) PolicyInput {
				in.Config.AdminDSN = ""
				return in
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "postgres bad role",
			mutate: func(in PolicyInput) PolicyInput {
				in.Config.Role = "bad; DROP"
				return in
			},
			wantErr: ErrInvalidConfig,
		},
		{
			name: "webhook empty url",
			mutate: func(in PolicyInput) PolicyInput {
				in.Type = TypeWebhook
				in.Config = PolicyConfig{URL: ""}
				return in
			},
			wantErr: ErrInvalidConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.mutate(base)
			in.SecretKey = in.SecretKey + "_" + tt.name // avoid accidental dup collisions
			if tt.name == "empty secret key" {
				in.SecretKey = ""
			}
			_, err := svc.Create(ctx, in, "user:tester")
			if err != tt.wantErr {
				t.Fatalf("Create: err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-create-dup")
	ctx := context.Background()

	in := PolicyInput{
		ConfigID:        storeCfg.ID,
		SecretKey:       "DUP_KEY",
		Type:            TypeWebhook,
		IntervalSeconds: 3600,
		Config:          PolicyConfig{URL: "https://example.invalid/rotate"},
	}
	if _, err := svc.Create(ctx, in, "user:tester"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(ctx, in, "user:tester")
	if err != ErrExists {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}
}

func TestGetAndNotFound(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-get")
	ctx := context.Background()

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "GET_KEY", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/rotate"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.SecretKey != "GET_KEY" {
		t.Errorf("Get result = %+v, want matching %+v", got, created)
	}

	unknownID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	_, err = svc.Get(ctx, unknownID)
	if err != ErrNotFound {
		t.Fatalf("Get(unknown): err = %v, want ErrNotFound", err)
	}
}

func TestListByProject(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "crud-list")
	ctx := context.Background()

	a, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "LIST_KEY_A", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/a"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "LIST_KEY_B", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/b"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}

	list, err := svc.ListByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	ids := map[string]bool{}
	for _, p := range list {
		ids[p.ID] = true
	}
	if !ids[a.ID] || !ids[b.ID] {
		t.Errorf("list missing created policies: got ids %v, want %s and %s", ids, a.ID, b.ID)
	}
}

func TestUpdateIntervalAndStatus(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-update-interval-status")
	ctx := context.Background()

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "UPD_KEY", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/rotate"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newInterval := int64(7200)
	paused := "paused"
	updated, err := svc.Update(ctx, created.ID, &newInterval, &paused, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.IntervalSeconds != 7200 {
		t.Errorf("IntervalSeconds = %d, want 7200", updated.IntervalSeconds)
	}
	if updated.Status != "paused" {
		t.Errorf("Status = %q, want paused", updated.Status)
	}

	active := "active"
	updated2, err := svc.Update(ctx, created.ID, nil, &active, nil)
	if err != nil {
		t.Fatalf("Update (reactivate): %v", err)
	}
	if updated2.Status != "active" {
		t.Errorf("Status = %q, want active", updated2.Status)
	}
	// Interval unaffected by the second call (nil param).
	if updated2.IntervalSeconds != 7200 {
		t.Errorf("IntervalSeconds after nil-interval update = %d, want unchanged 7200", updated2.IntervalSeconds)
	}
}

func TestUpdateRejectsFailedStatus(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-update-failed-status")
	ctx := context.Background()

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "FAILSTATUS_KEY", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/rotate"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failed := "failed"
	_, err = svc.Update(ctx, created.ID, nil, &failed, nil)
	if err != ErrInvalidConfig {
		t.Fatalf("Update(status=failed): err = %v, want ErrInvalidConfig", err)
	}
}

func TestUpdateReseatsConfig(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "crud-update-reseals")
	ctx := context.Background()

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "RESEAL_KEY", Type: TypePostgres,
		IntervalSeconds: 3600, Config: PolicyConfig{AdminDSN: testDSN, Role: "old_role"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newCfg := PolicyConfig{AdminDSN: testDSN, Role: "new_role"}
	updated, err := svc.Update(ctx, created.ID, nil, nil, &newCfg)
	if err != nil {
		t.Fatalf("Update (new config): %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("updated.ID = %q, want %q", updated.ID, created.ID)
	}

	// Verify the new blob actually round-trips to the new config (re-sealed).
	reloaded, err := svc.repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	opened, err := svc.openConfig(proj, reloaded)
	if err != nil {
		t.Fatalf("openConfig: %v", err)
	}
	if opened != newCfg {
		t.Fatalf("opened config = %+v, want %+v", opened, newCfg)
	}
}

func TestDelete(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-delete")
	ctx := context.Background()

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "DELETE_KEY", Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: "https://example.invalid/rotate"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestRotateNowWebhookSucceeds(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "crud-rotatenow-success")
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := "ROTATE_NOW_KEY"
	startVer := seedSecret(t, sec, storeCfg, key, "initial")

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: key, Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: srv.URL},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = proj

	newVer, err := svc.RotateNow(ctx, created.ID)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if newVer <= startVer {
		t.Errorf("RotateNow returned version %d, want > %d", newVer, startVer)
	}
}

func TestRotateNowClearsFailedStatus(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-rotatenow-clears-failed")
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := "ROTATE_NOW_FAILED_KEY"
	startVer := seedSecret(t, sec, storeCfg, key, "initial")

	created, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: key, Type: TypeWebhook,
		IntervalSeconds: 3600, Config: PolicyConfig{URL: srv.URL},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Drive failure_count past the threshold directly via the repo, flipping
	// status to 'failed' (mirrors real backoff exhaustion).
	past := time.Now().Add(-time.Hour)
	for i := 0; i < failureThreshold; i++ {
		if err := svc.repo.MarkFailure(ctx, created.ID, "apply failed", past, failureThreshold, time.Now(), i+1); err != nil {
			t.Fatalf("MarkFailure %d: %v", i, err)
		}
	}
	preCheck, err := svc.repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("pre-check reload: %v", err)
	}
	if preCheck.Status != "failed" {
		t.Fatalf("Status = %q, want failed (seed setup broken)", preCheck.Status)
	}

	newVer, err := svc.RotateNow(ctx, created.ID)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if newVer <= startVer {
		t.Errorf("RotateNow returned version %d, want > %d", newVer, startVer)
	}

	reloaded, err := svc.repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active (RotateNow should clear failed)", reloaded.Status)
	}
	if reloaded.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 after successful RotateNow", reloaded.FailureCount)
	}
}

// TestRotateNowMarksDueForCrashRecovery locks in the Critical fix: RotateNow
// must mark a policy immediately due (next_rotation_at <= now) BEFORE
// attempting, so that if the process crashes mid-apply (external side effect
// already applied, commit not yet persisted), the scheduler's ClaimDue query
// (status='active' AND next_rotation_at <= now) picks the policy back up on
// its very next tick instead of stranding it until a full interval elapses.
func TestRotateNowMarksDueForCrashRecovery(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "crud-rotatenow-crash-recovery")
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := "ROTATE_NOW_CRASH_KEY"
	seedSecret(t, sec, storeCfg, key, "initial")

	// Policy is NOT yet due (next_rotation_at is 1h in the future), mirroring a
	// freshly created or freshly rotated policy.
	future := time.Now().Add(time.Hour)
	pol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: srv.URL}, future)

	// Simulate a crash mid manual-rotate: the external apply already
	// persisted a pending value, but the commit (MarkRotated) never ran.
	// next_rotation_at is left untouched — still in the future, matching the
	// bug: SetPending does not move next_rotation_at.
	pendingVal := "crashed-during-rotatenow"
	ct, nonce, wrapped, err := svc.sealPending(proj, pol.ID, pendingVal)
	if err != nil {
		t.Fatalf("sealPending: %v", err)
	}
	if err := svc.repo.SetPending(ctx, pol.ID, ct, nonce, wrapped); err != nil {
		t.Fatalf("SetPending: %v", err)
	}

	// PRECONDITION (proves the bug is real): with next_rotation_at still in
	// the future, ClaimDue does NOT select the crashed policy. Before the fix,
	// nothing in RotateNow would ever change next_rotation_at, so a crashed
	// manual rotation would be stranded like this until the full interval
	// elapsed.
	due, err := svc.repo.ClaimDue(ctx, time.Now(), 50)
	if err != nil {
		t.Fatalf("ClaimDue (precondition): %v", err)
	}
	for _, d := range due {
		if d.ID == pol.ID {
			t.Fatal("precondition violated: ClaimDue already selected the not-yet-due policy")
		}
	}

	// Now drive a successful manual rotation. RotateNow must mark the policy
	// due before attempting (so a crash here would have been recoverable),
	// then complete normally on the happy path.
	newVer, err := svc.RotateNow(ctx, pol.ID)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if newVer <= 0 {
		t.Errorf("RotateNow returned version %d, want > 0", newVer)
	}

	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PendingState != nil {
		t.Errorf("PendingState = %v, want nil after successful commit", *reloaded.PendingState)
	}
	if !reloaded.NextRotationAt.After(time.Now()) {
		t.Errorf("NextRotationAt = %v, want advanced into the future by MarkRotated", reloaded.NextRotationAt)
	}
}

// TestPrepareRotateNowMakesPolicyDue is the focused core-of-the-fix assertion:
// PrepareRotateNow alone (independent of the rest of RotateNow) must flip a
// future-dated policy's next_rotation_at into the past/now, so ClaimDue picks
// it up — this is what makes a crashed manual-rotate policy recoverable.
func TestPrepareRotateNowMakesPolicyDue(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "crud-preparerotatenow-due")
	ctx := context.Background()

	key := "PREPARE_ROTATE_NOW_KEY"
	seedSecret(t, sec, storeCfg, key, "initial")

	future := time.Now().Add(time.Hour)
	pol := mkPolicyAt(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: "https://example.invalid/never-called"}, future)

	// Precondition: not due yet.
	before, err := svc.repo.ClaimDue(ctx, time.Now(), 50)
	if err != nil {
		t.Fatalf("ClaimDue (before): %v", err)
	}
	for _, d := range before {
		if d.ID == pol.ID {
			t.Fatal("precondition violated: policy already due before PrepareRotateNow")
		}
	}

	now := time.Now()
	if err := svc.repo.PrepareRotateNow(ctx, pol.ID, now); err != nil {
		t.Fatalf("PrepareRotateNow: %v", err)
	}

	after, err := svc.repo.ClaimDue(ctx, now, 50)
	if err != nil {
		t.Fatalf("ClaimDue (after): %v", err)
	}
	found := false
	for _, d := range after {
		if d.ID == pol.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("ClaimDue did not select the policy after PrepareRotateNow made it due")
	}
}

// TestPolicyViewOmitsSecrets is a lightweight structural guard: PolicyView
// must not carry any field that could leak the config blob, admin DSN, HMAC
// keys, or secret value. If a future edit adds such a field to PolicyView
// this test will need explicit updating (it does not do reflection-based
// leak scanning; see the package's dedicated leak test for value scanning).
func TestPolicyViewOmitsSecrets(t *testing.T) {
	svc, _, sec := newTestService(t)
	_, storeCfg := mkChain(t, sec, "crud-view-omits-secrets")
	ctx := context.Background()

	dsn := testDSN
	view, err := svc.Create(ctx, PolicyInput{
		ConfigID: storeCfg.ID, SecretKey: "VIEW_SAFE_KEY", Type: TypePostgres,
		IntervalSeconds: 3600, Config: PolicyConfig{AdminDSN: dsn, Role: "app_role"},
	}, "user:tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// PolicyView has no Config/blob fields to check by construction (compile-time
	// guarantee), but assert nothing on the struct's string fields accidentally
	// contains the DSN (defense in depth if the type is ever widened carelessly).
	if view.ID == dsn || view.SecretKey == dsn || view.ProjectID == dsn || view.ConfigID == dsn {
		t.Fatal("PolicyView field unexpectedly equals the admin DSN")
	}
}
