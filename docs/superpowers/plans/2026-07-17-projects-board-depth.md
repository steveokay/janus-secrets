# Projects & Board Depth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close gaps.md §2.4 — give the projects list and per-project board real depth: recency metadata + richer sort, admin rename (name-only), same-project clone-environment, project glyphs, card quick-actions, and inheritance connectors.

**Architecture:** Three thin backend additions (an isolated `last_activity_at` aggregate query, two `PATCH` rename endpoints behind new admin-scoped authz actions, and a `POST .../clone` endpoint whose service method composes the existing reviewed `RevealConfig`→`SetSecrets` crypto path — no new crypto, no migration), plus a frontend pass on `ProjectsList` and `ProjectBoard`.

**Tech Stack:** Go (`net/http`+chi, pgx), React+TS+Tailwind+TanStack Query, Vitest+MSW.

---

## Reference facts (verified against the code)

- API service is `s.service` = `*secrets.Service`; its repo fields are `s.envs *store.EnvironmentRepo`, `s.configs *store.ConfigRepo`, `s.secrets *store.SecretRepo`.
- `secrets.Service.RevealConfig(ctx, configID) (store.ConfigVersion, map[string]Secret, error)` returns a config's **own** latest decrypted secrets and **emits no audit** (the handler records reveal audit, not the service).
- `secrets.Service.SetSecrets(ctx, configID string, changes []SecretChange, message, actor string)` writes a batch as one config version. `SecretChange{Key string; Value []byte; Delete bool}`. `Secret{Key; Value []byte; ValueVersion int}`.
- `secrets.Service.CreateConfig(ctx, environmentID, name, inheritsFrom *string)` enforces same-env inheritance; `CreateEnvironment(ctx, projectID, slug, name)`.
- Store: `ProjectRepo.ListPage`, `EnvironmentRepo.ListByProjectPage`, `ConfigRepo.ListByEnvironment(ctx, envID)`. `store.Project`/`store.Environment` structs already have `CreatedAt`/`UpdatedAt`. `projectResponse`/`envResponse` already serialize `created_at`.
- Authz actions in `internal/authz/actions.go`: `EnvCreate`/`EnvDelete`/`ProjectCreate` are admin+ (`adminActions` set at ~line 74); `viewerActions`/`developerActions` above it. Handlers use `s.authorize(w,r,action,res,auditAction,path)` (checks + writes deny audit) and `s.can(r,action,res)`; audit via `s.record(r,action,path,result,"","")`.
- Web: `api.patch<T>(path, body)` exists (`web/src/lib/api.ts`). `endpoints` object in `web/src/lib/endpoints.ts`. `relativeTime(iso)` in `web/src/lib/relativeTime.ts`. Glyph: `glyphClass(slug)` in `web/src/home/glyph.ts`.
- Web tests are watch-mode by default — run `npm test -- --run`. Repo typecheck gate is `tsconfig` **ES2020** (no `.at()`, no ES2022-only APIs). Dual-theme check: `npm run smoke`.

---

## Task 1: `ProjectRepo.LastActivity` aggregate query

**Files:**
- Modify: `internal/store/projects.go`
- Test: `internal/store/projects_test.go` (append)

- [ ] **Step 1: Write the failing test**

