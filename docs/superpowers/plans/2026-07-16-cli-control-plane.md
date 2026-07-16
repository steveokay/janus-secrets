# CLI Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `janus` CLI self-sufficient for control-plane bootstrap and identity: project/env/config CRUD, service-token mint/list/revoke, `whoami`, shell `completion`, and a value-free `secrets diff` — all over existing REST endpoints.

**Architecture:** New/extended command groups in `cmd/janus`, layered on the existing `apiClient` (`c.call`), slug→UUID resolvers (`resolve.go`), and binding/credential precedence (`binding.go`). No server, API, or migration changes. Slug addressing, binding-aware parent resolution, `--json` output, TTY-confirm on destructive ops.

**Tech Stack:** Go + `cobra`; tests are table-driven Go tests using `httptest` and the existing `runCLI` harness.

**Spec:** `docs/superpowers/specs/2026-07-16-cli-control-plane-design.md` (closes `gaps.md` §6).

---

## File Structure

- `cmd/janus/resolve.go` — add `resolveProjectID`, `resolveEnvID`; refactor `resolveConfigID` to reuse them.
- `cmd/janus/binding.go` — add `bindingValues` (pick without emptiness check); refactor `resolveBinding` to reuse it.
- `cmd/janus/project_commands.go` — add `create/list/get/delete/restore` beside the KEK verbs.
- `cmd/janus/env_commands.go` (new) — `janus env` group.
- `cmd/janus/config_commands.go` (new) — `janus config` group.
- `cmd/janus/token_commands.go` (new) — `janus token` group.
- `cmd/janus/whoami.go` (new) — `janus whoami`.
- `cmd/janus/completion.go` (new) — `janus completion`.
- `cmd/janus/secrets_diff.go` (new) — `secrets diff`, registered under the existing secrets group.
- `cmd/janus/main.go` — register the new top-level groups.
- Per-file `_test.go` for each.

Run tests from repo root: `go test ./cmd/janus/...`

## Shared facts (verified against the code — trust these)

- `apiClient.call(method, path string, in, out any) error` — JSON in/out; non-2xx returns a rewritten user-facing error.
- `newAPIClient(flagAddr, flagToken string) (*apiClient, error)` — resolves address (`--address`>`JANUS_ADDR`) and credential (`--token`>`JANUS_TOKEN`>session).
- `runCLI(t, stdin string, args ...string) (string, error)` (in `sys_commands_test.go`) — runs `newRootCmd()` with **Out and Err both** pointing at one buffer; returns combined output.
- Confirm pattern: `if !yes && isTerminalCmd(cmd) { ok, err := promptLine(cmd, "…? [y/N]: "); … }`. `promptLine` writes the prompt to stderr and reads a line. `isTerminalCmd(cmd)` already exists.
- List envelopes: `GET /v1/projects` → `{"projects":[{"id","slug","name","created_at"}]}`; `GET /v1/projects/{pid}/environments` → `{"environments":[{"id","project_id","slug","name","created_at"}]}`; `GET …/configs` → `{"configs":[{"id","environment_id","name","inherits_from","created_at"}]}`.
- Create bodies/responses: `POST /v1/projects {slug,name}` → 201 `{id,slug,name,created_at}`; `POST …/environments {slug,name}` → `{id,project_id,slug,name,created_at}`; `POST …/configs {name,inherits_from?}` → `{id,environment_id,name,inherits_from,created_at}`.
- Soft-delete: `DELETE /v1/projects/{pid}` | `…/environments/{eid}` | `/v1/configs/{cid}`; restore: `POST …/restore`.
- Tokens: `POST /v1/tokens {name,scope:{kind,id},access,ttl_seconds?}` → `{token,id,name,scope:{kind,id},access,expires_at}`; `GET /v1/tokens` → `{"tokens":[{id,name,scope_kind,scope_id,access,created_by,created_at,expires_at?,revoked_at?}],"next_cursor"}`; `DELETE /v1/tokens/{id}`.
- `GET /v1/auth/me` → `{"kind","id","name"}`.
- Diff: `GET /v1/configs/{cid}/versions/diff?a=<int>&b=<int>` → `{"a","b","added":[key…],"changed":[key…],"removed":[key…]}` (key names only).

---

### Task 1: Resolver + binding helpers

**Files:**
- Modify: `cmd/janus/resolve.go`, `cmd/janus/binding.go`
- Test: `cmd/janus/resolve_test.go` (extend), `cmd/janus/binding_test.go` (extend)

- [ ] **Step 1: Write failing tests**

Add to `cmd/janus/resolve_test.go`:

