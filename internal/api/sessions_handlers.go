package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/steveokay/janus-secrets/internal/auth"
)

// withSessionMeta attaches the request's client metadata (IP, user-agent) so
// the session it mints can be recognized later in the management surface. Used
// on the login paths.
func withSessionMeta(r *http.Request) context.Context {
	return auth.WithSessionMeta(r.Context(), auth.SessionMeta{
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
	})
}

// currentCookieValue returns the caller's session cookie value, or "" if none
// (e.g. a service-token-authenticated request).
func currentCookieValue(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		return c.Value
	}
	return ""
}

type sessionView struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	ExpiresAt  string `json:"expires_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Current    bool   `json:"current"`
}

// handleSessionList returns the calling user's active sessions (self-service;
// no secret material, so it is a metadata read and is not audited — like /me).
func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != auth.KindUser {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "a user session is required")
		return
	}
	sessions, err := s.auth.ListSessions(r.Context(), p.ID, currentCookieValue(r))
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	out := make([]sessionView, 0, len(sessions))
	for _, si := range sessions {
		out = append(out, sessionView{
			ID:         si.ID,
			CreatedAt:  si.CreatedAt.UTC().Format(time.RFC3339),
			LastSeenAt: si.LastSeenAt.UTC().Format(time.RFC3339),
			ExpiresAt:  si.ExpiresAt.UTC().Format(time.RFC3339),
			IP:         si.IP,
			UserAgent:  si.UserAgent,
			Current:    si.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleSessionRevoke deletes one of the caller's own sessions by id.
func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != auth.KindUser {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "a user session is required")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.auth.RevokeSession(r.Context(), p.ID, id); err != nil {
		s.writeAuthError(w, err)
		return
	}
	if err := s.record(r, "auth.session.revoke", "sessions/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionRevokeOthers deletes every session of the caller except the
// current one — the "log out everywhere else" action.
func (s *Server) handleSessionRevokeOthers(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != auth.KindUser {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "a user session is required")
		return
	}
	n, err := s.auth.RevokeOtherSessions(r.Context(), p.ID, currentCookieValue(r))
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	if err := s.record(r, "auth.session.revoke_others", "users/"+p.ID, "success", "", "revoked="+strconv.Itoa(n)); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}
