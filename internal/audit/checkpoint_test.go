package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// memCkStore is an in-memory CheckpointStore backed by a memStore's rows so the
// engine's checkpoint/prune logic can be exercised without Postgres.
type memCkStore struct {
	events      *memStore
	checkpoints []store.AuditCheckpointRow
	failLatest  bool
	failInsert  bool
	failPrune   bool
	failHead    bool
}

func (c *memCkStore) Head(_ context.Context) (int64, []byte, int64, error) {
	if c.failHead {
		return 0, nil, 0, errBoom
	}
	n := len(c.events.rows)
	if n == 0 {
		return 0, nil, 0, nil
	}
	last := c.events.rows[n-1]
	return last.Seq, last.Hash, int64(n), nil
}

func (c *memCkStore) Insert(_ context.Context, cp store.AuditCheckpointRow) error {
	if c.failInsert {
		return errBoom
	}
	for _, existing := range c.checkpoints {
		if existing.ThroughSeq == cp.ThroughSeq {
			return store.ErrAlreadyExists
		}
	}
	c.checkpoints = append(c.checkpoints, cp)
	return nil
}

func (c *memCkStore) Latest(_ context.Context) (*store.AuditCheckpointRow, error) {
	if c.failLatest {
		return nil, errBoom
	}
	if len(c.checkpoints) == 0 {
		return nil, nil
	}
	best := c.checkpoints[0]
	for _, cp := range c.checkpoints[1:] {
		if cp.ThroughSeq > best.ThroughSeq {
			best = cp
		}
	}
	return &best, nil
}

