package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// seedVersions writes n config versions to configID, each setting KEY_A to a
// version-specific value plus, on odd versions, an extra key. It returns, per
// config version number, the full plaintext state at that version — the
// reference against which restorability is checked after a prune.
func seedVersions(t *testing.T, s *Service, configID string, n int) map[int]map[string]string {
	t.Helper()
	ctx := context.Background()
	want := map[int]map[string]string{}
	for i := 1; i <= n; i++ {
		changes := []SecretChange{{Key: "KEY_A", Value: []byte(fmt.Sprintf("a-%d", i))}}
		if i%2 == 1 {
			changes = append(changes, SecretChange{Key: "KEY_ODD", Value: []byte(fmt.Sprintf("odd-%d", i))})
		}
		cv, err := s.SetSecrets(ctx, configID, changes, fmt.Sprintf("save %d", i), "tester")
		if err != nil {
			t.Fatalf("SetSecrets %d: %v", i, err)
		}
		state, err := s.RevealConfigVersion(ctx, configID, cv.Version)
		if err != nil {
			t.Fatalf("RevealConfigVersion %d: %v", cv.Version, err)
		}
		snap := map[string]string{}
		for k, sec := range state {
			snap[k] = string(sec.Value)
		}
		want[cv.Version] = snap
	}
	return want
}

// assertRestorable is the headline invariant check: every config version that
// survived a prune must still resolve to EXACTLY the state it held before, and
// a rollback to it must reproduce that state.
//
// This is the property a naive value-version prune destroys. Because
// config_version_entries cascades on a secret_values delete (migration 000005),
// deleting a value row silently removes the manifest entries pointing at it, so
// an old config version would come back missing keys with no error anywhere.
func assertRestorable(t *testing.T, s *Service, configID string, want map[int]map[string]string, survivors []int) {
	t.Helper()
	ctx := context.Background()
	for _, v := range survivors {
		state, err := s.RevealConfigVersion(ctx, configID, v)
		if err != nil {
			t.Fatalf("surviving v%d no longer resolves: %v", v, err)
		}
		got := map[string]string{}
		for k, sec := range state {
			got[k] = string(sec.Value)
		}
		if len(got) != len(want[v]) {
			t.Fatalf("v%d: %d keys after prune, want %d (%v vs %v)", v, len(got), len(want[v]), got, want[v])
		}
		for k, wv := range want[v] {
			if got[k] != wv {
				t.Fatalf("v%d key %s: got %q want %q", v, k, got[k], wv)
			}
		}
	}
}

