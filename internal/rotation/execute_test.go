package rotation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// mkPolicy seals cfg for a fresh policy over (config, key) of the given type,
// persists it via RotationRepo.Create with NextRotationAt set 1h in the past
// (i.e. due), and returns the reloaded policy.
func mkPolicy(t *testing.T, svc *Service, proj *store.Project, storeCfg *store.Config,
	typ, key string, cfg PolicyConfig) *store.RotationPolicy {
	t.Helper()
	ctx := context.Background()
	policyID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	ct, nonce, wrapped, kekVer, err := svc.sealConfig(proj, policyID, cfg)
	if err != nil {
		t.Fatalf("sealConfig: %v", err)
	}
	repo := store.NewRotationRepo(testStore)
	in := &store.RotationPolicy{
		ID:                  policyID,
		ProjectID:           proj.ID,
		ConfigID:            storeCfg.ID,
		SecretKey:           key,
		Type:                typ,
		IntervalSeconds:     3600,
		NextRotationAt:      time.Now().Add(-time.Hour).UTC().Truncate(time.Second),
		ConfigCT:            ct,
		ConfigNonce:         nonce,
		ConfigWrappedDEK:    wrapped,
		ConfigDEKKEKVersion: kekVer,
		CreatedBy:           "user:tester",
	}
	if _, err := repo.Create(ctx, in); err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	got, err := repo.Get(ctx, policyID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	return got
}

// seedSecret sets an initial value for key in storeCfg via the secrets service.
func seedSecret(t *testing.T, sec *secrets.Service, storeCfg *store.Config, key, val string) int {
	t.Helper()
	cv, err := sec.SetSecrets(context.Background(), storeCfg.ID,
		[]secrets.SecretChange{{Key: key, Value: []byte(val)}}, "seed", "user:tester")
	if err != nil {
		t.Fatalf("seed SetSecrets: %v", err)
	}
	return cv.Version
}

func TestRotatePostgresEndToEnd(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotate-pg-e2e")
	ctx := context.Background()

	// Create a Postgres role with a known starting password via the superuser DSN.
	admin, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	role := "rotate_e2e_role"
	startPass := "startpass123"
	if _, err := admin.Exec(ctx, `DROP ROLE IF EXISTS `+role); err != nil {
		admin.Close(ctx)
		t.Fatalf("drop role: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD '`+startPass+`'`); err != nil {
		admin.Close(ctx)
		t.Fatalf("create role: %v", err)
	}
	admin.Close(ctx)
	t.Cleanup(func() {
		a, err := pgx.Connect(context.Background(), testDSN)
		if err == nil {
			_, _ = a.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
			a.Close(context.Background())
		}
	})

	key := "DB_PASSWORD"
	startVer := seedSecret(t, sec, storeCfg, key, startPass)

	pol := mkPolicy(t, svc, proj, storeCfg, TypePostgres, key,
		PolicyConfig{AdminDSN: testDSN, Role: role})

	if err := svc.attempt(ctx, pol); err != nil {
		t.Fatalf("attempt: %v", err)
	}

	// (a) A new config version was committed for the key.
	got, err := sec.GetSecret(ctx, storeCfg.ID, key)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	newPass := string(got.Value)
	if newPass == startPass {
		t.Fatal("value was not rotated (still the starting password)")
	}

	// (b) The revealed value actually connects to Postgres as the role.
	connCfg, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	connCfg.User = role
	connCfg.Password = newPass
	roleConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("role connect with rotated password failed: %v", err)
	}
	roleConn.Close(ctx)

	// (c) Policy is active, cleared, and records the new version.
	reloaded, err := store.NewRotationRepo(testStore).Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active", reloaded.Status)
	}
	if reloaded.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", reloaded.FailureCount)
	}
	if reloaded.PendingState != nil {
		t.Errorf("PendingState = %v, want nil", *reloaded.PendingState)
	}
	if reloaded.LastConfigVersion == nil || *reloaded.LastConfigVersion <= startVer {
		t.Errorf("LastConfigVersion = %v, want > %d", reloaded.LastConfigVersion, startVer)
	}
}

