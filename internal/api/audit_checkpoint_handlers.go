package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// handleAuditCheckpointGet returns the latest signed checkpoint (or null). Gated
// on audit:manage (owner-only). A plain read; not self-audited, mirroring the
// sibling audit read endpoints.
func (s *Server) handleAuditCheckpointGet(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.AuditManage, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	cp, err := s.audit.LatestCheckpoint(r.Context())
	if err != nil {
		s.writeCheckpointErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoint": cp})
}

// handleAuditCheckpointCreate captures the current chain head and stores a signed
// checkpoint. Owner-only (audit:manage). The action is self-audited (chained
// after the anchor it captures, so the checkpoint event itself is covered by the
// next checkpoint). A failed audit write 500s before returning success.
func (s *Server) handleAuditCheckpointCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditManage, authz.Instance(), "audit.checkpoint.create", "audit/checkpoint") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	info, err := s.audit.CreateCheckpoint(r.Context())
	if err != nil {
		s.writeCheckpointErr(w, err)
		return
	}
	if aerr := s.record(r, "audit.checkpoint.create", "audit/checkpoint", "success", "",
		"through_seq="+itoa64(info.ThroughSeq)); aerr != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoint": info})
}

// pruneCeilingUnbounded is the sentinel the audit engine reads as "no guard at
// all": any negative ceiling lets Prune remove the whole checkpointed prefix.
const pruneCeilingUnbounded = int64(-1)

// combinePruneCeilings folds independent ceilings on the audit-prune point into
// the single value audit.Prune accepts.
//
// Each input is either pruneCeilingUnbounded (-1 — "this constraint imposes no
// ceiling") or a non-negative seq ("do not prune past this seq"). Because every
// constraint must hold simultaneously, the combined ceiling is the SMALLEST
// active one; when no constraint is active the result is pruneCeilingUnbounded.
//
// Folding here, rather than teaching the audit engine about each policy, keeps
// the engine's Prune signature at one ceiling parameter and keeps retention
// policy in the API layer where the configuration lives.
func combinePruneCeilings(ceilings ...int64) int64 {
	out := pruneCeilingUnbounded
	for _, c := range ceilings {
		if c < 0 {
			continue // this constraint is inactive
		}
		if out < 0 || c < out {
			out = c
		}
	}
	return out
}

// shipPruneCeiling resolves the durable audit-ship high-water mark into a prune
// ceiling.
//
// The guard is bound to SHIPPING HISTORY, not to live wiring. An instance that
// shipped audit events for months and then had its JANUS_AUDIT_SHIP_*
// configuration removed must keep the "never prune un-shipped events"
// protection on its next boot, so the persisted mark is consulted whether or
// not s.auditShip happens to be wired in this process.
//
// Migration 000034 always creates the single audit_ship_state row (seeded to the
// audit head at migration time — 0 on a fresh install), so the row's mere
// existence cannot distinguish "never shipped" from "shipped". A mark of 0 is
// therefore the only "no shipping has ever happened" state, and it imposes no
// ceiling; any positive mark does. The shipper only ever advances the mark
// forward (AdvanceHighWater is monotonic), so any deployment that has shipped
// anything carries a positive mark forever.
//
// Fail-closed: an unreadable mark aborts the prune rather than pruning
// unguarded. An absent row (ErrNotFound — the mark was never written at all) is
// the documented no-guard case.
func (s *Server) shipPruneCeiling(ctx context.Context) (int64, error) {
	if s.st == nil {
		return pruneCeilingUnbounded, nil
	}
	hwm, err := store.NewAuditShipRepo(s.st).GetHighWater(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return pruneCeilingUnbounded, nil
	}
	if err != nil {
		return 0, err
	}
	if hwm <= 0 {
		return pruneCeilingUnbounded, nil
	}
	return hwm, nil
}

