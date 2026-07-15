package store

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// testKey returns 32 bytes all equal to b — a deterministic AES-256 key for tests.
func testKey(b byte) []byte {
	k := make([]byte, crypto.KeySize)
	for i := range k {
		k[i] = b
	}
	return k
}

// seededProjectID is captured by seedMasterWrapped so assertions can address the row.
var seededProjectID string

// seededDeletedProjectID is a SOFT-DELETED project captured by seedMasterWrapped;
// its wrapped_kek must still be re-wrapped by a master-key rotation (soft delete
// is reversible via Undelete, so stranding its KEK would cause data loss).
var seededDeletedProjectID string

// seededTransitName is the transit key name captured by seedMasterWrapped.
const seededTransitName = "test-transit"

// rewrapClosure returns a caller closure that decrypts under m1 and re-encrypts
// under m2 for the same AAD — exactly what the service layer will supply.
func rewrapClosure(m1, m2 []byte) func(old, aad []byte) ([]byte, error) {
	return func(old, aad []byte) ([]byte, error) {
		ct, err := crypto.ParseCiphertext(old)
		if err != nil {
			return nil, err
		}
		pt, err := crypto.Decrypt(m1, ct, aad)
		if err != nil {
			return nil, err
		}
		nc, err := crypto.Encrypt(m2, pt, aad)
		if err != nil {
			return nil, err
		}
		return nc.Marshal(), nil
	}
}

// resealShamir returns a reseal closure yielding a fresh Shamir SealConfig
// wrapped under m2. The store only writes the KCV; it does not verify it.
func resealShamir(m2 []byte) func() (*crypto.SealConfig, error) {
	return func() (*crypto.SealConfig, error) {
		kcv, err := crypto.Encrypt(m2, []byte("janus-key-check-v1"), []byte("janus:kcv"))
		if err != nil {
			return nil, err
		}
		return &crypto.SealConfig{
			Type:          crypto.SealTypeShamir,
			Threshold:     3,
			Shares:        5,
			KeyCheckValue: kcv.Marshal(),
		}, nil
	}
}

