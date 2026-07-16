package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/promote"
	"github.com/steveokay/janus-secrets/internal/store"
)

// canApproveRequest reports whether the caller may act as an approver for a
// promotion request — i.e. holds secret:promote on the request's target. Used
// both to filter project-wide listings and to gate get/approve/reject.
func (s *Server) canApproveRequest(r *http.Request, req *store.PromotionRequest) bool {
	return s.can(r, authz.SecretPromote, authz.Resource{ProjectID: req.ProjectID, EnvID: req.TargetEnvID}) == nil
}

// promoReqView maps a store.PromotionRequest to its value-free JSON wire
// shape: key NAMES only (via Selections), never secret values.
func promoReqView(p *store.PromotionRequest) map[string]any {
	keys := make([]string, 0, len(p.Selections))
	sels := make([]map[string]any, 0, len(p.Selections))
	for _, sel := range p.Selections {
		keys = append(keys, sel.Key)
		sels = append(sels, map[string]any{"key": sel.Key, "action": sel.Action})
	}
	out := map[string]any{
		"id":               p.ID,
		"project_id":       p.ProjectID,
		"source_config_id": p.SourceConfigID,
		"source_version":   p.SourceVersion,
		"target_env_id":    p.TargetEnvID,
		"target_name":      p.TargetName,
		"create_target":    p.CreateTarget,
		"keys":             keys,
		"selections":       sels,
		"note":             p.Note,
		"status":           p.Status,
		"requested_by":     p.RequestedBy,
		"created_at":       p.CreatedAt,
	}
	if p.TargetConfigID != nil {
		out["target_config_id"] = *p.TargetConfigID
	}
	if p.DecidedBy != nil {
		out["decided_by"] = *p.DecidedBy
	}
	if p.AppliedTargetVersion != nil {
		out["applied_target_version"] = *p.AppliedTargetVersion
	}
	return out
}

// handlePromoteRequestCreate files a new promotion request. Request rights are
// scoped to the SOURCE config (authz.PromotionRequest), not the target — actual
// application requires an approver with secret:promote on the target.
func (s *Server) handlePromoteRequestCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string `json:"from_config"`
		To            string `json:"to_config"`
		ToEnv         string `json:"to_env"`
		ToName        string `json:"to_name"`
		Create        bool   `json:"create"`
		SourceVersion int    `json:"source_version"`
		Note          string `json:"note"`
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
	if body.To == "" && body.ToEnv == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "to_config or to_env is required")
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
	if !s.authorize(w, r, authz.PromotionRequest, srcRes, "promotion.request.create", "configs/"+body.From) {
		return
	}

	targetEnvID := body.ToEnv
	if body.To != "" {
		// The service derives the target env from TargetConfigID; no separate
		// resolution needed here.
		targetEnvID = ""
	} else {
		tgtRes, err := s.resolveScopeResource(r.Context(), "environment", body.ToEnv)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		targetEnvID = tgtRes.EnvID
	}

	sels := make([]promote.Selection, 0, len(body.Selections))
	for _, sel := range body.Selections {
		sels = append(sels, promote.Selection{Key: sel.Key, Action: promote.Action(sel.Action)})
	}
	id, err := s.promote.CreateRequest(r.Context(), promote.CreateRequestInput{
		SourceConfigID: body.From,
		TargetConfigID: body.To,
		TargetEnvID:    targetEnvID,
		TargetName:     body.ToName,
		CreateTarget:   body.Create,
		SourceVersion:  body.SourceVersion,
		Selections:     sels,
		Note:           body.Note,
		RequestedBy:    promoteActorUser(r),
	})
	if err != nil {
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "promotion.request.create", "promote/requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "pending"})
}

