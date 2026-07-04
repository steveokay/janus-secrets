package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type mintTokenRequest struct {
	Name  string `json:"name"`
	Scope struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"scope"`
	Access     string `json:"access"`
	TTLSeconds *int64 `json:"ttl_seconds"`
}

func (s *Server) handleTokenMint(w http.ResponseWriter, r *http.Request) {
	var req mintTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), req.Scope.Kind, req.Scope.ID)
	switch {
	case errors.Is(err, errBadScopeKind):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid scope kind")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "scope target not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.can(r, authz.TokenMint, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	var ttl *time.Duration
	if req.TTLSeconds != nil {
		if *req.TTLSeconds <= 0 {
			writeError(w, http.StatusBadRequest, CodeValidation, "ttl_seconds must be positive")
			return
		}
		d := time.Duration(*req.TTLSeconds) * time.Second
		ttl = &d
	}
	raw, meta, err := s.auth.MintServiceToken(r.Context(), p, req.Name, req.Scope.Kind, req.Scope.ID, req.Access, ttl)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": raw, "id": meta.ID, "name": meta.Name,
		"scope":  map[string]string{"kind": meta.ScopeKind, "id": meta.ScopeID},
		"access": meta.Access, "expires_at": meta.ExpiresAt,
	})
}

func (s *Server) handleTokenList(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListTokens(r.Context())
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	out := make([]auth.TokenMeta, 0, len(list))
	for _, m := range list {
		res, err := s.resolveScopeResource(r.Context(), m.ScopeKind, m.ScopeID)
		if err != nil {
			continue // scope target gone; omit
		}
		if s.can(r, authz.TokenRead, res) == nil {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	list, err := s.auth.ListTokens(r.Context())
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	var meta *auth.TokenMeta
	for i := range list {
		if list[i].ID == id {
			meta = &list[i]
			break
		}
	}
	if meta == nil {
		writeError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), meta.ScopeKind, meta.ScopeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.can(r, authz.TokenRevoke, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	if err := s.auth.RevokeToken(r.Context(), id); err != nil {
		s.writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
