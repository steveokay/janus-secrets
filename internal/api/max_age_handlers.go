package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// Advisory secret max-age policy handlers. Setting/clearing a policy is a config
// write and reuses SecretWrite (the same permission that guards editing secret
// values). Reading policy reuses SecretRead. All mutations emit a value-free
// audit event (key name / config path / duration only — never a secret value).

// handleConfigMaxAgePut sets or clears the CONFIG DEFAULT advisory max-age.
// Body: {"max_age_seconds": 2160000} to set, {"max_age_seconds": null} to clear.
func (s *Server) handleConfigMaxAgePut(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.maxage.set", "configs/"+cid+"/max-age") {
		return
	}
	secs, clear, ok := decodeMaxAgeBody(w, r)
	if !ok {
		return
	}
	if clear {
		if err := s.service.ClearConfigMaxAge(r.Context(), cid); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.maxage.clear", "configs/"+cid+"/max-age", "success", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"max_age_seconds": nil})
		return
	}
	if err := s.service.SetConfigMaxAge(r.Context(), cid, secs, promoteActorUser(r)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.maxage.set", "configs/"+cid+"/max-age", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"max_age_seconds": secs})
}

// handleKeyMaxAgePut sets or clears a PER-KEY advisory max-age override.
func (s *Server) handleKeyMaxAgePut(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.SecretWrite, res, "secret.maxage.set", "configs/"+cid+"/secrets/"+key+"/max-age") {
		return
	}
	secs, clear, ok := decodeMaxAgeBody(w, r)
	if !ok {
		return
	}
	if clear {
		if err := s.service.ClearKeyMaxAge(r.Context(), cid, key); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.maxage.clear", "configs/"+cid+"/secrets/"+key+"/max-age", "success", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "max_age_seconds": nil})
		return
	}
	if err := s.service.SetKeyMaxAge(r.Context(), cid, key, secs, promoteActorUser(r)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "secret.maxage.set", "configs/"+cid+"/secrets/"+key+"/max-age", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "max_age_seconds": secs})
}

// handleMaxAgeList returns the config's advisory max-age policies. The config
// default appears under the empty-string key "". Reads are not audited.
func (s *Server) handleMaxAgeList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	pols, err := s.service.ListMaxAge(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(pols))
	for _, p := range pols {
		out = append(out, map[string]any{"key": p.Key, "max_age_seconds": p.MaxAgeSeconds})
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": out})
}

// decodeMaxAgeBody parses {"max_age_seconds": <int>|null}. clear is true when the
// field is explicitly null. Writes a 400 and returns ok=false on a malformed body
// or a non-positive value. A missing/omitted field is treated as clear (null).
func decodeMaxAgeBody(w http.ResponseWriter, r *http.Request) (secs int64, clear bool, ok bool) {
	var body struct {
		MaxAgeSeconds *int64 `json:"max_age_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return 0, false, false
	}
	if body.MaxAgeSeconds == nil {
		return 0, true, true
	}
	if *body.MaxAgeSeconds <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "max_age_seconds must be a positive integer or null")
		return 0, false, false
	}
	return *body.MaxAgeSeconds, false, true
}
