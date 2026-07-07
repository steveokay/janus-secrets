package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func (s *Server) handleOIDCFederate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		_ = s.record(r, "auth.federate", "auth/oidc/federate", "denied", "federation_denied", "bad request")
		writeError(w, http.StatusUnauthorized, "federation_denied", "federation exchange failed")
		return
	}
	res, err := s.auth.FederateCILogin(r.Context(), req.Token)
	if err != nil {
		// One response for every reason; the audit detail carries the real cause.
		_ = s.record(r, "auth.federate", "auth/oidc/federate", "denied", "federation_denied", federationReason(err))
		writeError(w, http.StatusUnauthorized, "federation_denied", "federation exchange failed")
		return
	}
	if err := s.record(r, "auth.federate", "auth/oidc/federate", "success", "",
		"binding="+res.Binding+" repository="+res.Repository+" sub="+res.Subject); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := map[string]any{
		"token": res.Token,
		"scope": map[string]any{"kind": res.Meta.ScopeKind, "id": res.Meta.ScopeID, "access": res.Meta.Access},
	}
	if res.Meta.ExpiresAt != nil {
		out["expires_at"] = res.Meta.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	writeJSON(w, http.StatusOK, out)
}

// federationReason maps a sentinel to a short audit detail (never returned to the caller).
func federationReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrFederationNotConfigured):
		return "not_configured"
	case errors.Is(err, auth.ErrFederationVerify):
		return "verify_failed"
	case errors.Is(err, auth.ErrFederationNoMatch):
		return "no_match"
	case errors.Is(err, auth.ErrFederationAmbiguous):
		return "ambiguous_match"
	default:
		return "error"
	}
}

type fedConfigRequest struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Enabled  bool   `json:"enabled"`
}

// handleFederationConfigGet: authz enforced by requireInstance middleware. Read — not audited.
func (s *Server) handleFederationConfigGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.auth.GetFederationConfig(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleFederationConfigPut(w http.ResponseWriter, r *http.Request) {
	var req fedConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := s.auth.SetFederationConfig(r.Context(), auth.FederationConfigInput{
		Issuer: req.Issuer, Audience: req.Audience, Enabled: req.Enabled,
	}); err != nil {
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid federation config")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	// Audit: issuer + audience only, never any secret material.
	if err := s.record(r, "oidc.federation.config.write", "oidc/federation", "success", "",
		"issuer="+req.Issuer+" audience="+req.Audience); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFederationConfigDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteFederationConfig(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.config.delete", "oidc/federation", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