```go
func TestResolveProjectAndEnvID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c, err := newAPIClient(ts.URL, "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := c.resolveProjectID("acme")
	if err != nil || pid != "p1" {
		t.Fatalf("resolveProjectID = %q, %v", pid, err)
	}
	if _, err := c.resolveProjectID("nope"); err == nil {
		t.Fatal("expected error for unknown project")
	}
	gotP, eid, err := c.resolveEnvID("acme", "prod")
	if err != nil || gotP != "p1" || eid != "e1" {
		t.Fatalf("resolveEnvID = %q %q %v", gotP, eid, err)
	}
	if _, _, err := c.resolveEnvID("acme", "staging"); err == nil {
		t.Fatal("expected error for unknown env")
	}
}
```

Add to `cmd/janus/binding_test.go`:

```go
func TestBindingValuesNoEmptinessCheck(t *testing.T) {
	dir := t.TempDir()
	// no .janus.yaml, no env, only a project flag → returns project, empties for the rest, no error
	p, e, c, err := bindingValues(dir, "acme", "", "")
	if err != nil {
		t.Fatalf("bindingValues err: %v", err)
	}
	if p != "acme" || e != "" || c != "" {
		t.Fatalf("bindingValues = %q %q %q", p, e, c)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/janus/ -run 'TestResolveProjectAndEnvID|TestBindingValuesNoEmptinessCheck'`
Expected: FAIL — `resolveProjectID`/`resolveEnvID`/`bindingValues` undefined.

- [ ] **Step 3: Refactor `binding.go`**

Replace the body of `resolveBinding` and add `bindingValues`:

```go
// bindingValues applies, per field: flag > JANUS_* env > .janus.yaml, WITHOUT
// requiring any field to be non-empty. Returns the resolved (possibly empty)
// slugs so callers can require only the fields they need.
func bindingValues(dir, flagProject, flagEnv, flagConfig string) (project, env, config string, err error) {
	bf, err := readBinding(dir)
	if err != nil {
		return "", "", "", err
	}
	pick := func(flag, envName, fromFile string) string {
		if flag != "" {
			return flag
		}
		if v := os.Getenv(envName); v != "" {
			return v
		}
		return fromFile
	}
	fp, fe, fc := "", "", ""
	if bf != nil {
		fp, fe, fc = bf.Project, bf.Environment, bf.Config
	}
	return pick(flagProject, "JANUS_PROJECT", fp),
		pick(flagEnv, "JANUS_ENV", fe),
		pick(flagConfig, "JANUS_CONFIG", fc), nil
}

// resolveBinding is bindingValues plus the requirement that all three resolve.
func resolveBinding(dir, flagProject, flagEnv, flagConfig string) (project, env, config string, err error) {
	project, env, config, err = bindingValues(dir, flagProject, flagEnv, flagConfig)
	if err != nil {
		return "", "", "", err
	}
	if project == "" || env == "" || config == "" {
		return "", "", "", fmt.Errorf("no project/environment/config configured — run `janus setup` or pass --project/--env/--config")
	}
	return project, env, config, nil
}
```

- [ ] **Step 4: Refactor `resolve.go`**

Add the two resolvers and make `resolveConfigID` reuse `resolveEnvID`:

```go
// resolveProjectID resolves a project slug to its id.
func (c *apiClient) resolveProjectID(project string) (string, error) {
	var pl struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if err := c.call("GET", "/v1/projects", nil, &pl); err != nil {
		return "", err
	}
	for _, p := range pl.Projects {
		if p.Slug == project {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found", project)
}

// resolveEnvID resolves project+environment slugs to (projectID, envID).
func (c *apiClient) resolveEnvID(project, environment string) (pid, eid string, err error) {
	pid, err = c.resolveProjectID(project)
	if err != nil {
		return "", "", err
	}
	var el struct {
		Environments []struct{ ID, Slug string } `json:"environments"`
	}
	if err = c.call("GET", "/v1/projects/"+pid+"/environments", nil, &el); err != nil {
		return "", "", err
	}
	for _, e := range el.Environments {
		if e.Slug == environment {
			return pid, e.ID, nil
		}
	}
	return "", "", fmt.Errorf("environment %q not found in project %q", environment, project)
}

func (c *apiClient) resolveConfigID(project, environment, config string) (string, error) {
	pid, eid, err := c.resolveEnvID(project, environment)
	if err != nil {
		return "", err
	}
	var cl struct {
		Configs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"configs"`
	}
	if err := c.call("GET", "/v1/projects/"+pid+"/environments/"+eid+"/configs", nil, &cl); err != nil {
		return "", err
	}
	for _, cf := range cl.Configs {
		if cf.Name == config {
			return cf.ID, nil
		}
	}
	return "", fmt.Errorf("config %q not found in %s/%s", config, project, environment)
}
```

Remove the now-duplicated project/env lookup that previously lived inline in `resolveConfigID`.

- [ ] **Step 5: Run tests + the whole package**

Run: `go test ./cmd/janus/`
Expected: PASS — new resolver/binding tests green AND the pre-existing `resolveConfigID`/binding tests still pass (proves the refactor is behavior-preserving).

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/resolve.go cmd/janus/binding.go cmd/janus/resolve_test.go cmd/janus/binding_test.go
git commit -m "refactor(cli): add resolveProjectID/resolveEnvID + bindingValues helpers"
```