// assertNoDanglingEntries proves directly in SQL that no surviving config
// version holds a live manifest entry whose secret_values row is gone. Under the
// ON DELETE CASCADE this should be structurally impossible — the cascade would
// have removed the ENTRY instead — so the companion check is that the surviving
// versions still hold the number of entries they are supposed to, which
// assertRestorable covers by value.
func assertNoDanglingEntries(t *testing.T, configID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM config_version_entries e
		   JOIN config_versions v ON v.id = e.config_version_id
		  WHERE v.config_id = $1::uuid AND NOT e.tombstone
		    AND NOT EXISTS (SELECT 1 FROM secret_values sv WHERE sv.id = e.secret_value_id)`,
		configID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d dangling manifest entries after prune", n)
	}
}

func countRows(t *testing.T, sql, configID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), sql, configID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const (
	sqlCountVersions = `SELECT count(*) FROM config_versions WHERE config_id = $1::uuid`
	sqlCountValues   = `SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`
)

// TestPruneKeepsEveryRetainedVersionRestorable is the headline test. After a
// prune that removes most of a config's history, every SURVIVING config version
// must still resolve to exactly the state it held before, and a rollback to the
// oldest survivor must reproduce that state.
func TestPruneKeepsEveryRetainedVersionRestorable(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	want := seedVersions(t, s, configID, 8)
	valuesBefore := countRows(t, sqlCountValues, configID)

	res, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedVersions != 5 {
		t.Fatalf("deleted %d config versions, want 5 (pruned=%v)", res.DeletedVersions, res.PrunedVersions)
	}
	if res.RetainedVersions != 3 {
		t.Fatalf("retained %d versions, want 3", res.RetainedVersions)
	}
	if res.DeletedValues == 0 {
		t.Fatal("prune removed config versions but garbage-collected no value versions")
	}
	if got := countRows(t, sqlCountValues, configID); got != valuesBefore-res.DeletedValues {
		t.Fatalf("secret_values = %d, want %d", got, valuesBefore-res.DeletedValues)
	}

	// THE INVARIANT: v6, v7, v8 survive and are each fully restorable.
	assertRestorable(t, s, configID, want, []int{6, 7, 8})
	assertNoDanglingEntries(t, configID)

	// And a rollback to the oldest survivor reproduces that survivor's state.
	cv, err := s.Rollback(ctx, configID, 6, "roll", "tester")
	if err != nil {
		t.Fatalf("rollback to oldest survivor: %v", err)
	}
	rolled, err := s.RevealConfigVersion(ctx, configID, cv.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(rolled) != len(want[6]) {
		t.Fatalf("rollback produced %d keys, want %d", len(rolled), len(want[6]))
	}
	for k, wv := range want[6] {
		if got := string(rolled[k].Value); got != wv {
			t.Fatalf("rollback key %s: got %q want %q", k, got, wv)
		}
	}

	// Pruned versions are genuinely gone.
	if _, err := s.RevealConfigVersion(ctx, configID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned v1 reveal: got %v, want ErrNotFound", err)
	}
}

// TestPruneStillReferencedValueSurvives pins the garbage-collection rule: a
// secret_values row that a SURVIVING config version still points at is kept even
// though older versions referencing it were pruned. KEY_STABLE is written once
// and carried forward by every later save, so a single value row is referenced
// by all eight manifests; pruning five of them must not remove it.
func TestPruneStillReferencedValueSurvives(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID,
		[]SecretChange{{Key: "KEY_STABLE", Value: []byte("never-changes")}}, "v1", "tester"); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 8; i++ {
		if _, err := s.SetSecrets(ctx, configID,
			[]SecretChange{{Key: "CHURN", Value: []byte(fmt.Sprintf("c-%d", i))}},
			fmt.Sprintf("v%d", i), "tester"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 2}); err != nil {
		t.Fatal(err)
	}

	state, err := s.RevealConfigVersion(ctx, configID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(state["KEY_STABLE"].Value); got != "never-changes" {
		t.Fatalf("carried-forward value lost: %q", got)
	}
	var stable int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id=$1::uuid AND key='KEY_STABLE'`,
		configID).Scan(&stable); err != nil {
		t.Fatal(err)
	}
	if stable != 1 {
		t.Fatalf("KEY_STABLE value rows = %d, want 1 (still referenced, must not be GC'd)", stable)
	}
	assertNoDanglingEntries(t, configID)
}

// TestPruneDryRunChangesNothing asserts the preview mode is genuinely read-only
// while still reporting exactly what a real prune would do.
func TestPruneDryRunChangesNothing(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	want := seedVersions(t, s, configID, 6)

	versionsBefore := countRows(t, sqlCountVersions, configID)
	valuesBefore := countRows(t, sqlCountValues, configID)

	dry, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 2, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun {
		t.Fatal("result does not report dry_run")
	}
	if dry.DeletedVersions != 4 {
		t.Fatalf("dry run would delete %d versions, want 4", dry.DeletedVersions)
	}
	if got := countRows(t, sqlCountVersions, configID); got != versionsBefore {
		t.Fatalf("dry run changed config_versions: %d != %d", got, versionsBefore)
	}
	if got := countRows(t, sqlCountValues, configID); got != valuesBefore {
		t.Fatalf("dry run changed secret_values: %d != %d", got, valuesBefore)
	}
	assertRestorable(t, s, configID, want, []int{1, 2, 3, 4, 5, 6})

	// The real prune matches the preview exactly.
	applied, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if applied.DeletedVersions != dry.DeletedVersions || applied.DeletedValues != dry.DeletedValues {
		t.Fatalf("real prune %v/%v != preview %v/%v",
			applied.DeletedVersions, applied.DeletedValues, dry.DeletedVersions, dry.DeletedValues)
	}
}

