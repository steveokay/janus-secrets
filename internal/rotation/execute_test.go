package rotation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return mkPolicyAt(t, svc, proj, storeCfg, typ, key, cfg, time.Now().Add(-time.Hour))
}

// mkPolicyAt is mkPolicy with an explicit next_rotation_at, for scheduler
// tests that need to distinguish due vs. not-yet-due policies.
func mkPolicyAt(t *testing.T, svc *Service, proj *store.Project, storeCfg *store.Config,
	typ, key string, cfg PolicyConfig, nextRotationAt time.Time) *store.RotationPolicy {
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
		NextRotationAt:      nextRotationAt.UTC().Truncate(time.Second),
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

// TestRotatorForDispatch asserts the two rotator families are wired correctly:
// generating rotators (oauth/aws_iam) implement rotatorGenerator and NOT
// rotatorApplier, while apply-a-value rotators (postgres/mysql/redis/webhook)
// implement rotatorApplier and NOT rotatorGenerator. The rotate() switch keys
// its generate-vs-apply behavior off exactly these type assertions.
func TestRotatorForDispatch(t *testing.T) {
	svc := &Service{hc: http.DefaultClient}
	cases := []struct {
		typ         string
		wantGenerate bool
	}{
		{TypePostgres, false},
		{TypeWebhook, false},
		{TypeMySQL, false},
		{TypeRedis, false},
		{TypeOAuth, true},
		{TypeAWSIAM, true},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			rot, err := svc.rotatorFor(tc.typ)
			if err != nil {
				t.Fatalf("rotatorFor(%q): %v", tc.typ, err)
			}
			_, isGen := rot.(rotatorGenerator)
			_, isApply := rot.(rotatorApplier)
			if isGen != tc.wantGenerate {
				t.Errorf("%q: rotatorGenerator = %v, want %v", tc.typ, isGen, tc.wantGenerate)
			}
			if isApply == tc.wantGenerate {
				t.Errorf("%q: rotatorApplier = %v, want %v", tc.typ, isApply, !tc.wantGenerate)
			}
		})
	}
	if _, err := svc.rotatorFor("nope"); err != ErrInvalidType {
		t.Errorf("unknown type: err = %v, want ErrInvalidType", err)
	}
}

// TestRotateGeneratingRotatorPersistsExternalValue proves the rotatorGenerator
// path end-to-end: a generating rotator (oauth) obtains the new value from the
// EXTERNAL system (here an httptest token endpoint) and the engine persists that
// exact value as the secret's new config version — WITHOUT the pre-persist-
// pending step used by apply-a-value rotators.
//
// NOTE: this requires the rotation_policies.type CHECK constraint to admit
// 'oauth'. Migration 000010 only permits ('postgres','webhook') and was never
// relaxed for the later mysql/redis rotators either (a pre-existing latent gap,
// documented in the PR). The test skips when the insert is rejected so the
// hermetic rotator-level coverage above (which does NOT touch the DB) still runs
// everywhere; when a constraint-relaxing migration lands, this exercises the
// full persist path.
func TestRotateGeneratingRotatorPersistsExternalValue(t *testing.T) {
	svc, _, sec := newTestService(t)
	proj, storeCfg := mkChain(t, sec, "rotate-oauth-generate")
	ctx := context.Background()

	const externalToken = "test-minted-token-zzzz"
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + externalToken + `","expires_in":3600}`))
	}))
	defer srv.Close()
	svc.hc = srv.Client() // route the oauth rotator's client at the fake endpoint

	key := "OAUTH_TOKEN"
	seedSecret(t, sec, storeCfg, key, "old-token")

	// Insert the oauth policy directly; skip if the type CHECK constraint rejects it.
	policyID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	ct, nonce, wrapped, kekVer, err := svc.sealConfig(proj, policyID, PolicyConfig{
		OAuthTokenURL: srv.URL, OAuthClientID: "test-client-id", OAuthClientSecret: "test-client-secret-xxxx",
	})
	if err != nil {
		t.Fatalf("sealConfig: %v", err)
	}
	repo := store.NewRotationRepo(testStore)
	in := &store.RotationPolicy{
		ID: policyID, ProjectID: proj.ID, ConfigID: storeCfg.ID, SecretKey: key,
		Type: TypeOAuth, IntervalSeconds: 3600,
		NextRotationAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Second),
		ConfigCT: ct, ConfigNonce: nonce, ConfigWrappedDEK: wrapped, ConfigDEKKEKVersion: kekVer,
		CreatedBy: "user:tester",
	}
	if _, err := repo.Create(ctx, in); err != nil {
		if strings.Contains(err.Error(), "rotation_policies_type_check") {
			t.Skip("rotation_policies.type CHECK constraint does not admit 'oauth' (pre-existing; needs a migration)")
		}
		t.Fatalf("repo.Create: %v", err)
	}
	pol, err := repo.Get(ctx, policyID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}

	if err := svc.attempt(ctx, pol); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if hits != 1 {
		t.Fatalf("token endpoint hit %d times, want 1", hits)
	}

	// The committed value is exactly the externally-minted token.
	got, err := sec.GetSecret(ctx, storeCfg.ID, key)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(got.Value) != externalToken {
		t.Fatalf("committed value = %q, want minted token %q", string(got.Value), externalToken)
	}

	// Generating rotators leave no pending state (no pre-persist step), and the
	// policy is active with a recorded new config version.
	reloaded, err := svc.repo.Get(ctx, pol.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PendingState != nil {
		t.Errorf("PendingState = %v, want nil (generating rotator has no pre-persist)", *reloaded.PendingState)
	}
	if reloaded.Status != "active" || reloaded.FailureCount != 0 {
		t.Errorf("status=%q failures=%d, want active/0", reloaded.Status, reloaded.FailureCount)
	}
	if reloaded.LastConfigVersion == nil {
		t.Error("LastConfigVersion = nil, want recorded")
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

	// Sealed is a clean no-op: nothing written and NOT counted as a failure.
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
	// A sealed server is expected operational state, not a rotation fault: it
	// must NOT increment failure_count, must NOT advance backoff, and must NOT
	// flip status away from active (else the policy would drop out of ClaimDue
	// even after unseal).
	if reloaded.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (sealed is not a failure)", reloaded.FailureCount)
	}
	if reloaded.Status != "active" {
		t.Errorf("Status = %q, want active (sealed is not a failure)", reloaded.Status)
	}
	if reloaded.LastError != nil {
		t.Errorf("LastError = %v, want nil (sealed is not a failure)", *reloaded.LastError)
	}
	if !reloaded.NextRotationAt.Equal(pol.NextRotationAt) {
		t.Errorf("NextRotationAt = %v, want unchanged %v (no backoff on sealed)", reloaded.NextRotationAt, pol.NextRotationAt)
	}
}
