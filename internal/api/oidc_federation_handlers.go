package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
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
		"binding="+res.Binding+" issuer="+res.Issuer+" repository="+res.Repository+" sub="+res.Subject); err != nil {
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
	case errors.Is(err, auth.ErrFederationIssuerUntrusted):
		return "issuer_untrusted"
	case errors.Is(err, auth.ErrFederationIssuerConflict):
		return "issuer_conflict"
	case errors.Is(err, auth.ErrFederationClaims):
		return "ambiguous_claims"
	default:
		return "error"
	}
}

type fedConfigRequest struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Preset   string `json:"preset"`
	// CACert is an optional PEM CA bundle used to verify TLS for this issuer's
	// discovery + JWKS fetches (empty → system roots). Malformed PEM is a 400
	// here, not a silent federation_denied at the first exchange.
	CACert  string `json:"ca_cert"`
	Enabled bool   `json:"enabled"`
}

// caCertAuditField reports whether a CA bundle is set, for the audit detail. The
// bundle itself is public material but is deliberately never written to the
// audit log: audit entries are value-free by construction, and a multi-kilobyte
// PEM in a detail string is noise that would push the useful fields out of view.
func caCertAuditField(pem string) string {
	if strings.TrimSpace(pem) != "" {
		return " ca_cert=set"
	}
	return ""
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
		Issuer: req.Issuer, Audience: req.Audience, Preset: req.Preset,
		CACert: req.CACert, Enabled: req.Enabled,
	}); err != nil {
		if errors.Is(err, auth.ErrFederationCACertInvalid) {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"ca_cert is not a valid PEM certificate bundle")
			return
		}
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid federation config")
			return
		}
		if errors.Is(err, auth.ErrFederationIssuerConflict) {
			// This endpoint replaces the whole trusted-issuer set; refuse rather
			// than silently drop the other issuers an admin added.
			writeError(w, http.StatusConflict, "conflict",
				"several federation issuers are configured; use /v1/sys/oidc/federation/issuers")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	// Audit: issuer + audience only, never any secret material.
	if err := s.record(r, "oidc.federation.config.write", "oidc/federation", "success", "",
		"issuer="+req.Issuer+" audience="+req.Audience+caCertAuditField(req.CACert)); err != nil {
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

// --- multi-issuer trust set (roadmap 7.3) ---------------------------------
// The legacy /v1/sys/oidc/federation endpoints above address a SINGLE issuer;
// these address the set, so a deployment can trust its CI provider and its
// Kubernetes cluster at the same time.

func (s *Server) handleFederationIssuersList(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListFederationIssuers(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if list == nil {
		list = []auth.FederationConfigView{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleFederationIssuerPut(w http.ResponseWriter, r *http.Request) {
	var req fedConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	v, err := s.auth.PutFederationIssuer(r.Context(), auth.FederationIssuerInput{
		Issuer: req.Issuer, Audience: req.Audience, Preset: req.Preset,
		CACert: req.CACert, Enabled: req.Enabled,
	})
	if err != nil {
		if errors.Is(err, auth.ErrFederationCACertInvalid) {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"ca_cert is not a valid PEM certificate bundle")
			return
		}
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid federation issuer")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	// Audit: issuer + audience + preset only, never any secret material (a
	// federation trust anchor is a public-key relationship; there is no secret).
	// The CA bundle is recorded as set/unset, never verbatim.
	if err := s.record(r, "oidc.federation.issuer.write", "oidc/federation/issuers", "success", "",
		"issuer="+v.Issuer+" audience="+v.Audience+" preset="+v.Preset+
			caCertAuditField(v.CACert)); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleFederationIssuerDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.auth.DeleteFederationIssuer(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.issuer.delete", "oidc/federation/issuers/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- end multi-issuer trust set -------------------------------------------

type fedBindingRequest struct {
	Name        string            `json:"name"`
	Issuer      string            `json:"issuer"`
	MatchClaims map[string]string `json:"match_claims"`
	ScopeKind   string            `json:"scope_kind"`
	ScopeID     string            `json:"scope_id"`
	Access      string            `json:"access"`
	TTLSeconds  int               `json:"ttl_seconds"`
	Enabled     bool              `json:"enabled"`
}

func (s *Server) handleFederationBindingsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListFederationBindings(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if list == nil {
		list = []auth.FederationBindingView{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleFederationBindingCreate(w http.ResponseWriter, r *http.Request) {
	var req fedBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	b, err := s.auth.CreateFederationBinding(r.Context(), auth.FederationBindingInput{
		Name: req.Name, Issuer: req.Issuer, MatchClaims: req.MatchClaims, ScopeKind: req.ScopeKind,
		ScopeID: req.ScopeID, Access: req.Access, TTLSeconds: req.TTLSeconds, Enabled: req.Enabled,
	})
	if err != nil {
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid binding")
			return
		}
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusBadRequest, CodeValidation, "unknown scope")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.binding.write", "oidc/federation/"+b.Name, "success", "",
		"issuer="+b.Issuer+" scope="+b.ScopeKind+":"+b.ScopeID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleFederationBindingDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.auth.DeleteFederationBinding(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.binding.delete", "oidc/federation/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
