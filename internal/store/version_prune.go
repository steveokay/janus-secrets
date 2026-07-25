package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrPruneBlocked is returned when a config cannot be pruned right now because
// an in-flight approval request depends on its history. It is a temporary,
// caller-fixable state (resolve or cancel the request, then retry), distinct
// from ErrConflict (absent/soft-deleted config).
var ErrPruneBlocked = errors.New("store: prune blocked by an in-flight request")

// errPruneDryRun is an internal signal that unwinds the prune transaction after
// the deletes have been staged and measured. It is never returned to callers.
var errPruneDryRun = errors.New("store: prune dry run")

// PrunePlan is the already-resolved retention decision for one config. The
// service layer computes it from the strictest of {prune request, instance-wide
// floor, per-config override}; the store applies it verbatim.
type PrunePlan struct {
	// KeepVersions is the number of newest config versions to retain. It is
	// clamped to at least 1 here, so the LATEST config version can never be
	// pruned regardless of what the caller asks for.
	KeepVersions int
	// KeepDays retains every config version created within the last N days.
	// 0 disables the age-based rule (the count-based rule still applies).
	KeepDays int
	// DryRun stages and measures the prune, then rolls the transaction back. No
	// row is removed and the returned counts describe what WOULD be removed.
	DryRun bool
}

// PruneResult reports what a prune removed (or, for a dry run, would remove).
// Value-free by construction: version numbers and counts only — never a key
// name's value, a DEK, a nonce, or a ciphertext.
type PruneResult struct {
	ConfigID string
	// LatestVersion is the config's newest version at prune time. Always retained.
	LatestVersion int
	// KeepVersions / KeepDays are the EFFECTIVE floor actually applied.
	KeepVersions int
	KeepDays     int
	// PrunedVersions lists the config version numbers removed, ascending.
	PrunedVersions []int
	// PinnedVersions lists config versions that the count/age rules would have
	// pruned but that an in-flight promotion request still depends on.
	PinnedVersions []int
	// DeletedVersions / DeletedValues are the row counts removed from
	// config_versions and secret_values respectively.
	DeletedVersions int
	DeletedValues   int
	// RetainedVersions is the number of config versions surviving the prune.
	RetainedVersions int
	DryRun           bool
}

