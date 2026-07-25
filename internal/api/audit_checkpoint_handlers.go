package api

import (
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

// handleAuditPrune hard-deletes the verified event prefix anchored by the latest
// checkpoint, respecting the ship high-water-mark guard. Owner-only. The prune
// action is audited (chained AFTER the delete, so it survives). A failed audit
// write after a successful delete is logged, not surfaced as a failure — the
// delete cannot be undone and the response must report what was removed.
func (s *Server) handleAuditPrune(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditManage, authz.Instance(), "audit.prune", "audit/prune") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	// Resolve the ship high-water mark: -1 when no shipper is configured (no
	// guard), else the durable last-shipped seq. A store failure here is
	// fail-closed (never prune on an unknown HWM).
	shipHWM := int64(-1)
	if s.auditShip != nil {
		hwm, err := store.NewAuditShipRepo(s.st).GetHighWater(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		shipHWM = hwm
	}
	res, err := s.audit.Prune(r.Context(), shipHWM)
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
		writeError(w, http.StatusConflict, "conflict", "prune blocked: events not yet shipped to the audit sink")
	case errors.Is(err, store.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", "a checkpoint already anchors the current chain head")
	default:
		s.writeServiceError(w, err)
	}
}
