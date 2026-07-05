package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/resolve"
)

// apiAuthorizer implements resolve.Authorizer by reusing the request-scoped
// s.can check: a reference dereference is permitted only if the caller could
// read the target config directly (strict, deny-by-default).
type apiAuthorizer struct {
	s *Server
	r *http.Request
}

func (a apiAuthorizer) CanReadSecrets(_ context.Context, t resolve.RawConfig) error {
	if err := a.s.can(a.r, authz.SecretRead, authz.Resource{
		ProjectID: t.ProjectID, EnvID: t.EnvID, ConfigID: t.ConfigID,
	}); err != nil {
		return resolve.ErrForbiddenReference
	}
	return nil
}

// resolverFor builds a request-scoped resolver: the raw reader is the secrets
// service; the authorizer is bound to this request's principal.
func (s *Server) resolverFor(r *http.Request) *resolve.Resolver {
	return resolve.New(s.service, apiAuthorizer{s: s, r: r})
}

// recordReveal writes the primary secret.reveal for cid plus one secret.reveal
// per distinct config dereferenced via a reference (provenance), fail-closed.
func (s *Server) recordReveal(r *http.Request, cid, detail string, prov []resolve.Provenance) error {
	if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets", "success", "", detail); err != nil {
		return err
	}
	for _, p := range prov {
		if err := s.record(r, "secret.reveal", "configs/"+p.ConfigID+"/secrets",
			"success", "", "via reference from configs/"+cid); err != nil {
			return err
		}
	}
	return nil
}

// auditResolveDenial records a fail-closed denied secret.reveal when a resolution
// was refused by authorization (a forbidden reference on the read path),
// mirroring authorize()'s central denial auditing so a denied secret-access
// attempt is never unaudited. It targets the resource the caller requested (the
// exact forbidden target id is not exposed by the atomic failure). Non-authz
// resolution failures (cycle, unresolved, bad syntax) are config errors, not
// access decisions, and are not audited here. Returns false iff the denial's own
// audit write failed — the caller must then 500 and stop (do not leak the 403).
func (s *Server) auditResolveDenial(w http.ResponseWriter, r *http.Request, resource string, err error) bool {
	if !errors.Is(err, resolve.ErrForbiddenReference) {
		return true
	}
	if aerr := s.record(r, "secret.reveal", resource, "denied", CodeForbidden, "forbidden reference"); aerr != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return false
	}
	return true
}
