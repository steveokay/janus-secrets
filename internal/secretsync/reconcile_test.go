package secretsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crypto/rand"

	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
	"golang.org/x/crypto/nacl/box"
)

// TestProjectAuthorizer is the definitive, DB-free security unit test: the
// system authorizer must permit same-project reference targets and REFUSE
// cross-project ones (the cross-project-exfil guard).
func TestProjectAuthorizer(t *testing.T) {
	a := projectAuthorizer{projectID: "p1"}
	if err := a.CanReadSecrets(context.Background(), resolve.RawConfig{ProjectID: "p1"}); err != nil {
		t.Fatalf("same-project target: got %v, want nil", err)
	}
	err := a.CanReadSecrets(context.Background(), resolve.RawConfig{ProjectID: "p2"})
	if !errors.Is(err, resolve.ErrForbiddenReference) {
		t.Fatalf("cross-project target: got %v, want ErrForbiddenReference", err)
	}
}

// reconcileGHServer is a fake GitHub API that (a) serves a real NaCl public key
// and (b) counts/records PUTs, with an injectable status override to simulate
// failures.
type reconcileGHServer struct {
	srv        *httptest.Server
	pubKeyHits int
	puts       map[string]bool
	failStatus int // 0 = OK; else returned for all endpoints
}

func newReconcileGHServer(t *testing.T) *reconcileGHServer {
	t.Helper()
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	f := &reconcileGHServer{puts: map[string]bool{}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.failStatus != 0 {
			w.WriteHeader(f.failStatus)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/public-key"):
			f.pubKeyHits++
			_ = json.NewEncoder(w).Encode(ghPublicKey{
				KeyID: "kid-1",
				Key:   base64.StdEncoding.EncodeToString(pub[:]),
			})
		case r.Method == http.MethodPut:
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			f.puts[name] = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.srv = httptest.NewServer(h)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *reconcileGHServer) reset() {
	f.pubKeyHits = 0
	f.puts = map[string]bool{}
}

// seedGHTarget creates a github sync target for cfg with the given desired
// secrets already set, pointing at the fake server. Returns the target.
func seedGHTarget(t *testing.T, svc *Service, sec *secrets.Service, proj *store.Project, cfg *store.Config, kv map[string]string) *store.SyncTarget {
	t.Helper()
	ctx := context.Background()

	changes := make([]secrets.SecretChange, 0, len(kv))
	for k, v := range kv {
		changes = append(changes, secrets.SecretChange{Key: k, Value: []byte(v)})
	}
	if _, err := sec.SetSecrets(ctx, cfg.ID, changes, "seed", "user:tester"); err != nil {
		t.Fatalf("SetSecrets: %v", err)
	}

	targetID, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	ct, nonce, wrapped, kekVer, err := svc.sealCreds(proj, targetID, Creds{PAT: "ghp_x"})
	if err != nil {
		t.Fatalf("sealCreds: %v", err)
	}
	in := &store.SyncTarget{
		ID:                 targetID,
		ProjectID:          proj.ID,
		ConfigID:           cfg.ID,
		Provider:           ProviderGitHub,
		Prune:              true,
		IntervalSeconds:    3600,
		NextSyncAt:         time.Now().UTC(),
		CredsCT:            ct,
		CredsNonce:         nonce,
		CredsWrappedDEK:    wrapped,
		CredsDEKKEKVersion: kekVer,
		Addr:               []byte(`{"owner":"o","repo":"r"}`),
		CreatedBy:          "user:tester",
	}
	tgt, err := svc.repo.Create(ctx, in)
	if err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	return tgt
}

func TestReconcileSyncsResolvedSecrets(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "sync-reconcile-basic")
	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"})

	ctx := context.Background()
	if err := svc.attempt(ctx, tgt, false); err != nil {
		t.Fatalf("attempt: %v", err)
	}

	if !fake.puts["API_KEY"] || !fake.puts["DB_URL"] {
		t.Fatalf("fake did not receive both PUTs: %v", fake.puts)
	}

	got, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if len(got.ManagedKeys) != 2 {
		t.Fatalf("ManagedKeys = %v, want 2 keys", got.ManagedKeys)
	}
	if got.SyncedFingerprint == nil {
		t.Fatal("SyncedFingerprint is nil after successful sync")
	}
	// synced_config_version must reflect the config's current version (the seeded
	// secrets created v1+), not a frozen 0.
	if got.SyncedConfigVersion == nil || *got.SyncedConfigVersion < 1 {
		t.Fatalf("SyncedConfigVersion = %v, want the config's current version (>=1)", got.SyncedConfigVersion)
	}
	if got.Status != "active" {
		t.Fatalf("Status = %q, want active", got.Status)
	}
	if got.FailureCount != 0 {
		t.Fatalf("FailureCount = %d, want 0", got.FailureCount)
	}
}

func TestReconcileSkipsWhenUnchanged(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "sync-reconcile-skip")
	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"API_KEY": "s3cret"})

	ctx := context.Background()
	if err := svc.attempt(ctx, tgt, false); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if fake.pubKeyHits == 0 || len(fake.puts) == 0 {
		t.Fatalf("first sync made no calls: pk=%d puts=%v", fake.pubKeyHits, fake.puts)
	}

	// Reload to pick up the stored fingerprint, then sync again with the SAME
	// secrets: change-detection must short-circuit before any external call.
	tgt, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	fake.reset()
	if err := svc.attempt(ctx, tgt, false); err != nil {
		t.Fatalf("unchanged attempt: %v", err)
	}
	if fake.pubKeyHits != 0 || len(fake.puts) != 0 {
		t.Fatalf("unchanged sync made calls: pk=%d puts=%v (want none)", fake.pubKeyHits, fake.puts)
	}

	// force=true must sync again even though the fingerprint matches.
	if err := svc.attempt(ctx, tgt, true); err != nil {
		t.Fatalf("forced attempt: %v", err)
	}
	if fake.pubKeyHits == 0 || !fake.puts["API_KEY"] {
		t.Fatalf("forced sync made no calls: pk=%d puts=%v", fake.pubKeyHits, fake.puts)
	}
}

