# Master-Key Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rotate the master key online — mint fresh 256-bit master material, re-wrap every master-wrapped blob under it in one atomic transaction, re-seal (new Shamir shares / new KMS blob + fresh KCV), swap the in-memory master — owner-only, value-free, never decrypting a secret value.

**Architecture:** Eager-atomic re-wrap (no lazy versioning). `internal/masterkeys` orchestrates; `internal/crypto` gains `Keyring.RotateMaster` (holds the write-lock across persist+swap) and `Unsealer.Reseal`; `internal/store/masterkey.go` does the single `withTx` across five tables; API/CLI/UI mirror the merged project-KEK rotation feature. Shamir adds a proof-of-possession rekey ceremony; KMS is a single call.

**Tech Stack:** Go (stdlib `crypto/*` + `x/crypto`, `pgx`, `chi`, `cobra`), PostgreSQL, React/TypeScript (Nocturne tokens). testcontainers for store/service integration tests.

**Spec:** `docs/superpowers/specs/2026-07-15-master-key-rotation-design.md`. **Mirror the merged project-KEK rotation** at every layer: `internal/projectkeys/service.go`, `internal/store/project_kek_versions.go`, `internal/store/projects.go` (`RotateKEK` closure pattern), `internal/api/kek_handlers.go`, `cmd/janus/project_commands.go`, `internal/authz/actions.go` (`KEKManage`).

**Global rules (every task):**
- Never log/return/audit a secret value, key, or share. Leak tests enforce.
- AES-256-GCM, fresh random nonces, byte-identical AAD on re-wrap. stdlib crypto only.
- Value-free: rotation touches only key blobs (project KEKs, superseded KEK versions, auth key, OIDC secrets, transit material) — never a DEK or secret value.
- Do NOT run `make migrate`; store/service tests use testcontainers. Do NOT touch the running dev container (ports 8210/5433) except the final sanctioned rebuild.
- Trust `go build`/`go test`, not stale-LSP `undefined` noise.
- Commit after each task with explicit paths (never `git add -A`). Co-author trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## File Structure

**Create:**
- `migrations/000019_master_key_rotation.up.sql` / `.down.sql` — `seal_config` version + rotated_at columns.
- `internal/store/masterkey.go` — `MasterKeyRepo`: `RewrapAllUnderNewMaster`, `GetMasterKeyMeta`.
- `internal/store/masterkey_test.go` — testcontainers round-trip + rollback.
- `internal/masterkeys/service.go` — orchestrator (KMS rotate + Shamir ceremony).
- `internal/masterkeys/service_test.go` — round-trip, never-decrypts-value, ceremony, atomicity.
- `internal/masterkeys/leak_test.go` — sentinel-not-in-logs.
- `internal/api/masterkey_handlers.go` — HTTP handlers.
- `internal/api/masterkey_e2e_test.go` — owner-only / sealed / KMS / Shamir e2e.
- `cmd/janus/masterkey_commands.go` — `janus master-key` CLI.
- `cmd/janus/masterkey_commands_test.go` — CLI wire tests.
- `web/src/settings/MasterKeySection.tsx` + `.test.tsx` — Settings UI.

**Modify:**
- `internal/crypto/keyring.go` — add `RotateMaster`; remove `TODO(rotation)`.
- `internal/crypto/keyring_test.go` — `RotateMaster` unit tests.
- `internal/crypto/unseal.go` — add `Reseal` to `Unsealer` interface + `ReconstructAndVerifyShamir` helper.
- `internal/crypto/shamir.go` — implement `Reseal` on `ShamirUnsealer`.
- `internal/crypto/kms.go` — implement `Reseal` on `KMSUnsealer`.
- `internal/crypto/unseal_test.go` / `shamir_unsealer_test.go` / `kms_test.go` — `Reseal` tests.
- `internal/authz/actions.go` — add `SysMasterKey` action to owner set.
- `internal/authz/actions_test.go` — role-matrix assertion.
- `internal/api/server.go` — register routes + `masterKeys` field.
- `internal/api/boot.go` — construct + wire `masterkeys.Service`.
- `cmd/janus/main.go` — register `newMasterKeyCmd()`.
- `web/src/lib/endpoints.ts` — master-key endpoints + types.
- `web/src/settings/SettingsPage.tsx` (or the instance-seal host) — mount `MasterKeySection`.
- `status.md` — feature status entry (final task).

---

## Task 1: Migration — seal_config version + rotated_at

**Files:**
- Create: `migrations/000019_master_key_rotation.up.sql`
- Create: `migrations/000019_master_key_rotation.down.sql`

- [ ] **Step 1: Write the up migration**

`migrations/000019_master_key_rotation.up.sql`:
```sql
-- Master-key rotation observability. seal_config is the single-row (id=1)
-- seal metadata table. Existing instances default to version 1 (never rotated).
ALTER TABLE seal_config
    ADD COLUMN master_key_version    integer     NOT NULL DEFAULT 1,
    ADD COLUMN master_key_rotated_at timestamptz;
```

- [ ] **Step 2: Write the down migration**

`migrations/000019_master_key_rotation.down.sql`:
```sql
ALTER TABLE seal_config
    DROP COLUMN IF EXISTS master_key_rotated_at,
    DROP COLUMN IF EXISTS master_key_version;
```

- [ ] **Step 3: Verify migration parses (golang-migrate loads on server boot / testcontainers)**

Run: `go build ./...`
Expected: builds clean (migrations are embedded; a syntax error surfaces in store tests later).

- [ ] **Step 4: Commit**

```bash
git add migrations/000019_master_key_rotation.up.sql migrations/000019_master_key_rotation.down.sql
git commit -m "feat(migrate): seal_config master_key_version + rotated_at (000019)"
```

---

## Task 2: Keyring.RotateMaster

Adds the in-memory rotation primitive. It holds the **write-lock** across the whole operation so no concurrent unwrap sees a half-swapped master. It exposes an unwrap-under-old + wrap-under-new closure pair to the caller, runs the caller's `persist` (DB tx) while still holding the lock, and only on success swaps `master` to the new key and zeroizes the old.

**Files:**
- Modify: `internal/crypto/keyring.go`
- Test: `internal/crypto/keyring_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/crypto/keyring_test.go`:
```go
func TestRotateMasterRewrapsAndSwaps(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	if err := kr.Unseal(m1); err != nil {
		t.Fatal(err)
	}
	// A project KEK wrapped under M1.
	kek, _ := GenerateKey()
	wrapped, err := kr.WrapProjectKEK(kek, "p1")
	if err != nil {
		t.Fatal(err)
	}
	old := wrapped.Marshal()

	m2, _ := GenerateKey()
	var newBlob []byte
	persisted := false
	err = kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			pt, uerr := unwrap(old, ProjectKEKAAD("p1"))
			if uerr != nil {
				return uerr
			}
			defer zero(pt)
			nb, werr := wrap(pt, ProjectKEKAAD("p1"))
			newBlob = nb
			return werr
		},
		func() error { persisted = true; return nil },
	)
	if err != nil {
		t.Fatalf("RotateMaster: %v", err)
	}
	if !persisted {
		t.Fatal("persist not called")
	}
	// The new blob must unwrap under the swapped-in master to the same KEK.
	ct, _ := ParseCiphertext(newBlob)
	got, err := kr.UnwrapProjectKEK(ct, "p1")
	if err != nil {
		t.Fatalf("unwrap after rotate: %v", err)
	}
	if !bytes.Equal(got, kek) {
		t.Fatal("re-wrapped KEK mismatch")
	}
	// The old blob must NO LONGER unwrap (master changed).
	if _, err := kr.UnwrapProjectKEK(mustParse(t, old), "p1"); err == nil {
		t.Fatal("old blob still unwraps after rotation — master not swapped")
	}
}

func TestRotateMasterPersistFailureKeepsOldMaster(t *testing.T) {
	kr := NewKeyring()
	m1, _ := GenerateKey()
	_ = kr.Unseal(m1)
	kek, _ := GenerateKey()
	wrapped, _ := kr.WrapProjectKEK(kek, "p1")

	m2, _ := GenerateKey()
	wantErr := errors.New("db down")
	err := kr.RotateMaster(m2,
		func(unwrap func([]byte, []byte) ([]byte, error), wrap func([]byte, []byte) ([]byte, error)) error {
			return nil
		},
		func() error { return wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want persist error, got %v", err)
	}
	// Old master still installed: original blob still unwraps.
	if _, err := kr.UnwrapProjectKEK(wrapped, "p1"); err != nil {
		t.Fatalf("old master lost after failed persist: %v", err)
	}
}

func TestRotateMasterSealed(t *testing.T) {
	kr := NewKeyring()
	m2, _ := GenerateKey()
	err := kr.RotateMaster(m2,
		func(_ func([]byte, []byte) ([]byte, error), _ func([]byte, []byte) ([]byte, error)) error { return nil },
		func() error { return nil })
	if !errors.Is(err, ErrSealed) {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}

func mustParse(t *testing.T, b []byte) Ciphertext {
	t.Helper()
	ct, err := ParseCiphertext(b)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}
```

