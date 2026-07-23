package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// record writes an audit event for the current request's principal. It no-ops
// (returns nil) when no recorder is wired (unit-test servers). A non-nil return
// means the caller must fail the request — the audit log is fail-closed.
func (s *Server) record(r *http.Request, action, resource, result, code, detail string) error {
	p, _ := PrincipalFrom(r.Context())
	kind := string(p.Kind)
	if kind == "" {
		kind = "anonymous"
	}
	return s.recordActor(r, audit.Actor{Kind: kind, ID: p.ID, Name: p.Name}, action, resource, result, code, detail)
}

// recordActor writes an audit event for an explicit actor (used where the actor
// is not the request principal, e.g. a failed login records an anonymous actor
// with the attempted email).
func (s *Server) recordActor(r *http.Request, actor audit.Actor, action, resource, result, code, detail string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(r.Context(), audit.Event{
		Actor: actor, Action: action, Resource: resource, Detail: detail,
		Result: result, ResultCode: code, IP: r.RemoteAddr,
	})
}

// recordTokenIP records that a service token authenticated from ip and, when
// this is a genuinely new (token, ip) pair, emits a value-free token.new_ip
// audit event carrying the IP (never the token value or HMAC). It is
// best-effort: a store/audit failure is logged but MUST NOT fail the
// authenticated request (the middleware calls this after auth already
// succeeded). Naturally throttled — the store write only reports "new" on a
// first-seen pair, so the audit event fires at most once per (token, ip).
func (s *Server) recordTokenIP(r *http.Request, tokenID, ip string) {
	if s.auth == nil {
		return
	}
	isNew, err := s.auth.NoteTokenIP(r.Context(), tokenID, ip)
	if err != nil {
		if s.logger != nil {
			s.logger.WarnContext(r.Context(), "new-ip note failed (non-fatal)", "err", err)
		}
		return
	}
	if !isNew {
		return
	}
	// Value-free: actor is the token, resource is the token id, detail is the IP.
	if aerr := s.recordActor(r, audit.Actor{Kind: "service_token", ID: tokenID},
		"token.new_ip", "tokens/"+tokenID, "success", "", "ip="+ip); aerr != nil {
		if s.logger != nil {
			s.logger.WarnContext(r.Context(), "new-ip audit failed (non-fatal)", "err", aerr)
		}
	}
}

// authorize evaluates an authz decision and, on denial, records a denied event
// then writes 403 — centralizing denial auditing so every 403 is captured in
// one place. Returns true iff the caller may proceed (the success event is
// recorded later by the handler, after the action). A denial whose own audit
// write fails 500s (never proceed, never silently drop the denial).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, action authz.Action, res authz.Resource, auditAction, auditResource string) bool {
	err := s.can(r, action, res)
	if err == nil {
		return true
	}
	if errors.Is(err, authz.ErrForbidden) {
		if aerr := s.record(r, auditAction, auditResource, "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return false
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return false
	}
	s.writeAuthzError(w, err) // non-forbidden (e.g. store error) → 500
	return false
}
