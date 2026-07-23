package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// Per-key secret annotation handlers. An annotation is human-facing metadata —
// an owner label and a free-text note — attached to a key so "what is this and
// who do I ask" is answerable. Value-free: no secret VALUES are ever read or
// written here. Setting/clearing is a config write and reuses SecretWrite (the
// same permission that guards editing secret values); reading rides the
// masked-secrets list (SecretRead). Mutations emit a value-free audit event
// (key name / config path only — never owner/note text or a secret value).

// handleKeyAnnotationPut sets or clears a key's owner + note annotation.
// Body: {"owner": "...", "note": "..."}. A field may be null/omitted/empty to
// leave it unset; when BOTH end up empty the annotation is cleared entirely.
func (s *Server) handleKeyAnnotationPut(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.annotation.set", "configs/"+cid+"/secrets/"+key+"/annotation") {
		return
	}
	var body struct {
		Owner *string `json:"owner"`
		Note  *string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	owner, note, cleared, err := s.service.SetAnnotation(r.Context(), cid, key, body.Owner, body.Note, promoteActorUser(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	action := "secret.annotation.set"
	if cleared {
		action = "secret.annotation.clear"
	}
	if err := s.record(r, action, "configs/"+cid+"/secrets/"+key+"/annotation", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := map[string]any{"key": key, "owner": nil, "note": nil}
	if owner != nil {
		out["owner"] = *owner
	}
	if note != nil {
		out["note"] = *note
	}
	writeJSON(w, http.StatusOK, out)
}