Add to `internal/store/projects_test.go` (uses the existing testcontainer harness — mirror an existing test's setup for `newTestStore`/`ProjectRepo`; look at the top of the file for the exact helper names and reuse them):

```go
func TestProjectRepo_LastActivity(t *testing.T) {
	st := newTestStore(t)                 // reuse this file's existing harness helper
	ctx := context.Background()
	pr := store.NewProjectRepo(st)
	er := store.NewEnvironmentRepo(st)
	cr := store.NewConfigRepo(st)
	sr := store.NewSecretRepo(st)

	// p1 has a config version; p2 has an env+config but no version; p3 is empty.
	p1, _ := pr.Create(ctx, "", "p1", "P1", []byte("k"), 1)
	p2, _ := pr.Create(ctx, "", "p2", "P2", []byte("k"), 1)
	p3, _ := pr.Create(ctx, "", "p3", "P3", []byte("k"), 1)
	e1, _ := er.Create(ctx, p1.ID, "dev", "Dev")
	c1, _ := cr.Create(ctx, e1.ID, "root", nil)
	e2, _ := er.Create(ctx, p2.ID, "dev", "Dev")
	_, _ = cr.Create(ctx, e2.ID, "root", nil)
	// SaveConfigVersion with an empty change set still stamps a version row.
	_, err := sr.SaveConfigVersion(ctx, c1.ID, nil, "init", "tester")
	if err != nil {
		t.Fatalf("save version: %v", err)
	}

	m, err := pr.LastActivity(ctx, []string{p1.ID, p2.ID, p3.ID})
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}
	if _, ok := m[p1.ID]; !ok {
		t.Errorf("p1 should have activity")
	}
	if _, ok := m[p2.ID]; ok {
		t.Errorf("p2 has no config version, should be absent")
	}
	if _, ok := m[p3.ID]; ok {
		t.Errorf("p3 is empty, should be absent")
	}
}
```

> If `ProjectRepo.Create`'s exact signature differs, match it to the one in `internal/store/projects.go:29`. If `SaveConfigVersion` rejects a nil change set, pass one `store.Change{Key:"K", Encrypt: ...}` following an existing store test that saves a version, or simplest: reuse whatever helper other store tests use to create a version.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestProjectRepo_LastActivity -v`
Expected: FAIL — `pr.LastActivity undefined`.

- [ ] **Step 3: Implement `LastActivity`**

Add to `internal/store/projects.go`:

```go
// LastActivity returns, for each given project id that has at least one config
// version, the timestamp of its most recent version (max config_versions.created_at
// across the project's live environments and configs). Ids with no activity are
// absent from the map. Empty input returns an empty map without querying.
func (r *ProjectRepo) LastActivity(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx, `
		SELECT p.id::text, max(cv.created_at)
		FROM projects p
		JOIN environments e ON e.project_id = p.id AND e.deleted_at IS NULL
		JOIN configs c ON c.environment_id = e.id AND c.deleted_at IS NULL
		JOIN config_versions cv ON cv.config_id = c.id
		WHERE p.id::text = ANY($1)
		GROUP BY p.id`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ts time.Time
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, mapError(err)
		}
		out[id] = ts
	}
	return out, mapError(rows.Err())
}
```

Ensure `import "time"` is present in `projects.go` (add to the import block if missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestProjectRepo_LastActivity -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/projects.go internal/store/projects_test.go
git commit -m "feat(store): ProjectRepo.LastActivity aggregate over config versions"
```

---

## Task 2: `EnvironmentRepo.LastActivity` aggregate query

**Files:**
- Modify: `internal/store/environments.go`
- Test: `internal/store/environments_test.go` (append; if the file doesn't exist, create it with the same harness import as `projects_test.go`)

- [ ] **Step 1: Write the failing test**

```go
func TestEnvironmentRepo_LastActivity(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pr := store.NewProjectRepo(st)
	er := store.NewEnvironmentRepo(st)
	cr := store.NewConfigRepo(st)
	sr := store.NewSecretRepo(st)

	p, _ := pr.Create(ctx, "", "p", "P", []byte("k"), 1)
	e1, _ := er.Create(ctx, p.ID, "dev", "Dev")   // will get a version
	e2, _ := er.Create(ctx, p.ID, "prod", "Prod") // stays empty
	c1, _ := cr.Create(ctx, e1.ID, "root", nil)
	if _, err := sr.SaveConfigVersion(ctx, c1.ID, nil, "init", "tester"); err != nil {
		t.Fatalf("save version: %v", err)
	}

	m, err := er.LastActivity(ctx, []string{e1.ID, e2.ID})
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}
	if _, ok := m[e1.ID]; !ok {
		t.Errorf("e1 should have activity")
	}
	if _, ok := m[e2.ID]; ok {
		t.Errorf("e2 empty, should be absent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestEnvironmentRepo_LastActivity -v`
Expected: FAIL — `er.LastActivity undefined`.

- [ ] **Step 3: Implement `LastActivity`**

Add to `internal/store/environments.go` (add `import "time"`):

```go
// LastActivity returns, for each given environment id that has at least one
// config version, the timestamp of its most recent version. Ids with no
// activity are absent. Empty input returns an empty map without querying.
func (r *EnvironmentRepo) LastActivity(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx, `
		SELECT e.id::text, max(cv.created_at)
		FROM environments e
		JOIN configs c ON c.environment_id = e.id AND c.deleted_at IS NULL
		JOIN config_versions cv ON cv.config_id = c.id
		WHERE e.id::text = ANY($1)
		GROUP BY e.id`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ts time.Time
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, mapError(err)
		}
		out[id] = ts
	}
	return out, mapError(rows.Err())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestEnvironmentRepo_LastActivity -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/environments.go internal/store/environments_test.go
git commit -m "feat(store): EnvironmentRepo.LastActivity aggregate over config versions"
```

---

## Task 3: Serialize `last_activity_at` on the list responses

**Files:**
- Modify: `internal/api/projects_handlers.go` (`projectResponse`, `handleProjectList`)
- Modify: `internal/api/environments_handlers.go` (`envResponse`, `handleEnvList`)
- Modify: `docs/openapi.yaml` (the two list responses)
- Test: `internal/api/projects_handlers_test.go`, `internal/api/environments_handlers_test.go` (append; reuse each file's existing server harness)

- [ ] **Step 1: Write the failing tests**

In `projects_handlers_test.go`, add a test that creates a project with a saved config version, GETs `/v1/projects`, and asserts the project's JSON has non-null `last_activity_at`; and a second project with no versions has `last_activity_at == null`. Mirror an existing handler test in that file for auth/setup (owner token). Assert on the parsed body:

```go
// after decoding {"projects":[...]} into a slice of map[string]any
// find the active project entry:
if got["last_activity_at"] == nil {
	t.Errorf("active project should have last_activity_at, got nil")
}
// for the empty project entry:
if empty["last_activity_at"] != nil {
	t.Errorf("empty project last_activity_at should be null, got %v", empty["last_activity_at"])
}
```

Add the analogous test in `environments_handlers_test.go` against `GET /v1/projects/{pid}/environments`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'ProjectList|EnvList' -v`
Expected: FAIL — key `last_activity_at` absent (nil for the active one too).

- [ ] **Step 3: Add the field + wire the aggregate**

In `projects_handlers.go`, add to `projectResponse`:

```go
LastActivityAt *string `json:"last_activity_at"`
```

Leave `projectView` unchanged (single-get has no activity). In `handleProjectList`, after building `out` (the authz-filtered slice) and before writing JSON, backfill activity for only the visible ids:

```go
ids := make([]string, len(out))
for i := range out {
	ids[i] = out[i].ID
}
act, err := store.NewProjectRepo(s.st).LastActivity(r.Context(), ids)
if err != nil {
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	return
}
for i := range out {
	if ts, ok := act[out[i].ID]; ok {
		v := ts.UTC().Format(time.RFC3339)
		out[i].LastActivityAt = &v
	}
}
```

Do the equivalent in `environments_handlers.go`: add `LastActivityAt *string \`json:"last_activity_at"\`` to `envResponse`, and in `handleEnvList` collect `out[i].ID`, call `store.NewEnvironmentRepo(s.st).LastActivity(...)`, and set the pointers. (`time` is already imported in both handler files.)

- [ ] **Step 4: Update OpenAPI**

In `docs/openapi.yaml`, add to the project and environment list-item schemas:

```yaml
last_activity_at:
  type: string
  format: date-time
  nullable: true
  description: Most recent config-version timestamp across the entity's configs; null if none.
```

- [ ] **Step 5: Run tests to verify pass (incl. drift test)**

Run: `go test ./internal/api/ -run 'ProjectList|EnvList|OpenAPI' -v`
Expected: PASS (drift test still green — no new routes).

- [ ] **Step 6: Commit**

```bash
git add internal/api/projects_handlers.go internal/api/environments_handlers.go internal/api/projects_handlers_test.go internal/api/environments_handlers_test.go docs/openapi.yaml
git commit -m "feat(api): expose last_activity_at on project & environment lists"
```

---

## Task 4: New authz actions `project:update` and `env:update`

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/actions_test.go`

- [ ] **Step 1: Write the failing test**

In `actions_test.go`, add `ProjectUpdate` and `EnvUpdate` to the `allActions` slice (top of file) and to the admin & owner expected-set entries in the role→actions matrix; add negative expectations that viewer and developer do **not** have them. Follow the exact structure already in that file (it enumerates each role's granted actions).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/authz/ -run TestRole -v`
Expected: FAIL — `undefined: authz.ProjectUpdate` / `EnvUpdate`.

- [ ] **Step 3: Add the actions**

In `internal/authz/actions.go`, add the constants next to `EnvCreate`:

```go
ProjectUpdate    Action = "project:update" // rename, admin+ project-scoped
EnvUpdate        Action = "env:update"     // rename, admin+ project-scoped
```

Add both to the `adminActions` set (the `union(..., setOf(ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage, ...))` line) so admins and owners inherit them.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/authz/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/actions_test.go
git commit -m "feat(authz): add admin+ project:update and env:update actions"
```

---

## Task 5: `UpdateName` repo methods

**Files:**
- Modify: `internal/store/projects.go`, `internal/store/environments.go`
- Test: `internal/store/projects_test.go`, `internal/store/environments_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestProjectRepo_UpdateName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pr := store.NewProjectRepo(st)
	p, _ := pr.Create(ctx, "", "p", "Old", []byte("k"), 1)
	if err := pr.UpdateName(ctx, p.ID, "New"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	got, _ := pr.Get(ctx, p.ID)
	if got.Name != "New" {
		t.Errorf("name = %q, want New", got.Name)
	}
	if err := pr.UpdateName(ctx, "00000000-0000-0000-0000-000000000000", "X"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing id: want ErrNotFound, got %v", err)
	}
}
```

Add the analogous `TestEnvironmentRepo_UpdateName` (create project+env, rename env, assert, missing-id → ErrNotFound).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run UpdateName -v`
Expected: FAIL — `UpdateName undefined`.

- [ ] **Step 3: Implement**

`internal/store/projects.go`:

```go
// UpdateName sets a project's display name. Slug is immutable. ErrNotFound if
// the project does not exist or is soft-deleted.
func (r *ProjectRepo) UpdateName(ctx context.Context, id, name string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE projects SET name = $2, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id, name)
}
```

`internal/store/environments.go`:

```go
// UpdateName sets an environment's display name. Slug is immutable. ErrNotFound
// if the environment does not exist or is soft-deleted.
func (r *EnvironmentRepo) UpdateName(ctx context.Context, id, name string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE environments SET name = $2, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id, name)
}
```

(`execAffectingOne` already maps zero rows → `ErrNotFound`; see `SoftDelete`.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run UpdateName -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/projects.go internal/store/environments.go internal/store/projects_test.go internal/store/environments_test.go
git commit -m "feat(store): UpdateName for projects and environments (name-only)"
```

---

## Task 6: Rename handlers + routes

**Files:**
- Modify: `internal/api/projects_handlers.go`, `internal/api/environments_handlers.go`
- Modify: `internal/api/server.go` (register 2 routes)
- Modify: `docs/openapi.yaml`
- Test: `internal/api/projects_handlers_test.go`, `internal/api/environments_handlers_test.go`

- [ ] **Step 1: Write the failing tests**

In `projects_handlers_test.go`, add tests for `PATCH /v1/projects/{pid}`:
- owner token, body `{"name":"Renamed"}` → 200, response `name == "Renamed"`, `slug` unchanged.
- developer token → 403 (developers lack `project:update`).
- empty/whitespace name → 400 `CodeValidation`.
- unknown pid → 404.

Add the analogous set for `PATCH /v1/projects/{pid}/environments/{eid}` in `environments_handlers_test.go`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'ProjectRename|EnvRename' -v`
Expected: FAIL — route not found (404 for the happy path with wrong reason / handler undefined).

- [ ] **Step 3: Implement the handlers**

`projects_handlers.go`:

```go
type renameRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleProjectRename(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.ProjectUpdate, authz.Resource{ProjectID: pid}, "project.update", "projects/"+pid) {
		return
	}
	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name is required")
		return
	}
	repo := store.NewProjectRepo(s.st)
	if err := repo.UpdateName(r.Context(), pid, req.Name); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.update", "projects/"+pid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	p, err := repo.Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectView(p))
}
```

Add `"strings"` to the imports of `projects_handlers.go` if not present.

`environments_handlers.go` (reuse the same `renameRequest` type — it lives in `projects_handlers.go`, same package):

```go
func (s *Server) handleEnvRename(w http.ResponseWriter, r *http.Request) {
	eid := chi.URLParam(r, "eid")
	res, err := s.resolveScopeResource(r.Context(), "environment", eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.EnvUpdate, res, "env.update", "environments/"+eid) {
		return
	}
	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name is required")
		return
	}
	repo := store.NewEnvironmentRepo(s.st)
	if err := repo.UpdateName(r.Context(), eid, req.Name); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.update", "environments/"+eid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	e, err := repo.Get(r.Context(), eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envView(e))
}
```

Add `"strings"` to `environments_handlers.go` imports.

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, in the group that has the project mutation routes (near `handleProjectDelete`/`handleProjectRestore`, ~line 248) add:

```go
r.Patch("/v1/projects/{pid}", s.handleProjectRename)
```

In the environments group (~line 278) add:

```go
r.Patch("/v1/projects/{pid}/environments/{eid}", s.handleEnvRename)
```

- [ ] **Step 5: Update OpenAPI**

Add both `patch` operations to `docs/openapi.yaml` (request body `{name}`, 200 with the project/env schema, 400/403/404). Keep examples value-free.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/api/ -run 'ProjectRename|EnvRename|OpenAPI' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ docs/openapi.yaml
git commit -m "feat(api): PATCH rename for projects and environments (name-only)"
```

---

## Task 7: `secrets.Service.CloneEnvironment`

**Files:**
- Create: `internal/secrets/clone.go`
- Test: `internal/secrets/clone_test.go`

- [ ] **Step 1: Write the failing test**

The `internal/secrets` package has an integration harness (`harness_test.go`). Reuse it to build a `*Service` with a real store + unsealed keyring. Write:

```go
func TestCloneEnvironment(t *testing.T) {
	h := newHarness(t)                 // reuse this package's existing harness constructor
	ctx := context.Background()
	svc := h.svc

	// Source env: root config with a secret, plus a branch config inheriting root.
	p := h.newProject(t, "app")
	src := mustEnv(t, svc, p.ID, "dev", "Dev")
	root := mustConfig(t, svc, src.ID, "root", nil)
	branch := mustConfig(t, svc, src.ID, "branch", &root.ID)
	if _, err := svc.SetSecrets(ctx, root.ID, []secrets.SecretChange{{Key: "API_KEY", Value: []byte("v1")}}, "", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSecrets(ctx, branch.ID, []secrets.SecretChange{{Key: "BRANCH_ONLY", Value: []byte("b1")}}, "", "tester"); err != nil {
		t.Fatal(err)
	}

	newEnv, err := svc.CloneEnvironment(ctx, p.ID, src.ID, "staging", "Staging", "tester")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// The new env has both configs; inheritance is remapped (not pointing at source ids).
	cfgs, _ := svc.ListConfigs(ctx, newEnv.ID) // or the store ConfigRepo.ListByEnvironment
	var newRoot, newBranch *store.Config
	for _, c := range cfgs {
		switch c.Name {
		case "root":
			newRoot = c
		case "branch":
			newBranch = c
		}
	}
	if newRoot == nil || newBranch == nil {
		t.Fatal("both configs should be cloned")
	}
	if newBranch.InheritsFrom == nil || *newBranch.InheritsFrom != newRoot.ID {
		t.Errorf("branch should inherit the NEW root id, got %v", newBranch.InheritsFrom)
	}
	// Own values copied and decryptable under the new config's AAD.
	s, err := svc.GetSecret(ctx, newRoot.ID, "API_KEY")
	if err != nil || string(s.Value) != "v1" {
		t.Errorf("cloned root API_KEY = %q err=%v, want v1", s.Value, err)
	}
	b, err := svc.GetSecret(ctx, newBranch.ID, "BRANCH_ONLY")
	if err != nil || string(b.Value) != "b1" {
		t.Errorf("cloned branch BRANCH_ONLY = %q err=%v, want b1", b.Value, err)
	}
}
```

> Adapt helper names (`newHarness`, `h.svc`, `mustEnv`, `mustConfig`, `ListConfigs`) to whatever `harness_test.go` and the service already expose. If the service has no `ListConfigs`, use `store.NewConfigRepo(...).ListByEnvironment` in the test. `store.Config.InheritsFrom` is the field name (verify in `internal/store` types).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/secrets/ -run TestCloneEnvironment -v`
Expected: FAIL — `svc.CloneEnvironment undefined`.

- [ ] **Step 3: Implement `CloneEnvironment`**

Create `internal/secrets/clone.go`:

```go
package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/store"
)

// CloneEnvironment creates a new environment in the same project and deep-copies
// the source environment's config tree and each config's own latest secrets.
//
// Inheritance is preserved: configs are created in topological order and each
// branch's inherits_from is remapped from the source config id to the freshly
// created one. Secret values are re-encrypted under the new config's AAD via the
// normal SetSecrets write path (the value AAD binds config_id, so blobs cannot be
// copied verbatim). No secret value is logged or audited here — the caller emits
// a single value-free env.clone event.
func (s *Service) CloneEnvironment(ctx context.Context, projectID, srcEnvID, newSlug, newName, actor string) (*store.Environment, error) {
	newEnv, err := s.CreateEnvironment(ctx, projectID, newSlug, newName)
	if err != nil {
		return nil, err
	}

	srcConfigs, err := s.configs.ListByEnvironment(ctx, srcEnvID)
	if err != nil {
		return nil, mapStoreErr(err)
	}

	// Topological order: a config is created only after any config it inherits
	// from within this env has been created (so we can remap inherits_from).
	// idMap: source config id -> new config id.
	idMap := make(map[string]string, len(srcConfigs))
	remaining := append([]*store.Config(nil), srcConfigs...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, c := range remaining {
			var newInherits *string
			if c.InheritsFrom != nil {
				mapped, ok := idMap[*c.InheritsFrom]
				if !ok {
					// Parent not created yet (or points outside this env slice) —
					// defer. If it never resolves we drop the link below.
					next = append(next, c)
					continue
				}
				newInherits = &mapped
			}
			nc, err := s.CreateConfig(ctx, newEnv.ID, c.Name, newInherits)
			if err != nil {
				return nil, err
			}
			idMap[c.ID] = nc.ID
			progressed = true

			// Copy this config's own latest secrets (decrypt → re-encrypt).
			if err := s.copyOwnSecrets(ctx, c.ID, nc.ID, actor); err != nil {
				return nil, err
			}
		}
		remaining = next
		if !progressed {
			// A cycle or an inherits_from pointing outside this env: create the
			// stragglers with no inheritance link rather than looping forever.
			for _, c := range remaining {
				nc, err := s.CreateConfig(ctx, newEnv.ID, c.Name, nil)
				if err != nil {
					return nil, err
				}
				idMap[c.ID] = nc.ID
				if err := s.copyOwnSecrets(ctx, c.ID, nc.ID, actor); err != nil {
					return nil, err
				}
			}
			break
		}
	}
	return newEnv, nil
}

// copyOwnSecrets reads a source config's own latest secrets and writes them into
// the destination config as one new version. Plaintext lives only transiently in
// memory; nothing is logged or audited here.
func (s *Service) copyOwnSecrets(ctx context.Context, srcConfigID, dstConfigID, actor string) error {
	_, state, err := s.RevealConfig(ctx, srcConfigID)
	if err != nil {
		return err
	}
	if len(state) == 0 {
		return nil
	}
	changes := make([]SecretChange, 0, len(state))
	for _, sec := range state {
		changes = append(changes, SecretChange{Key: sec.Key, Value: sec.Value})
	}
	_, err = s.SetSecrets(ctx, dstConfigID, changes, "Cloned environment", actor)
	return err
}
```

> Verify `store.Config.InheritsFrom` is the exact field name; adjust if it's `Inherits` etc. `RevealConfig` returns `map[string]Secret` keyed by key — the `Secret.Key` field is set, so ranging over values is fine.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/secrets/ -run TestCloneEnvironment -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/clone.go internal/secrets/clone_test.go
git commit -m "feat(secrets): CloneEnvironment deep-copies config tree + own secrets"
```

---

## Task 8: Clone handler + route + leak test

**Files:**
- Modify: `internal/api/environments_handlers.go`
- Modify: `internal/api/server.go` (1 route)
- Modify: `docs/openapi.yaml`
- Test: `internal/api/environments_handlers_test.go`, and extend the existing log-leak test

- [ ] **Step 1: Write the failing tests**

In `environments_handlers_test.go` add tests for `POST /v1/projects/{pid}/environments/{eid}/clone`:
- admin/owner token, body `{"slug":"staging","name":"Staging"}`, source env with a secret → 201, response is the new env (different id, `slug=="staging"`); a follow-up reveal of the cloned config returns the same secret value.
- developer token → 403 (developer lacks `env:create`).
- duplicate slug (clone into an existing env slug) → 409 `CodeConflict`.
- assert an audit event `env.clone` was recorded (query the audit store as sibling tests do) and that it contains **no** secret value.

Extend the repo-wide log/audit leak test (the one that greps captured logs for known secret values — search `internal/api` for the existing leak test) to run a clone of a config containing a sentinel secret value and assert the sentinel never appears in logs or audit details.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'EnvClone|Leak' -v`
Expected: FAIL — route/handler undefined.

- [ ] **Step 3: Implement the handler**

`environments_handlers.go`:

```go
type cloneEnvRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func (s *Server) handleEnvClone(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	eid := chi.URLParam(r, "eid")
	// Authorize env creation on the project (admin+). Source read is implied by
	// the same admin scope.
	if !s.authorize(w, r, authz.EnvCreate, authz.Resource{ProjectID: pid}, "env.clone", "environments/"+eid) {
		return
	}
	var req cloneEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}
	newEnv, err := s.service.CloneEnvironment(r.Context(), pid, eid, req.Slug, req.Name, actorOf(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.clone", "environments/"+newEnv.ID, "success", "", "from:"+eid); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, envView(newEnv))
}
```

> `actorOf(r)` is the helper used by `secrets_write_handlers.go`; confirm the exact name and reuse it. The audit `detail` carries only the source env id — value-free.

- [ ] **Step 4: Register the route**

In `server.go`, in the environments group (~line 278):

```go
r.Post("/v1/projects/{pid}/environments/{eid}/clone", s.handleEnvClone)
```

- [ ] **Step 5: Update OpenAPI**

Add the `post .../clone` operation (request `{slug,name}`, 201 env schema, 400/403/404/409) to `docs/openapi.yaml`.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/api/ -run 'EnvClone|Leak|OpenAPI' -v`
Expected: PASS.

- [ ] **Step 7: Full backend gate**

Run: `go test ./... -race` then `gosec ./...` and `govulncheck ./...`
Expected: all green. (If Docker isn't available for testcontainer tests, note it and run non-integration packages; the merge environment runs the full suite.)

- [ ] **Step 8: Commit**

```bash
git add internal/api/ docs/openapi.yaml
git commit -m "feat(api): POST clone-environment (admin+, value-free env.clone audit)"
```

---

## Task 9: Web endpoints + types

**Files:**
- Modify: `web/src/lib/endpoints.ts`
- Test: none (thin; covered by consumer tests)

- [ ] **Step 1: Extend the types and endpoints**

In `web/src/lib/endpoints.ts`:

```ts
export interface Project { id: string; slug: string; name: string; created_at?: string; last_activity_at?: string | null }
export interface Environment { id: string; slug: string; name: string; created_at?: string; last_activity_at?: string | null }
```

Add to the `endpoints` object:

```ts
renameProject: (pid: string, name: string) =>
  api.patch<Project>(`/v1/projects/${pid}`, { name }),
renameEnvironment: (pid: string, eid: string, name: string) =>
  api.patch<Environment>(`/v1/projects/${pid}/environments/${eid}`, { name }),
cloneEnvironment: (pid: string, eid: string, slug: string, name: string) =>
  api.post<Environment>(`/v1/projects/${pid}/environments/${eid}/clone`, { slug, name }),
```

- [ ] **Step 2: Typecheck**

Run: `cd web && npm run typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/endpoints.ts
git commit -m "feat(web): rename + clone endpoints; created_at/last_activity_at types"
```

---

## Task 10: `recencyLabel` helper

**Files:**
- Create: `web/src/home/recency.ts`
- Test: `web/src/home/recency.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, it, expect } from 'vitest'
import { recencyLabel } from './recency'

describe('recencyLabel', () => {
  it('prefers activity when present', () => {
    const l = recencyLabel({ created_at: '2020-01-01T00:00:00Z', last_activity_at: '2020-06-01T00:00:00Z' })
    expect(l).toMatch(/^active /)
  })
  it('falls back to created when no activity', () => {
    const l = recencyLabel({ created_at: '2020-01-01T00:00:00Z', last_activity_at: null })
    expect(l).toMatch(/^created /)
  })
  it('returns empty string when nothing known', () => {
    expect(recencyLabel({})).toBe('')
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/home/recency.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```ts
import { relativeTime } from '../lib/relativeTime'

/** Card recency line: "active 2h ago" if there's activity, else "created 3d ago", else "". */
export function recencyLabel(x: { created_at?: string; last_activity_at?: string | null }): string {
  if (x.last_activity_at) return `active ${relativeTime(x.last_activity_at)}`
  if (x.created_at) return `created ${relativeTime(x.created_at)}`
  return ''
}
```

> Confirm `relativeTime` returns a bare "2h ago" style string (check `web/src/lib/relativeTime.ts`); if it returns "2 hours ago" the regex in the test still matches `^active `. Keep the label composition as-is.

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/home/recency.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/home/recency.ts web/src/home/recency.test.ts
git commit -m "feat(web): recencyLabel helper for project/env cards"
```

---

## Task 11: ProjectsList — glyph, recency line, richer sort

**Files:**
- Modify: `web/src/home/ProjectsList.tsx`
- Test: `web/src/home/ProjectsList.test.tsx`

- [ ] **Step 1: Write the failing tests**

Add tests (mirror the existing render/setup in `ProjectsList.test.tsx`, which already mocks `endpoints`):
- renders a project glyph badge (assert an element with the project initial and a `bg-glyph-*` class, or a stable `data-testid` you add).
- renders a recency line: with `last_activity_at` set, text matches `/active /`; with only `created_at`, `/created /`.
- sort select offers `Newest`, `Oldest`, `Recently active` options; selecting `Newest` orders a project with a later `created_at` before an earlier one (assert DOM order of names).

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/home/ProjectsList.test.tsx`
Expected: FAIL — new options/glyph/recency absent.

- [ ] **Step 3: Implement**

In `ProjectsList.tsx`:
- Extend `type Sort` to `'name-asc' | 'name-desc' | 'created-desc' | 'created-asc' | 'activity-desc'`.
- Add options to the `<select>`: `Newest` (`created-desc`), `Oldest` (`created-asc`), `Recently active` (`activity-desc`).
- Extend the `shown` sort:

```ts
list.sort((a, b) => {
  switch (sort) {
    case 'name-asc': return a.name.localeCompare(b.name)
    case 'name-desc': return b.name.localeCompare(a.name)
    case 'created-desc': return (b.created_at ?? '').localeCompare(a.created_at ?? '')
    case 'created-asc': return (a.created_at ?? '').localeCompare(b.created_at ?? '')
    case 'activity-desc': {
      // nulls last
      const av = a.last_activity_at ?? '', bv = b.last_activity_at ?? ''
      if (av && bv) return bv.localeCompare(av)
      if (av) return -1
      if (bv) return 1
      return a.name.localeCompare(b.name)
    }
  }
})
```

(ISO-8601 UTC strings sort lexicographically = chronologically, so string compare is correct.)
- In `ProjectCard`, add the glyph badge (copy the `glyphClass(project.slug)` + initial pattern from `HomeProjects.tsx`) and a recency line using `recencyLabel(project)` rendered in `text-ink-faint` (only when non-empty).

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/home/ProjectsList.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/home/ProjectsList.tsx web/src/home/ProjectsList.test.tsx
git commit -m "feat(web): projects list glyph, recency line, created/activity sort"
```

---

## Task 12: ProjectsList — quick-action menu + Rename dialog

**Files:**
- Create: `web/src/home/CardMenu.tsx` (small reusable ⋯ menu) and `web/src/structure/RenameDialog.tsx`
- Modify: `web/src/home/ProjectsList.tsx`
- Test: `web/src/home/ProjectsList.test.tsx`, `web/src/structure/RenameDialog.test.tsx`

- [ ] **Step 1: Write the failing tests**

`RenameDialog.test.tsx`: renders with the current name pre-filled; typing a new name and clicking Save calls `onSubmit(newName)`; empty name disables Save; Esc/Cancel calls `onClose`.

`ProjectsList.test.tsx`: opening a card's `⋯` menu shows `Rename` and `Delete`; clicking `Rename` opens the dialog; submitting calls `endpoints.renameProject` (mock) and invalidates `['projects']` (assert the mock was called with the new name).

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/home/ProjectsList.test.tsx src/structure/RenameDialog.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `RenameDialog.tsx`: a small controlled dialog built on the existing modal/`ConfirmDialog` idiom (reuse the project's dialog primitive — check how `CreateForms.tsx` builds its modal and follow it). Props `{ title, initial, onSubmit(name), onClose }`; trims input; Save disabled when empty or unchanged; token styling only.
- `CardMenu.tsx`: a keyboard-accessible ⋯ dropdown (button + list). If the codebase already has a menu primitive (search `web/src/ui` for a Menu/Dropdown), reuse it instead of creating `CardMenu`. Otherwise implement a minimal one: opens on click, closes on select/Esc/outside-click, `aria-haspopup`/`aria-expanded`.
- In `ProjectCard`, replace the always-hover trash button with a ⋯ menu containing `Rename` (opens `RenameDialog` → `endpoints.renameProject(project.id, name)` mutation, toast, invalidate `['projects']`) and `Delete` (the existing `ConfirmDialog` soft-delete). Preserve current delete behavior/labels.

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/home/ProjectsList.test.tsx src/structure/RenameDialog.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/home/ProjectsList.tsx web/src/home/CardMenu.tsx web/src/structure/RenameDialog.tsx web/src/home/ProjectsList.test.tsx web/src/structure/RenameDialog.test.tsx
git commit -m "feat(web): project card quick-action menu with rename"
```

---

## Task 13: ProjectBoard — env header menu (rename / clone / delete)

**Files:**
- Create: `web/src/structure/CloneEnvDialog.tsx`
- Modify: `web/src/home/ProjectBoard.tsx`
- Test: `web/src/home/ProjectBoard.test.tsx`, `web/src/structure/CloneEnvDialog.test.tsx`

- [ ] **Step 1: Write the failing tests**

`CloneEnvDialog.test.tsx`: two inputs (slug, name); Save disabled until slug non-empty; submitting calls `onSubmit(slug, name)`.

`ProjectBoard.test.tsx`: the env column header exposes a `⋯` menu with `Rename`, `Clone environment`, `Delete`; `Rename` opens `RenameDialog` → calls `endpoints.renameEnvironment`; `Clone environment` opens `CloneEnvDialog` → calls `endpoints.cloneEnvironment` and invalidates `['envs', pid]` + `['configs', pid, ...]`. Also assert the column shows a recency subline when `last_activity_at` is present. (Reuse the existing ProjectBoard test setup/mocks.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/home/ProjectBoard.test.tsx src/structure/CloneEnvDialog.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `CloneEnvDialog.tsx`: modal with slug + name inputs (reuse the `CreateEnvironmentForm` field styling), `onSubmit(slug, name)`; token styling.
- In `EnvColumn`, replace the standalone delete button with the `CardMenu` (from Task 12) offering:
  - `Rename` → `RenameDialog` (`endpoints.renameEnvironment(pid, env.id, name)`, toast, invalidate `['envs', pid]`).
  - `Clone environment` → `CloneEnvDialog` (`endpoints.cloneEnvironment(pid, env.id, slug, name)`, toast, on success invalidate `['envs', pid]` and `['configs', pid]`-prefixed queries).
  - `Delete` → existing `ConfirmDialog` soft-delete (unchanged).
- Thread `env.last_activity_at` into `EnvColumn` and render `recencyLabel(env)` as a small `text-ink-faint` subline under the header (only when non-empty). `Environment` from the env list already carries the field once Task 9 lands; confirm `useEnvironments` returns the list objects verbatim.

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/home/ProjectBoard.test.tsx src/structure/CloneEnvDialog.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/home/ProjectBoard.tsx web/src/structure/CloneEnvDialog.tsx web/src/home/ProjectBoard.test.tsx web/src/structure/CloneEnvDialog.test.tsx
git commit -m "feat(web): board env-header menu — rename, clone, delete + recency subline"
```

---

## Task 14: ProjectBoard — inheritance connectors

**Files:**
- Modify: `web/src/home/ProjectBoard.tsx`
- Test: `web/src/home/ProjectBoard.test.tsx`

- [ ] **Step 1: Write the failing test**

Add a test: given a root config and a child config with `inherits_from` = root, the child's rendered node has a connector element (assert a stable `data-testid="inherit-connector"` you add on the connector span, present only for `depth > 0`). Root (depth 0) has none.

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/home/ProjectBoard.test.tsx`
Expected: FAIL — no connector element.

- [ ] **Step 3: Implement**

In `ConfigCard`/`ConfigNodes`, replace the bare `↳` + `ml-4` indent with a left rail + elbow connector for `depth > 0`:
- Wrap the indented child in a container with `relative pl-4`; add an absolutely-positioned connector `<span data-testid="inherit-connector" aria-hidden className="absolute left-0 top-0 h-full w-px bg-line">` plus a short horizontal elbow `border-b border-line` at the row's vertical center. Keep the existing `↳` glyph or fold it into the elbow. Token colors only (`bg-line`/`border-line`), no raw palette. Respect the existing cycle-safe `seen`-set walk (unchanged).

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/home/ProjectBoard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/home/ProjectBoard.tsx web/src/home/ProjectBoard.test.tsx
git commit -m "feat(web): inheritance connector lines on the project board"
```

---

## Task 15: Full gate + dual-theme smoke + docs

**Files:**
- Modify: `gaps.md` (mark §2.4 done)

- [ ] **Step 1: Full web suite + typecheck + smoke**

Run: `cd web && npm test -- --run && npm run typecheck && npm run smoke`
Expected: all PASS; smoke shows both light and dark render clean.

- [ ] **Step 2: Full backend gate**

Run: `go test ./... -race && gosec ./... && govulncheck ./...`
Expected: all green.

- [ ] **Step 3: no-raw-palette guard**

Run: `cd web && npm test -- --run src/test/no-raw-palette.test.ts`
Expected: PASS (all new components use token classes only).

- [ ] **Step 4: Update gaps.md**

Edit §2.4 to mark card metadata (glyph/recency), created/activity sort + quick-action menu, env-column rename/clone, and inheritance connectors as DONE (dated 2026-07-17), noting author metadata remains a non-goal (no column) and `janus env clone` CLI is a possible follow-up.

- [ ] **Step 5: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark §2.4 projects/board depth done"
```

---

## Self-review checklist (author, before handoff)

- **Spec coverage:** A (metadata) → Tasks 1–3; B (rename) → Tasks 4–6; C (clone) → Tasks 7–8; D (frontend) → Tasks 9–14; testing/guardrails → Task 15. All covered.
- **Type consistency:** `LastActivity(ctx, ids) map[string]time.Time`, `UpdateName(ctx, id, name)`, `CloneEnvironment(ctx, projectID, srcEnvID, newSlug, newName, actor) (*store.Environment, error)`, `recencyLabel(x)`, endpoints `renameProject`/`renameEnvironment`/`cloneEnvironment` — used identically across backend/frontend tasks.
- **No migration** anywhere; `created_at` already serialized (Task 3 only adds `last_activity_at`).
- **Value-free:** clone reuses `RevealConfig`→`SetSecrets` (no reveal audit at service layer); handler emits one `env.clone` event with only ids; leak test extended (Task 8).
- **Open verification points flagged inline** (harness helper names, `store.Config.InheritsFrom` field name, `actorOf`, existing menu primitive) — the implementer confirms against the code, no guessing baked in.