// handlePromoteRequestList lists promotion requests, either "mine" (as
// requester) or project-wide filtered to requests the caller may see (their
// own, or ones they could approve).
func (s *Server) handlePromoteRequestList(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "project is required")
		return
	}
	status := r.URL.Query().Get("status")
	mine := r.URL.Query().Get("mine") == "true"

	// Authorization is per-item, not a coarse project-level pre-gate: each
	// returned request is individually filtered to ones the caller may see
	// (their own, or ones they could approve via secret:promote on the target).
	// This mirrors handleTokenList / handleTrashList — an authenticated caller
	// with no visible requests simply gets an empty list, never a leak. Env-
	// scoped-only callers (the common case) are therefore not blanket-403'd the
	// way a bare {ProjectID}-only secret:read gate would (env bindings never
	// satisfy an EnvID-less resource; see internal/authz bindingApplies).
	var list []*store.PromotionRequest
	var err error
	if mine {
		list, err = s.promote.ListRequestsByRequester(r.Context(), promoteActorUser(r), status)
	} else {
		list, err = s.promote.ListRequestsByProject(r.Context(), projectID, status)
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}

	actor := promoteActorUser(r)
	views := make([]map[string]any, 0, len(list))
	for _, req := range list {
		if mine && req.ProjectID != projectID {
			continue
		}
		if !mine && actor != req.RequestedBy && !s.canApproveRequest(r, req) {
			continue
		}
		views = append(views, promoReqView(req))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": views})
}

// handlePromoteRequestGet fetches one request, visible to its requester or to
// anyone who could approve it (secret:promote on the target). When the target
// config already exists, includes a value-free diff (key names only).
func (s *Server) handlePromoteRequestGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.promote.GetRequest(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	actor := promoteActorUser(r)
	if actor != req.RequestedBy && !s.canApproveRequest(r, req) {
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return
	}

	view := promoReqView(req)
	if req.TargetConfigID != nil {
		diff, err := s.promote.Preview(r.Context(), req.SourceConfigID, *req.TargetConfigID, actor)
		if err != nil {
			s.writePromoteError(w, err)
			return
		}
		entries := make([]map[string]any, 0, len(diff.Entries))
		for _, e := range diff.Entries {
			entries = append(entries, map[string]any{
				"key":    e.Key,
				"status": string(e.Status),
				"locked": e.Locked,
			})
		}
		view["diff"] = map[string]any{
			"source_version": diff.SourceVersion,
			"target_exists":  diff.TargetExists,
			"entries":        entries,
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// handlePromoteRequestApprove applies a pending request. Four-eyes: the
// requester may not approve their own request.
func (s *Server) handlePromoteRequestApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.promote.GetRequest(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretPromote, authz.Resource{ProjectID: req.ProjectID, EnvID: req.TargetEnvID}, "promotion.request.approve", "promote/requests/"+id) {
		return
	}
	actor := promoteActorUser(r)
	if actor == req.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot approve your own request")
		return
	}

	res, err := s.promote.ApproveRequest(r.Context(), id, actor)
	if err != nil {
		if errors.Is(err, promote.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writePromoteError(w, err)
		return
	}
	appliedKeys := make([]string, 0, len(res.Applied))
	for _, a := range res.Applied {
		appliedKeys = append(appliedKeys, a.Key)
	}
	if err := s.record(r, "promotion.request.approve", "promote/requests/"+id, "success", "", strings.Join(appliedKeys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target_version": res.TargetVersion, "applied": appliedKeys, "skipped": res.Skipped,
	})
}

// handlePromoteRequestReject declines a pending request. Four-eyes: the
// requester may not reject their own request.
func (s *Server) handlePromoteRequestReject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	req, err := s.promote.GetRequest(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretPromote, authz.Resource{ProjectID: req.ProjectID, EnvID: req.TargetEnvID}, "promotion.request.reject", "promote/requests/"+id) {
		return
	}
	actor := promoteActorUser(r)
	if actor == req.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot reject your own request")
		return
	}

	if err := s.promote.RejectRequest(r.Context(), id, actor, body.Note); err != nil {
		if errors.Is(err, promote.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "promotion.request.reject", "promote/requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "rejected"})
}

// handlePromoteRequestCancel withdraws a pending request. Only the original
// requester may cancel.
func (s *Server) handlePromoteRequestCancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.promote.GetRequest(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	actor := promoteActorUser(r)
	if actor != req.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "only the requester can cancel")
		return
	}

	if err := s.promote.CancelRequest(r.Context(), id, actor); err != nil {
		if errors.Is(err, promote.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "promotion.request.cancel", "promote/requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled"})
}