Ensure the test file imports `bytes` and `errors`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/crypto/ -run TestRotateMaster`
Expected: FAIL — `kr.RotateMaster undefined`.

- [ ] **Step 3: Implement RotateMaster**

In `internal/crypto/keyring.go`, first replace the `TODO(rotation)` comment on `WrapProjectKEK` (lines 48–52) with:
```go
// WrapProjectKEK wraps a project KEK under the master key, bound to projectID.
// Master-key rotation (see Keyring.RotateMaster) re-wraps eagerly and
// atomically, so ciphertext carries no master-key version (KeyVersion == 0).
```

Then add:
```go
// RotateMaster swaps the master key to newMaster, re-wrapping caller-supplied
// blobs from the old key to the new one. It holds the write lock for the whole
// operation so no concurrent unwrap observes a half-rotated master.
//
// rewrap receives two closures bound to the old (unwrap) and new (wrap) master:
// it must re-encrypt every master-wrapped blob and stage the new ciphertext for
// persist. persist then commits those new ciphertexts plus the re-seal metadata
// in a single DB transaction. Only if BOTH succeed is the in-memory master
// swapped and the old key zeroized; if either fails the old master is retained
// unchanged. newMaster is copied; the caller zeroes its copy.
//
// unwrap/wrap use Encrypt/Decrypt directly so they handle both 32-byte keys and
// arbitrary-length blobs (e.g. OIDC client secrets). AAD must be byte-identical
// to each blob's read path.
func (k *Keyring) RotateMaster(
	newMaster []byte,
	rewrap func(unwrap func(oldCT, aad []byte) (plain []byte, err error),
		wrap func(plain, aad []byte) (newCT []byte, err error)) error,
	persist func() error,
) error {
	if len(newMaster) != KeySize {
		return ErrInvalidKeySize
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.master == nil {
		return ErrSealed
	}
	nm := append([]byte(nil), newMaster...)

	unwrap := func(oldCT, aad []byte) ([]byte, error) {
		ct, err := ParseCiphertext(oldCT)
		if err != nil {
			return nil, ErrDecryptFailed
		}
		return Decrypt(k.master, ct, aad)
	}
	wrap := func(plain, aad []byte) ([]byte, error) {
		ct, err := Encrypt(nm, plain, aad)
		if err != nil {
			return nil, err
		}
		return ct.Marshal(), nil
	}
	if err := rewrap(unwrap, wrap); err != nil {
		zero(nm)
		return err
	}
	if err := persist(); err != nil {
		zero(nm)
		return err
	}
	zero(k.master)
	k.master = nm
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/crypto/ -run TestRotateMaster`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/keyring.go internal/crypto/keyring_test.go
git commit -m "feat(crypto): Keyring.RotateMaster (atomic master re-wrap + swap)"
```

---

## Task 3: Unsealer.Reseal + Shamir possession helper

Two crypto additions: (a) a stateless `ReconstructAndVerifyShamir` used by the ceremony to prove the operator holds current shares; (b) a `Reseal` method on the `Unsealer` interface that produces the new `SealConfig` (fresh KCV) and, for Shamir, the new shares — preserving the **stored** threshold/shares shape.

**Files:**
- Modify: `internal/crypto/unseal.go`, `internal/crypto/shamir.go`, `internal/crypto/kms.go`
- Test: `internal/crypto/shamir_unsealer_test.go`, `internal/crypto/kms_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/crypto/shamir_unsealer_test.go`:
```go
func TestShamirReconstructAndVerify(t *testing.T) {
	st := newMemSealStore()
	u := NewShamirUnsealer(st, 5, 3)
	res, err := u.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.Get(context.Background())

	// 3 valid shares reconstruct + verify.
	master, err := ReconstructAndVerifyShamir(cfg, res.Shares[:3])
	if err != nil {
		t.Fatalf("verify with 3 shares: %v", err)
	}
	if len(master) != KeySize {
		t.Fatal("bad master length")
	}
	zero(master)

	// Too few shares.
	if _, err := ReconstructAndVerifyShamir(cfg, res.Shares[:2]); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("want ErrNotEnoughShares, got %v", err)
	}
	// A wrong share fails KCV.
	bad := append([][]byte(nil), res.Shares[0], res.Shares[1])
	junk := append([]byte(nil), res.Shares[2]...)
	junk[len(junk)-1] ^= 0xFF
	bad = append(bad, junk)
	if _, err := ReconstructAndVerifyShamir(cfg, bad); err == nil {
		t.Fatal("wrong share passed verification")
	}
}

func TestShamirReseal(t *testing.T) {
	st := newMemSealStore()
	u := NewShamirUnsealer(st, 5, 3)
	if _, err := u.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2, _ := GenerateKey()
	cfg, shares, err := u.Reseal(context.Background(), m2)
	if err != nil {
		t.Fatalf("Reseal: %v", err)
	}
	if cfg.Type != SealTypeShamir || cfg.Threshold != 3 || cfg.Shares != 5 {
		t.Fatalf("shape not preserved: %+v", cfg)
	}
	if len(shares) != 5 {
		t.Fatalf("want 5 new shares, got %d", len(shares))
	}
	// New shares reconstruct to M2 and pass the new KCV.
	got, err := ReconstructAndVerifyShamir(cfg, shares[:3])
	if err != nil {
		t.Fatalf("verify new shares: %v", err)
	}
	if !bytes.Equal(got, m2) {
		t.Fatal("resealed shares do not reconstruct M2")
	}
}
```

Add to `internal/crypto/kms_test.go` (reuse the existing fake `KMSClient` in that file):
```go
func TestKMSReseal(t *testing.T) {
	st := newMemSealStore()
	client := newFakeKMS()
	u := NewKMSUnsealer(st, client)
	if _, err := u.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2, _ := GenerateKey()
	cfg, shares, err := u.Reseal(context.Background(), m2)
	if err != nil {
		t.Fatalf("Reseal: %v", err)
	}
	if shares != nil {
		t.Fatal("KMS reseal must not return shares")
	}
	if cfg.Type != SealTypeAWSKMS || len(cfg.WrappedMasterKey) == 0 {
		t.Fatalf("bad KMS cfg: %+v", cfg)
	}
	// The wrapped master decrypts back to M2 and the KCV verifies.
	pt, err := client.Decrypt(context.Background(), cfg.WrappedMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, m2) {
		t.Fatal("KMS reseal wrapped wrong key")
	}
	if err := verifyKCV(m2, cfg.KeyCheckValue); err != nil {
		t.Fatalf("KCV: %v", err)
	}
}
```

> If helper names differ (`newMemSealStore`, `newFakeKMS`), reuse whatever the existing `*_test.go` files in `internal/crypto/` already define — grep before writing.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/crypto/ -run 'Reseal|ReconstructAndVerify'`
Expected: FAIL — `ReconstructAndVerifyShamir`, `Reseal` undefined.

- [ ] **Step 3: Add Reseal to the interface + the stateless helper**

In `internal/crypto/unseal.go`, extend the interface and add the helper:
```go
type Unsealer interface {
	Init(ctx context.Context) (*InitResult, error)
	Unseal(ctx context.Context) ([]byte, error)
	// Reseal produces seal metadata (fresh KCV) for newMaster without changing
	// the seal shape. Shamir returns new shares; KMS returns nil shares. It does
	// NOT persist — the caller writes the returned SealConfig transactionally.
	Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error)
}

// ReconstructAndVerifyShamir rebuilds a master key from submitted shares and
// verifies it against cfg's key check value. Used by the rekey ceremony to
// prove possession of >= threshold current shares. Returns ErrNotEnoughShares
// below threshold and ErrKeyCheckFailed / ErrInvalidShare on a wrong share.
// The returned key is the caller's to zero.
func ReconstructAndVerifyShamir(cfg *SealConfig, shares [][]byte) ([]byte, error) {
	if cfg == nil || cfg.Type != SealTypeShamir {
		return nil, ErrInvalidSealConfig
	}
	if len(shares) < cfg.Threshold {
		return nil, ErrNotEnoughShares
	}
	var master []byte
	if cfg.Threshold == 1 {
		if len(shares) != 1 {
			return nil, ErrInvalidShare
		}
		master = append([]byte(nil), shares[0]...)
	} else {
		m, err := shamir.Combine(shares)
		if err != nil {
			return nil, ErrInvalidShare
		}
		master = m
	}
	if len(master) != KeySize {
		zero(master)
		return nil, ErrKeyCheckFailed
	}
	if err := verifyKCV(master, cfg.KeyCheckValue); err != nil {
		zero(master)
		return nil, err
	}
	return master, nil
}
```
`internal/crypto/unseal.go` must import `"github.com/steveokay/janus-secrets/internal/crypto/shamir"`.

- [ ] **Step 4: Implement Reseal on ShamirUnsealer**

In `internal/crypto/shamir.go`:
```go
// Reseal splits newMaster into fresh shares using the CURRENTLY STORED shape
// (threshold/shares from seal_config, not the constructor defaults) and builds
// a new KCV. It does not persist; the caller writes the returned config.
func (s *ShamirUnsealer) Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(newMaster) != KeySize {
		return nil, nil, ErrInvalidKeySize
	}
	var parts [][]byte
	if cfg.Shares == 1 && cfg.Threshold == 1 {
		parts = [][]byte{append([]byte(nil), newMaster...)}
	} else {
		parts, err = shamir.Split(newMaster, cfg.Shares, cfg.Threshold)
		if err != nil {
			return nil, nil, err
		}
	}
	kcv, err := makeKCV(newMaster)
	if err != nil {
		return nil, nil, err
	}
	return &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     cfg.Threshold,
		Shares:        cfg.Shares,
		KeyCheckValue: kcv,
	}, parts, nil
}
```

- [ ] **Step 5: Implement Reseal on KMSUnsealer**

In `internal/crypto/kms.go` (mirror its `Init`):
```go
// Reseal wraps newMaster under KMS and builds a new KCV. No operator shares.
func (u *KMSUnsealer) Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error) {
	if len(newMaster) != KeySize {
		return nil, nil, ErrInvalidKeySize
	}
	wrapped, err := u.client.Encrypt(ctx, newMaster)
	if err != nil {
		return nil, nil, err
	}
	kcv, err := makeKCV(newMaster)
	if err != nil {
		return nil, nil, err
	}
	return &SealConfig{
		Type:             SealTypeAWSKMS,
		KeyCheckValue:    kcv,
		WrappedMasterKey: wrapped,
	}, nil, nil
}
```

- [ ] **Step 6: Run to verify they pass**

Run: `go test ./internal/crypto/`
Expected: PASS (all crypto tests, including the new `Reseal`/`ReconstructAndVerify` ones). Fix any other `Unsealer` implementers flagged by the compiler (e.g. test doubles) by adding a `Reseal` method.

- [ ] **Step 7: Commit**

```bash
git add internal/crypto/unseal.go internal/crypto/shamir.go internal/crypto/kms.go internal/crypto/shamir_unsealer_test.go internal/crypto/kms_test.go
git commit -m "feat(crypto): Unsealer.Reseal + Shamir possession verify helper"
```

---

## Task 4: Store — RewrapAllUnderNewMaster + GetMasterKeyMeta

One transaction: `SELECT … FOR UPDATE` every master-wrapped blob across five tables, re-wrap each via the caller's closure, `UPDATE` it back, then write the re-seal config + bump version. Mirrors `internal/store/projects.go`'s `RotateKEK` transaction discipline.

**Files:**
- Create: `internal/store/masterkey.go`, `internal/store/masterkey_test.go`

- [ ] **Step 1: Write the failing test (testcontainers)**

`internal/store/masterkey_test.go` — mirror the setup in `internal/store/project_kek_versions_test.go` (uses the shared `testStore` helper). Seed a project (`wrapped_kek`), an `auth_config` row, an `oidc_providers` row, and a transit key+version; then rotate and assert every blob changed and re-unwraps under M2, and that an injected reseal error rolls everything back:
```go
func TestRewrapAllUnderNewMasterRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedMasterWrapped(t, st) // helper below: inserts project/auth/oidc/transit rows wrapped under M1

	repo := NewMasterKeyRepo(st)
	// re-wrap closure: unwrap under M1, wrap under M2, both via crypto.Encrypt/Decrypt.
	m1, m2 := masterKeys(t) // fixed test keys, see helper
	rewrap := func(old, aad []byte) ([]byte, error) {
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
	reseal := func() (*crypto.SealConfig, error) {
		kcv, _ := makeTestKCV(m2)
		return &crypto.SealConfig{Type: crypto.SealTypeShamir, Threshold: 3, Shares: 5, KeyCheckValue: kcv}, nil
	}
	if err := repo.RewrapAllUnderNewMaster(ctx, rewrap, reseal); err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	// Assert: the project KEK now unwraps under M2, not M1.
	assertProjectKEKUnwrapsUnder(t, st, m2)
	assertOIDCSecretUnwrapsUnder(t, st, m2)
	assertTransitMaterialUnwrapsUnder(t, st, m2)
	// version bumped, rotated_at set.
	meta, _ := repo.GetMasterKeyMeta(ctx)
	if meta.Version != 2 || meta.RotatedAt == nil {
		t.Fatalf("meta not updated: %+v", meta)
	}
}