// TestPruneFloorsAreRespected covers the retention floors: the instance-wide
// floor and a per-config override each RAISE the effective retention, and never
// lower it, whatever the request asks for.
func TestPruneFloorsAreRespected(t *testing.T) {
	ctx := context.Background()
	nine := 9
	two := 2

	for _, tc := range []struct {
		name         string
		floor        RetentionFloor
		override     *int // per-config min_versions, nil = none
		keepVersions int
		wantRetained int
	}{
		{"no floor, request wins", RetentionFloor{}, nil, 2, 2},
		{"instance floor raises", RetentionFloor{MinVersions: 6}, nil, 2, 6},
		{"config override raises", RetentionFloor{}, &nine, 2, 9},
		{"override below floor cannot weaken it", RetentionFloor{MinVersions: 6}, &two, 1, 6},
		{"request above both floors wins", RetentionFloor{MinVersions: 3}, &two, 8, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newService(t)
			s.SetRetentionFloor(tc.floor)
			_, configID := mkChain(t, s)
			want := seedVersions(t, s, configID, 10)
			if tc.override != nil {
				if err := s.SetVersionRetention(ctx, configID, tc.override, nil, ""); err != nil {
					t.Fatal(err)
				}
			}
			res, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: tc.keepVersions})
			if err != nil {
				t.Fatal(err)
			}
			if res.RetainedVersions != tc.wantRetained {
				t.Fatalf("retained %d versions, want %d (effective keep=%d)",
					res.RetainedVersions, tc.wantRetained, res.KeepVersions)
			}
			survivors := make([]int, 0, tc.wantRetained)
			for v := 10 - tc.wantRetained + 1; v <= 10; v++ {
				survivors = append(survivors, v)
			}
			assertRestorable(t, s, configID, want, survivors)
			assertNoDanglingEntries(t, configID)
		})
	}
}

