package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// bootPostgres starts a throwaway Postgres and returns its DSN.
func bootPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("janus"),
		tcpostgres.WithUsername("janus"),
		tcpostgres.WithPassword("janus-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skip("postgres/docker not available:", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestBootFreshDatabaseMigratesAndStaysSealed(t *testing.T) {
	dsn := bootPostgres(t)
	srv, st, err := Boot(context.Background(), BootConfig{
		DatabaseURL: dsn,
		SealType:    crypto.SealTypeShamir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !srv.keyring.Sealed() {
		t.Fatal("fresh boot must be sealed")
	}
	// Migrations applied: the seal config store works (returns uninitialized).
	if _, err := srv.seals.Get(context.Background()); err != crypto.ErrNoSealConfig {
		t.Fatalf("seal store on fresh db: %v, want ErrNoSealConfig", err)
	}
}

func TestBootSealTypeRequiredWhenUninitialized(t *testing.T) {
	dsn := bootPostgres(t)
	_, _, err := Boot(context.Background(), BootConfig{DatabaseURL: dsn})
	if err == nil || !strings.Contains(err.Error(), "JANUS_SEAL_TYPE") {
		t.Fatalf("uninitialized boot without seal type: %v", err)
	}
}

func TestBootTypeMismatchIsFatal(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()

	// First boot + init as shamir.
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	u := crypto.NewShamirUnsealer(srv.seals, 1, 1)
	if _, err := u.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Second boot claiming awskms → fatal.
	if _, _, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeAWSKMS}); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("type mismatch boot: %v", err)
	}
}

func TestBootKMSAutoUnseal(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()
	client := &fakeKMS{}
	factory := func(context.Context) (crypto.KMSClient, error) { return client, nil }

	// Boot 1: uninitialized; init via the unsealer, which auto-wraps.
	srv1, st1, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeAWSKMS, NewKMSClient: factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv1.unsealer.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st1.Close()

	// Boot 2: initialized KMS seal → auto-unseals at boot.
	srv2, st2, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: "", NewKMSClient: factory, // type from storage
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if srv2.keyring.Sealed() {
		t.Fatal("initialized kms boot must auto-unseal")
	}
	// The first-unseal hook must have materialized the token-HMAC key.
	if _, err := srvStoreAuthKey(ctx, srv2); err != nil {
		t.Fatalf("hmac key not bootstrapped at unseal: %v", err)
	}

	// Boot 3: KMS down at boot → stays sealed but serves; retry endpoint works.
	client.fail = true
	srv3, st3, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, NewKMSClient: factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	if !srv3.keyring.Sealed() {
		t.Fatal("boot during kms outage must stay sealed, not fail")
	}
	client.fail = false
	if err := srv3.unsealNow(ctx); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	if srv3.keyring.Sealed() {
		t.Fatal("retry should unseal")
	}
}

// srvStoreAuthKey reads the wrapped HMAC key through the server's own repos.
func srvStoreAuthKey(ctx context.Context, srv *Server) ([]byte, error) {
	return srv.auth.WrappedHMACKeyForTest(ctx)
}

func TestBootReconcilesInstanceOwner(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()

	// Boot 1 + bootstrap: the admin becomes instance owner via bootstrapAdmin.
	// bootstrapAdmin only creates a user (Argon2id hash + store write) and grants
	// the owner binding — none of which touches the sealed keyring — so no
	// init/unseal ceremony is needed here.
	srv1, st1, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv1.bootstrapAdmin(ctx, "root@corp.io"); err != nil {
		t.Fatal(err)
	}
	rb := store.NewRoleBindingRepo(st1)
	if n, _ := rb.CountInstanceOwners(ctx); n != 1 {
		t.Fatalf("owners after bootstrap: %d", n)
	}

	// Remove the owner binding, re-boot → reconciliation regrants it to the
	// oldest user (never-lock-out self-heal).
	members, _ := rb.ListForScope(ctx, "instance", "")
	if len(members) != 1 {
		t.Fatalf("instance members: %d", len(members))
	}
	if err := rb.DeleteForSubjectScope(ctx, members[0].SubjectUserID, "instance", nil, nil); err != nil {
		t.Fatal(err)
	}
	st1.Close()

	srv2, st2, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	_ = srv2
	if n, _ := store.NewRoleBindingRepo(st2).CountInstanceOwners(ctx); n != 1 {
		t.Fatalf("owners after reconciliation: %d, want 1", n)
	}
}