func TestRewrapAllRollsBackOnResealError(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedMasterWrapped(t, st)
	repo := NewMasterKeyRepo(st)
	m1, m2 := masterKeys(t)
	rewrap := func(old, aad []byte) ([]byte, error) {
		ct, _ := crypto.ParseCiphertext(old)
		pt, err := crypto.Decrypt(m1, ct, aad)
		if err != nil {
			return nil, err
		}
		nc, _ := crypto.Encrypt(m2, pt, aad)
		return nc.Marshal(), nil
	}
	boom := errors.New("reseal failed")
	err := repo.RewrapAllUnderNewMaster(ctx, rewrap, func() (*crypto.SealConfig, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want reseal error, got %v", err)
	}
	// Everything still under M1 (nothing committed), version still 1.
	assertProjectKEKUnwrapsUnder(t, st, m1)
	meta, _ := repo.GetMasterKeyMeta(ctx)
	if meta.Version != 1 {
		t.Fatalf("version changed despite rollback: %d", meta.Version)
	}
}
```
Write the helpers (`seedMasterWrapped`, `masterKeys`, `assert*UnwrapsUnder`, `makeTestKCV`) in the test file. `seedMasterWrapped` inserts: one `projects` row with `wrapped_kek = crypto.WrapKey(m1, kek, crypto.ProjectKEKAAD(pid)).Marshal()`; the singleton `auth_config`; one `oidc_providers` row with `wrapped_client_secret = crypto.Encrypt(m1, []byte("client-secret"), crypto.OIDCClientSecretAAD()).Marshal()`; one `transit_keys` + `transit_key_versions` with material wrapped under `crypto.TransitKeyAAD(name, version)`. (Insert a `seal_config` row with `master_key_version=1` if the test store doesn't already have one.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestRewrapAll`
Expected: FAIL — `NewMasterKeyRepo` undefined.

- [ ] **Step 3: Implement the repo**

`internal/store/masterkey.go`:
```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

type MasterKeyRepo struct{ s *Store }

func NewMasterKeyRepo(s *Store) *MasterKeyRepo { return &MasterKeyRepo{s: s} }

type MasterKeyMeta struct {
	Version   int
	RotatedAt *time.Time
	SealType  string
}

// RewrapAllUnderNewMaster re-wraps every master-wrapped blob and writes the new
// seal config in ONE transaction. rewrap(old, aad) returns the blob re-wrapped
// under the new master; reseal returns the new SealConfig (fresh KCV / wrapped
// master). All rows are SELECT ... FOR UPDATE'd to serialize against concurrent
// project-KEK rotation or transit-key creation. Any error rolls back the whole
// rotation — the master is unchanged on disk.
func (r *MasterKeyRepo) RewrapAllUnderNewMaster(
	ctx context.Context,
	rewrap func(old, aad []byte) (newCT []byte, err error),
	reseal func() (*crypto.SealConfig, error),
) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		// 1. projects.wrapped_kek (current KEKs)
		if err := rewrapRows(ctx, tx, rewrap,
			`SELECT id::text, wrapped_kek FROM projects WHERE deleted_at IS NULL FOR UPDATE`,
			func(id string) []byte { return crypto.ProjectKEKAAD(id) },
			`UPDATE projects SET wrapped_kek=$2 WHERE id=$1::uuid`); err != nil {
			return err
		}
		// 2. project_kek_versions.wrapped_kek (superseded KEKs)
		if err := rewrapRows(ctx, tx, rewrap,
			`SELECT project_id::text, wrapped_kek FROM project_kek_versions FOR UPDATE`,
			func(pid string) []byte { return crypto.ProjectKEKAAD(pid) },
			// keyed by (project_id, wrapped_kek) — see note below; use version key
			``); err != nil {
			return err
		}
		// 3. auth_config.wrapped_token_hmac_key (single row)
		if err := rewrapSingle(ctx, tx, rewrap, crypto.AuthKeyAAD(),
			`SELECT wrapped_token_hmac_key FROM auth_config WHERE id=1 FOR UPDATE`,
			`UPDATE auth_config SET wrapped_token_hmac_key=$1 WHERE id=1`); err != nil {
			return err
		}
		// 4. oidc_providers.wrapped_client_secret (all rows, arbitrary length)
		if err := rewrapRows(ctx, tx, rewrap,
			`SELECT id::text, wrapped_client_secret FROM oidc_providers FOR UPDATE`,
			func(_ string) []byte { return crypto.OIDCClientSecretAAD() },
			`UPDATE oidc_providers SET wrapped_client_secret=$2 WHERE id=$1::uuid`); err != nil {
			return err
		}
		// 5. transit_key_versions.wrapped_material (join transit_keys for name)
		if err := rewrapTransit(ctx, tx, rewrap); err != nil {
			return err
		}
		// 6. re-seal (fresh KCV / wrapped master) + bump version.
		cfg, err := reseal()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE seal_config
			    SET type=$1, threshold=$2, shares=$3, key_check_value=$4,
			        wrapped_master_key=$5,
			        master_key_version = master_key_version + 1,
			        master_key_rotated_at = now()
			  WHERE id=1`,
			cfg.Type, nullInt(cfg.Threshold), nullInt(cfg.Shares), cfg.KeyCheckValue, cfg.WrappedMasterKey)
		return err
	})
}