// retentionPruneCeiling resolves the optional minimum-retention floor
// (JANUS_AUDIT_RETAIN_MIN_DAYS / JANUS_AUDIT_RETAIN_MIN_EVENTS) into a prune
// ceiling.
//
// Both settings default to off (zero), preserving the historical behavior where
// an owner may prune the entire checkpointed prefix. When both are set the
// stricter (smaller) ceiling wins, so the floor retains at least N days AND at
// least N events. A returned 0 means the floor retains everything.
//
// Fail-closed: a store failure aborts the prune rather than pruning unguarded.
func (s *Server) retentionPruneCeiling(ctx context.Context) (int64, error) {
	days, events := s.cfg.AuditRetainMinDays, s.cfg.AuditRetainMinEvents
	if (days <= 0 && events <= 0) || s.st == nil {
		return pruneCeilingUnbounded, nil
	}
	repo := store.NewAuditRetentionRepo(s.st)
	byDays, byEvents := pruneCeilingUnbounded, pruneCeilingUnbounded
	if days > 0 {
		v, err := repo.SeqOlderThanDays(ctx, days)
		if err != nil {
			return 0, err
		}
		byDays = v
	}
	if events > 0 {
		v, err := repo.SeqRetainingNewest(ctx, events)
		if err != nil {
			return 0, err
		}
		byEvents = v
	}
	return combinePruneCeilings(byDays, byEvents), nil
}

// handleAuditPrune hard-deletes the verified event prefix anchored by the latest
// checkpoint, respecting the ship high-water-mark guard and the optional
// minimum-retention floor. Owner-only. The prune action is audited (chained
// AFTER the delete, so it survives). A failed audit write after a successful
// delete is logged, not surfaced as a failure — the delete cannot be undone and
// the response must report what was removed.
func (s *Server) handleAuditPrune(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditManage, authz.Instance(), "audit.prune", "audit/prune") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	// Two independent ceilings on the prune point — the durable ship high-water
	// mark and the operator's minimum-retention floor — are combined here into
	// the single ceiling audit.Prune accepts. Both resolvers are fail-closed: a
	// store failure 500s and prunes nothing.
	shipCeiling, err := s.shipPruneCeiling(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	retentionCeiling, err := s.retentionPruneCeiling(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	// The engine reports every blocked prune as ErrPrunePastShipHWM, whose
	// message names only the audit sink. Pre-check the retention floor here so a
	// floor-blocked prune returns a message that actually names the floor.
	if retentionCeiling == 0 {
		writeError(w, http.StatusConflict, "conflict",
			"prune blocked: the minimum audit-retention floor retains every event")
		return
	}
	res, err := s.audit.Prune(r.Context(), combinePruneCeilings(shipCeiling, retentionCeiling))
	if err != nil {
		s.writeCheckpointErr(w, err)
		return
	}
	// Audit the prune AFTER the delete so the event chains onto the surviving log.
	if aerr := s.record(r, "audit.prune", "audit/prune", "success", "",
		"pruned_through="+itoa64(res.PrunedThrough)+",deleted="+itoa64(res.Deleted)); aerr != nil {
		s.logger.Error("audit write failed after prune completed; delete already applied", "err", aerr)
	}
	writeJSON(w, http.StatusOK, res)
}

// writeCheckpointErr maps the audit-checkpoint sentinels to the HTTP envelope.
func (s *Server) writeCheckpointErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crypto.ErrSealed):
		s.writeServiceError(w, err) // 503 while sealed (MAC key unavailable)
	case errors.Is(err, audit.ErrCheckpointsDisabled):
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "checkpointing not configured")
	case errors.Is(err, audit.ErrNoEvents):
		writeError(w, http.StatusBadRequest, CodeValidation, "no audit events to checkpoint")
	case errors.Is(err, audit.ErrNoCheckpoint):
		writeError(w, http.StatusBadRequest, CodeValidation, "no checkpoint exists; create one before pruning")
	case errors.Is(err, audit.ErrCheckpointMAC):
		writeError(w, http.StatusConflict, "conflict", "checkpoint integrity check failed; refusing to prune")
	case errors.Is(err, audit.ErrChainInvalid):
		writeError(w, http.StatusConflict, "conflict", "audit chain does not verify; refusing to checkpoint")
	case errors.Is(err, audit.ErrPrunePastShipHWM):
		// Name the mark explicitly: on an instance that never shipped but was
		// seeded by migration 000034, "not yet shipped" alone reads as wrong.
		// See docs/operations.md → "Seeded high-water mark".
		writeError(w, http.StatusConflict, "conflict",
			"prune blocked by the audit-ship high-water mark: events at or below it are not yet shipped to the audit sink")
	case errors.Is(err, store.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", "a checkpoint already anchors the current chain head")
	default:
		s.writeServiceError(w, err)
	}
}
