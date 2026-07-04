package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "email and password are required")
		return
	}
	cookie, err := s.auth.Login(r.Context(), req.Email, []byte(req.Password))
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	p, err := s.auth.VerifySession(r.Context(), cookie)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	http.SetCookie(w, sessionCookie(r, cookie, 24*time.Hour))
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"id": p.ID, "email": p.Name},
	})
}

// sessionCookie builds the session cookie; Secure is set when the request
// arrived over TLS (behind a proxy this follows the upstream scheme).
func sessionCookie(r *http.Request, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(ttl / time.Second),
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			s.writeAuthError(w, err)
			return
		}
	}
	// Expire the cookie client-side regardless.
	expired := sessionCookie(r, "", 0)
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"kind": string(p.Kind), "id": p.ID, "name": p.Name,
	})
}

type passwordChangeRequest struct {
	Old string `json:"old"`
	New string `json:"new"`
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != auth.KindUser {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "a user session is required")
		return
	}
	var req passwordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Old == "" || len(req.New) < 12 {
		writeError(w, http.StatusBadRequest, CodeValidation, "old password and a new password of at least 12 characters are required")
		return
	}
	if err := s.auth.ChangePassword(r.Context(), p.ID, []byte(req.Old), []byte(req.New)); err != nil {
		s.writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeAuthError maps auth/crypto errors to the envelope without detail leaks.
func (s *Server) writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	case errors.Is(err, auth.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "authentication required")
	case errors.Is(err, auth.ErrValidation):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid input")
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, crypto.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