// rewrapRows re-wraps a two-column (idText, blob) result set and updates each
// row by id. The aadFor closure builds the per-row AAD from the id column.
func rewrapRows(ctx context.Context, tx pgx.Tx,
	rewrap func(old, aad []byte) ([]byte, error),
	query string, aadFor func(id string) []byte, update string) error {
	type row struct {
		id  string
		old []byte
	}
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return err
	}
	var rs []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.old); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rr := range rs {
		nc, err := rewrap(rr.old, aadFor(rr.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, update, rr.id, nc); err != nil {
			return err
		}
	}
	return nil
}

func rewrapSingle(ctx context.Context, tx pgx.Tx,
	rewrap func(old, aad []byte) ([]byte, error),
	aad []byte, query, update string) error {
	var old []byte
	err := tx.QueryRow(ctx, query).Scan(&old)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil // nothing configured yet (e.g. auth key not materialized)
		}
		return err
	}
	nc, err := rewrap(old, aad)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, update, nc)
	return err
}

func rewrapTransit(ctx context.Context, tx pgx.Tx,
	rewrap func(old, aad []byte) ([]byte, error)) error {
	type row struct {
		id      string
		name    string
		version int
		old     []byte
	}
	rows, err := tx.Query(ctx,
		`SELECT v.id::text, k.name, v.version, v.wrapped_material
		   FROM transit_key_versions v
		   JOIN transit_keys k ON k.id = v.transit_key_id
		  FOR UPDATE OF v`)
	if err != nil {
		return err
	}
	var rs []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.name, &rr.version, &rr.old); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rr := range rs {
		nc, err := rewrap(rr.old, crypto.TransitKeyAAD(rr.name, rr.version))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE transit_key_versions SET wrapped_material=$2 WHERE id=$1::uuid`, rr.id, nc); err != nil {
			return err
		}
	}
	return nil
}

func (r *MasterKeyRepo) GetMasterKeyMeta(ctx context.Context) (MasterKeyMeta, error) {
	var m MasterKeyMeta
	err := r.s.pool.QueryRow(ctx,
		`SELECT type, master_key_version, master_key_rotated_at FROM seal_config WHERE id=1`).
		Scan(&m.SealType, &m.Version, &m.RotatedAt)
	if err != nil {
		return MasterKeyMeta{}, mapError(err)
	}
	return m, nil
}
```

**Note for step 3:** `project_kek_versions` has PK `(project_id, version)`, so the `UPDATE` for table 2 must key on both columns. Adjust `rewrapRows` to a dedicated `rewrapKEKVersions` helper that selects `project_id::text, version, wrapped_kek` and updates `WHERE project_id=$1::uuid AND version=$2` (AAD = `ProjectKEKAAD(project_id)`). Model it on `rewrapTransit`. Use `nullInt` if it exists in the store package; otherwise pass `cfg.Threshold`/`cfg.Shares` as plain ints and let `omitempty`-style NULLs be handled the way `sealconfig.go`'s `Put` already does (mirror that file's parameter handling exactly).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run 'TestRewrapAll|TestMasterKeyMeta'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/masterkey.go internal/store/masterkey_test.go
git commit -m "feat(store): RewrapAllUnderNewMaster + GetMasterKeyMeta (000019)"
```

---

## Task 5: masterkeys.Service — KMS rotate + round-trip/leak tests

The orchestrator. This task builds the KMS single-call path and the shared rotation core (`performRotation`), plus the value-free guarantees. The Shamir ceremony is Task 6.

**Files:**
- Create: `internal/masterkeys/service.go`, `internal/masterkeys/service_test.go`, `internal/masterkeys/leak_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/masterkeys/service_test.go` — build a real keyring + testcontainers store (mirror `internal/projectkeys/service_test.go` setup helpers). Cover:
```go
// KMS rotate: write a secret under M1, rotate, secret still readable; version bumped.
func TestKMSRotateRoundTrip(t *testing.T) { /* see projectkeys/service_test.go for secret round-trip harness */ }

// Rotation never opens a secret value: corrupt a value's ciphertext, rotation still succeeds.
func TestRotateNeverDecryptsValue(t *testing.T) { /* corrupt secret_values ciphertext, expect Rotate == nil */ }

// Sealed keyring: Rotate returns ErrSealed.
func TestRotateSealed(t *testing.T) { /* kr.Seal(); expect crypto.ErrSealed */ }
```
`internal/masterkeys/leak_test.go` — mirror `internal/projectkeys/leak_test.go`: write sentinel `"SENTINEL-MASTER-ROTATE-7b1c"`, capture slog output across a rotation, assert the sentinel never appears.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/masterkeys/`
Expected: FAIL — package/`Service` undefined.

- [ ] **Step 3: Implement the service core + KMS path**

