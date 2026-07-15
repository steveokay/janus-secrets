package api

import (
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// handleTrashList returns soft-deleted projects/environments/configs the caller
// is allowed to restore, grouped. An item is included only if the caller passes
// the SAME delete authorization that gates its restore (mirrors handleTokenList's
// per-item filter) — no existence leak, no new permission. Parent-name fields are
// best-effort non-secret labels; a missing/deleted parent falls back to its id.
// This is a metadata read (like the audit event list) and is NOT self-audited.
func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pr := store.NewProjectRepo(s.st)
	er := store.NewEnvironmentRepo(s.st)
	cr := store.NewConfigRepo(s.st)

	projName := map[string]string{}
	projLabel := func(pid string) string {
		if n, ok := projName[pid]; ok {
			return n
		}
		n := pid
		if p, err := pr.GetIncludingDeleted(ctx, pid); err == nil {
			n = p.Name
		}
		projName[pid] = n
		return n
	}
	envMeta := map[string]*store.Environment{}
	envLookup := func(eid string) *store.Environment {
		if e, ok := envMeta[eid]; ok {
			return e
		}
		e, err := er.GetIncludingDeleted(ctx, eid)
		if err != nil {
			e = nil
		}
		envMeta[eid] = e
		return e
	}
	iso := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}

	projects := []map[string]any{}
	dp, err := pr.ListDeleted(ctx)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	for _, p := range dp {
		if s.can(r, authz.ProjectDelete, authz.Resource{ProjectID: p.ID}) != nil {
			continue
		}
		projects = append(projects, map[string]any{
			"id": p.ID, "slug": p.Slug, "name": p.Name, "deleted_at": iso(p.DeletedAt),
		})
	}

	environments := []map[string]any{}
	de, err := er.ListDeleted(ctx)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	for _, e := range de {
		if s.can(r, authz.EnvDelete, authz.Resource{ProjectID: e.ProjectID, EnvID: e.ID}) != nil {
			continue
		}
		environments = append(environments, map[string]any{
			"id": e.ID, "slug": e.Slug, "name": e.Name,
			"project_id": e.ProjectID, "project_name": projLabel(e.ProjectID),
			"deleted_at": iso(e.DeletedAt),
		})
	}

	configs := []map[string]any{}
	dc, err := cr.ListDeleted(ctx)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	for _, c := range dc {
		env := envLookup(c.EnvironmentID)
		pid, projLbl, envLbl := "", "", c.EnvironmentID
		if env != nil {
			pid, envLbl = env.ProjectID, env.Name
			projLbl = projLabel(env.ProjectID)
		}
		if s.can(r, authz.ConfigDelete, authz.Resource{ProjectID: pid, EnvID: c.EnvironmentID, ConfigID: c.ID}) != nil {
			continue
		}
		configs = append(configs, map[string]any{
			"id": c.ID, "name": c.Name,
			"environment_id": c.EnvironmentID, "environment_name": envLbl,
			"project_id": pid, "project_name": projLbl,
			"deleted_at": iso(c.DeletedAt),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"projects": projects, "environments": environments, "configs": configs,
	})
}