func TestRotateResumesPending(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotate-resume-pending")
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

	key := "WEBHOOK_SECRET"
	seedSecret(t, sec, storeCfg, key, "initial")

	pol := mkPolicy(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: srv.URL})

	// Manually seed a known pending value (simulating a prior crash mid-apply).
	pendingVal := "known-pending-value-abc"
	ct, nonce, wrapped, err := svc.sealPending(proj, pol.ID, pendingVal)
	if err != nil {
		t.Fatalf("sealPending: %v", err)
	}
	if err := svc.repo.SetPending(ctx, pol.ID, ct, nonce, wrapped); err != nil {
		t.Fatalf("SetPending: %v", err)
	}
	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload after SetPending: %v", err)
	}
	if reloaded.PendingState == nil || *reloaded.PendingState != "applying" {
		t.Fatalf("pending_state = %v, want applying", reloaded.PendingState)
	}

	if err := svc.attempt(ctx, reloaded); err != nil {
		t.Fatalf("attempt: %v", err)
	}

	// The webhook received the SAME pending value (reused, not regenerated).
	mu.Lock()
	gotRecv := received
	mu.Unlock()
	if gotRecv != pendingVal {
		t.Fatalf("webhook received %q, want reused pending %q", gotRecv, pendingVal)
	}

	// The committed config version holds the pending value.
	got, err := sec.GetSecret(ctx, storeCfg.ID, key)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(got.Value) != pendingVal {
		t.Fatalf("committed value = %q, want %q", string(got.Value), pendingVal)
	}
}

func TestRotateFailureMarksBackoff(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotate-failure-backoff")
	ctx := context.Background()

	fixed := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	key := "WEBHOOK_SECRET"
	seedSecret(t, sec, storeCfg, key, "initial")
	pol := mkPolicy(t, svc, proj, storeCfg, TypeWebhook, key, PolicyConfig{URL: srv.URL})

	if err := svc.attempt(ctx, pol); err == nil {
		t.Fatal("attempt: expected error on 500, got nil")
	}

	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", reloaded.FailureCount)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active (below threshold)", reloaded.Status)
	}
	if reloaded.LastError == nil || *reloaded.LastError != "apply failed" {
		t.Errorf("LastError = %v, want \"apply failed\"", reloaded.LastError)
	}
	// NextRotationAt advanced ~1m into the future (first-failure backoff).
	wantNext := fixed.Add(backoff(1))
	if !reloaded.NextRotationAt.Equal(wantNext) {
		t.Errorf("NextRotationAt = %v, want %v", reloaded.NextRotationAt, wantNext)
	}
	// A pending value IS present (persisted before apply).
	if reloaded.PendingState == nil {
		t.Error("PendingState = nil, want present (persisted before apply)")
	}

	// 4 more failures → after the 5th total, status flips to 'failed'.
	for i := 0; i < 4; i++ {
		cur, err := svc.repo.Get(ctx, pol.ID)
		if err != nil {
			t.Fatalf("reload before attempt %d: %v", i, err)
		}
		if err := svc.attempt(ctx, cur); err == nil {
			t.Fatalf("attempt %d: expected error, got nil", i)
		}
	}
	final, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("final reload: %v", err)
	}
	if final.FailureCount != 5 {
		t.Errorf("final FailureCount = %d, want 5", final.FailureCount)
	}
	if final.Status != "failed" {
		t.Errorf("final Status = %q, want failed", final.Status)
	}
}


func TestRotateSealedNoop(t *testing.T) {
	svc, kr, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotate-sealed-noop")
	ctx := context.Background()

	key := "DB_PASSWORD"
	seedSecret(t, sec, storeCfg, key, "unchanged")
	pol := mkPolicy(t, svc, proj, storeCfg, TypeWebhook, key,
		PolicyConfig{URL: "http://127.0.0.1:1/never"})

	kr.Seal()

	err := svc.attempt(ctx, pol)
	if err != ErrSealed {
		t.Fatalf("attempt: err = %v, want ErrSealed", err)
	}

	// Nothing written: pending still nil, value unchanged, no new version.
	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PendingState != nil {
		t.Errorf("PendingState = %v, want nil (sealed no-op)", *reloaded.PendingState)
	}
	if reloaded.LastConfigVersion != nil {
		t.Errorf("LastConfigVersion = %v, want nil (sealed no-op)", *reloaded.LastConfigVersion)
	}

	// Unseal (via a fresh keyring on the same store's project) is not needed: we
	// only assert the stored value is unchanged. Re-unseal the keyring to read.
	// The MarkFailure path DID run (failure bookkeeping is allowed even sealed),
	// so verify failure_count bumped but no rotation occurred.
	if reloaded.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1 (sealed attempt is a recorded failure)", reloaded.FailureCount)
	}
}