`internal/masterkeys/service.go`:
```go
package masterkeys

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Unsealer is the subset of crypto.Unsealer the service needs.
type Unsealer interface {
	Reseal(ctx context.Context, newMaster []byte) (*crypto.SealConfig, [][]byte, error)
}

type Service struct {
	kr       *crypto.Keyring
	unsealer Unsealer
	repo     *store.MasterKeyRepo
	seals    crypto.SealConfigStore // to read the current seal type / cfg for the ceremony

	// Shamir rekey ceremony state (Task 6).
	rekey rekeyState
}

func NewService(kr *crypto.Keyring, u Unsealer, repo *store.MasterKeyRepo, seals crypto.SealConfigStore) *Service {
	return &Service{kr: kr, unsealer: u, repo: repo, seals: seals}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// performRotation is the shared core for both KMS and Shamir. It generates a
// fresh master, re-wraps every master-wrapped blob and writes the new seal
// config atomically (via the store transaction driven inside RotateMaster's
// persist), then swaps the in-memory master. Returns the new shares (Shamir) or
// nil (KMS) and the new master-key version.
func (s *Service) performRotation(ctx context.Context) (shares [][]byte, version int, err error) {
	if s.kr.Sealed() {
		return nil, 0, crypto.ErrSealed
	}
	m2, err := crypto.GenerateKey()
	if err != nil {
		return nil, 0, err
	}
	defer zero(m2)

	var newCfg *crypto.SealConfig
	var newShares [][]byte

	rerr := s.kr.RotateMaster(m2,
		func(unwrap func(old, aad []byte) ([]byte, error), wrap func(plain, aad []byte) ([]byte, error)) error {
			// The store transaction (persist) calls back into these closures per
			// row; we hand RotateMaster's closures straight through to the repo.
			return nil // no staging needed here; see persist below
		},
		func() error {
			// One DB transaction: re-wrap all rows + write reseal config.
			return s.repo.RewrapAllUnderNewMaster(ctx,
				func(old, aad []byte) ([]byte, error) { return s.rewrapOne(old, aad) },
				func() (*crypto.SealConfig, error) {
					cfg, sh, rerr := s.unsealer.Reseal(ctx, m2)
					if rerr != nil {
						return nil, rerr
					}
					newCfg, newShares = cfg, sh
					return cfg, nil
				})
		},
	)
	if rerr != nil {
		zeroShares(newShares)
		return nil, 0, rerr
	}
	_ = newCfg
	meta, err := s.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		zeroShares(newShares)
		return nil, 0, err
	}
	return newShares, meta.Version, nil
}
```

**Important design correction for step 3:** `RotateMaster`'s `rewrap` closure and `persist` closure need the old→new unwrap/wrap bound to M1 (in keyring) and M2. But the DB re-wrap happens inside `persist` (the store tx), which needs those same closures. The clean wiring: keep `RotateMaster`'s `rewrap` param, and have the store's `rewrap(old, aad)` delegate to the keyring closures. Restructure so `RotateMaster` passes its `unwrap`/`wrap` closures out to `persist` via captured variables:

```go
func (s *Service) performRotation(ctx context.Context) ([][]byte, int, error) {
	if s.kr.Sealed() {
		return nil, 0, crypto.ErrSealed
	}
	m2, err := crypto.GenerateKey()
	if err != nil {
		return nil, 0, err
	}
	defer zero(m2)

	var unwrapFn func(old, aad []byte) ([]byte, error)
	var wrapFn func(plain, aad []byte) ([]byte, error)
	var newShares [][]byte

	err = s.kr.RotateMaster(m2,
		func(unwrap func(old, aad []byte) ([]byte, error), wrap func(plain, aad []byte) ([]byte, error)) error {
			unwrapFn, wrapFn = unwrap, wrap // capture for persist
			return nil
		},
		func() error {
			return s.repo.RewrapAllUnderNewMaster(ctx,
				func(old, aad []byte) ([]byte, error) {
					pt, e := unwrapFn(old, aad)
					if e != nil {
						return nil, e
					}
					defer zero(pt)
					return wrapFn(pt, aad)
				},
				func() (*crypto.SealConfig, error) {
					cfg, sh, e := s.unsealer.Reseal(ctx, m2)
					if e != nil {
						return nil, e
					}
					newShares = sh
					return cfg, nil
				})
		},
	)
	if err != nil {
		zeroShares(newShares)
		return nil, 0, err
	}
	meta, err := s.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		return nil, 0, err
	}
	return newShares, meta.Version, nil
}

func (s *Service) rewrapOne(old, aad []byte) ([]byte, error) { return nil, errors.New("unused") }

func zeroShares(ss [][]byte) {
	for _, s := range ss {
		zero(s)
	}
}
```
(Delete the placeholder `rewrapOne`; it is superseded by the inline closure above. Delete the first `performRotation` draft — keep only this corrected version.)

Then the KMS entrypoint:
```go
// Rotate performs a single-call rotation for KMS-unsealed instances. Shamir
// instances must use the rekey ceremony (RekeyInit/Submit) and Rotate returns
// ErrShamirCeremonyRequired.
func (s *Service) Rotate(ctx context.Context) (int, error) {
	cfg, err := s.seals.Get(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.Type != crypto.SealTypeAWSKMS {
		return 0, ErrShamirCeremonyRequired
	}
	_, version, err := s.performRotation(ctx)
	return version, err
}

var ErrShamirCeremonyRequired = errors.New("shamir seal requires a rekey ceremony")

// Status returns the current seal type + master-key version/rotated_at + any
// in-progress ceremony progress (0/0 for KMS).
func (s *Service) Status(ctx context.Context) (Status, error) {
	meta, err := s.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{UnsealType: meta.SealType, Version: meta.Version, RotatedAt: meta.RotatedAt}
	s.rekey.fill(&st)
	return st, nil
}

type Status struct {
	UnsealType     string
	Version        int
	RotatedAt      *time.Time
	RekeyInProg    bool
	Submitted      int
	Required       int
}
```
Add the `time` import. Define `rekeyState` minimally in this file for now (Task 6 fills it):
```go
type rekeyState struct{}
func (rekeyState) fill(*Status) {}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/masterkeys/`
Expected: PASS (KMS round-trip, never-decrypts-value, sealed, leak).

- [ ] **Step 5: Commit**

```bash
git add internal/masterkeys/service.go internal/masterkeys/service_test.go internal/masterkeys/leak_test.go
git commit -m "feat(masterkeys): rotation core + KMS single-call path (value-free)"
```

---

## Task 6: masterkeys.Service — Shamir rekey ceremony

Adds proof-of-possession: init opens the single ceremony, submit accumulates current shares (dedup), and on reaching threshold verifies possession via `ReconstructAndVerifyShamir`, then runs `performRotation` and returns the new shares once.

**Files:**
- Modify: `internal/masterkeys/service.go`
- Test: `internal/masterkeys/service_test.go`

- [ ] **Step 1: Write the failing tests**
```go
func TestRekeyCeremonyHappyPath(t *testing.T) {
	// Shamir 3-of-5 instance; init, submit 3 current shares, complete → new shares
	// reconstruct M2; old shares no longer unseal.
}
func TestRekeyRejectsWrongShares(t *testing.T) {
	// submit 2 valid + 1 tampered → on threshold, possession check fails; no rotation;
	// version unchanged; ceremony closed.
}
func TestRekeyOnlyOneCeremony(t *testing.T) {
	// second Init while active → ErrRekeyInProgress.
}
func TestRekeyCancelClearsState(t *testing.T) {
	// init, submit 1, cancel → Status.RekeyInProg == false, Submitted == 0.
}
func TestRekeyRequiresShamir(t *testing.T) {
	// KMS instance: RekeyInit → ErrKMSNoCeremony.
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/masterkeys/ -run TestRekey`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the ceremony**