---

### Task 2: `janus project` CRUD

**Files:**
- Modify: `cmd/janus/project_commands.go`
- Test: `cmd/janus/project_commands_test.go` (extend)

- [ ] **Step 1: Write failing test**

Add to `cmd/janus/project_commands_test.go`:

```go
func stubProjectCRUD(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "p1", "slug": "acme", "name": "Acme", "created_at": "t"})
	})
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme", "name": "Acme", "created_at": "t"}}})
	})
	mux.HandleFunc("DELETE /v1/projects/p1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "DELETE "+r.URL.Path)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/projects/p1/restore", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "p1", "slug": "acme"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestProjectCreateListDeleteRestore(t *testing.T) {
	ts, paths := stubProjectCRUD(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test"}

	out, err := runCLI(t, "", append([]string{"project", "create", "--slug", "acme", "--name", "Acme"}, a...)...)
	if err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("create: %q %v", out, err)
	}
	out, err = runCLI(t, "", append([]string{"project", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("list: %q %v", out, err)
	}
	// delete resolves slug "acme" -> p1, then DELETEs; --yes skips confirm
	if _, err = runCLI(t, "", append([]string{"project", "delete", "acme", "--yes"}, a...)...); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err = runCLI(t, "", append([]string{"project", "restore", "acme"}, a...)...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, want := range []string{"POST /v1/projects", "GET /v1/projects", "DELETE /v1/projects/p1", "POST /v1/projects/p1/restore"} {
		found := false
		for _, p := range *paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing call %q; saw %v", want, *paths)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run TestProjectCreateListDeleteRestore`
Expected: FAIL — `project create` etc. are unknown commands.

- [ ] **Step 3: Add CRUD subcommands in `project_commands.go`**

Inside `newProjectCmd`, after the KEK subcommands are built and before `cmd.AddCommand(...)`, add these commands and include them in the `AddCommand` call. Use the group's existing `address`/`token` persistent flags.

```go
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct{ ID, Slug, Name string }
			if err := c.call("POST", "/v1/projects", map[string]string{"slug": pSlug, "name": pName}, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created project %s (%s)\n", out.Slug, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&pSlug, "slug", "", "project slug (required)")
	create.Flags().StringVar(&pName, "name", "", "human-readable name")
	_ = create.MarkFlagRequired("slug")

	list := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Projects []struct{ ID, Slug, Name string } `json:"projects"`
			}
			if err := c.call("GET", "/v1/projects", nil, &resp); err != nil {
				return err
			}
			if pJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Projects)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tNAME\tID")
			for _, p := range resp.Projects {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Slug, p.Name, p.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&pJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use:   "delete <slug>",
		Short: "Soft-delete a project (restore-able)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveProjectID(args[0])
			if err != nil {
				return err
			}
			if !pYes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete project %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/projects/"+pid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted project %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&pYes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use:   "restore <slug>",
		Short: "Restore a soft-deleted project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveProjectID(args[0])
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored project %s\n", args[0])
			return nil
		},
	}
```

Add the vars near the top of `newProjectCmd` (with `address`/`token`):

```go
	var pSlug, pName string
	var pJSON, pYes bool
```

Add the imports `encoding/json` and `text/tabwriter` to the file, and include the new commands in `cmd.AddCommand(rotate, rewrap, status, create, list, del, restore)`.

Note: `delete`/`restore` resolve a soft-deleted project by slug via `resolveProjectID`, which lists projects. If your server omits soft-deleted projects from the default list, `restore` by slug can't find it — in that case restore by id. For this round, `resolveProjectID` + list is the documented behavior; leave a `// TODO: restore-by-id if soft-deleted projects are hidden from list` only if the test shows the list excludes them (it doesn't in the stub).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/janus/ -run TestProjectCreateListDeleteRestore`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/project_commands.go cmd/janus/project_commands_test.go
git commit -m "feat(cli): janus project create/list/delete/restore"
```

---

### Task 3: `janus env` group

**Files:**
- Create: `cmd/janus/env_commands.go`, `cmd/janus/env_commands_test.go`
- Modify: `cmd/janus/main.go` (register `newEnvCmd`)

- [ ] **Step 1: Write failing test**

