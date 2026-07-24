package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/promote"
	"github.com/steveokay/janus-secrets/internal/store"
)

// promoteActorUser returns the acting user's id for the config_locked_keys
// created_by FK, or "" for a service token (whose id is not a users.id and
// must not be written to the FK; the store maps "" -> NULL).
func promoteActorUser(r *http.Request) string {
	p, _ := PrincipalFrom(r.Context())
	if p.Kind == auth.KindUser {
		return p.ID
	}
	return ""
}

func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	steps, err := store.NewPipelineRepo(s.st).Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.EnvironmentID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": ids})
}

func (s *Server) handlePipelinePut(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.PromotionManage, authz.Resource{ProjectID: pid}, "promotion.pipeline.set", "projects/"+pid+"/pipeline") {
		return
	}
	var body struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := store.NewPipelineRepo(s.st).Set(r.Context(), pid, body.EnvironmentIDs); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.pipeline.set", "projects/"+pid+"/pipeline", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": body.EnvironmentIDs})
}

func (s *Server) handleLockedKeysList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	keys, err := store.NewLockedKeyRepo(s.st).List(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleLockedKeyCreate(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.lock", "configs/"+cid+"/locked-keys") {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "key is required")
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Lock(r.Context(), cid, body.Key, promoteActorUser(r)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.lock", "configs/"+cid+"/locked-keys/"+body.Key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key, "locked": true})
}

func (s *Server) handleLockedKeyDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key) {
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Unlock(r.Context(), cid, key); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "locked": false})
}

// configResourceByID resolves a config's scope chain from an explicit id.
func (s *Server) configResourceByID(ctx context.Context, cid string) (authz.Resource, error) {
	return s.resolveScopeResource(ctx, "config", cid)
}

// promoteDiffView maps a promote.Diff to its JSON wire shape.
func promoteDiffView(d promote.Diff) map[string]any {
	entries := make([]map[string]any, 0, len(d.Entries))
	for _, e := range d.Entries {
		entries = append(entries, map[string]any{
			"key":          e.Key,
			"status":       string(e.Status),
			"source_value": e.SourceValue,
			"target_value": e.TargetValue,
			"locked":       e.Locked,
		})
	}
	return map[string]any{
		"source_version": d.SourceVersion,
		"target_exists":  d.TargetExists,
		"entries":        entries,
	}
}

// writePromoteError maps promote sentinels to HTTP; else delegates.
func (s *Server) writePromoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promote.ErrIllegalStep), errors.Is(err, promote.ErrNoPipeline):
		writeError(w, http.StatusConflict, "pipeline_step_not_allowed", "not the next pipeline step")
	case errors.Is(err, promote.ErrLockedKey):
		writeError(w, http.StatusConflict, "key_locked", "a selected key is locked on the target")
	default:
		s.writeServiceError(w, err)
	}
}

func (s *Server) handlePromotePreview(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	toEnv := r.URL.Query().Get("to_env")
	if from == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from config id is required")
		return
	}
	if to == "" && toEnv == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "to or to_env is required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), from)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	// Preview reveals the source → secret:read on source, audited.
	if !s.authorize(w, r, authz.SecretRead, srcRes, "secret.reveal", "configs/"+from+"/secrets") {
		return
	}

	// Create-mode preview: target env has no config yet. Every source key is an
	// "add". Gate on config:create for the target env (only someone who could
	// create the target may preview-create it). Reveals the SOURCE only.
	if to == "" {
		dstEnvRes, err := s.resolveScopeResource(r.Context(), "environment", toEnv)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.can(r, authz.ConfigCreate, dstEnvRes); err != nil {
			s.writeAuthzError(w, err)
			return
		}
		diff, err := s.promote.PreviewCreate(r.Context(), from, toEnv, promoteActorUser(r))
		if err != nil {
			s.writePromoteError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+from+"/secrets", "success", "", "promote-preview-create"); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, promoteDiffView(diff))
		return
	}

	// Existing-target preview: reveals both sides → secret:read on both, audited.
	dstRes, err := s.configResourceByID(r.Context(), to)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, dstRes); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	diff, err := s.promote.Preview(r.Context(), from, to, promoteActorUser(r))
	if err != nil {
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "secret.reveal", "configs/"+from+"/secrets", "success", "", "promote-preview"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "secret.reveal", "configs/"+to+"/secrets", "success", "", "promote-preview"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, promoteDiffView(diff))
}