// TestPruneNeverRemovesLatestVersion drives the store directly with a
// deliberately hostile plan (keep zero versions, no age floor) and asserts the
// latest version still survives and still resolves.
func TestPruneNeverRemovesLatestVersion(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	want := seedVersions(t, s, configID, 5)

	res, err := store.NewSecretRepo(testStore).PruneConfigVersions(ctx, configID,
		store.PrunePlan{KeepVersions: 0, KeepDays: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.KeepVersions != 1 {
		t.Fatalf("KeepVersions=0 was not clamped to 1: %d", res.KeepVersions)
	}
	if res.RetainedVersions != 1 {
		t.Fatalf("retained %d versions, want exactly the latest", res.RetainedVersions)
	}
	assertRestorable(t, s, configID, want, []int{5})
	assertNoDanglingEntries(t, configID)

	// The service layer refuses a request that asks for nothing at all rather
	// than defaulting to something destructive.
	if _, err := s.PruneVersions(ctx, configID, PruneRequest{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty prune request: got %v, want ErrValidation", err)
	}
}

// TestPruneKeepDaysRetainsRecentVersions covers the age-based rule: with every
// version freshly written, a positive keep_days retains all of them even though
// keep_versions alone would prune most.
func TestPruneKeepDaysRetainsRecentVersions(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	want := seedVersions(t, s, configID, 5)

	res, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1, KeepDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedVersions != 0 {
		t.Fatalf("age floor did not protect same-day versions: deleted %d", res.DeletedVersions)
	}
	assertRestorable(t, s, configID, want, []int{1, 2, 3, 4, 5})

	// Backdate v1..v3 past the age floor; they then become prunable.
	if _, err := testPool.Exec(ctx,
		`UPDATE config_versions SET created_at = now() - interval '30 days'
		  WHERE config_id = $1::uuid AND version <= 3`, configID); err != nil {
		t.Fatal(err)
	}
	res, err = s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1, KeepDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedVersions != 3 {
		t.Fatalf("deleted %d versions, want 3 (pruned=%v)", res.DeletedVersions, res.PrunedVersions)
	}
	assertRestorable(t, s, configID, want, []int{4, 5})
	assertNoDanglingEntries(t, configID)
}

// mkUser inserts a bare users row and returns its id (the approval tables have
// NOT NULL user foreign keys).
func mkUser(t *testing.T) string {
	t.Helper()
	var id string
	email := fmt.Sprintf("prune-%d@corp.io", slugSeq.Add(1))
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPruneBlockedByPendingEditRequest asserts the fail-closed guard: a config
// with a pending (or in-flight 'applying') four-eyes edit request is not pruned
// at all, and nothing is destroyed.
func TestPruneBlockedByPendingEditRequest(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{"pending", "applying"} {
		t.Run(status, func(t *testing.T) {
			s := newService(t)
			_, configID := mkChain(t, s)
			want := seedVersions(t, s, configID, 5)
			userID := mkUser(t)

			// The proposal blob is opaque here; the guard is about its existence.
			if _, err := testPool.Exec(ctx,
				`INSERT INTO config_edit_requests
				   (config_id, requested_by, status, proposed_ciphertext, wrapped_dek, nonce, changed_keys)
				 VALUES ($1::uuid, $2::uuid, $3, '\x00'::bytea, '\x00'::bytea, '\x00'::bytea, '["KEY_A"]'::jsonb)`,
				configID, userID, status); err != nil {
				t.Fatal(err)
			}

			_, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1})
			if !errors.Is(err, store.ErrPruneBlocked) {
				t.Fatalf("prune with a %s edit request: got %v, want ErrPruneBlocked", status, err)
			}
			if got := countRows(t, sqlCountVersions, configID); got != 5 {
				t.Fatalf("blocked prune still deleted versions: %d remain", got)
			}
			assertRestorable(t, s, configID, want, []int{1, 2, 3, 4, 5})

			// Resolving the request unblocks the prune.
			if _, err := testPool.Exec(ctx,
				`UPDATE config_edit_requests SET status='rejected' WHERE config_id=$1::uuid`, configID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1}); err != nil {
				t.Fatalf("prune after resolving the edit request: %v", err)
			}
		})
	}
}

// TestPrunePinsPendingPromotionSource asserts that a config version a pending
// promotion request reads as its source is retained even though the retention
// floor would drop it — approving that request needs that exact version's state.
func TestPrunePinsPendingPromotionSource(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	projectID, configID := mkChain(t, s)
	want := seedVersions(t, s, configID, 6)
	userID := mkUser(t)

	// The pending request promotes FROM v2 of this config.
	var envID string
	if err := testPool.QueryRow(ctx,
		`SELECT environment_id::text FROM configs WHERE id=$1::uuid`, configID).Scan(&envID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO promotion_requests
		   (project_id, source_config_id, source_version, target_env_id, requested_by, status)
		 VALUES ($1::uuid, $2::uuid, 2, $3::uuid, $4::uuid, 'pending')`,
		projectID, configID, envID, userID); err != nil {
		t.Fatal(err)
	}

	res, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PinnedVersions) != 1 || res.PinnedVersions[0] != 2 {
		t.Fatalf("pinned versions = %v, want [2]", res.PinnedVersions)
	}
	for _, v := range res.PrunedVersions {
		if v == 2 {
			t.Fatal("pruned the source version of a pending promotion request")
		}
	}
	// v2 survives alongside the latest and is still fully readable.
	assertRestorable(t, s, configID, want, []int{2, 6})
	assertNoDanglingEntries(t, configID)
}

// TestPruneRefusesSoftDeletedConfig asserts a config sitting in the trash is not
// pruned — it may still be restored, so its history must stay intact.
func TestPruneRefusesSoftDeletedConfig(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	seedVersions(t, s, configID, 4)

	if err := store.NewConfigRepo(testStore).SoftDelete(ctx, configID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1}); err == nil {
		t.Fatal("prune of a soft-deleted config succeeded; want an error")
	}
	if got := countRows(t, sqlCountVersions, configID); got != 4 {
		t.Fatalf("soft-deleted config lost versions: %d remain, want 4", got)
	}
}

// TestVersionRetentionOverrideCRUD covers the per-config override lifecycle and
// its validation.
func TestVersionRetentionOverrideCRUD(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	s.SetRetentionFloor(RetentionFloor{MinVersions: 4, MinDays: 7})

	pol, err := s.GetVersionRetention(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if pol.OverrideVersions != nil || pol.OverrideDays != nil {
		t.Fatalf("fresh config already has an override: %+v", pol)
	}
	if pol.EffectiveVersions != 4 || pol.EffectiveDays != 7 {
		t.Fatalf("effective floor = %d/%d, want the instance floor 4/7", pol.EffectiveVersions, pol.EffectiveDays)
	}

	twenty, one := 20, 1
	if err := s.SetVersionRetention(ctx, configID, &twenty, &one, ""); err != nil {
		t.Fatal(err)
	}
	pol, err = s.GetVersionRetention(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	// min_versions is raised by the override; min_days stays at the (stricter)
	// instance floor, because an override may never weaken it.
	if pol.EffectiveVersions != 20 || pol.EffectiveDays != 7 {
		t.Fatalf("effective floor = %d/%d, want 20/7", pol.EffectiveVersions, pol.EffectiveDays)
	}

	if err := s.ClearVersionRetention(ctx, configID); err != nil {
		t.Fatal(err)
	}
	pol, err = s.GetVersionRetention(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if pol.OverrideVersions != nil || pol.EffectiveVersions != 4 {
		t.Fatalf("clear did not fall back to the instance floor: %+v", pol)
	}

	// Validation.
	zero := 0
	for name, err := range map[string]error{
		"both nil":     s.SetVersionRetention(ctx, configID, nil, nil, ""),
		"zero version": s.SetVersionRetention(ctx, configID, &zero, nil, ""),
		"zero days":    s.SetVersionRetention(ctx, configID, nil, &zero, ""),
	} {
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: got %v, want ErrValidation", name, err)
		}
	}
}