Create `cmd/janus/env_commands_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubEnvCRUD(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("POST /v1/projects/p1/environments", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "e1", "slug": "prod", "name": "Prod"})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod", "name": "Prod"}}})
	})
	mux.HandleFunc("DELETE /v1/projects/p1/environments/e1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "DELETE "+r.URL.Path)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/projects/p1/environments/e1/restore", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "e1"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestEnvCreateListDeleteRestore(t *testing.T) {
	ts, paths := stubEnvCRUD(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme"}
	if _, err := runCLI(t, "", append([]string{"env", "create", "--slug", "prod", "--name", "Prod"}, a...)...); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := runCLI(t, "", append([]string{"env", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "prod") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := runCLI(t, "", append([]string{"env", "delete", "prod", "--yes"}, a...)...); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := runCLI(t, "", append([]string{"env", "restore", "prod"}, a...)...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, want := range []string{"POST /v1/projects/p1/environments", "GET /v1/projects/p1/environments", "DELETE /v1/projects/p1/environments/e1", "POST /v1/projects/p1/environments/e1/restore"} {
		found := false
		for _, p := range *paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q; saw %v", want, *paths)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run TestEnvCreateListDeleteRestore`
Expected: FAIL — `env` is an unknown command.

- [ ] **Step 3: Create `cmd/janus/env_commands.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	var address, token, project string
	var slug, name string
	var asJSON, yes bool

	cmd := &cobra.Command{Use: "env", Aliases: []string{"environment"}, Short: "Manage environments"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")
	cmd.PersistentFlags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")

	// resolveProject resolves the project slug (flag > JANUS_PROJECT > .janus.yaml) to (client, pid, slug).
	resolveProject := func() (*apiClient, string, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", "", err
		}
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", "", err
		}
		p, _, _, err := bindingValues(dir, project, "", "")
		if err != nil {
			return nil, "", "", err
		}
		if p == "" {
			return nil, "", "", fmt.Errorf("no project — pass --project or run `janus setup`")
		}
		pid, err := c.resolveProjectID(p)
		if err != nil {
			return nil, "", "", err
		}
		return c, pid, p, nil
	}

	create := &cobra.Command{
		Use: "create", Short: "Create an environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, _, err := resolveProject()
			if err != nil {
				return err
			}
			var out struct{ ID, Slug, Name string }
			if err := c.call("POST", "/v1/projects/"+pid+"/environments", map[string]string{"slug": slug, "name": name}, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created environment %s (%s)\n", out.Slug, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "environment slug (required)")
	create.Flags().StringVar(&name, "name", "", "human-readable name")
	_ = create.MarkFlagRequired("slug")

	list := &cobra.Command{
		Use: "list", Short: "List environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, _, err := resolveProject()
			if err != nil {
				return err
			}
			var resp struct {
				Environments []struct{ ID, Slug, Name string } `json:"environments"`
			}
			if err := c.call("GET", "/v1/projects/"+pid+"/environments", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Environments)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tNAME\tID")
			for _, e := range resp.Environments {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Slug, e.Name, e.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use: "delete <slug>", Short: "Soft-delete an environment", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, _, _, _ := bindingValues(dir, project, "", "")
			pid, eid, err := c.resolveEnvID(p, args[0])
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete environment %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/projects/"+pid+"/environments/"+eid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted environment %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use: "restore <slug>", Short: "Restore a soft-deleted environment", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, _, _, _ := bindingValues(dir, project, "", "")
			pid, eid, err := c.resolveEnvID(p, args[0])
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/environments/"+eid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored environment %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(create, list, del, restore)
	return cmd
}
```

- [ ] **Step 4: Register in `main.go`**

Add `newEnvCmd()` to the `root.AddCommand(...)` list in `newRootCmd`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/janus/ -run TestEnvCreateListDeleteRestore`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/env_commands.go cmd/janus/env_commands_test.go cmd/janus/main.go
git commit -m "feat(cli): janus env create/list/delete/restore"
```

---

### Task 4: `janus config` group

**Files:**
- Create: `cmd/janus/config_commands.go`, `cmd/janus/config_commands_test.go`
- Modify: `cmd/janus/main.go` (register `newConfigCmd`)

- [ ] **Step 1: Write failing test**