func (c *memCkStore) PruneThrough(_ context.Context, throughSeq int64) (int64, error) {
	if c.failPrune {
		return 0, errBoom
	}
	var kept []store.AuditRow
	var deleted int64
	for _, r := range c.events.rows {
		if r.Seq <= throughSeq {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	c.events.rows = kept
	return deleted, nil
}

// testKey is a fixed checkpoint MAC key for deterministic tests.
var testKey = []byte("0123456789abcdef0123456789abcdef")

func keyFn(k []byte) MACKeyFunc {
	return func(_ context.Context) ([]byte, error) {
		out := make([]byte, len(k)) // Recorder zeroizes the returned slice; hand a copy
		copy(out, k)
		return out, nil
	}
}

// ckRec builds a Recorder wired with an in-memory checkpoint store over the same
// event rows, using the fixed test MAC key.
func ckRec(t *testing.T) (*Recorder, *memStore, *memCkStore) {
	t.Helper()
	m := &memStore{}
	ck := &memCkStore{events: m}
	r := New(m).WithCheckpoints(ck, keyFn(testKey))
	return r, m, ck
}

func seed(t *testing.T, r *Recorder, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := r.Record(ctx, Event{
			Actor: Actor{Kind: "user", Name: "a@b.c"},
			Action: "token.mint", Resource: "tokens/x", Result: "success", IP: "1.1.1.1",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeriveCheckpointKeyIsDomainSeparated(t *testing.T) {
	tokenKey := []byte("token-hmac-key-material-32-bytes!")
	ck := DeriveCheckpointKey(tokenKey)
	if len(ck) != 32 {
		t.Fatalf("derived key len = %d, want 32", len(ck))
	}
	// The derived key must not equal a plain HMAC of the same input under a
	// different label — and must be deterministic.
	ck2 := DeriveCheckpointKey(tokenKey)
	if string(ck) != string(ck2) {
		t.Fatal("derivation must be deterministic")
	}
	if string(ck) == string(tokenKey) {
		t.Fatal("derived key must differ from the token key")
	}
	// A different token key yields a different checkpoint key.
	other := DeriveCheckpointKey([]byte("different-token-key-material-32b!"))
	if string(ck) == string(other) {
		t.Fatal("distinct token keys must derive distinct checkpoint keys")
	}
}

func TestCheckpointMACCreateAndVerify(t *testing.T) {
	mac := computeCheckpointMAC(testKey, 42, []byte("hashbytes"), 42)
	cp := Checkpoint{ThroughSeq: 42, ThroughHash: []byte("hashbytes"), EventCount: 42, MAC: mac}
	if !verifyCheckpointMAC(testKey, cp) {
		t.Fatal("freshly computed MAC must verify")
	}
	// A different key must not verify.
	if verifyCheckpointMAC([]byte("wrong-key-wrong-key-wrong-key-32"), cp) {
		t.Fatal("MAC must not verify under a different key")
	}
}

func TestCheckpointMACFieldsUnambiguous(t *testing.T) {
	// Length-prefixing must make (seq, hash, count) triples that differ only in
	// how the hash boundary falls produce distinct MACs.
	a := computeCheckpointMAC(testKey, 1, []byte("AB"), 3)
	b := computeCheckpointMAC(testKey, 1, []byte("A"), 3) // shorter hash, same trailing count
	if string(a) == string(b) {
		t.Fatal("different hash lengths must not collide")
	}
	c := computeCheckpointMAC(testKey, 12, []byte("x"), 3)
	d := computeCheckpointMAC(testKey, 1, []byte("x"), 23)
	if string(c) == string(d) {
		t.Fatal("shifting digits between seq and count must not collide")
	}
}

func TestCreateCheckpointHappyPath(t *testing.T) {
	r, m, ck := ckRec(t)
	seed(t, r, 3)
	info, err := r.CreateCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ThroughSeq != 3 || info.EventCount != 3 || !info.MACValid {
		t.Fatalf("checkpoint info = %+v", info)
	}
	if len(ck.checkpoints) != 1 || ck.checkpoints[0].ThroughSeq != 3 {
		t.Fatalf("stored checkpoints = %+v", ck.checkpoints)
	}
	// The stored MAC must anchor the actual head hash.
	if string(ck.checkpoints[0].ThroughHash) != string(m.rows[2].Hash) {
		t.Fatal("checkpoint through_hash must equal head hash")
	}
}

func TestCreateCheckpointEmptyChainRefused(t *testing.T) {
	r, _, _ := ckRec(t)
	if _, err := r.CreateCheckpoint(context.Background()); !errors.Is(err, ErrNoEvents) {
		t.Fatalf("want ErrNoEvents, got %v", err)
	}
}

func TestCreateCheckpointDisabledWithoutWiring(t *testing.T) {
	r, _ := rec(t) // no checkpoint store wired
	if _, err := r.CreateCheckpoint(context.Background()); !errors.Is(err, ErrCheckpointsDisabled) {
		t.Fatalf("want ErrCheckpointsDisabled, got %v", err)
	}
	if _, err := r.Prune(context.Background(), -1); !errors.Is(err, ErrCheckpointsDisabled) {
		t.Fatalf("prune want ErrCheckpointsDisabled, got %v", err)
	}
}

func TestVerifyFromCheckpointHappyPath(t *testing.T) {
	r, _, _ := ckRec(t)
	ctx := context.Background()
	seed(t, r, 5)
	if _, err := r.CreateCheckpoint(ctx); err != nil { // anchor at seq 5
		t.Fatal(err)
	}
	seed(t, r, 3) // three more events (seq 6,7,8)
	res, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || !res.FromCheckpoint || res.Checkpoint == nil {
		t.Fatalf("verify = %+v", res)
	}
	if res.Checkpoint.ThroughSeq != 5 || res.Count != 8 || res.HeadSeq != 8 {
		t.Fatalf("verify counts wrong: %+v", res)
	}
}

func TestVerifyFromCheckpointAfterPrune(t *testing.T) {
	r, m, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 5)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	seed(t, r, 3) // seq 6,7,8
	// Prune the verified prefix (seq <= 5). No shipper → shipHWM = -1.
	pr, err := r.Prune(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PrunedThrough != 5 || pr.Deleted != 5 {
		t.Fatalf("prune = %+v", pr)
	}
	if len(m.rows) != 3 || m.rows[0].Seq != 6 {
		t.Fatalf("post-prune rows = %+v", m.rows)
	}
	// Anchor rows survive.
	if len(ck.checkpoints) != 1 {
		t.Fatal("prune must not delete checkpoint anchors")
	}
	// Verify still passes walking forward from the checkpoint over the pruned log.
	res, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || !res.FromCheckpoint || res.Count != 8 || res.HeadSeq != 8 {
		t.Fatalf("verify after prune = %+v", res)
	}
}

func TestVerifyDetectsTamperedCheckpointMAC(t *testing.T) {
	r, _, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 4)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	ck.checkpoints[0].MAC[0] ^= 0xFF // tamper the MAC
	res, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid || res.Reason != "checkpoint_mac_invalid" || res.BrokenAtSeq != 4 {
		t.Fatalf("verify = %+v", res)
	}
	if res.Checkpoint == nil || res.Checkpoint.MACValid {
		t.Fatal("checkpoint info must report MACValid=false")
	}
}

func TestVerifyDetectsTamperedCheckpointThroughHash(t *testing.T) {
	r, _, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 4)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	// Flip a byte of the anchored hash; the MAC no longer matches the fields.
	ck.checkpoints[0].ThroughHash[0] ^= 0xFF
	res, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid || res.Reason != "checkpoint_mac_invalid" {
		t.Fatalf("verify = %+v", res)
	}
}

func TestVerifyNoCheckpointWalksFromGenesis(t *testing.T) {
	r, _, _ := ckRec(t)
	ctx := context.Background()
	seed(t, r, 3)
	res, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.FromCheckpoint || res.Checkpoint != nil || res.Count != 3 {
		t.Fatalf("verify = %+v", res)
	}
}

func TestPruneGuards(t *testing.T) {
	t.Run("no checkpoint refuses", func(t *testing.T) {
		r, _, _ := ckRec(t)
		seed(t, r, 3)
		if _, err := r.Prune(context.Background(), -1); !errors.Is(err, ErrNoCheckpoint) {
			t.Fatalf("want ErrNoCheckpoint, got %v", err)
		}
	})

	t.Run("tampered checkpoint refuses", func(t *testing.T) {
		r, _, ck := ckRec(t)
		ctx := context.Background()
		seed(t, r, 3)
		if _, err := r.CreateCheckpoint(ctx); err != nil {
			t.Fatal(err)
		}
		ck.checkpoints[0].MAC[0] ^= 0xFF
		if _, err := r.Prune(ctx, -1); !errors.Is(err, ErrCheckpointMAC) {
			t.Fatalf("want ErrCheckpointMAC, got %v", err)
		}
	})

	t.Run("clamps to ship HWM", func(t *testing.T) {
		r, m, _ := ckRec(t)
		ctx := context.Background()
		seed(t, r, 10)
		if _, err := r.CreateCheckpoint(ctx); err != nil { // anchor at 10
			t.Fatal(err)
		}
		// Only 4 events shipped: prune must stop at 4, not 10.
		pr, err := r.Prune(ctx, 4)
		if err != nil {
			t.Fatal(err)
		}
		if pr.PrunedThrough != 4 || pr.Deleted != 4 {
			t.Fatalf("prune = %+v", pr)
		}
		if len(m.rows) != 6 || m.rows[0].Seq != 5 {
			t.Fatalf("post-prune rows = %+v", m.rows)
		}
	})

	t.Run("nothing shipped refuses", func(t *testing.T) {
		r, _, _ := ckRec(t)
		ctx := context.Background()
		seed(t, r, 5)
		if _, err := r.CreateCheckpoint(ctx); err != nil {
			t.Fatal(err)
		}
		// Ship HWM 0 → nothing shipped → nothing prunable.
		if _, err := r.Prune(ctx, 0); !errors.Is(err, ErrPrunePastShipHWM) {
			t.Fatalf("want ErrPrunePastShipHWM, got %v", err)
		}
	})

	t.Run("does not delete anchor rows", func(t *testing.T) {
		r, _, ck := ckRec(t)
		ctx := context.Background()
		seed(t, r, 5)
		if _, err := r.CreateCheckpoint(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Prune(ctx, -1); err != nil {
			t.Fatal(err)
		}
		if len(ck.checkpoints) != 1 {
			t.Fatal("prune must leave checkpoint anchors intact")
		}
	})
}

// flipCopy returns a copy of b with the first byte flipped. The in-memory rows
// share slices (a row's PrevHash IS the previous row's Hash), so tampering must
// replace the slice rather than mutate it in place, or the "tamper" silently
// edits two fields at once.
func flipCopy(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[0] ^= 0xFF
	return out
}

// TestCreateCheckpointVerifiesBeforeSigning covers M-5: a checkpoint must attest
// "the chain up to here is intact", not merely "this is what the head row said".
// Each case tampers the chain (or the previous anchor) in a different way and
// asserts CreateCheckpoint refuses with ErrChainInvalid and writes NO anchor row.
func TestCreateCheckpointVerifiesBeforeSigning(t *testing.T) {
	tests := []struct {
		name string
		// tamper mutates the seeded state after `seedN` events (and after an
		// optional first checkpoint) but before the checkpoint under test.
		firstCheckpointAfter int // 0 = no prior checkpoint; else seed this many, anchor, then seed more
		seedN                int
		tamper               func(t *testing.T, m *memStore, ck *memCkStore)
	}{
		{
			name:   "tampered event field from genesis",
			seedN:  4,
			tamper: func(_ *testing.T, m *memStore, _ *memCkStore) { m.rows[1].Action = "user.disable" },
		},
		{
			name:   "tampered stored hash from genesis",
			seedN:  4,
			tamper: func(_ *testing.T, m *memStore, _ *memCkStore) { m.rows[2].Hash = flipCopy(m.rows[2].Hash) },
		},
		{
			name:   "tampered prev_hash link from genesis",
			seedN:  4,
			tamper: func(_ *testing.T, m *memStore, _ *memCkStore) { m.rows[3].PrevHash = flipCopy(m.rows[3].PrevHash) },
		},
		{
			name:   "deleted event from genesis",
			seedN:  4,
			tamper: func(_ *testing.T, m *memStore, _ *memCkStore) { m.rows = append(m.rows[:1], m.rows[2:]...) },
		},
		{
			name:                 "tampered event past a prior checkpoint",
			firstCheckpointAfter: 3,
			seedN:                3,
			tamper:               func(_ *testing.T, m *memStore, _ *memCkStore) { m.rows[4].Action = "tampered" },
		},
		{
			name:                 "forged previous checkpoint MAC",
			firstCheckpointAfter: 3,
			seedN:                2,
			tamper:               func(_ *testing.T, _ *memStore, ck *memCkStore) { ck.checkpoints[0].MAC[0] ^= 0xFF },
		},
		{
			name:                 "previous anchor sits above the head (log truncated)",
			firstCheckpointAfter: 5,
			seedN:                2,
			tamper: func(_ *testing.T, m *memStore, _ *memCkStore) {
				m.rows = m.rows[:3] // head drops to seq 3, below the seq-5 anchor
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, m, ck := ckRec(t)
			ctx := context.Background()
			if tc.firstCheckpointAfter > 0 {
				seed(t, r, tc.firstCheckpointAfter)
				if _, err := r.CreateCheckpoint(ctx); err != nil {
					t.Fatalf("first checkpoint: %v", err)
				}
			}
			seed(t, r, tc.seedN)
			before := len(ck.checkpoints)
			tc.tamper(t, m, ck)

			if _, err := r.CreateCheckpoint(ctx); !errors.Is(err, ErrChainInvalid) {
				t.Fatalf("want ErrChainInvalid, got %v", err)
			}
			if len(ck.checkpoints) != before {
				t.Fatalf("no checkpoint row may be written on a failed verify: %d -> %d",
					before, len(ck.checkpoints))
			}
		})
	}
}

// TestCreateCheckpointFromPreviousCheckpoint proves the happy path when a prior
// anchor exists: the walk starts at the anchor (not genesis) and still signs the
// real head.
func TestCreateCheckpointFromPreviousCheckpoint(t *testing.T) {
	r, m, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 3)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	seed(t, r, 4) // seq 4..7
	info, err := r.CreateCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ThroughSeq != 7 || !info.MACValid {
		t.Fatalf("checkpoint info = %+v", info)
	}
	if len(ck.checkpoints) != 2 || ck.checkpoints[1].ThroughSeq != 7 {
		t.Fatalf("stored checkpoints = %+v", ck.checkpoints)
	}
	if string(ck.checkpoints[1].ThroughHash) != string(m.rows[6].Hash) {
		t.Fatal("checkpoint through_hash must equal head hash")
	}
}

// TestCreateCheckpointAfterPruneStillVerifies proves the pre-sign walk anchors on
// the surviving checkpoint and does not require the pruned prefix to still exist.
func TestCreateCheckpointAfterPruneStillVerifies(t *testing.T) {
	r, _, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 5)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	seed(t, r, 3) // seq 6,7,8
	if _, err := r.Prune(ctx, -1); err != nil {
		t.Fatal(err)
	}
	info, err := r.CreateCheckpoint(ctx)
	if err != nil {
		t.Fatalf("checkpoint over a pruned log must succeed: %v", err)
	}
	if info.ThroughSeq != 8 || !info.MACValid {
		t.Fatalf("checkpoint info = %+v", info)
	}
	if len(ck.checkpoints) != 2 {
		t.Fatalf("stored checkpoints = %+v", ck.checkpoints)
	}
}

// TestCreateCheckpointAtSameHeadConflicts proves re-anchoring an unchanged head
// is still a store conflict (not ErrChainInvalid) — the head matches the anchor.
func TestCreateCheckpointAtSameHeadConflicts(t *testing.T) {
	r, _, _ := ckRec(t)
	ctx := context.Background()
	seed(t, r, 3)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateCheckpoint(ctx); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("want store.ErrAlreadyExists, got %v", err)
	}
}

// TestCreateCheckpointPropagatesStoreError proves a store failure during the
// pre-sign walk surfaces as the store error, not as ErrChainInvalid.
func TestCreateCheckpointPropagatesStoreError(t *testing.T) {
	r, m, ck := ckRec(t)
	ctx := context.Background()
	seed(t, r, 3)
	m.failIterate = true
	if _, err := r.CreateCheckpoint(ctx); err != errBoom {
		t.Fatalf("want errBoom, got %v", err)
	}
	if len(ck.checkpoints) != 0 {
		t.Fatal("no checkpoint may be written when the walk fails")
	}
}

// TestCheckpointStoreErrorsSurface proves store failures on the new guard paths
// surface as store errors (fail-closed), never as a silent success or a
// misleading ErrChainInvalid.
func TestCheckpointStoreErrorsSurface(t *testing.T) {
	t.Run("create: anchor lookup fails", func(t *testing.T) {
		r, _, ck := ckRec(t)
		seed(t, r, 3)
		ck.failLatest = true
		if _, err := r.CreateCheckpoint(context.Background()); err != errBoom {
			t.Fatalf("want errBoom, got %v", err)
		}
		if len(ck.checkpoints) != 0 {
			t.Fatal("no checkpoint may be written when the anchor lookup fails")
		}
	})

	t.Run("prune: boundary scan fails", func(t *testing.T) {
		r, m, _ := ckRec(t)
		ctx := context.Background()
		seed(t, r, 3)
		if _, err := r.CreateCheckpoint(ctx); err != nil {
			t.Fatal(err)
		}
		seed(t, r, 2)
		rows := len(m.rows)
		m.failIterate = true
		if _, err := r.Prune(ctx, -1); err != errBoom {
			t.Fatalf("want errBoom, got %v", err)
		}
		if len(m.rows) != rows {
			t.Fatal("prune must not delete anything when the boundary scan fails")
		}
	})
}

// TestPruneRefusesBrokenAnchorBoundary proves prune will not hard-delete the
// prefix when the first surviving event no longer links to the anchor — that
// prefix is the remaining evidence of the break.
func TestPruneRefusesBrokenAnchorBoundary(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(m *memStore)
	}{
		{
			name:   "survivor prev_hash does not match the anchor",
			tamper: func(m *memStore) { m.rows[5].PrevHash = flipCopy(m.rows[5].PrevHash) }, // seq 6
		},
		{
			name:   "survivor seq is not anchor+1 (gap)",
			tamper: func(m *memStore) { m.rows = append(m.rows[:5], m.rows[6:]...) }, // drop seq 6
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, m, _ := ckRec(t)
			ctx := context.Background()
			seed(t, r, 5)
			if _, err := r.CreateCheckpoint(ctx); err != nil { // anchor at 5
				t.Fatal(err)
			}
			seed(t, r, 3) // seq 6,7,8
			tc.tamper(m)
			rows := len(m.rows)
			if _, err := r.Prune(ctx, -1); !errors.Is(err, ErrChainInvalid) {
				t.Fatalf("want ErrChainInvalid, got %v", err)
			}
			if len(m.rows) != rows {
				t.Fatal("prune must not delete anything when it refuses")
			}
		})
	}
}

