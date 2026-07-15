package masterkeys

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testStore *store.Store  // nil if Docker is unavailable
	testPool  *pgxpool.Pool // separate pool for tamper/verification SQL
	slugSeq   atomic.Int64  // makes per-test project slugs unique
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, dsn, err := startPostgres(ctx)
	if err != nil {
		fmt.Println("masterkeys tests will be skipped: could not start postgres:", err)
		os.Exit(m.Run())
	}
	st, err := store.Open(ctx, dsn)
	if err == nil {
		if mErr := st.Migrate(ctx); mErr == nil {
			testStore = st
			testPool, _ = pgxpool.New(ctx, dsn)
		} else {
			fmt.Println("masterkeys tests will be skipped: migrate failed:", mErr)
			st.Close()
		}
	} else {
		fmt.Println("masterkeys tests will be skipped: open failed:", err)
	}
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	if testStore != nil {
		testStore.Close()
	}
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func startPostgres(ctx context.Context) (testcontainers.Container, string, error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("janus"),
		tcpostgres.WithUsername("janus"),
		tcpostgres.WithPassword("janus-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return nil, "", err
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, "", err
	}
	return ctr, dsn, nil
}

// fakeKMS implements crypto.KMSClient with a reversible transform (prefix). It
// mirrors the crypto package's test fake so a KMSUnsealer can Init/Unseal/Reseal
// deterministically without a real cloud KMS.
type fakeKMS struct{}

func (fakeKMS) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return append([]byte("kms:"), plaintext...), nil
}

func (fakeKMS) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	return bytes.TrimPrefix(ciphertext, []byte("kms:")), nil
}

// harness bundles a rotation Service together with a secrets.Service (sharing
// the same unsealed keyring and store) so a test can write/read secrets through
// the real read path and rotate the master key.
type harness struct {
	svc   *Service
	sec   *secrets.Service
	kr    *crypto.Keyring
	seals crypto.SealConfigStore
}

// resetSeal truncates seal_config plus every master-wrapped/secret table so each
// harness starts from a clean, uninitialized state. Master-key rotation re-wraps
// EVERY project KEK (including from prior tests, wrapped under a now-discarded
// master), so leftover rows from an earlier harness would fail the rewrap; a full
// reset keeps each test's master the only one in play.
func resetSeal(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`TRUNCATE seal_config, auth_config, oidc_providers,
		          transit_key_versions, transit_keys,
		          project_kek_versions, projects
		 RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// newKMSHarness builds a rotation Service over a KMS-unsealed keyring: it
// initializes an awskms seal_config via a KMSUnsealer(fake), unseals it into the
// keyring, and wires a secrets.Service on the same keyring/store.
func newKMSHarness(t *testing.T) *harness {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetSeal(t)
	ctx := context.Background()

	seals := store.NewSealConfigStore(testStore)
	unsealer := crypto.NewKMSUnsealer(seals, fakeKMS{})
	if _, err := unsealer.Init(ctx); err != nil {
		t.Fatalf("KMS Init: %v", err)
	}
	master, err := unsealer.Unseal(ctx)
	if err != nil {
		t.Fatalf("KMS Unseal: %v", err)
	}
	kr := crypto.NewKeyring()
	if err := kr.Unseal(master); err != nil {
		t.Fatalf("keyring Unseal: %v", err)
	}

	svc := NewService(kr, unsealer, store.NewMasterKeyRepo(testStore), seals)
	return &harness{svc: svc, sec: secrets.NewService(testStore, kr), kr: kr, seals: seals}
}

// newShamirHarness builds a rotation Service over a Shamir-unsealed keyring. The
// single-call Rotate must reject this instance with ErrShamirCeremonyRequired.
func newShamirHarness(t *testing.T) *harness {
	t.Helper()
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetSeal(t)
	ctx := context.Background()

	seals := store.NewSealConfigStore(testStore)
	unsealer := crypto.NewShamirUnsealer(seals, 1, 1) // 1-of-1: share is the master
	res, err := unsealer.Init(ctx)
	if err != nil {
		t.Fatalf("Shamir Init: %v", err)
	}
	if _, err := unsealer.SubmitShare(ctx, res.Shares[0]); err != nil {
		t.Fatalf("SubmitShare: %v", err)
	}
	master, err := unsealer.Unseal(ctx)
	if err != nil {
		t.Fatalf("Shamir Unseal: %v", err)
	}
	kr := crypto.NewKeyring()
	if err := kr.Unseal(master); err != nil {
		t.Fatalf("keyring Unseal: %v", err)
	}

	svc := NewService(kr, unsealer, store.NewMasterKeyRepo(testStore), seals)
	return &harness{svc: svc, sec: secrets.NewService(testStore, kr), kr: kr, seals: seals}
}

// mkChain creates a fresh project→env→config chain with a unique project slug,
// returning the project id and config id.
func (h *harness) mkChain(t *testing.T) (projectID, configID string) {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("proj-%d", slugSeq.Add(1))
	p, err := h.sec.CreateProject(ctx, slug, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e, err := h.sec.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	c, err := h.sec.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return p.ID, c.ID
}

// writeSecret sets one key/value in a config as a batched save.
func writeSecret(t *testing.T, sec *secrets.Service, configID, key, val string) {
	t.Helper()
	if _, err := sec.SetSecrets(context.Background(), configID,
		[]secrets.SecretChange{{Key: key, Value: []byte(val)}}, "m", "u"); err != nil {
		t.Fatalf("SetSecrets %s: %v", key, err)
	}
}

// reveal returns the plaintext of one key through the real (rotation-aware) read path.
func reveal(t *testing.T, sec *secrets.Service, configID, key string) string {
	t.Helper()
	got, err := sec.GetSecret(context.Background(), configID, key)
	if err != nil {
		t.Fatalf("GetSecret %s: %v", key, err)
	}
	return string(got.Value)
}