Replace the placeholder `rekeyState` with:
```go
import "sync" // add to imports

type rekeyState struct {
	mu        sync.Mutex
	active    bool
	nonce     string
	required  int
	submitted map[string][]byte
}

func (r *rekeyState) fill(st *Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st.RekeyInProg = r.active
	st.Submitted = len(r.submitted)
	if r.active {
		st.Required = r.required
	}
}

var (
	ErrRekeyInProgress = errors.New("a rekey ceremony is already in progress")
	ErrNoRekey         = errors.New("no rekey ceremony in progress")
	ErrKMSNoCeremony   = errors.New("kms seal does not use a rekey ceremony")
	ErrRekeyNonce      = errors.New("rekey nonce mismatch")
)

// RekeyInit opens the single Shamir rekey ceremony and returns a nonce + the
// number of current shares required. Owner-gated at the API layer.
func (s *Service) RekeyInit(ctx context.Context) (nonce string, required int, err error) {
	if s.kr.Sealed() {
		return "", 0, crypto.ErrSealed
	}
	cfg, err := s.seals.Get(ctx)
	if err != nil {
		return "", 0, err
	}
	if cfg.Type != crypto.SealTypeShamir {
		return "", 0, ErrKMSNoCeremony
	}
	s.rekey.mu.Lock()
	defer s.rekey.mu.Unlock()
	if s.rekey.active {
		return "", 0, ErrRekeyInProgress
	}
	n, err := crypto.GenerateKey() // 32 random bytes → hex nonce
	if err != nil {
		return "", 0, err
	}
	s.rekey.active = true
	s.rekey.nonce = hex.EncodeToString(n)
	s.rekey.required = cfg.Threshold
	s.rekey.submitted = make(map[string][]byte)
	return s.rekey.nonce, cfg.Threshold, nil
}

// RekeySubmit accepts one current share. When >= threshold distinct shares are
// held it verifies possession (reconstruct + KCV), performs the rotation, and
// returns the new shares exactly once with complete=true. Below threshold it
// returns complete=false and progress.
func (s *Service) RekeySubmit(ctx context.Context, nonce string, share []byte) (complete bool, newShares [][]byte, version int, submitted, required int, err error) {
	s.rekey.mu.Lock()
	if !s.rekey.active {
		s.rekey.mu.Unlock()
		return false, nil, 0, 0, 0, ErrNoRekey
	}
	if nonce != s.rekey.nonce {
		s.rekey.mu.Unlock()
		return false, nil, 0, 0, 0, ErrRekeyNonce
	}
	if len(share) < 2 {
		s.rekey.mu.Unlock()
		return false, nil, 0, len(s.rekey.submitted), s.rekey.required, crypto.ErrInvalidShare
	}
	key := hex.EncodeToString(share)
	if _, dup := s.rekey.submitted[key]; !dup {
		s.rekey.submitted[key] = append([]byte(nil), share...)
	}
	if len(s.rekey.submitted) < s.rekey.required {
		submitted, required = len(s.rekey.submitted), s.rekey.required
		s.rekey.mu.Unlock()
		return false, nil, 0, submitted, required, nil
	}
	// Threshold reached: gather shares, then release the lock before the DB work.
	parts := make([][]byte, 0, len(s.rekey.submitted))
	for _, p := range s.rekey.submitted {
		parts = append(parts, p)
	}
	cfg, cerr := s.seals.Get(ctx)
	s.rekey.mu.Unlock()
	if cerr != nil {
		return false, nil, 0, 0, 0, cerr
	}
	// Proof of possession: reconstruct + verify against the CURRENT KCV.
	candidate, verr := crypto.ReconstructAndVerifyShamir(cfg, parts)
	if verr != nil {
		s.closeRekey()
		return false, nil, 0, 0, 0, verr
	}
	zero(candidate) // we only needed proof; keyring already holds the master
	shares, ver, rerr := s.performRotation(ctx)
	s.closeRekey()
	if rerr != nil {
		zeroShares(shares)
		return false, nil, 0, 0, 0, rerr
	}
	return true, shares, ver, s.rekey.required, s.rekey.required, nil
}

// RekeyCancel drops the ceremony and zeroizes accumulated shares.
func (s *Service) RekeyCancel() error {
	s.rekey.mu.Lock()
	defer s.rekey.mu.Unlock()
	if !s.rekey.active {
		return ErrNoRekey
	}
	s.clearLocked()
	return nil
}

func (s *Service) closeRekey() { s.rekey.mu.Lock(); s.clearLocked(); s.rekey.mu.Unlock() }
func (s *Service) clearLocked() {
	for _, p := range s.rekey.submitted {
		zero(p)
	}
	s.rekey.submitted = nil
	s.rekey.active = false
	s.rekey.nonce = ""
	s.rekey.required = 0
}
```
Add `"encoding/hex"` to imports.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/masterkeys/`
Expected: PASS (all, including ceremony).

- [ ] **Step 5: Commit**

```bash
git add internal/masterkeys/service.go internal/masterkeys/service_test.go
git commit -m "feat(masterkeys): Shamir rekey ceremony (proof-of-possession)"
```

---

## Task 7: AuthZ — SysMasterKey action

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/actions_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/authz/actions_test.go`:
```go
func TestSysMasterKeyOwnerOnly(t *testing.T) {
	if !roleActions[RoleOwner][SysMasterKey] {
		t.Fatal("owner must have sys:master-key")
	}
	for _, role := range []Role{RoleViewer, RoleDeveloper, RoleAdmin} {
		if roleActions[role][SysMasterKey] {
			t.Fatalf("%s must NOT have sys:master-key", role)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/authz/ -run TestSysMasterKey`
Expected: FAIL — `SysMasterKey` undefined.

- [ ] **Step 3: Add the action**

In `internal/authz/actions.go`, add the constant near `KEKManage`:
```go
	SysMasterKey Action = "sys:master-key" // instance-scoped, owner-only (master-key rotation / rekey)
```
And add `SysMasterKey` to the `ownerActions` set:
```go
	ownerActions = union(adminActions, setOf(ProjectDelete, KEKManage, SysMasterKey))
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/authz/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/actions_test.go
git commit -m "feat(authz): sys:master-key action (owner-only)"
```

---

## Task 8: API handlers + wiring

Endpoints mirror `internal/api/kek_handlers.go`. Instance-scoped authz (`authz.Resource{}`), value-free audit, sealed→503 via `writeServiceError`. Wire the service in `boot.go` and register routes in `server.go`.

**Files:**
- Create: `internal/api/masterkey_handlers.go`, `internal/api/masterkey_e2e_test.go`
- Modify: `internal/api/server.go`, `internal/api/boot.go`

- [ ] **Step 1: Write the failing e2e test**

`internal/api/masterkey_e2e_test.go` — mirror `internal/api/kek_e2e_test.go`. Cover: admin (no `sys:master-key`) → 403 on rotate + rekey/init; owner status → 200 with `unseal_type`; **KMS instance** owner rotate → 200 `{master_key_version:2}`; **Shamir instance** owner rotate → 400 (`ErrShamirCeremonyRequired`); Shamir owner init→submit×3→ 200 `{complete:true,new_shares,master_key_version}`; sealed server → 503. Use the existing test harness that builds a `Server` with a fake KMS or Shamir seal (see how `kek_e2e_test.go` and `boot`/`sys` tests construct the server + owner principal).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestMasterKey`
Expected: FAIL — handlers/routes undefined.

- [ ] **Step 3: Implement handlers**

`internal/api/masterkey_handlers.go`:
```go
package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/masterkeys"
)

func (s *Server) handleMasterKeyStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.SysMasterKey, authz.Resource{}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	st, err := s.masterKeys.Status(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	resp := map[string]any{
		"unseal_type":        st.UnsealType,
		"master_key_version": st.Version,
		"rekey_in_progress":  st.RekeyInProg,
		"submitted":          st.Submitted,
		"required":           st.Required,
	}
	if st.RotatedAt != nil {
		resp["rotated_at"] = st.RotatedAt.UTC().Format(timeRFC3339) // reuse the format used elsewhere
	} else {
		resp["rotated_at"] = nil
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMasterKeyRotate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Resource{}, "sys.master-key.rotate", "sys/master-key") {
		return
	}
	version, err := s.masterKeys.Rotate(r.Context())
	if err != nil {
		if errors.Is(err, masterkeys.ErrShamirCeremonyRequired) {
			writeError(w, http.StatusBadRequest, CodeBadRequest, "shamir seal requires a rekey ceremony")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rotate", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"master_key_version": version})
}

func (s *Server) handleMasterKeyRekeyInit(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Resource{}, "sys.master-key.rekey.init", "sys/master-key") {
		return
	}
	nonce, required, err := s.masterKeys.RekeyInit(r.Context())
	if err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rekey.init", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nonce": nonce, "required": required, "submitted": 0})
}