// PruneConfigVersions removes old config VERSIONS of one config and then
// garbage-collects the secret_values rows no surviving manifest entry
// references.
//
// # Why the granularity is a config version, not a value version
//
// Migration 000005 declared
//
//	config_version_entries.secret_value_id -> secret_values (id) ON DELETE CASCADE
//
// so deleting a secret_values row SILENTLY deletes the manifest entries that
// point at it. A "delete value versions older than N days" implementation would
// therefore strip keys out of old config versions with no error anywhere, and a
// rollback to one of them would restore an incomplete config. Pruning is instead
// done at the config-version granularity — the unit of diff and rollback — and
// secret_values are only ever removed once nothing references them.
//
// # Invariant
//
//	Every RETAINED config version is fully restorable.
//
// That is, after a prune, every non-tombstone config_version_entries row of
// every surviving config version still resolves to a live secret_values row. The
// transaction re-asserts it directly: the number of manifest entries belonging
// to the versions that are meant to survive is counted BEFORE the deletes and
// again AFTER them, and a mismatch (which is exactly what the secret_values
// cascade eating manifest rows would look like) aborts and rolls back.
//
// # Guards (fail-closed)
//
//   - The latest config version is never pruned (KeepVersions is clamped to >= 1).
//   - A soft-deleted or absent config is refused (ErrConflict) — it may still be
//     restored from the trash.
//   - A config with a pending or in-flight ('applying') edit request is refused
//     entirely (ErrPruneBlocked). Such a request carries an envelope-encrypted
//     proposal that will be committed against this config, and its DEK is wrapped
//     under a project-KEK version whose retirement is itself gated on the request;
//     rather than reason about which historical versions that may implicate, the
//     whole config is left alone until the request is resolved.
//   - A config version named as the SOURCE of a pending promotion request is
//     retained even when the count/age rules would drop it: approving that
//     request reads that exact version's state.
//
// The config row is locked FOR UPDATE with the same predicate
// SaveConfigVersion / Rollback use, so a concurrent save or rollback cannot
// interleave. That matters because Rollback REUSES existing secret_values ids:
// without the lock a rollback could resurrect a reference to a row this prune
// had already decided was unreferenced.
func (r *SecretRepo) PruneConfigVersions(ctx context.Context, configID string, plan PrunePlan) (PruneResult, error) {
	if plan.KeepVersions < 1 {
		plan.KeepVersions = 1 // the latest version is never prunable
	}
	if plan.KeepDays < 0 {
		plan.KeepDays = 0
	}
	res := PruneResult{
		ConfigID:       configID,
		KeepVersions:   plan.KeepVersions,
		KeepDays:       plan.KeepDays,
		PrunedVersions: []int{},
		PinnedVersions: []int{},
		DryRun:         plan.DryRun,
	}

	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		// Serialize against SaveConfigVersion / Rollback and confirm the config
		// is live. Same predicate as the write paths so the lock actually
		// excludes them.
		var live bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM configs WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, configID).
			Scan(&live); err != nil {
			if errors.Is(mapError(err), ErrNotFound) {
				return ErrConflict
			}
			return mapError(err)
		}

		// Guard: any pending/in-flight edit request freezes the whole config.
		var pendingEdits int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM config_edit_requests
			  WHERE config_id = $1::uuid AND status IN ('pending', 'applying')`, configID).
			Scan(&pendingEdits); err != nil {
			return mapError(err)
		}
		if pendingEdits > 0 {
			return ErrPruneBlocked
		}

		// Guard: versions a pending promotion request reads as its source.
		pinned := map[int]bool{}
		pinRows, err := tx.Query(ctx,
			`SELECT DISTINCT source_version FROM promotion_requests
			  WHERE source_config_id = $1::uuid AND status = 'pending'`, configID)
		if err != nil {
			return mapError(err)
		}
		for pinRows.Next() {
			var v int
			if err := pinRows.Scan(&v); err != nil {
				pinRows.Close()
				return mapError(err)
			}
			pinned[v] = true
		}
		pinRows.Close()
		if err := pinRows.Err(); err != nil {
			return mapError(err)
		}

		// Every version of this config, newest first.
		type versionRow struct {
			id        string
			version   int
			createdAt time.Time
		}
		rows, err := tx.Query(ctx,
			`SELECT id::text, version, created_at FROM config_versions
			  WHERE config_id = $1::uuid ORDER BY version DESC`, configID)
		if err != nil {
			return mapError(err)
		}
		var all []versionRow
		for rows.Next() {
			var vr versionRow
			if err := rows.Scan(&vr.id, &vr.version, &vr.createdAt); err != nil {
				rows.Close()
				return mapError(err)
			}
			all = append(all, vr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return mapError(err)
		}
		if len(all) == 0 {
			return ErrNotFound // a config with no versions has no history to prune
		}
		res.LatestVersion = all[0].version

		// Partition. The rules are additive RETENTION rules: a version survives
		// if ANY of them keeps it, and is pruned only when none does.
		//
		//  1. index < KeepVersions      → among the newest N (index 0 = latest)
		//  2. created_at >= age cutoff  → younger than the age floor
		//  3. pinned                    → an in-flight promotion reads it
		//
		// The cutoff is computed from the database clock so it cannot drift with
		// the application host.
		var cutoff time.Time
		if plan.KeepDays > 0 {
			if err := tx.QueryRow(ctx,
				`SELECT now() - make_interval(days => $1::int)`, plan.KeepDays).Scan(&cutoff); err != nil {
				return mapError(err)
			}
		}
		var prunableIDs []string
		var survivingIDs []string
		for i, vr := range all {
			switch {
			case i < plan.KeepVersions:
			case plan.KeepDays > 0 && !vr.createdAt.Before(cutoff):
			case pinned[vr.version]:
				res.PinnedVersions = append(res.PinnedVersions, vr.version)
			default:
				prunableIDs = append(prunableIDs, vr.id)
				res.PrunedVersions = append(res.PrunedVersions, vr.version)
				continue
			}
			survivingIDs = append(survivingIDs, vr.id)
		}
		// Report ascending; `all` is newest-first.
		reverseInts(res.PrunedVersions)
		reverseInts(res.PinnedVersions)
		res.RetainedVersions = len(survivingIDs)

		if len(prunableIDs) == 0 {
			if plan.DryRun {
				return errPruneDryRun
			}
			return nil
		}

		// Restorability invariant, part 1: how many live manifest entries do the
		// versions that must SURVIVE hold right now?
		var liveEntriesBefore int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM config_version_entries
			  WHERE config_version_id = ANY($1::uuid[]) AND NOT tombstone`, survivingIDs).
			Scan(&liveEntriesBefore); err != nil {
			return mapError(err)
		}

		// Delete the pruned config versions. Their config_version_entries go with
		// them via the config_version_id cascade (migration 000005).
		tag, err := tx.Exec(ctx,
			`DELETE FROM config_versions WHERE id = ANY($1::uuid[])`, prunableIDs)
		if err != nil {
			return mapError(err)
		}
		res.DeletedVersions = int(tag.RowsAffected())

		// Garbage-collect secret_values of this config that no surviving manifest
		// entry references. Scoped to config_id so a bug here can never reach
		// another config's history.
		tag, err = tx.Exec(ctx,
			`DELETE FROM secret_values sv
			  WHERE sv.config_id = $1::uuid
			    AND NOT EXISTS (
			      SELECT 1 FROM config_version_entries e WHERE e.secret_value_id = sv.id)`,
			configID)
		if err != nil {
			return mapError(err)
		}
		res.DeletedValues = int(tag.RowsAffected())

		// Restorability invariant, part 2: the surviving versions must still hold
		// exactly the manifest entries they held before. A shortfall is precisely
		// the failure mode the secret_values ON DELETE CASCADE would produce, and
		// it aborts the whole prune.
		var liveEntriesAfter int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM config_version_entries
			  WHERE config_version_id = ANY($1::uuid[]) AND NOT tombstone`, survivingIDs).
			Scan(&liveEntriesAfter); err != nil {
			return mapError(err)
		}
		if liveEntriesAfter != liveEntriesBefore {
			return errors.New("store: prune would orphan manifest entries; aborted")
		}

		if plan.DryRun {
			return errPruneDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errPruneDryRun) {
		return PruneResult{}, err
	}
	return res, nil
}

// reverseInts reverses s in place.
func reverseInts(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