func (s *Server) handlePromoteApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string `json:"from_config"`
		To            string `json:"to_config"`
		ToEnv         string `json:"to_env"`
		ToName        string `json:"to_name"`
		Create        bool   `json:"create"`
		SourceVersion int    `json:"source_version"`
		Selections    []struct {
			Key    string `json:"key"`
			Action string `json:"action"`
		} `json:"selections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if body.From == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from_config is required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), body.From)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, srcRes); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	// Target authz: promote on the target. If creating, resolve by env + require config:create.
	var dstRes authz.Resource
	if body.To != "" {
		if dstRes, err = s.configResourceByID(r.Context(), body.To); err != nil {
			s.writeServiceError(w, err)
			return
		}
	} else {
		if body.ToEnv == "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "to_env is required when creating the target")
			return
		}
		if dstRes, err = s.resolveScopeResource(r.Context(), "environment", body.ToEnv); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.can(r, authz.ConfigCreate, dstRes); err != nil {
			s.writeAuthzError(w, err)
			return
		}
	}
	if !s.authorize(w, r, authz.SecretPromote, dstRes, "secret.promote", "configs/"+body.To) {
		return
	}

	// Idempotency is handled upstream by the generic Idempotency-Key middleware;
	// this handler always performs the apply. The response is value-free (target
	// version + applied key NAMES only) — never a secret value.
	sels := make([]promote.Selection, 0, len(body.Selections))
	for _, sel := range body.Selections {
		sels = append(sels, promote.Selection{Key: sel.Key, Action: promote.Action(sel.Action)})
	}

	// A promotion into an EXISTING PROTECTED target (require_approval) is still a
	// direct write to that config's secrets, so it must go through the four-eyes
	// edit-request flow rather than committing unilaterally. Compute the promotion
	// changeset (same pipeline/locked/drift rules as Apply) and file it as a
	// pending edit request instead of applying. (A create-target promotion cannot
	// be protected — the target does not exist yet — so this only applies when
	// body.To is set.)
	if body.To != "" {
		protected, handled := s.requireApproval(w, r, body.To)
		if handled {
			return
		}
		if protected {
			plan, err := s.promote.Plan(r.Context(), promote.ApplyRequest{
				SourceConfigID: body.From, TargetConfigID: body.To,
				SourceVersion: body.SourceVersion, Selections: sels, Actor: promoteActorUser(r),
			})
			if err != nil {
				s.writePromoteError(w, err)
				return
			}
			if len(plan.Changes) == 0 {
				// Nothing to propose (every selected key drifted away). Mirror
				// Apply's value-free empty result rather than filing an empty request.
				writeJSON(w, http.StatusOK, map[string]any{
					"target_version": 0, "applied": []string{}, "skipped": plan.Skipped,
				})
				return
			}
			s.submitEditRequest(w, r, body.To, plan.Changes, plan.Message)
			return
		}
	}

	res, err := s.promote.Apply(r.Context(), promote.ApplyRequest{
		SourceConfigID: body.From, TargetConfigID: body.To, TargetEnvID: body.ToEnv, TargetName: body.ToName,
		CreateTarget: body.Create, SourceVersion: body.SourceVersion, Selections: sels, Actor: promoteActorUser(r),
	})
	if err != nil {
		s.writePromoteError(w, err)
		return
	}
	appliedKeys := make([]string, 0, len(res.Applied))
	for _, a := range res.Applied {
		appliedKeys = append(appliedKeys, a.Key)
	}
	if err := s.record(r, "secret.promote", "configs/"+body.To, "success", "", strings.Join(appliedKeys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target_version": res.TargetVersion, "applied": appliedKeys, "skipped": res.Skipped,
	})
}