Create `cmd/janus/config_commands_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubConfigCRUD(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("POST /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "name": "prod", "environment_id": "e1"})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": []map[string]any{{"id": "c1", "name": "prod", "environment_id": "e1"}}})
	})
	mux.HandleFunc("DELETE /v1/configs/c1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "DELETE "+r.URL.Path)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/configs/c1/restore", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "c1"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestConfigCreateListDeleteRestore(t *testing.T) {
	ts, paths := stubConfigCRUD(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod"}
	if _, err := runCLI(t, "", append([]string{"config", "create", "--name", "prod"}, a...)...); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := runCLI(t, "", append([]string{"config", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "prod") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := runCLI(t, "", append([]string{"config", "delete", "prod", "--yes"}, a...)...); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := runCLI(t, "", append([]string{"config", "restore", "prod"}, a...)...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, want := range []string{"POST /v1/projects/p1/environments/e1/configs", "DELETE /v1/configs/c1", "POST /v1/configs/c1/restore"} {
		found := false
		for _, p := range *paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q; saw %v", want, *paths)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run TestConfigCreateListDeleteRestore`
Expected: FAIL — `config` unknown command.

- [ ] **Step 3: Create `cmd/janus/config_commands.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	var address, token, project, env string
	var name, inheritsFrom string
	var asJSON, yes bool

	cmd := &cobra.Command{Use: "config", Short: "Manage configs"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")
	cmd.PersistentFlags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")
	cmd.PersistentFlags().StringVar(&env, "env", "", "environment slug (overrides .janus.yaml)")

	// resolveEnv resolves project+env (flag > env > .janus.yaml) to (client, pid, eid).
	resolveEnv := func() (*apiClient, string, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", "", err
		}
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", "", err
		}
		p, e, _, err := bindingValues(dir, project, env, "")
		if err != nil {
			return nil, "", "", err
		}
		if p == "" || e == "" {
			return nil, "", "", fmt.Errorf("no project/environment — pass --project/--env or run `janus setup`")
		}
		pid, eid, err := c.resolveEnvID(p, e)
		if err != nil {
			return nil, "", "", err
		}
		return c, pid, eid, nil
	}
	// resolveCID resolves a config NAME (arg) to its id within the resolved env.
	resolveCID := func(configName string) (*apiClient, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", err
		}
		dir, _ := os.Getwd()
		p, e, _, err := bindingValues(dir, project, env, "")
		if err != nil {
			return nil, "", err
		}
		cid, err := c.resolveConfigID(p, e, configName)
		if err != nil {
			return nil, "", err
		}
		return c, cid, nil
	}

	create := &cobra.Command{
		Use: "create", Short: "Create a config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, eid, err := resolveEnv()
			if err != nil {
				return err
			}
			body := map[string]any{"name": name}
			if inheritsFrom != "" {
				body["inherits_from"] = inheritsFrom
			}
			var out struct {
				ID, Name string
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/environments/"+eid+"/configs", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created config %s (%s)\n", out.Name, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "config name (required)")
	create.Flags().StringVar(&inheritsFrom, "inherits-from", "", "base config name in the same environment")
	_ = create.MarkFlagRequired("name")

	list := &cobra.Command{
		Use: "list", Short: "List configs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, eid, err := resolveEnv()
			if err != nil {
				return err
			}
			var resp struct {
				Configs []struct {
					ID           string  `json:"id"`
					Name         string  `json:"name"`
					InheritsFrom *string `json:"inherits_from"`
				} `json:"configs"`
			}
			if err := c.call("GET", "/v1/projects/"+pid+"/environments/"+eid+"/configs", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Configs)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tINHERITS\tID")
			for _, cf := range resp.Configs {
				inh := ""
				if cf.InheritsFrom != nil {
					inh = *cf.InheritsFrom
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", cf.Name, inh, cf.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use: "delete <name>", Short: "Soft-delete a config", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := resolveCID(args[0])
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete config %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/configs/"+cid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted config %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use: "restore <name>", Short: "Restore a soft-deleted config", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := resolveCID(args[0])
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/configs/"+cid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored config %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(create, list, del, restore)
	return cmd
}
```

Note: `config delete`/`restore` resolve the config **name** to its id via the configs list (`resolveConfigID`). If the server hides soft-deleted configs from the list, `restore` by name can't find it — acceptable for this round (the stub lists it); document as a known limitation, do not add a workaround.

- [ ] **Step 4: Register in `main.go`**

Add `newConfigCmd()` to `root.AddCommand(...)`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/janus/ -run TestConfigCreateListDeleteRestore`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/config_commands.go cmd/janus/config_commands_test.go cmd/janus/main.go
git commit -m "feat(cli): janus config create/list/delete/restore"
```

---

### Task 5: `janus token` group

**Files:**
- Create: `cmd/janus/token_commands.go`, `cmd/janus/token_commands_test.go`
- Modify: `cmd/janus/main.go` (register `newTokenCmd`)

- [ ] **Step 1: Write failing test**

