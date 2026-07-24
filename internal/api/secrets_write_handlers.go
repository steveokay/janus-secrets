package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/editreq"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

type secretChangeBody struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Delete bool   `json:"delete"`
	Type   string `json:"type"`
}

type batchWriteRequest struct {
	Message string             `json:"message"`
	Changes []secretChangeBody `json:"changes"`
}

type versionResponse struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

// actorOf returns a stable actor id for version attribution.
func actorOf(r *http.Request) string {
	p, _ := PrincipalFrom(r.Context())
	return p.ID
}

// requireApproval reports whether the config is protected (four-eyes). A lookup
// error is treated as "not protected == false" only when the config truly does
// not exist; any other error is surfaced. It returns (protected, handled): when
// handled is true an error response was already written and the caller returns.
func (s *Server) requireApproval(w http.ResponseWriter, r *http.Request, cid string) (bool, bool) {
	c, err := store.NewConfigRepo(s.st).Get(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return false, true
	}
	return c.RequireApproval, false
}

// applyWrite runs SetSecrets and writes the version response, sharing the
// audit + error handling across batch and per-key writes. When the target
// config is PROTECTED (require_approval), the changes are NOT committed; instead
// they become a pending, envelope-encrypted config edit request (202 Accepted),
// awaiting four-eyes approval.
func (s *Server) applyWrite(w http.ResponseWriter, r *http.Request, cid string,
	changes []secrets.SecretChange, message, auditDetail string) {
	protected, handled := s.requireApproval(w, r, cid)
	if handled {
		return
	}
	if protected {
		s.submitEditRequest(w, r, cid, changes, message)
		return
	}
	cv, err := s.service.SetSecrets(r.Context(), cid, changes, message, actorOf(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.write", "configs/"+cid+"/secrets", "success", "", auditDetail); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, versionResponse{
		Version: cv.Version, ID: cv.ID, CreatedAt: cv.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// submitEditRequest stores the proposed changes envelope-encrypted as a pending
// config edit request and responds 202 with the request id. Value-free audit
// (changed key NAMES only). Requires the editreq service (production wiring).
func (s *Server) submitEditRequest(w http.ResponseWriter, r *http.Request, cid string,
	changes []secrets.SecretChange, message string) {
	if s.editreq == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "approval workflow unavailable")
		return
	}
	reason := strings.TrimSpace(r.Header.Get("X-Edit-Reason"))
	keys := make([]string, 0, len(changes))
	for _, ch := range changes {
		keys = append(keys, ch.Key)
	}
	id, err := s.editreq.Create(r.Context(), editreq.CreateInput{
		ConfigID:    cid,
		Changes:     changes,
		Message:     message,
		Reason:      reason,
		RequestedBy: actorOf(r),
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.edit_request.create", "configs/"+cid+"/edit-requests/"+id, "success", "", "keys="+strings.Join(keys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"edit_request_id": id, "status": "pending", "keys": keys,
	})
}

func (s *Server) handleSecretsBatchWrite(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.write", "configs/"+cid+"/secrets") {
		return
	}
	var req batchWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Changes) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "at least one change is required")
		return
	}
	changes := make([]secrets.SecretChange, 0, len(req.Changes))
	seen := make(map[string]bool, len(req.Changes))
	for _, c := range req.Changes {
		if c.Key == "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "each change needs a key")
			return
		}
		if seen[c.Key] {
			writeError(w, http.StatusBadRequest, CodeValidation, "duplicate key in batch: "+c.Key)
			return
		}
		seen[c.Key] = true
		changes = append(changes, secrets.SecretChange{Key: c.Key, Value: []byte(c.Value), Delete: c.Delete, Type: c.Type})
	}
	s.applyWrite(w, r, cid, changes, req.Message, "keys="+strconv.Itoa(len(changes)))
}

type perKeyWriteRequest struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

func (s *Server) handleSecretPut(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.write", "configs/"+cid+"/secrets/"+chi.URLParam(r, "key")) {
		return
	}
	key := chi.URLParam(r, "key")
	var req perKeyWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "value is required")
		return
	}
	s.applyWrite(w, r, cid, []secrets.SecretChange{{Key: key, Value: []byte(req.Value), Type: req.Type}}, "", "key="+key)
}

func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.delete", "configs/"+cid+"/secrets/"+key) {
		return
	}
	protected, handled := s.requireApproval(w, r, cid)
	if handled {
		return
	}
	if protected {
		s.submitEditRequest(w, r, cid, []secrets.SecretChange{{Key: key, Delete: true}}, "")
		return
	}
	cv, err := s.service.SetSecrets(r.Context(), cid,
		[]secrets.SecretChange{{Key: key, Delete: true}}, "", actorOf(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.delete", "configs/"+cid+"/secrets/"+key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, versionResponse{
		Version: cv.Version, ID: cv.ID, CreatedAt: cv.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}
