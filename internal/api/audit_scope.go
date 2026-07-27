package api

import (
	"context"
	"net/http"
)

// auditScope carries the project an in-flight request is acting on, so audit
// events can be filtered by scope on read (see migration 000048).
//
// It is captured at AUTHORIZATION time rather than threaded through the ~144
// record() call sites, because an event's project scope IS the scope its
// operation was authorized against. A pointer lives in the request context so
// s.can can record into it without replacing the context the handler holds.
type auditScope struct {
	projectID string
	// ambiguous is set when one request authorizes against two DIFFERENT
	// projects — a cross-project operation such as comparing configs in two
	// projects, or any per-item filtering loop. Such an event belongs to no
	// single project, so it is recorded with no scope and stays visible only to
	// instance-wide readers. Fail-closed: better unattributed than attributed
	// to one project and hidden from the other's trail.
	ambiguous bool
	// explicit marks a scope set deliberately by a handler that knows better
	// than the authorization resource did (project creation authorizes against
	// the instance scope, but the event plainly belongs to the new project).
	// An explicit scope wins and is never made ambiguous.
	explicit bool
}

type auditScopeKeyType struct{}

var auditScopeKey auditScopeKeyType

// withAuditScope attaches a fresh scope holder. Applied once per request in the
// global middleware chain so every handler has one, including unauthenticated
// routes — a failed login simply resolves to no project.
func withAuditScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), auditScopeKey, &auditScope{})))
	})
}

func auditScopeFrom(ctx context.Context) *auditScope {
	sc, _ := ctx.Value(auditScopeKey).(*auditScope)
	return sc // nil is fine: noteAuditProject and auditProjectOf both tolerate it
}

// noteAuditProject records the project an authorization decision was made
// against. Called from s.can on every check, so coverage is automatic.
//
// The first non-empty project wins; a second, DIFFERENT project marks the
// request ambiguous and clears the scope. Repeating the same project is not
// ambiguous — handlers routinely authorize the same resource twice.
func noteAuditProject(ctx context.Context, projectID string) {
	sc := auditScopeFrom(ctx)
	if sc == nil || projectID == "" || sc.explicit {
		return
	}
	switch {
	case sc.ambiguous:
		return
	case sc.projectID == "":
		sc.projectID = projectID
	case sc.projectID != projectID:
		sc.ambiguous = true
		sc.projectID = ""
	}
}

// setAuditProject pins the scope for a handler whose event belongs to a project
// its authorization check did not name — project creation being the case that
// matters, since it authorizes against the instance scope but produces an event
// the new project's team should see.
func setAuditProject(r *http.Request, projectID string) {
	sc := auditScopeFrom(r.Context())
	if sc == nil || projectID == "" {
		return
	}
	sc.projectID = projectID
	sc.ambiguous = false
	sc.explicit = true
}

// auditProjectOf returns the resolved scope, or nil for an event that belongs
// to no single project (instance-level actions, and cross-project operations).
func auditProjectOf(ctx context.Context) *string {
	sc := auditScopeFrom(ctx)
	if sc == nil || sc.projectID == "" {
		return nil
	}
	pid := sc.projectID
	return &pid
}
