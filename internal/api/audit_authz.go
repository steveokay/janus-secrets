package api

import (
	"net/http"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// authorizeAuditRead decides how much of the audit log a caller may read, and
// returns the project restriction to apply.
//
//	(nil,  true)  — instance-wide reader: no restriction
//	(list, true)  — scoped reader: restrict to these projects
//	(_,    false) — denied; a response has already been written
//
// A scoped reader gets `audit:read` from a project-scoped binding. Since
// `audit:read` is already in the admin bundle, a project admin holds it today
// and no new action is needed.
//
// Two deliberate limits, both documented rather than silently widened:
//
//   - Scoping is PROJECT level only. An environment-scoped binding confers
//     nothing here, because only the project is recorded on an event. Such a
//     caller is denied outright rather than shown a partial trail.
//   - Events with no project scope — instance-level actions, cross-project
//     operations, and everything written before migration 000048 — are visible
//     only to instance-wide readers. A team's scoped history therefore starts
//     at the upgrade.
func (s *Server) authorizeAuditRead(w http.ResponseWriter, r *http.Request, auditAction string) ([]string, bool) {
	// Instance-wide first: unchanged behaviour for owners/admins.
	if s.can(r, authz.AuditRead, authz.Instance()) == nil {
		return nil, true
	}
	projects, err := store.NewProjectRepo(s.st).ListPage(r.Context(), 0, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return nil, false
	}
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	// One binding+grant load for the whole set. Calling s.can per project would
	// re-query direct bindings, group bindings and break-glass on every
	// iteration — thousands of queries to render one page on a large instance.
	principal, _ := PrincipalFrom(r.Context())
	readable, err := s.authz.AllowedProjects(r.Context(), principal, authz.AuditRead, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return nil, false
	}
	if len(readable) == 0 {
		// Deny-by-default, and audited like any other denial.
		if aerr := s.record(r, auditAction, "audit", "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return nil, false
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return nil, false
	}
	return readable, true
}
