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

// handleKeyAnnotationPut sets or clears a key's note.
// Body: {"note": "..."}. An empty/omitted note clears the annotation.
//
// `owner` used to live here and no longer does — it moved to the PROJECT in
// migration 000049, because a service has an owner and its individual keys
// almost never do. Sending it is REJECTED rather than ignored: silently
// dropping a field an operator supplied would leave them believing ownership
// was recorded when it was not.
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
	if body.Owner != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"owner is set on the project, not the key — use PATCH /v1/projects/{pid}")
		return
	}
	note, cleared, err := s.service.SetAnnotation(r.Context(), cid, key, body.Note, promoteActorUser(r))
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
	out := map[string]any{"key": key, "note": nil}
	if note != nil {
		out["note"] = *note
	}
	writeJSON(w, http.StatusOK, out)
}
