package api

import (
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
