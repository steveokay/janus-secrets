package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// handleSecretsList serves the masked list (no audit) or, with ?reveal=true, the
// full-value reveal of every key (audited).
func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if r.URL.Query().Get("reveal") == "true" {
		if !s.authorize(w, r, authz.SecretRead, res, "secret.reveal", "configs/"+cid+"/secrets") {
			return
		}
		if r.URL.Query().Get("raw") == "true" {
			cv, all, err := s.service.RevealConfig(r.Context(), cid)
			if err != nil {
				s.writeServiceError(w, err)
				return
			}
			out := make(map[string]string, len(all))
			for k, sec := range all {
				out[k] = string(sec.Value)
			}
			if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets", "success", "", "raw"); err != nil {
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"version": cv.Version, "secrets": out})
			return
		}
		values, prov, err := s.resolverFor(r).Resolve(r.Context(), cid)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		out := make(map[string]string, len(values))
		for k, v := range values {
			out[k] = string(v)
		}
		if err := s.recordReveal(r, cid, "all", prov); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		version, verr := s.service.LatestVersion(r.Context(), cid)
		if verr != nil {
			s.writeServiceError(w, verr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version, "secrets": out})
		return
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	metas, err := s.service.ListSecretsMerged(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	masked := make(map[string]any, len(metas))
	for _, m := range metas {
		masked[m.Key] = map[string]any{
			"value_version": m.ValueVersion,
			"created_at":    m.CreatedAt.UTC().Format(time.RFC3339),
			"origin":        m.Origin,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": masked})
}

// handleSecretGet reveals one key's latest value, or a historical value with
// ?version={valueVersion}. Always audited.
func (s *Server) handleSecretGet(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.SecretRead, res, "secret.reveal", "configs/"+cid+"/secrets/"+key) {
		return
	}
	// Historical reads are always raw (a past version is a stored artifact).
	if v := r.URL.Query().Get("version"); v != "" {
		vv, convErr := strconv.Atoi(v)
		if convErr != nil || vv < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation, "version must be a positive integer")
			return
		}
		sec, err := s.service.GetSecretVersion(r.Context(), cid, key, vv)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets/"+key, "success", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(sec.Value), "value_version": sec.ValueVersion})
		return
	}
	if r.URL.Query().Get("raw") == "true" {
		sec, err := s.service.GetSecret(r.Context(), cid, key)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets/"+key, "success", "", "raw"); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(sec.Value)})
		return
	}
	val, prov, err := s.resolverFor(r).ResolveKey(r.Context(), cid, key)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.recordReveal(r, cid, "key "+key, prov); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(val)})
}

// handleKeyHistory serves masked value-version metadata for one key (no audit).
func (s *Server) handleKeyHistory(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	hist, err := s.service.KeyHistory(r.Context(), cid, key)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(hist))
	for _, m := range hist {
		out = append(out, map[string]any{
			"value_version": m.ValueVersion,
			"created_at":    m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "history": out})
}