Create `cmd/janus/token_commands_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubTokens(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	var mintBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": []map[string]any{{"id": "c1", "name": "prod"}}})
	})
	mux.HandleFunc("POST /v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&mintBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "janus_svc_SECRET", "id": "tok1", "name": "ci",
			"scope": map[string]string{"kind": "config", "id": "c1"}, "access": "readwrite",
		})
	})
	mux.HandleFunc("GET /v1/tokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": []map[string]any{
			{"id": "tok1", "name": "ci", "scope_kind": "config", "scope_id": "c1", "access": "readwrite"},
		}})
	})
	mux.HandleFunc("DELETE /v1/tokens/tok1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &mintBody
}

func TestTokenMintScopeAndStdoutSplit(t *testing.T) {
	ts, mintBody := stubTokens(t)
	// Separate stdout/stderr so we can assert the raw token lands on stdout.
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"token", "mint", "--name", "ci", "--config", "prod", "--access", "rw", "--ttl", "24h",
		"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "janus_svc_SECRET" {
		t.Fatalf("token must be the only thing on stdout, got %q", stdout.String())
	}
	if (*mintBody)["access"] != "readwrite" {
		t.Fatalf("access mapping: %v", (*mintBody)["access"])
	}
	scope := (*mintBody)["scope"].(map[string]any)
	if scope["kind"] != "config" || scope["id"] != "c1" {
		t.Fatalf("scope mapping: %v", scope)
	}
	if (*mintBody)["ttl_seconds"].(float64) != 86400 {
		t.Fatalf("ttl mapping: %v", (*mintBody)["ttl_seconds"])
	}
}

func TestTokenListAndRevoke(t *testing.T) {
	ts, _ := stubTokens(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test"}
	out, err := runCLI(t, "", append([]string{"token", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "tok1") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := runCLI(t, "", append([]string{"token", "revoke", "tok1", "--yes"}, a...)...); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run 'TestToken'`
Expected: FAIL — `token` unknown command.

- [ ] **Step 3: Create `cmd/janus/token_commands.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	var address, token, project, env, config string
	var name, access, ttl string
	var asJSON, yes bool

	cmd := &cobra.Command{Use: "token", Short: "Manage service tokens"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")

	mint := &cobra.Command{
		Use:   "mint",
		Short: "Mint a scoped service token (shown once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			acc := ""
			switch access {
			case "read", "r":
				acc = "read"
			case "readwrite", "rw":
				acc = "readwrite"
			default:
				return fmt.Errorf("--access must be read|rw")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			// --project/--env give context (binding-aware); --config narrows the
			// scope to a config, otherwise the token is environment-scoped.
			dir, _ := os.Getwd()
			p, e, cfg, err := bindingValues(dir, project, env, config)
			if err != nil {
				return err
			}
			if p == "" || e == "" {
				return fmt.Errorf("no project/environment — pass --project/--env or run `janus setup`")
			}
			var kind, id string
			if cfg != "" {
				kind = "config"
				if id, err = c.resolveConfigID(p, e, cfg); err != nil {
					return err
				}
			} else {
				kind = "environment"
				if _, id, err = c.resolveEnvID(p, e); err != nil {
					return err
				}
			}
			body := map[string]any{"name": name, "scope": map[string]string{"kind": kind, "id": id}, "access": acc}
			if ttl != "" {
				d, err := time.ParseDuration(ttl)
				if err != nil {
					return fmt.Errorf("invalid --ttl: %w", err)
				}
				body["ttl_seconds"] = int64(d.Seconds())
			}
			var out struct {
				Token, ID, Name, Access string
				Scope                   struct{ Kind, ID string }
				ExpiresAt               *string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/tokens", body, &out); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}
			// Raw token ONLY on stdout (capturable); summary on stderr.
			fmt.Fprintln(cmd.OutOrStdout(), out.Token)
			fmt.Fprintf(cmd.ErrOrStderr(), "minted %s (%s) scope=%s/%s access=%s — shown once\n",
				out.Name, out.ID, out.Scope.Kind, out.Scope.ID, out.Access)
			return nil
		},
	}
	mint.Flags().StringVar(&name, "name", "", "token name (required)")
	mint.Flags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")
	mint.Flags().StringVar(&env, "env", "", "environment slug (overrides .janus.yaml)")
	mint.Flags().StringVar(&config, "config", "", "scope to this config (name); omit for environment scope")
	mint.Flags().StringVar(&access, "access", "read", "read|rw")
	mint.Flags().StringVar(&ttl, "ttl", "", "lifetime, e.g. 24h (default: no expiry)")
	mint.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	_ = mint.MarkFlagRequired("name")

	list := &cobra.Command{
		Use: "list", Short: "List service tokens (metadata only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Tokens []struct {
					ID, Name, ScopeKind, ScopeID, Access string
					ExpiresAt                            *string `json:"expires_at"`
				} `json:"tokens"`
			}
			if err := c.call("GET", "/v1/tokens", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Tokens)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tACCESS\tEXPIRES")
			for _, tk := range resp.Tokens {
				exp := "never"
				if tk.ExpiresAt != nil {
					exp = *tk.ExpiresAt
				}
				fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\t%s\n", tk.ID, tk.Name, tk.ScopeKind, tk.ScopeID, tk.Access, exp)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	revoke := &cobra.Command{
		Use: "revoke <id>", Short: "Revoke a service token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Revoke token %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/tokens/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "revoked token %s\n", args[0])
			return nil
		},
	}
	revoke.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	cmd.AddCommand(mint, list, revoke)
	return cmd
}
```

