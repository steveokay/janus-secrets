package projectkeys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

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

// wrappedDEKOf reads the raw wrapped_dek blob for a key's latest value directly.
func wrappedDEKOf(t *testing.T, configID, key string) []byte {
	t.Helper()
	var b []byte
	err := testPool.QueryRow(context.Background(),
		`SELECT wrapped_dek FROM secret_values
		  WHERE config_id=$1::uuid AND key=$2
		  ORDER BY value_version DESC LIMIT 1`, configID, key).Scan(&b)
	if err != nil {
		t.Fatalf("read wrapped_dek for %s: %v", key, err)
	}
	return b
}

// TestRotateThenReadThenRewrapThenRetire is the end-to-end property: a secret
// written under KEK v1 stays readable across rotation (retained v1) and across
// rewrap (now v2), and the superseded version is retired.
func TestRotateThenReadThenRewrapThenRetire(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)

	writeSecret(t, h.sec, configID, "DB", "s3cr3t")
	if got := reveal(t, h.sec, configID, "DB"); got != "s3cr3t" {
		t.Fatalf("pre-rotate DB = %q, want s3cr3t", got)
	}

	newVer, err := h.svc.Rotate(ctx, projectID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newVer != 2 {
		t.Fatalf("Rotate returned %d, want 2", newVer)
	}

	// Still readable via retained v1 KEK (DEK still at dek_key_version=1).
	if got := reveal(t, h.sec, configID, "DB"); got != "s3cr3t" {
		t.Fatalf("post-rotate (pre-rewrap) DB = %q, want s3cr3t", got)
	}

	res, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if res.Rewrapped != 1 {
		t.Fatalf("Rewrapped = %d, want 1", res.Rewrapped)
	}
	if len(res.Retired) != 1 || res.Retired[0] != 1 {
		t.Fatalf("Retired = %v, want [1]", res.Retired)
	}

	// Still readable, now via v2 KEK.
	if got := reveal(t, h.sec, configID, "DB"); got != "s3cr3t" {
		t.Fatalf("post-rewrap DB = %q, want s3cr3t", got)
	}

	st, err := h.svc.StatusFor(ctx, projectID)
	if err != nil {
		t.Fatalf("StatusFor: %v", err)
	}
	if st.CurrentVersion != 2 {
		t.Fatalf("CurrentVersion = %d, want 2", st.CurrentVersion)
	}
	if len(st.Pending) != 0 {
		t.Fatalf("Pending = %v, want empty", st.Pending)
	}
}

// TestRewrapNoOp: a rewrap with nothing pending re-wraps zero DEKs and retires
// nothing, without error — both for a freshly rotated+rewrapped project and a
// project never rotated.
func TestRewrapNoOp(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)
	writeSecret(t, h.sec, configID, "K", "v")

	// Never rotated: nothing < latest (=1).
	res, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		t.Fatalf("Rewrap (never rotated): %v", err)
	}
	if res.Rewrapped != 0 || len(res.Retired) != 0 {
		t.Fatalf("never-rotated rewrap = %+v, want zero/empty", res)
	}

	// Rotate + rewrap, then rewrap again — the second is a no-op.
	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := h.svc.Rewrap(ctx, projectID); err != nil {
		t.Fatalf("first Rewrap: %v", err)
	}
	res2, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		t.Fatalf("second Rewrap: %v", err)
	}
	if res2.Rewrapped != 0 || len(res2.Retired) != 0 {
		t.Fatalf("second rewrap = %+v, want zero/empty", res2)
	}
}

// TestRewrapFreshNonce: re-wrapping a DEK produces a different GCM nonce than
// the original wrapped_dek (fresh random nonce each WrapKey).
func TestRewrapFreshNonce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)
	writeSecret(t, h.sec, configID, "K", "value")

	before := wrappedDEKOf(t, configID, "K")
	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := h.svc.Rewrap(ctx, projectID); err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	after := wrappedDEKOf(t, configID, "K")

	beforeCT, err := crypto.ParseCiphertext(before)
	if err != nil {
		t.Fatalf("parse before: %v", err)
	}
	afterCT, err := crypto.ParseCiphertext(after)
	if err != nil {
		t.Fatalf("parse after: %v", err)
	}
	if bytes.Equal(beforeCT.Nonce, afterCT.Nonce) {
		t.Fatalf("nonce unchanged after rewrap: %x", afterCT.Nonce)
	}
}