// TestPruneAllowsWhenNothingSurvivesTheAnchor proves the boundary guard does not
// block the ordinary case where the anchor IS the head (no events past it).
func TestPruneAllowsWhenNothingSurvivesTheAnchor(t *testing.T) {
	r, m, _ := ckRec(t)
	ctx := context.Background()
	seed(t, r, 4)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	pr, err := r.Prune(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PrunedThrough != 4 || pr.Deleted != 4 || len(m.rows) != 0 {
		t.Fatalf("prune = %+v, rows = %d", pr, len(m.rows))
	}
}

func TestLatestCheckpoint(t *testing.T) {
	r, _, _ := ckRec(t)
	ctx := context.Background()
	if cp, err := r.LatestCheckpoint(ctx); err != nil || cp != nil {
		t.Fatalf("empty: cp=%+v err=%v", cp, err)
	}
	seed(t, r, 2)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	cp, err := r.LatestCheckpoint(ctx)
	if err != nil || cp == nil || cp.ThroughSeq != 2 || !cp.MACValid {
		t.Fatalf("cp=%+v err=%v", cp, err)
	}
}

// TestEventCountConsistentAcrossPrune pins QA finding I-6: a checkpoint taken
// after a prune must record the same lifetime total that Verify reports at that
// moment. Previously CreateCheckpoint used the store's COUNT(*) (a retained-row
// count), so the checkpoint recorded a smaller number than the verify right
// before it — the two disagreed about the same chain.
func TestEventCountConsistentAcrossPrune(t *testing.T) {
	r, m, _ := ckRec(t)
	ctx := context.Background()

	seed(t, r, 5)
	if _, err := r.CreateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	seed(t, r, 3) // seq 6,7,8
	if _, err := r.Prune(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if len(m.rows) != 3 {
		t.Fatalf("expected 3 retained rows after prune, got %d", len(m.rows))
	}

	// Verify reports the lifetime total (anchor count + walked).
	before, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 8 {
		t.Fatalf("verify count = %d, want 8 (lifetime, not retained)", before.Count)
	}

	// A checkpoint captured now must agree with it — not report the 3 retained rows.
	info, err := r.CreateCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.EventCount != before.Count {
		t.Fatalf("checkpoint event_count = %d, want %d (must match verify's count)",
			info.EventCount, before.Count)
	}
	if info.EventCount == int64(len(m.rows)) {
		t.Fatalf("event_count %d is the retained-row count — the I-6 regression", info.EventCount)
	}

	// And verify anchored on the NEW checkpoint still reports the same total.
	after, err := r.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != before.Count {
		t.Fatalf("verify count drifted after re-anchoring: %d → %d", before.Count, after.Count)
	}
}