func TestReconcileSealedNoop(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "sync-reconcile-sealed")
	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"API_KEY": "s3cret"})

	ctx := context.Background()
	// Seal the keyring: openCreds → unwrapProjectKEK returns ErrSealed.
	svc.kr.(interface{ Seal() }).Seal()

	err := svc.attempt(ctx, tgt, false)
	if !errors.Is(err, ErrSealed) {
		t.Fatalf("attempt while sealed: got %v, want ErrSealed", err)
	}
	if fake.pubKeyHits != 0 || len(fake.puts) != 0 {
		t.Fatalf("sealed sync made calls: pk=%d puts=%v (want none)", fake.pubKeyHits, fake.puts)
	}

	got, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if got.FailureCount != 0 {
		t.Fatalf("sealed must not bump failure_count: got %d", got.FailureCount)
	}
	if got.Status != "active" {
		t.Fatalf("sealed must not flip status: got %q", got.Status)
	}
}

func TestReconcileFailureBackoff(t *testing.T) {
	svc, sec := newTestService(t)
	fake := newReconcileGHServer(t)
	fake.failStatus = http.StatusInternalServerError // provider apply fails
	svc.githubBaseURL = fake.srv.URL

	proj, cfg := mkChain(t, sec, "sync-reconcile-fail")
	tgt := seedGHTarget(t, svc, sec, proj, cfg, map[string]string{"API_KEY": "s3cret"})

	ctx := context.Background()
	before := time.Now().UTC()
	if err := svc.attempt(ctx, tgt, false); err == nil {
		t.Fatal("attempt: got nil, want error")
	}

	got, err := svc.repo.Get(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if got.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", got.FailureCount)
	}
	if got.Status != "active" {
		t.Fatalf("Status = %q, want active (below threshold)", got.Status)
	}
	if got.LastError == nil || *got.LastError != "apply failed" {
		t.Fatalf("LastError = %v, want \"apply failed\"", got.LastError)
	}
	// next_sync_at advanced ~1m (backoff base) into the future.
	if !got.NextSyncAt.After(before.Add(30 * time.Second)) {
		t.Fatalf("NextSyncAt = %v, want >= ~1m after %v", got.NextSyncAt, before)
	}
}