// seedMasterWrapped clears the master-wrapped tables and inserts one row in each,
// all wrapped under m1, so a rotation can be exercised end to end.
func seedMasterWrapped(t *testing.T, st *Store, m1 []byte) {
	t.Helper()
	ctx := context.Background()

	// Isolate: resetDB does not truncate the transit/oidc/auth tables.
	if _, err := st.pool.Exec(ctx,
		`TRUNCATE seal_config, auth_config, oidc_providers,
		          transit_key_versions, transit_keys,
		          project_kek_versions, projects RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("seed truncate: %v", err)
	}

	mustMarshal := func(ct crypto.Ciphertext, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatalf("wrap: %v", err)
		}
		return ct.Marshal()
	}

	// seal_config (id=1) with a KCV under m1; migration adds master_key_version DEFAULT 1.
	kcv := mustMarshal(crypto.Encrypt(m1, []byte("janus-key-check-v1"), []byte("janus:kcv")))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO seal_config (id, type, threshold, shares, key_check_value)
		 VALUES (1, 'shamir', 3, 5, $1)
		 ON CONFLICT (id) DO NOTHING`, kcv); err != nil {
		t.Fatalf("seed seal_config: %v", err)
	}

	// projects: capture the generated id, wrap a KEK bound to it.
	pid, err := st.NewID(ctx)
	if err != nil {
		t.Fatalf("seed newid: %v", err)
	}
	seededProjectID = pid
	projKEK := mustMarshal(crypto.WrapKey(m1, testKey(0xAA), crypto.ProjectKEKAAD(pid)))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO projects (id, slug, name, wrapped_kek, kek_version)
		 VALUES ($1::uuid, 'seed-proj', 'Seed Proj', $2, 1)`, pid, projKEK); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	// A SOFT-DELETED project (deleted_at set). Undelete is reversible, so its
	// wrapped_kek must be re-wrapped by rotation too — otherwise undelete strands it.
	pid2, err := st.NewID(ctx)
	if err != nil {
		t.Fatalf("seed newid (deleted): %v", err)
	}
	seededDeletedProjectID = pid2
	delKEK := mustMarshal(crypto.WrapKey(m1, testKey(0xAD), crypto.ProjectKEKAAD(pid2)))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO projects (id, slug, name, wrapped_kek, kek_version, deleted_at)
		 VALUES ($1::uuid, 'seed-proj-del', 'Seed Proj Deleted', $2, 1, now())`, pid2, delKEK); err != nil {
		t.Fatalf("seed deleted project: %v", err)
	}

	// project_kek_versions: a superseded v1, same AAD (project-bound).
	pkv := mustMarshal(crypto.WrapKey(m1, testKey(0xAB), crypto.ProjectKEKAAD(pid)))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO project_kek_versions (project_id, version, wrapped_kek)
		 VALUES ($1::uuid, 1, $2)`, pid, pkv); err != nil {
		t.Fatalf("seed project_kek_versions: %v", err)
	}

	// auth_config (single row).
	authKey := mustMarshal(crypto.WrapKey(m1, testKey(0xBB), crypto.AuthKeyAAD()))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO auth_config (id, wrapped_token_hmac_key) VALUES (1, $1)`, authKey); err != nil {
		t.Fatalf("seed auth_config: %v", err)
	}

	// oidc_providers (arbitrary-length secret, not key-sized).
	oidcSecret := mustMarshal(crypto.Encrypt(m1, []byte("client-secret-xyz"), crypto.OIDCClientSecretAAD()))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO oidc_providers (name, issuer, client_id, wrapped_client_secret, redirect_url)
		 VALUES ('seed-oidc', 'https://issuer.example', 'client-1', $1, 'https://app/cb')`, oidcSecret); err != nil {
		t.Fatalf("seed oidc_providers: %v", err)
	}

	// transit_keys + transit_key_versions (version 1, AAD binds name+version).
	tkID, err := st.NewID(ctx)
	if err != nil {
		t.Fatalf("seed transit newid: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO transit_keys (id, name, key_type, latest_version, min_decryption_version)
		 VALUES ($1::uuid, $2, 'aes256-gcm', 1, 1)`, tkID, seededTransitName); err != nil {
		t.Fatalf("seed transit_keys: %v", err)
	}
	tkvID, err := st.NewID(ctx)
	if err != nil {
		t.Fatalf("seed transit version newid: %v", err)
	}
	tmat := mustMarshal(crypto.WrapKey(m1, testKey(0xCC), crypto.TransitKeyAAD(seededTransitName, 1)))
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO transit_key_versions (id, transit_key_id, version, wrapped_material)
		 VALUES ($1::uuid, $2::uuid, 1, $3)`, tkvID, tkID, tmat); err != nil {
		t.Fatalf("seed transit_key_versions: %v", err)
	}
}

// unwrapAt fetches a single blob and attempts to decrypt it under key with aad.
func unwrapAt(t *testing.T, st *Store, sql string, key, aad []byte, args ...any) ([]byte, error) {
	t.Helper()
	var blob []byte
	if err := st.pool.QueryRow(context.Background(), sql, args...).Scan(&blob); err != nil {
		t.Fatalf("select blob: %v", err)
	}
	ct, err := crypto.ParseCiphertext(blob)
	if err != nil {
		t.Fatalf("parse blob: %v", err)
	}
	return crypto.Decrypt(key, ct, aad)
}

func assertProjectKEKUnwraps(t *testing.T, st *Store, key []byte) {
	t.Helper()
	assertNamedProjectKEKUnwraps(t, st, seededProjectID, key)
}

// assertNamedProjectKEKUnwraps asserts the wrapped_kek of the given project (and
// its v1 project_kek_versions row) unwraps under key and NOT under the other key.
func assertNamedProjectKEKUnwraps(t *testing.T, st *Store, projectID string, key []byte) {
	t.Helper()
	aad := crypto.ProjectKEKAAD(projectID)
	pt, err := unwrapAt(t, st,
		`SELECT wrapped_kek FROM projects WHERE id=$1::uuid`, key, aad, projectID)
	if err != nil {
		t.Fatalf("project KEK did not unwrap under expected key: %v", err)
	}
	if len(pt) != crypto.KeySize {
		t.Fatalf("project KEK plaintext size = %d, want %d", len(pt), crypto.KeySize)
	}
	// It must NOT unwrap under the other key.
	other := testKey(0x11)
	if key[0] == other[0] {
		other = testKey(0x22)
	}
	if _, err := crypto.Decrypt(other, mustParse(t, st,
		`SELECT wrapped_kek FROM projects WHERE id=$1::uuid`, projectID), aad); err == nil {
		t.Fatalf("project KEK unexpectedly unwrapped under the wrong master key")
	}
	// project_kek_versions row too.
	if _, err := unwrapAt(t, st,
		`SELECT wrapped_kek FROM project_kek_versions WHERE project_id=$1::uuid AND version=1`,
		key, aad, projectID); err != nil {
		t.Fatalf("project_kek_versions did not unwrap under expected key: %v", err)
	}
}

func assertOIDCUnwraps(t *testing.T, st *Store, key []byte) {
	t.Helper()
	if _, err := unwrapAt(t, st,
		`SELECT wrapped_client_secret FROM oidc_providers WHERE name='seed-oidc'`,
		key, crypto.OIDCClientSecretAAD()); err != nil {
		t.Fatalf("oidc client secret did not unwrap under expected key: %v", err)
	}
}

func assertTransitUnwraps(t *testing.T, st *Store, key []byte) {
	t.Helper()
	if _, err := unwrapAt(t, st,
		`SELECT wrapped_material FROM transit_key_versions WHERE version=1`,
		key, crypto.TransitKeyAAD(seededTransitName, 1)); err != nil {
		t.Fatalf("transit material did not unwrap under expected key: %v", err)
	}
}

func assertAuthUnwraps(t *testing.T, st *Store, key []byte) {
	t.Helper()
	if _, err := unwrapAt(t, st,
		`SELECT wrapped_token_hmac_key FROM auth_config WHERE id=1`,
		key, crypto.AuthKeyAAD()); err != nil {
		t.Fatalf("auth hmac key did not unwrap under expected key: %v", err)
	}
}

func mustParse(t *testing.T, st *Store, sql string, args ...any) crypto.Ciphertext {
	t.Helper()
	var blob []byte
	if err := st.pool.QueryRow(context.Background(), sql, args...).Scan(&blob); err != nil {
		t.Fatalf("select blob: %v", err)
	}
	ct, err := crypto.ParseCiphertext(blob)
	if err != nil {
		t.Fatalf("parse blob: %v", err)
	}
	return ct
}

func TestRewrapAllUnderNewMasterRoundTrip(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	m1 := testKey(0x11)
	m2 := testKey(0x22)
	seedMasterWrapped(t, st, m1)

	repo := NewMasterKeyRepo(st)
	if err := repo.RewrapAllUnderNewMaster(ctx, rewrapClosure(m1, m2), resealShamir(m2)); err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	assertProjectKEKUnwraps(t, st, m2)
	// The soft-deleted project's wrapped_kek must be re-wrapped under M2 too.
	if _, err := unwrapAt(t, st,
		`SELECT wrapped_kek FROM projects WHERE id=$1::uuid`,
		m2, crypto.ProjectKEKAAD(seededDeletedProjectID), seededDeletedProjectID); err != nil {
		t.Fatalf("soft-deleted project KEK did not unwrap under M2: %v", err)
	}
	assertAuthUnwraps(t, st, m2)
	assertOIDCUnwraps(t, st, m2)
	assertTransitUnwraps(t, st, m2)

	// Defense-in-depth: a non-projects blob must NOT decrypt under the old master.
	if _, err := crypto.Decrypt(m1, mustParse(t, st,
		`SELECT wrapped_client_secret FROM oidc_providers WHERE name='seed-oidc'`),
		crypto.OIDCClientSecretAAD()); err == nil {
		t.Fatalf("oidc client secret unexpectedly decrypted under the old master")
	}

	meta, err := repo.GetMasterKeyMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 2 || meta.RotatedAt == nil {
		t.Fatalf("meta not updated: %+v", meta)
	}
	if meta.SealType != crypto.SealTypeShamir {
		t.Fatalf("seal type = %q, want %q", meta.SealType, crypto.SealTypeShamir)
	}
}

func TestRewrapAllRollsBackOnResealError(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	m1 := testKey(0x11)
	m2 := testKey(0x22)
	seedMasterWrapped(t, st, m1)

	repo := NewMasterKeyRepo(st)
	boom := errors.New("reseal failed")
	err := repo.RewrapAllUnderNewMaster(ctx, rewrapClosure(m1, m2),
		func() (*crypto.SealConfig, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want reseal error, got %v", err)
	}
	// Nothing changed: still M1, version still 1.
	assertProjectKEKUnwraps(t, st, m1)
	assertAuthUnwraps(t, st, m1)
	assertOIDCUnwraps(t, st, m1)
	assertTransitUnwraps(t, st, m1)

	meta, err := repo.GetMasterKeyMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 1 {
		t.Fatalf("version changed despite rollback: %d", meta.Version)
	}
	if meta.RotatedAt != nil {
		t.Fatalf("rotated_at set despite rollback: %v", meta.RotatedAt)
	}
}

func TestMasterKeyMetaInitial(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	seedMasterWrapped(t, st, testKey(0x11))
	repo := NewMasterKeyRepo(st)
	meta, err := repo.GetMasterKeyMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 1 || meta.RotatedAt != nil || meta.SealType != "shamir" {
		t.Fatalf("initial meta = %+v", meta)
	}
}