func (s *Server) handleMasterKeyRekeySubmit(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Resource{}, "sys.master-key.rekey.submit", "sys/master-key") {
		return
	}
	var body struct {
		Nonce string `json:"nonce"`
		Share string `json:"share"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return // decodeJSON already wrote the 400
	}
	share, derr := decodeShare(body.Share) // hex or base64 per how init/unseal shares are encoded on the wire
	if derr != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid share encoding")
		return
	}
	complete, shares, version, submitted, required, err := s.masterKeys.RekeySubmit(r.Context(), body.Nonce, share)
	zero(share)
	if err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	action := "sys.master-key.rekey.submit"
	if complete {
		action = "sys.master-key.rekey.complete"
	}
	if err := s.record(r, action, "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !complete {
		writeJSON(w, http.StatusOK, map[string]any{"complete": false, "submitted": submitted, "required": required})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"complete":           true,
		"master_key_version": version,
		"new_shares":         encodeShares(shares), // same encoding as the init/unseal share format
	})
	zeroShares(shares)
}

func (s *Server) handleMasterKeyRekeyCancel(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Resource{}, "sys.master-key.rekey.cancel", "sys/master-key") {
		return
	}
	if err := s.masterKeys.RekeyCancel(); err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rekey.cancel", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeMasterKeyErr maps ceremony/seal errors to envelopes. Sealed → 503 via
// writeServiceError; ceremony-state errors → 4xx.
func (s *Server) writeMasterKeyErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crypto.ErrSealed):
		s.writeServiceError(w, err)
	case errors.Is(err, masterkeys.ErrRekeyInProgress):
		writeError(w, http.StatusConflict, CodeConflict, "a rekey ceremony is already in progress")
	case errors.Is(err, masterkeys.ErrKMSNoCeremony):
		writeError(w, http.StatusBadRequest, CodeBadRequest, "kms seal does not use a rekey ceremony")
	case errors.Is(err, masterkeys.ErrNoRekey), errors.Is(err, masterkeys.ErrRekeyNonce),
		errors.Is(err, crypto.ErrInvalidShare), errors.Is(err, crypto.ErrNotEnoughShares),
		errors.Is(err, crypto.ErrKeyCheckFailed), errors.Is(err, crypto.ErrDuplicateShare):
		writeError(w, http.StatusBadRequest, CodeBadRequest, "rekey share rejected")
	default:
		s.writeServiceError(w, err)
	}
}
```

**Adapt to existing helpers:** use whatever this package already provides — `decodeJSON`/`readJSON`, the share encode/decode used by the unseal endpoint (grep `sys.go` / the unseal handler for how a submitted share is decoded and how init shares are rendered — reuse that exact codec via `decodeShare`/`encodeShares`, or inline it), the error `Code*` constants, and the timestamp format constant. Do not invent new share encodings — match the unseal/init wire format so operators paste the same share strings.

- [ ] **Step 4: Register routes + wire the service**

In `internal/api/server.go`, add a `masterKeys *masterkeys.Service` field to `Server`, and register (mirroring the `s.projectKeys != nil` block):
```go
if s.masterKeys != nil {
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(s.auth))
		r.Get("/v1/sys/master-key", s.handleMasterKeyStatus)
		r.Post("/v1/sys/master-key/rotate", s.handleMasterKeyRotate)
		r.Post("/v1/sys/master-key/rekey/init", s.handleMasterKeyRekeyInit)
		r.Post("/v1/sys/master-key/rekey/submit", s.handleMasterKeyRekeySubmit)
		r.Delete("/v1/sys/master-key/rekey", s.handleMasterKeyRekeyCancel)
	})
}
```

In `internal/api/boot.go`, after the unsealer + keyring + store are built (near where `projectKeys` is constructed), add:
```go
srv.masterKeys = masterkeys.NewService(
	keyring,
	unsealer, // the concrete *ShamirUnsealer / *KMSUnsealer satisfies masterkeys.Unsealer via Reseal
	store.NewMasterKeyRepo(st),
	seals, // the crypto.SealConfigStore already built for the unsealer
)
```
The `unsealer` local is already an `crypto.Unsealer`; `masterkeys.Unsealer` only needs `Reseal`, which both implementations now have. If `boot.go` holds the unsealer as the interface type, pass it directly.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/api/ -run TestMasterKey && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/api/masterkey_handlers.go internal/api/masterkey_e2e_test.go internal/api/server.go internal/api/boot.go
git commit -m "feat(api): master-key rotate + rekey ceremony endpoints (owner-only)"
```

---

## Task 9: CLI — janus master-key

Mirror `cmd/janus/project_commands.go`. `status`, `rotate` (KMS), `rekey` (Shamir; `--share` repeatable or interactive prompt, `--cancel`).

**Files:**
- Create: `cmd/janus/masterkey_commands.go`, `cmd/janus/masterkey_commands_test.go`
- Modify: `cmd/janus/main.go` (register `newMasterKeyCmd()`)

- [ ] **Step 1: Write the failing test**

`cmd/janus/masterkey_commands_test.go` — mirror `cmd/janus/project_commands_test.go`: stub server for `GET /v1/sys/master-key`, `POST /v1/sys/master-key/rotate`, `.../rekey/init`, `.../rekey/submit`; assert command tree (`status`, `rotate`, `rekey`), wire paths, and that `rekey --share A --share B --share C` drives init then three submits and prints the returned new shares.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/janus/ -run TestMasterKey`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the command**

`cmd/janus/masterkey_commands.go` — parent + subcommands using the shared `newAPIClient(address, token)` + `c.call(method, path, body, &out)` helpers (see `project_commands.go`). `status` GETs and prints unseal type / version / rotated_at / ceremony progress. `rotate` POSTs `/rotate` and prints the new version (surfacing the 400 "requires a rekey ceremony" clearly). `rekey`: POST `/rekey/init`, then for each `--share` POST `/rekey/submit` with `{nonce, share}`; on the completing response print the new shares with a "store these now — they will not be shown again" warning; `--cancel` sends `DELETE /rekey`. If no `--share` flags are given, prompt interactively on stdin for `required` shares (read without echo if a helper exists; otherwise line-read).

- [ ] **Step 4: Register in main.go**

In `cmd/janus/main.go`, add `newMasterKeyCmd(),` to the command list (next to `newProjectCmd()`).

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./cmd/janus/ -run TestMasterKey && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/masterkey_commands.go cmd/janus/masterkey_commands_test.go cmd/janus/main.go
git commit -m "feat(cli): janus master-key status/rotate/rekey"
```

---

## Task 10: Web — endpoints + Settings status

Frontend uses Nocturne tokens only. Add API bindings and the Settings status line; the rotate control + share modal are Task 11.

**Files:**
- Modify: `web/src/lib/endpoints.ts`
- Create: `web/src/settings/MasterKeySection.tsx`, `web/src/settings/MasterKeySection.test.tsx`
- Modify: the Settings instance-seal host (grep for the seal/backup section, e.g. `web/src/settings/SettingsPage.tsx` or `InstanceSection.tsx`) to mount `<MasterKeySection/>`.

- [ ] **Step 1: Write the failing test**

`web/src/settings/MasterKeySection.test.tsx` — msw-mock `GET /v1/sys/master-key` returning `{unseal_type:'awskms', master_key_version:3, rotated_at:'2026-07-15T00:00:00Z', rekey_in_progress:false, submitted:0, required:0}`; assert the section renders "version 3" and a "Rotate master key" button. Mirror `web/src/settings/OIDCSection.test.tsx` for render/mock setup. **msw mocks MUST mirror the Go handler wire shapes exactly.**

- [ ] **Step 2: Run to verify it fails**

Run (from `web/`): `npm test -- MasterKeySection`
Expected: FAIL — component missing.

- [ ] **Step 3: Implement endpoints + status section**

In `web/src/lib/endpoints.ts` add:
```ts
export interface MasterKeyStatus {
  unseal_type: 'shamir' | 'awskms'
  master_key_version: number
  rotated_at: string | null
  rekey_in_progress: boolean
  submitted: number
  required: number
}
// in the endpoints object:
masterKeyStatus: () => api.get<MasterKeyStatus>('/v1/sys/master-key'),
rotateMasterKey: () => api.post<{ master_key_version: number }>('/v1/sys/master-key/rotate', {}),
rekeyInit: () => api.post<{ nonce: string; required: number; submitted: number }>('/v1/sys/master-key/rekey/init', {}),
rekeySubmit: (nonce: string, share: string) =>
  api.post<{ complete: boolean; submitted?: number; required?: number; master_key_version?: number; new_shares?: string[] }>(
    '/v1/sys/master-key/rekey/submit', { nonce, share }),
rekeyCancel: () => api.del('/v1/sys/master-key/rekey'),
```
(Use whatever `api` helper names exist — `api.post`/`api.del`; match `endpoints.ts` conventions.)

`web/src/settings/MasterKeySection.tsx` — a card (tokens only, mirror `OIDCSection.tsx`) using TanStack Query `useQuery(['master-key'], endpoints.masterKeyStatus)`. Render unseal type, "Master key version N", "Last rotated {date|Never}", and a **Rotate master key** button (Task 11 wires its behavior). Hide/disable gracefully on 403 (owner-only), matching how other owner-only settings sections handle 403.

- [ ] **Step 4: Run to verify it passes**

Run (from `web/`): `npm test -- MasterKeySection`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/endpoints.ts web/src/settings/MasterKeySection.tsx web/src/settings/MasterKeySection.test.tsx web/src/settings/SettingsPage.tsx
git commit -m "feat(web): master-key status in Settings instance section"
```

---

## Task 11: Web — rotate control, confirm, Shamir share modal, RevealOnce shares

**Files:**
- Modify: `web/src/settings/MasterKeySection.tsx`, `web/src/settings/MasterKeySection.test.tsx`

- [ ] **Step 1: Write the failing tests**
```
- KMS: click Rotate → typed/plain danger ConfirmDialog → confirm → POST /rotate →
  success toast shows new version; status refetched.
- Shamir: click Rotate → confirm → share-submission modal appears (required=3);
  paste 3 shares → on completion the new shares render in a RevealOnce ("shown once");
  closing clears them from state.
- Shamir wrong-share: submit returns 400 → inline error in the modal, no shares shown.
```
Use msw to mock the endpoints; mirror the RevealOnce assertions from `web/src/settings` or `ChangePassword`/token tests.

- [ ] **Step 2: Run to verify they fail**

Run (from `web/`): `npm test -- MasterKeySection`
Expected: FAIL.

- [ ] **Step 3: Implement**

Extend `MasterKeySection.tsx`:
- **Rotate button** → `ConfirmDialog` (tone `danger`, uses the solid modal surfaces we shipped). Copy: names the consequence ("mints new key material and re-seals; Shamir instances receive new shares — store them immediately").
- **KMS branch:** on confirm, `useMutation(endpoints.rotateMasterKey)`; success → toast `Master key rotated (vN)`, `invalidateQueries(['master-key'])`.
- **Shamir branch:** on confirm, open a share-submission modal (compose from `Modal`/`Sheet` + `Input`, mirroring the unseal share UI). Call `rekeyInit()` for the nonce+required, then per pasted share call `rekeySubmit(nonce, share)` and show progress `submitted/required`. On `complete:true`, render `new_shares` via `RevealOnce` (never logged, cleared on close). On a rejected share, show an inline error and let the operator cancel (`rekeyCancel()`), which also fires on modal close if a ceremony is open.
- **Security:** the new shares are the only sensitive strings rendered; keep them in ephemeral state only, never in a toast/log; clear on unmount/close (mirror `RevealOnce` + `IssuedCredsModal` patterns).

- [ ] **Step 4: Run to verify they pass**

Run (from `web/`): `npm test -- MasterKeySection`
Expected: PASS.

- [ ] **Step 5: Full web gate**

Run (from `web/`): `npm test && npm run smoke`
Expected: all suites PASS; dual-theme smoke green (light + dark).

- [ ] **Step 6: Commit**

```bash
git add web/src/settings/MasterKeySection.tsx web/src/settings/MasterKeySection.test.tsx
git commit -m "feat(web): master-key rotate control + Shamir rekey modal (shares shown once)"
```

---

## Task 12: Final gate, status doc, container rebuild

**Files:**
- Modify: `status.md`

- [ ] **Step 1: Full backend gate**

Run:
```
go build ./...
go test ./... -race
GOTOOLCHAIN=go1.26.5 govulncheck ./...
gosec ./...
```
Expected: all green. Confirm the crypto leak test and `internal/masterkeys/leak_test.go` pass. Investigate any gosec finding (CLAUDE.md treats them as build failures) — re-encode raw writes through `writeJSON`, etc.

- [ ] **Step 2: Full web gate**

Run (from `web/`): `npm test && npx tsc --noEmit && npm run build && npm run smoke`
Expected: all green.

- [ ] **Step 3: Update status.md**

Add a feature entry for master-key rotation (mirror the project-KEK rotation entry): endpoints, CLI, UI, migration 000019, value-free, owner-only, Shamir ceremony vs KMS.

- [ ] **Step 4: Commit + rebuild the dev container (sanctioned)**

```bash
git add status.md
git commit -m "docs(status): master-key rotation (completes gaps.md §4.1)"
docker compose up -d --build
./scripts/dev-unseal.sh
curl -s http://localhost:8210/v1/sys/health
```
Expected: health `{"initialized":true,"sealed":false,"status":"ok"}`; the running instance is now at migration v19. (On Windows do NOT run host `npm ci`; the Docker build compiles web+Go inside the image.)

- [ ] **Step 5: Final holistic review**

Dispatch a final code-reviewer over the whole branch diff before the PR: confirm value-free audit, byte-identical AADs, atomic rollback, owner-only gating, ceremony possession check, no share/key in logs or errors, and that DEK-wrapped configs (rotation/sync/dynamic) were correctly left out of the re-wrap set.

---

## Self-Review (against the spec)

**Spec coverage:** §2 re-wrap set → Task 4 (all five tables incl. OIDC + transit-name join). §3 eager-atomic + write-lock → Task 2 (`RotateMaster`) + Task 4 (single tx). §4.1 Shamir ceremony → Task 6 + Task 8/9/11. §4.2 KMS single-call → Task 5 + Task 8/9/11. §5 components → Tasks 2–11 (one per component). §6 invariants → Tasks 2,4,5 tests (nonce-fresh, value-free, constant-time KCV, zeroize, atomic). §7 API → Task 8. §8 CLI → Task 9. §9 UI → Tasks 10–11. §10 testing → per-task tests + Task 12 gate. §11 non-goals respected (no shape change — `Reseal` reads stored threshold/shares; no version table; no KMS ceremony; DEK layer untouched).

**Placeholder scan:** none — every code step has concrete code; Task 4 flags the two-column PK adjustment and the `nullInt`/sealconfig parameter-handling to mirror explicitly rather than leaving it vague.

**Type consistency:** `RotateMaster(newMaster, rewrap(unwrap,wrap), persist)` is used identically in Tasks 2, 5. `Reseal(ctx, newMaster) (*SealConfig, [][]byte, error)` consistent across Tasks 3, 5, 8. `MasterKeyMeta{Version,RotatedAt,SealType}` consistent Tasks 4, 5, 8. `Status{UnsealType,Version,RotatedAt,RekeyInProg,Submitted,Required}` consistent Tasks 5, 8, 10. Endpoint JSON keys (`unseal_type`, `master_key_version`, `new_shares`, `nonce`, `required`, `submitted`, `complete`) consistent Tasks 8, 9, 10, 11.