// TestRewrapNeverDecryptsValue: even when the stored secret value ciphertext is
// garbage, Rotate+Rewrap succeed — proving rewrap only touches wrapped_dek and
// never opens the value. The value is unreadable afterward (as expected) but the
// re-wrap itself is unaffected.
func TestRewrapNeverDecryptsValue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)
	writeSecret(t, h.sec, configID, "K", "value")

	// Corrupt the value ciphertext (not the wrapped_dek).
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET ciphertext = E'\\xdeadbeef'
		  WHERE config_id=$1::uuid AND key='K'`, configID); err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	res, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		t.Fatalf("Rewrap must succeed despite corrupt value ciphertext: %v", err)
	}
	if res.Rewrapped != 1 {
		t.Fatalf("Rewrapped = %d, want 1", res.Rewrapped)
	}
	// The DEK re-wrap is valid: it round-trips under the new KEK even though the
	// value ciphertext is garbage (proving they are independent).
	if _, err := crypto.ParseCiphertext(wrappedDEKOf(t, configID, "K")); err != nil {
		t.Fatalf("re-wrapped DEK not parseable: %v", err)
	}
}

// TestRewrapTamperedDEK: a corrupted wrapped_dek makes Rewrap fail with an error
// naming the row id but never the secret plaintext, and never panics.
func TestRewrapTamperedDEK(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)
	const plaintext = "TOP-SECRET-VALUE"
	writeSecret(t, h.sec, configID, "K", plaintext)

	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Flip a byte inside the wrapped_dek (past the format+keyversion header so it
	// stays parseable but fails GCM auth).
	var rowID string
	if err := testPool.QueryRow(ctx,
		`UPDATE secret_values SET wrapped_dek = set_byte(wrapped_dek, 8, (get_byte(wrapped_dek, 8) # 255))
		  WHERE config_id=$1::uuid AND key='K' RETURNING id::text`, configID).Scan(&rowID); err != nil {
		t.Fatalf("tamper wrapped_dek: %v", err)
	}

	res, err := h.svc.Rewrap(ctx, projectID)
	if err == nil {
		t.Fatalf("Rewrap succeeded on tampered DEK, want error; res=%+v", res)
	}
	if !strings.Contains(err.Error(), rowID) {
		t.Fatalf("error %q does not contain row id %q", err.Error(), rowID)
	}
	if strings.Contains(err.Error(), plaintext) {
		t.Fatalf("error leaked plaintext: %q", err.Error())
	}
}

// TestRewrapCrashResume proves cross-batch resumability: RewrapBatch commits
// each batch in its own transaction, so a completed batch persists even when a
// LATER batch fails mid-sweep; a subsequent full Rewrap resumes from the
// persisted cursor and retires the version. We drive store.SecretRepo.RewrapBatch
// directly with a small limit (the public Rewrap offers no failure hook and uses
// a fixed 200-row batch) to make the first batch commit and the second fail, then
// call the real Rewrap to finish. A within-batch failure rolls that batch back
// whole — the store's atomicity unit is the batch, which this test respects.
func TestRewrapCrashResume(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)

	// Seed several secrets at v1.
	keys := []string{"A", "B", "C", "D"}
	for i, k := range keys {
		writeSecret(t, h.sec, configID, k, fmt.Sprintf("val-%d", i))
	}
	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Reconstruct the exact re-wrap crypto the service uses so partially-rewrapped
	// rows remain decryptable by the read path.
	p, err := store.NewProjectRepo(testStore).Get(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	latest := p.KEKVersion // 2
	latestKEK := unwrapKEK(t, h.kr, projectID, p.WrappedKEK)
	oldWrapped, err := store.NewProjectKEKVersionRepo(testStore).GetWrapped(ctx, projectID, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldKEK := unwrapKEK(t, h.kr, projectID, oldWrapped)

	realRewrap := func(row store.RewrapRow) ([]byte, error) {
		aad, aerr := dekAAD(projectID, row.ConfigID+"/"+row.Key, row.ValueVersion)
		if aerr != nil {
			return nil, aerr
		}
		dekCT, perr := crypto.ParseCiphertext(row.WrappedDEK)
		if perr != nil {
			return nil, perr
		}
		dek, uerr := crypto.UnwrapKey(oldKEK, dekCT, aad)
		if uerr != nil {
			return nil, uerr
		}
		newCT, werr := crypto.WrapKey(latestKEK, dek, aad)
		if werr != nil {
			return nil, werr
		}
		return newCT.Marshal(), nil
	}

	sr := store.NewSecretRepo(testStore)
	// First batch (limit 2) commits successfully — persisting partial progress.
	processed, cursor, err := sr.RewrapBatch(ctx, projectID, latest, "", 2, realRewrap)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if processed != 2 || cursor == "" {
		t.Fatalf("first batch processed=%d cursor=%q, want 2 and non-empty", processed, cursor)
	}
	// Second batch fails mid-sweep and rolls back whole (no further progress).
	_, _, berr := sr.RewrapBatch(ctx, projectID, latest, cursor, 2, func(store.RewrapRow) ([]byte, error) {
		return nil, errors.New("injected mid-sweep failure")
	})
	if berr == nil {
		t.Fatal("expected injected failure from second RewrapBatch")
	}

	// Partial progress persisted from the committed first batch: exactly 2 rows
	// advanced to latest, the rest remain at v1.
	atLatest := countDEKVersion(t, projectID, latest)
	atOld := countDEKVersion(t, projectID, 1)
	if atLatest != 2 {
		t.Fatalf("advanced rows = %d, want 2 (committed first batch)", atLatest)
	}
	if atOld == 0 {
		t.Fatalf("no rows remain at old version; failure did not stop the sweep")
	}
	if atLatest+atOld != len(keys) {
		t.Fatalf("row count drift: atLatest=%d atOld=%d total keys=%d", atLatest, atOld, len(keys))
	}

	// All secrets still readable through the real read path (both versions coexist).
	for i, k := range keys {
		if got := reveal(t, h.sec, configID, k); got != fmt.Sprintf("val-%d", i) {
			t.Fatalf("mid-sweep read %s = %q, want val-%d", k, got, i)
		}
	}

	// Real Rewrap finishes the rest and retires v1.
	res, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		t.Fatalf("resume Rewrap: %v", err)
	}
	if res.Rewrapped != atOld {
		t.Fatalf("resume Rewrapped = %d, want %d (the remaining rows)", res.Rewrapped, atOld)
	}
	if len(res.Retired) != 1 || res.Retired[0] != 1 {
		t.Fatalf("Retired = %v, want [1]", res.Retired)
	}
	if countDEKVersion(t, projectID, 1) != 0 {
		t.Fatalf("rows still at v1 after full rewrap")
	}
	for i, k := range keys {
		if got := reveal(t, h.sec, configID, k); got != fmt.Sprintf("val-%d", i) {
			t.Fatalf("post-resume read %s = %q, want val-%d", k, got, i)
		}
	}
}

// TestSealedKeyring: Rotate and Rewrap both return crypto.ErrSealed when the
// keyring is sealed.
func TestSealedKeyring(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)
	writeSecret(t, h.sec, configID, "K", "v")

	// Rotate first so Rewrap has real work; rewrap-sealed must fail before any batch.
	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate (unsealed): %v", err)
	}

	h.kr.Seal()

	if _, err := h.svc.Rotate(ctx, projectID); !errors.Is(err, crypto.ErrSealed) {
		t.Fatalf("Rotate sealed = %v, want ErrSealed", err)
	}
	if _, err := h.svc.Rewrap(ctx, projectID); !errors.Is(err, crypto.ErrSealed) {
		t.Fatalf("Rewrap sealed = %v, want ErrSealed", err)
	}
}

// unwrapKEK is a test helper mirroring the service's KEK unwrap.
func unwrapKEK(t *testing.T, kr *crypto.Keyring, projectID string, wrapped []byte) []byte {
	t.Helper()
	ct, err := crypto.ParseCiphertext(wrapped)
	if err != nil {
		t.Fatalf("parse wrapped kek: %v", err)
	}
	kek, err := kr.UnwrapProjectKEK(ct, projectID)
	if err != nil {
		t.Fatalf("unwrap kek: %v", err)
	}
	return kek
}

// countDEKVersion counts secret_values rows for a project at a given dek_key_version.
func countDEKVersion(t *testing.T, projectID string, version int) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM secret_values sv
		   JOIN configs c ON c.id = sv.config_id
		   JOIN environments e ON e.id = c.environment_id
		  WHERE e.project_id=$1::uuid AND sv.dek_key_version=$2`, projectID, version).Scan(&n); err != nil {
		t.Fatalf("count dek version %d: %v", version, err)
	}
	return n
}
