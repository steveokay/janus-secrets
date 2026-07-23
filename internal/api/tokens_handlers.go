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
	Access      string   `json:"access"`
	TTLSeconds  *int64   `json:"ttl_seconds"`
	IPAllowlist []string `json:"ip_allowlist"`
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
	if !s.authorize(w, r, authz.TokenMint, res, "token.mint", "tokens") {
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
	// Validate the IP allowlist at the API boundary (net.ParseCIDR) before minting.
	allow, err := auth.ValidateCIDRs(req.IPAllowlist)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid CIDR in ip_allowlist")
		return
	}
	raw, meta, err := s.auth.MintServiceToken(r.Context(), p, req.Name, req.Scope.Kind, req.Scope.ID, req.Access, ttl, allow)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	if err := s.record(r, "token.mint", "tokens/"+meta.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": raw, "id": meta.ID, "name": meta.Name,
		"scope":        map[string]string{"kind": meta.ScopeKind, "id": meta.ScopeID},
		"access":       meta.Access, "expires_at": meta.ExpiresAt,
		"ip_allowlist": meta.IPAllowlist,
	})
}

func (s *Server) handleTokenList(w http.ResponseWriter, r *http.Request) {
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	list, next, err := s.auth.ListTokensPage(r.Context(), pp.limit, pp.after)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	// Visibility filter (security boundary): drop tokens whose scope target is
	// gone or the caller can't read. The next cursor keys on the RAW scan
	// position, so a page may surface fewer than limit visible tokens — the
	// client keeps paging until next_cursor is null.
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
	var nextTok *string
	if next != nil {
		t := encodeCursor(next.CreatedAt, next.ID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out, "next_cursor": nextTok})
}

type updateTokenRequest struct {
	// IPAllowlist replaces the token's CIDR allowlist. An empty list clears it
	// (any IP). Absent field is treated as "no change" only if the caller omits
	// the key; JSON decoding of a missing key yields nil, which we distinguish
	// via a pointer so an explicit [] can clear while omission is a no-op.
	IPAllowlist *[]string `json:"ip_allowlist"`
}

// handleTokenUpdate updates mutable token metadata (currently only the IP
// allowlist). Value-free: the token value/HMAC are never touched. Requires the
// same authorization as revoke (TokenRevoke on the token's scope) — both are
// token-management operations on the scope.
func (s *Server) handleTokenUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
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
	if !s.authorize(w, r, authz.TokenRevoke, res, "token.update", "tokens/"+id) {
		return
	}
	if req.IPAllowlist != nil {
		if _, verr := auth.ValidateCIDRs(*req.IPAllowlist); verr != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid CIDR in ip_allowlist")
			return
		}
		if err := s.auth.SetTokenIPAllowlist(r.Context(), id, *req.IPAllowlist); err != nil {
			s.writeAuthError(w, err)
			return
		}
	}
	if err := s.record(r, "token.update", "tokens/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newIPWindow is the look-back for the "token used from a new IP recently"
// in-tray aggregate.
const newIPWindow = 24 * time.Hour

// handleTokenNewIPs serves the cheap value-free aggregate behind the Overview
// in-tray "token used from a new IP" item: the number of (token, ip)
// first-sightings within the last 24h. Instance TokenRead (admin view of token
// metadata). Not self-audited (a metadata read, like the token list).
func (s *Server) handleTokenNewIPs(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.TokenRead, authz.Instance(), "token.new_ips", "tokens") {
		return
	}
	n, err := s.auth.CountRecentNewIPs(r.Context(), newIPWindow)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "window_hours": 24})
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
	if !s.authorize(w, r, authz.TokenRevoke, res, "token.revoke", "tokens/"+id) {
		return
	}
	if err := s.auth.RevokeToken(r.Context(), id); err != nil {
		s.writeAuthError(w, err)
		return
	}
	if err := s.record(r, "token.revoke", "tokens/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
