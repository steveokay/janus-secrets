package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
)

func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.auth.GetOIDCProvider(r.Context())
	if err != nil || !v.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "name": v.Name})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	url, err := s.auth.StartOIDCLogin(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "oidc_not_configured", "OIDC login is not configured")
			return
		}
		s.writeServiceError(w, err) // crypto.ErrSealed → 503
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		_ = s.record(r, "auth.login", "auth/oidc", "denied", "oidc_denied", "provider error")
		writeError(w, http.StatusBadRequest, "oidc_denied", "authentication failed")
		return
	}
	cookie, p, err := s.auth.CompleteOIDCLogin(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		code, status := "oidc_denied", http.StatusUnauthorized
		if errors.Is(err, auth.ErrInvalidOIDCState) {
			code, status = "invalid_oidc_state", http.StatusBadRequest
		}
		_ = s.record(r, "auth.login", "auth/oidc", "denied", code, "")
		writeError(w, status, code, "authentication failed")
		return
	}
	http.SetCookie(w, sessionCookie(r, cookie, 24*time.Hour))
	if err := s.recordActor(r, audit.Actor{Kind: string(auth.KindUser), ID: p.ID, Name: p.Name},
		"auth.login", "auth/oidc", "success", "", "method=oidc"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

type oidcConfigRequest struct {
	Name         string   `json:"name"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`
	RedirectURL  string   `json:"redirect_url"`
	Enabled      bool     `json:"enabled"`
}

// handleOIDCConfigGet: authz enforced by requireInstance middleware. Read — not audited.
func (s *Server) handleOIDCConfigGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.auth.GetOIDCProvider(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v) // OIDCProviderView: secret_set only, never the secret
}

func (s *Server) handleOIDCConfigPut(w http.ResponseWriter, r *http.Request) {
	var req oidcConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := s.auth.SetOIDCProvider(r.Context(), auth.OIDCProviderInput{
		Name: req.Name, Issuer: req.Issuer, ClientID: req.ClientID,
		ClientSecret: req.ClientSecret, Scopes: req.Scopes,
		RedirectURL: req.RedirectURL, Enabled: req.Enabled,
	}); err != nil {
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid provider config")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	// Audit: issuer + client_id only, NEVER the secret.
	if err := s.record(r, "oidc.config.write", "oidc", "success", "", "issuer="+req.Issuer+" client_id="+req.ClientID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleOIDCConfigDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteOIDCProvider(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.config.delete", "oidc", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
