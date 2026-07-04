package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
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
	p, _ := PrincipalFrom(r.Context())
	var req mintTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
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
	// The one and only exposure of the raw token.
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
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeToken(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