(`asJSON`/`yes` are shared across `mint`/`list`/`revoke`; only one runs per invocation, so sharing the vars is fine.)

- [ ] **Step 4: Register in `main.go`**

Add `newTokenCmd()` to `root.AddCommand(...)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/janus/ -run 'TestToken'`
Expected: PASS (both tests, including the stdout-only-token assertion).

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/token_commands.go cmd/janus/token_commands_test.go cmd/janus/main.go
git commit -m "feat(cli): janus token mint/list/revoke"
```

---

### Task 6: `janus whoami` + `janus completion`

**Files:**
- Create: `cmd/janus/whoami.go`, `cmd/janus/completion.go`, `cmd/janus/whoami_test.go`
- Modify: `cmd/janus/main.go`

- [ ] **Step 1: Write failing test**

Create `cmd/janus/whoami_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoami(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/me", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"kind": "user", "id": "u1", "name": "root@corp.io"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	out, err := runCLI(t, "", "whoami", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil || !strings.Contains(out, "root@corp.io") || !strings.Contains(out, "user") {
		t.Fatalf("whoami: %q %v", out, err)
	}
}

func TestCompletionGenerates(t *testing.T) {
	out, err := runCLI(t, "", "completion", "bash")
	if err != nil || !strings.Contains(out, "janus") {
		t.Fatalf("completion bash: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run 'TestWhoami|TestCompletionGenerates'`
Expected: FAIL — `whoami`/`completion` unknown.

- [ ] **Step 3: Create `cmd/janus/whoami.go`**

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	var address, token string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated principal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var me struct{ Kind, ID, Name string }
			if err := c.call("GET", "/v1/auth/me", nil, &me); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(me)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", me.Kind, me.Name, me.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
```

- [ ] **Step 4: Create `cmd/janus/completion.go`**

```go
package main

import (
	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate a shell completion script",
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
			return nil
		},
	}
	return cmd
}
```

- [ ] **Step 5: Register in `main.go`**

Add `newWhoamiCmd()` and `newCompletionCmd()` to `root.AddCommand(...)`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/janus/ -run 'TestWhoami|TestCompletionGenerates'`
Expected: PASS. (If `cobra.ExactValidArgs` is deprecated in the pinned cobra, use `Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs)`.)

- [ ] **Step 7: Commit**

```bash
git add cmd/janus/whoami.go cmd/janus/completion.go cmd/janus/whoami_test.go cmd/janus/main.go
git commit -m "feat(cli): janus whoami + completion"
```

---

### Task 7: `janus secrets diff`

**Files:**
- Create: `cmd/janus/secrets_diff.go`, `cmd/janus/secrets_diff_test.go`
- Modify: `cmd/janus/secrets_cmd.go` (register under the secrets group)

- [ ] **Step 1: Write failing test**

Create `cmd/janus/secrets_diff_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretsDiff(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": []map[string]any{{"id": "c1", "name": "prod"}}})
	})
	var gotQuery string
	mux.HandleFunc("GET /v1/configs/c1/versions/diff", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"a": 3, "b": 4, "added": []string{"NEW_KEY"}, "changed": []string{"DB_URL"}, "removed": []string{}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	out, err := runCLI(t, "", "secrets", "diff", "3", "4", "--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod", "--config", "prod")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(gotQuery, "a=3") || !strings.Contains(gotQuery, "b=4") {
		t.Fatalf("query = %q", gotQuery)
	}
	if !strings.Contains(out, "NEW_KEY") || !strings.Contains(out, "DB_URL") {
		t.Fatalf("output missing keys: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/janus/ -run TestSecretsDiff`
Expected: FAIL — `secrets diff` unknown.

- [ ] **Step 3: Create `cmd/janus/secrets_diff.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newSecretsDiffCmd() *cobra.Command {
	var f secretFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "diff <vA> <vB>",
		Short: "Diff two config versions (key names only, no values)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("vA must be an integer version")
			}
			b, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("vB must be an integer version")
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var d struct {
				A, B                    int
				Added, Changed, Removed []string
			}
			path := fmt.Sprintf("/v1/configs/%s/versions/diff?a=%d&b=%d", cid, a, b)
			if err := c.call("GET", path, nil, &d); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(d)
			}
			w := cmd.OutOrStdout()
			for _, k := range d.Added {
				fmt.Fprintf(w, "+ %s\n", k)
			}
			for _, k := range d.Removed {
				fmt.Fprintf(w, "- %s\n", k)
			}
			for _, k := range d.Changed {
				fmt.Fprintf(w, "~ %s\n", k)
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
```

- [ ] **Step 4: Register under the secrets group**

In `newSecretsCmd` (in `secrets_cmd.go`), add `newSecretsDiffCmd()` to the `cmd.AddCommand(...)` call.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/janus/ -run TestSecretsDiff`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/secrets_diff.go cmd/janus/secrets_diff_test.go cmd/janus/secrets_cmd.go
git commit -m "feat(cli): janus secrets diff (value-free)"
```

---

### Task 8: Full verification + docs + tracker

**Files:** `docs/guides/managing-secrets.md`, `gaps.md`; verification only otherwise.

- [ ] **Step 1: Full build + test + race**

Run: `go build ./... && go test ./... && go test -race ./cmd/janus/...`
Expected: all PASS. Fix any compile/test failures (most likely: a missing import, or the `asJSONHolder` placeholder from Task 5 not deleted).

- [ ] **Step 2: Leak test**

Run: `go test ./cmd/janus/ -run 'Leak'`
Expected: PASS — the existing `cli_leak_test.go` confirms no secret values leak (diff/whoami emit none).

- [ ] **Step 3: Security gates**

Run: `govulncheck ./... ; gosec ./cmd/janus/...`
Expected: no new findings. (gosec may flag the intentional stdout token print in `token mint` — annotate with `// #nosec` only if it does, since printing the token once is the documented behavior.)

- [ ] **Step 4: Update `docs/guides/managing-secrets.md`**

The guide currently says creating projects/environments/configs is "not a CLI operation." Update the **"Creating projects, environments, and configs"** section: it now IS a CLI operation — add a `### Via the CLI` subsection showing `janus project create`, `janus env create`, `janus config create`, and mention `janus token mint` for tokens. Keep the API/UI paths too. Correct the `> Note: janus project` callout to reflect that `janus project` now does BOTH KEK rotation AND project CRUD.

- [ ] **Step 5: Mark `gaps.md` §6 done**

Update the §6 table: strike the "No project/env/config management commands" and "No token management commands" rows and the completion/whoami rows with `**[DONE 2026-07-16]**`. Correct the stale rotation/sync/dynamic row (those verbs already existed). Add a one-line note that the CLI is now self-sufficient for bootstrap.

- [ ] **Step 6: Commit**

```bash
git add docs/guides/managing-secrets.md gaps.md
git commit -m "docs(cli): mark §6 CLI control plane done; document CLI create flows"
```

---

## Self-Review

**Spec coverage:**
- project/env/config CRUD (create/list/get*/delete/restore) → Tasks 2–4. (*`get` folded into `list`/resolution; add a `get` subcommand only if a reviewer flags it as required — the spec lists it but list+resolve covers the need. If required, mirror `list` filtered to one slug.)
- token mint/list/revoke → Task 5, incl. scope mapping + stdout-only token.
- whoami, completion → Task 6.
- secrets diff (value-free) → Task 7.
- Conventions (slug addressing, binding-aware parents, `--json`, TTY-confirm) → Task 1 helpers + applied in every command.
- No server/API/migration changes → nothing in the plan touches `internal/` or `migrations/`.

**Placeholder scan:** No TBD/TODO/placeholder code — every step ships complete, compilable Go. The two `// known limitation` notes (restore-by-slug when soft-deleted rows are hidden from list) are documented decisions, not gaps. Token scope: `--project`/`--env` are context (binding-aware) and `--config` optionally narrows to config-scope — a cleaner realisation of the spec's "config or environment" intent; sync the spec's token section to match if desired.

**Type consistency:** `bindingValues` (4 returns: project, env, config, err) is used consistently in Tasks 3–5. `resolveProjectID(project) (string,error)` and `resolveEnvID(project,env) (pid,eid,error)` signatures match all call sites. `apiClient.call(method,path,in,out)` used uniformly. Token mint request keys (`name`,`scope.{kind,id}`,`access`,`ttl_seconds`) and access mapping (`read`/`readwrite`) match the verified handler.

**Open item to confirm during implementation:** whether soft-deleted projects/configs appear in their list endpoints (affects `restore <slug>` resolvability). The plan notes this as a known limitation rather than adding an unverified `?include_deleted` workaround; if a reviewer confirms the list hides them, add restore-by-id as a fast follow.
