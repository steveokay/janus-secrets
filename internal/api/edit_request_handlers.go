package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/editreq"
	"github.com/steveokay/janus-secrets/internal/store"
)

// handleRequireApprovalSet toggles a config's protected (four-eyes) flag. Admin+
// scope, reusing the promotion:manage permission that already guards config
// promotion settings (pipeline, locked keys). Value-free.
func (s *Server) handleRequireApprovalSet(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.PromotionManage, res, "config.require_approval.set", "configs/"+cid+"/require-approval") {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := store.NewConfigRepo(s.st).SetRequireApproval(r.Context(), cid, body.Enabled); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "config.require_approval.set", "configs/"+cid+"/require-approval", "success", "", "enabled="+boolStr(body.Enabled)); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"require_approval": body.Enabled})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// editReqActor returns the acting user's id, or "" for a service token (which
// has no users.id and cannot be a requester/approver of an edit request).
func editReqActor(r *http.Request) string { return promoteActorUser(r) }

// editReqView maps a store.ConfigEditRequest to its value-free JSON wire shape:
// changed key NAMES only, never proposed secret values or ciphertext.
func editReqView(er *store.ConfigEditRequest) map[string]any {
	out := map[string]any{
		"id":           er.ID,
		"config_id":    er.ConfigID,
		"requested_by": er.RequestedBy,
		"reason":       er.Reason,
		"status":       er.Status,
		"keys":         er.ChangedKeys,
		"message":      er.Message,
		"created_at":   er.CreatedAt,
	}
	if er.ResolvedBy != nil {
		out["resolved_by"] = *er.ResolvedBy
	}
	if er.ResolvedAt != nil {
		out["resolved_at"] = *er.ResolvedAt
	}
	if er.AppliedVersion != nil {
		out["applied_version"] = *er.AppliedVersion
	}
	return out
}

// handleEditRequestList lists a config's edit requests. Visible to anyone who
// can read the config; value-free (key names only).
func (s *Server) handleEditRequestList(w http.ResponseWriter, r *http.Request) {
	if s.editreq == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "approval workflow unavailable")
		return
	}
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	status := r.URL.Query().Get("status")
	list, err := s.editreq.ListByConfig(r.Context(), cid, status)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	views := make([]map[string]any, 0, len(list))
	for _, er := range list {
		views = append(views, editReqView(er))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": views})
}

// canApproveEdit reports whether the caller may approve edits to the request's
// config — i.e. holds secret:write there. Used both for the get gate and the
// approve/reject actions.
func (s *Server) canApproveEdit(r *http.Request, er *store.ConfigEditRequest) bool {
	res, err := s.configResourceByID(r.Context(), er.ConfigID)
	if err != nil {
		return false
	}
	return s.can(r, authz.SecretWrite, res) == nil
}

// handleEditRequestApprove applies a pending edit request. Four-eyes: the
// approver must be a DIFFERENT user than the requester and must hold
// secret:write on the config.
func (s *Server) handleEditRequestApprove(w http.ResponseWriter, r *http.Request) {
	if s.editreq == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "approval workflow unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	er, err := s.editreq.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	res, err := s.configResourceByID(r.Context(), er.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.edit_request.approve", "configs/"+er.ConfigID+"/edit-requests/"+id) {
		return
	}
	actor := editReqActor(r)
	if actor == "" || actor == er.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot approve your own request")
		return
	}
	out, err := s.editreq.Approve(r.Context(), id, actor)
	if err != nil {
		if errors.Is(err, editreq.ErrSelfApproval) {
			writeError(w, http.StatusForbidden, CodeForbidden, "cannot approve your own request")
			return
		}
		if errors.Is(err, editreq.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.edit_request.approve", "configs/"+er.ConfigID+"/edit-requests/"+id, "success", "", "keys="+strings.Join(out.Keys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": out.Version, "keys": out.Keys, "status": "applied"})
}

// handleEditRequestReject declines a pending edit request. Four-eyes: the
// rejecter must be a different user than the requester and hold secret:write.
func (s *Server) handleEditRequestReject(w http.ResponseWriter, r *http.Request) {
	if s.editreq == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "approval workflow unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	er, err := s.editreq.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	res, err := s.configResourceByID(r.Context(), er.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.edit_request.reject", "configs/"+er.ConfigID+"/edit-requests/"+id) {
		return
	}
	actor := editReqActor(r)
	if actor == "" || actor == er.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot reject your own request")
		return
	}
	if err := s.editreq.Reject(r.Context(), id, actor); err != nil {
		if errors.Is(err, editreq.ErrSelfApproval) {
			writeError(w, http.StatusForbidden, CodeForbidden, "cannot reject your own request")
			return
		}
		if errors.Is(err, editreq.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.edit_request.reject", "configs/"+er.ConfigID+"/edit-requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "rejected"})
}

// handleEditRequestCancel withdraws a pending edit request. Only the original
// requester may cancel.
func (s *Server) handleEditRequestCancel(w http.ResponseWriter, r *http.Request) {
	if s.editreq == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "approval workflow unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	er, err := s.editreq.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	actor := editReqActor(r)
	if actor == "" || actor != er.RequestedBy {
		writeError(w, http.StatusForbidden, CodeForbidden, "only the requester can cancel")
		return
	}
	if err := s.editreq.Cancel(r.Context(), id, actor); err != nil {
		if errors.Is(err, editreq.ErrRequestConflict) {
			writeError(w, http.StatusConflict, "conflict", "request not pending")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.edit_request.cancel", "configs/"+er.ConfigID+"/edit-requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled"})
}
