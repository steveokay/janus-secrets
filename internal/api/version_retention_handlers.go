package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Secret value-version retention (roadmap 8.2).
//
// Every save writes an immutable secret_values row; nothing has ever removed
// one. These handlers expose the explicit, audited pruning policy:
//
//	GET    /v1/configs/{cid}/versions/retention   read the resolved policy
//	PUT    /v1/configs/{cid}/versions/retention   set/clear the config override
//	POST   /v1/configs/{cid}/versions/prune       prune (dry-run by default)
//
// PRUNING GRANULARITY. Pruning removes whole CONFIG VERSIONS — the unit of diff
// and rollback — and only then garbage-collects secret_values rows nothing
// references. It never removes a value version directly: migration 000005 makes
// config_version_entries cascade on a secret_values delete, so a value-level
// prune would silently strip keys out of old config versions and a rollback to
// one of them would restore an incomplete config. See
// store.SecretRepo.PruneConfigVersions.
//
// Everything below is VALUE-FREE: requests, responses, audit details and logs
// carry config ids, version numbers and counts only — never a secret value, a
// DEK, a nonce or a ciphertext.

// handleVersionRetentionGet returns the resolved retention policy for a config:
// the instance-wide floor, the config's override, and the effective floor. A
// plain read gated on secret:read; not audited (no secret is revealed).
func (s *Server) handleVersionRetentionGet(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	pol, err := s.service.GetVersionRetention(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, retentionView(pol))
}

// retentionView is the value-free wire shape of a resolved retention policy.
func retentionView(p secrets.RetentionPolicy) map[string]any {
	return map[string]any{
		"instance_min_versions":  p.Floor.MinVersions,
		"instance_min_days":      p.Floor.MinDays,
		"config_min_versions":    p.OverrideVersions,
		"config_min_days":        p.OverrideDays,
		"effective_min_versions": p.EffectiveVersions,
		"effective_min_days":     p.EffectiveDays,
	}
}

// handleVersionRetentionPut sets or clears the config's retention override.
//
// Body: {"min_versions": 25, "min_days": 90} — either field may be null. A body
// with both fields null CLEARS the override (falling back to the instance
// floor). Because an override can only ever retain MORE than the instance floor,
// setting one is safe; clearing one can relax retention, so both directions are
// gated on the same owner-only secret:prune permission that guards the prune
// itself, and both are audited.
func (s *Server) handleVersionRetentionPut(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	resource := "configs/" + cid + "/versions/retention"
	if !s.authorize(w, r, authz.SecretPrune, res, "secret.retention.set", resource) {
		return
	}
	var body struct {
		MinVersions *int `json:"min_versions"`
		MinDays     *int `json:"min_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}

	if body.MinVersions == nil && body.MinDays == nil {
		if err := s.service.ClearVersionRetention(r.Context(), cid); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if aerr := s.record(r, "secret.retention.clear", resource, "success", "", ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
	} else {
		if err := s.service.SetVersionRetention(r.Context(), cid, body.MinVersions, body.MinDays, promoteActorUser(r)); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if aerr := s.record(r, "secret.retention.set", resource, "success", "",
			"min_versions="+optInt(body.MinVersions)+",min_days="+optInt(body.MinDays)); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
	}

	pol, err := s.service.GetVersionRetention(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, retentionView(pol))
}

// optInt renders an optional int for a value-free audit detail ("none" for nil).
func optInt(v *int) string {
	if v == nil {
		return "none"
	}
	return strconv.Itoa(*v)
}

// handleVersionPrune destroys old config versions of a config and
// garbage-collects the secret_values rows nothing references any more.
//
// Owner-only (secret:prune). DESTRUCTIVE and irreversible, so it defaults to a
// dry run: a caller must pass "dry_run": false to actually delete. The response
// is value-free — version numbers and counts only.
//
// Audit is FAIL-CLOSED on the dry run (nothing was destroyed, so a failed audit
// write can safely 500) and best-effort after a real prune, mirroring
// handleAuditPrune: the delete cannot be undone, so the response must still
// report what was removed while the audit failure is logged loudly.
func (s *Server) handleVersionPrune(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	resource := "configs/" + cid + "/versions/prune"
	if !s.authorize(w, r, authz.SecretPrune, res, "secret.version.prune", resource) {
		return
	}
	// dry_run defaults to TRUE when omitted: an operator who forgets the field
	// gets a preview, never a destroy.
	body := struct {
		KeepVersions int   `json:"keep_versions"`
		KeepDays     int   `json:"keep_days"`
		DryRun       *bool `json:"dry_run"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	dryRun := true
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}

	result, err := s.service.PruneVersions(r.Context(), cid, secrets.PruneRequest{
		KeepVersions: body.KeepVersions,
		KeepDays:     body.KeepDays,
		DryRun:       dryRun,
	})
	if err != nil {
		if errors.Is(err, store.ErrPruneBlocked) {
			writeError(w, http.StatusConflict, "conflict",
				"prune blocked: this config has a pending or in-flight edit request; resolve it first")
			return
		}
		s.writeServiceError(w, err)
		return
	}

	detail := "dry_run=" + strconv.FormatBool(dryRun) +
		",keep_versions=" + strconv.Itoa(result.KeepVersions) +
		",keep_days=" + strconv.Itoa(result.KeepDays) +
		",versions_deleted=" + strconv.Itoa(result.DeletedVersions) +
		",values_deleted=" + strconv.Itoa(result.DeletedValues) +
		",versions_retained=" + strconv.Itoa(result.RetainedVersions)
	action := "secret.version.prune"
	if dryRun {
		action = "secret.version.prune.preview"
	}
	if aerr := s.record(r, action, resource, "success", "", detail); aerr != nil {
		if dryRun {
			// Nothing was destroyed — fail closed.
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		s.logger.Error("audit write failed after secret version prune completed; delete already applied",
			"config_id", cid, "err", aerr)
	}
	writeJSON(w, http.StatusOK, pruneView(result))
}

// pruneView is the value-free wire shape of a prune result.
func pruneView(p store.PruneResult) map[string]any {
	pruned := p.PrunedVersions
	if pruned == nil {
		pruned = []int{}
	}
	pinned := p.PinnedVersions
	if pinned == nil {
		pinned = []int{}
	}
	return map[string]any{
		"dry_run":           p.DryRun,
		"latest_version":    p.LatestVersion,
		"keep_versions":     p.KeepVersions,
		"keep_days":         p.KeepDays,
		"pruned_versions":   pruned,
		"pinned_versions":   pinned,
		"versions_deleted":  p.DeletedVersions,
		"values_deleted":    p.DeletedValues,
		"versions_retained": p.RetainedVersions,
	}
}
