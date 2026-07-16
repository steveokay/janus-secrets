# Promotion Approval Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users without `secret:promote` on a target env file value-free, version-pinned promotion **requests** that a `secret:promote` holder reviews and approves (applying the existing `promote.Apply`), across REST, the `janus promote` CLI, and the promotion UI.

**Architecture:** A new `promotion_requests` table + `store.PromotionRequestRepo` (following the `RotationRepo` pattern), request-lifecycle methods added to the existing `promote.Service` (reusing `Apply` unchanged), six REST handlers, `janus promote` subcommands, and a Requests view in the promotion UI. One new RBAC action `promotion:request`. No change to Phase-A direct promotion.

**Tech Stack:** Go, pgx/v5, chi, cobra; React + TS + TanStack Query; Postgres (testcontainers for repo/e2e tests).

**Spec:** `docs/superpowers/specs/2026-07-16-promotion-approval-design.md`.

## Concurrency note (why claim-first)

`promote.Service.Apply` calls `secrets.Service.SetSecrets`, which owns its own transaction; `Apply` cannot join an external `pgx.Tx` (there is no cross-package tx passing — `store.withTx` is unexported). So approve **cannot** hold a `SELECT … FOR UPDATE` across Apply. Instead: **claim-first** — atomically CAS `pending → applied` (`UPDATE … WHERE status='pending'`, `RowsAffected` must be 1; a racer gets 0 → `409`), then run `Apply`, then either record `applied_target_version` or, on Apply error, CAS `applied → pending` to release the claim. This prevents a double-promotion (only the CAS winner runs Apply) and keeps the 4-state machine. On single-node (the deployment model) the crash-window between claim and Apply completion is acceptable, matching the codebase's documented single-node stance.

## Shared facts (verified — trust these)

- `promote.Service` (`internal/promote/service.go`): `New(sec *secrets.Service, st *store.Store) *Service`; `Apply(ctx, ApplyRequest) (ApplyResult, error)`; `Preview(ctx, sourceConfigID, targetConfigID, actor string) (Diff, error)`. `Action` = `ActionSet "set"` | `ActionRemove "remove"`. `Selection{Key string; Action Action}`. `ApplyRequest{SourceConfigID, TargetConfigID, TargetEnvID, TargetName string; CreateTarget bool; SourceVersion int; Selections []Selection; Actor string}`. `ApplyResult{TargetVersion int; Applied []Selection; Skipped []string}`.
- Store: `type Store struct{ pool *pgxpool.Pool; dsn string }`; repos are `struct{ s *Store }` with `NewXxxRepo(s *Store)`; use `r.s.pool.Exec/Query/QueryRow` and `r.s.withTx(ctx, func(tx pgx.Tx) error{…})`. `mapError(err)`, `ErrNotFound` exist. A single-row mutation checks `tag.RowsAffected()`.
- authz (`internal/authz/actions.go`): actions are `const X Action = "…"`; `developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse, DynamicIssue, SecretPromote))`; `roleAllows(role, action) bool`; matrix test in `promotion_actions_test.go`.
- API helpers: `s.can(r, action, res) error`; `s.authorize(w, r, action, res, auditAction, auditResource) bool`; `s.record(r, action, resource, result, code, detail) error`; `writeError(w, status, code, msg)`; `writeJSON(w, status, v)`; `PrincipalFrom(ctx) (auth.Principal, bool)` (Principal has `.Kind`, `.ID`; `auth.KindUser`); `promoteActorUser(r) string` (user id or ""); `configResourceByID(ctx, cid) (authz.Resource, error)`; `resolveScopeResource(ctx, "environment"|"config", id) (authz.Resource, error)`; `s.writeServiceError`, `s.writeAuthzError`, `s.writePromoteError`. Error codes like `CodeValidation`, `CodeInternal`.
- Routes/construction (`server.go`): `if kr!=nil && st!=nil && svc!=nil { s.promote = promote.New(svc, st) }` (server.go:106); promote routes are in `if s.promote != nil { r.Group(func(r chi.Router){ r.Use(RequireAuth(s.auth)); … }) }` (server.go:260).
- Migrations: next is `000021`; style = `gen_random_uuid()` (no extension), `timestamptz`, inline FK `REFERENCES projects (id)`, index `idx_<table>_<purpose>`. `configs` has NO `project_id` — join via `environments`.
- CLI: `newPromoteCmd()` (`cmd/janus/promotion_commands.go`) is a single command `Use:"promote --to <env>"`; `f.bind(cmd)` registers `--project/--env/--config/--address/--token`; `resolveBinding(dir, f.project, f.env, f.config) (project, env, config string, err error)`; `newAPIClient(f.address, f.token)`; `c.call(method, path, in, out)`. A command may have both a `RunE` and subcommands (cobra routes `janus promote request …` to the child, `janus promote --to x` to the parent). `runCLI(t, stdin, args...) (string, error)` harness (Out+Err → one buffer).

---

### Task 1: `promotion:request` RBAC action

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/promotion_actions_test.go`

- [ ] **Step 1: Add failing matrix rows**

In `internal/authz/promotion_actions_test.go`, add to the `cases` slice in `TestPromotionActionMatrix`:

```go
		{RoleViewer, PromotionRequest, false},
		{RoleDeveloper, PromotionRequest, true},
		{RoleAdmin, PromotionRequest, true},
		{RoleOwner, PromotionRequest, true},
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/authz/ -run TestPromotionActionMatrix`
Expected: FAIL — `undefined: PromotionRequest`.

- [ ] **Step 3: Add the action**

In `internal/authz/actions.go`, in the const block next to `SecretPromote`:

```go
	PromotionRequest Action = "promotion:request" // developer+, source-env scoped (approval workflow)
```

Then add it to `developerActions`:

```go
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse, DynamicIssue, SecretPromote, PromotionRequest))
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/authz/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/promotion_actions_test.go
git commit -m "feat(authz): promotion:request action (developer+)"
```

---

### Task 2: Migration + `PromotionRequestRepo`

**Files:**
- Create: `migrations/000021_promotion_requests.up.sql`, `migrations/000021_promotion_requests.down.sql`
- Create: `internal/store/promotion_requests.go`, `internal/store/promotion_requests_test.go`

- [ ] **Step 1: Write the migration**

`migrations/000021_promotion_requests.up.sql`:

```sql
CREATE TABLE promotion_requests (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id             uuid NOT NULL REFERENCES projects (id),
    source_config_id       uuid NOT NULL REFERENCES configs (id),
    source_version         integer NOT NULL,
    target_config_id       uuid REFERENCES configs (id),
    target_env_id          uuid NOT NULL REFERENCES environments (id),
    target_name            text NOT NULL DEFAULT '',
    create_target          boolean NOT NULL DEFAULT false,
    selections             jsonb NOT NULL DEFAULT '[]'::jsonb,
    note                   text NOT NULL DEFAULT '',
    status                 text NOT NULL DEFAULT 'pending',
    requested_by           uuid NOT NULL REFERENCES users (id),
    decided_by             uuid REFERENCES users (id),
    decision_note          text NOT NULL DEFAULT '',
    applied_target_version integer,
    created_at             timestamptz NOT NULL DEFAULT now(),
    decided_at             timestamptz
);

CREATE INDEX idx_promotion_requests_project_status ON promotion_requests (project_id, status, created_at DESC, id DESC);
CREATE INDEX idx_promotion_requests_target_status ON promotion_requests (target_env_id, status);
CREATE INDEX idx_promotion_requests_requester ON promotion_requests (requested_by, status);
```

`migrations/000021_promotion_requests.down.sql`:

```sql
DROP INDEX IF EXISTS idx_promotion_requests_requester;
DROP INDEX IF EXISTS idx_promotion_requests_target_status;
DROP INDEX IF EXISTS idx_promotion_requests_project_status;
DROP TABLE IF EXISTS promotion_requests;
```

(Confirm the `users` table PK column is `id` and the FK targets match `projects (id)`, `configs (id)`, `environments (id)` by grepping an existing migration; adjust the referenced column if a repo uses a different PK name.)

- [ ] **Step 2: Write the repo test (fails first)**

Create `internal/store/promotion_requests_test.go`. Use the existing store test harness (look at `internal/store/rotation_test.go` for how it boots Postgres + seeds a project/env/config/user; reuse the same helper, e.g. `newTestStore(t)` and any seed helpers). The test creates a pending request, gets it, lists it, claims it, sets version, and asserts a second claim conflicts:

```go
package store

import (
	"context"
	"testing"
)

func TestPromotionRequestLifecycle(t *testing.T) {
	st := newTestStore(t) // existing harness; boots Postgres + migrations
	ctx := context.Background()
	seed := seedProjectEnvConfigUser(t, st) // reuse/create a helper returning ids (project, srcConfig, tgtEnv, user)

	repo := NewPromotionRequestRepo(st)
	req := &PromotionRequest{
		ProjectID:      seed.ProjectID,
		SourceConfigID: seed.SourceConfigID,
		SourceVersion:  1,
		TargetEnvID:    seed.TargetEnvID,
		CreateTarget:   true,
		TargetName:     "prod",
		Selections:     []PromotionSelection{{Key: "DB_URL", Action: "set"}},
		Note:           "please promote",
		RequestedBy:    seed.UserID,
	}
	created, err := repo.Create(ctx, req)
	if err != nil || created.ID == "" || created.Status != "pending" {
		t.Fatalf("create: %+v %v", created, err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Note != "please promote" || len(got.Selections) != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	list, err := repo.ListByProject(ctx, seed.ProjectID, "pending")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	// Claim (pending->applied) once; second claim conflicts.
	if err := repo.ClaimForApply(ctx, created.ID, seed.ApproverID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := repo.ClaimForApply(ctx, created.ID, seed.ApproverID); err != ErrNotFound {
		t.Fatalf("second claim should conflict, got %v", err)
	}
	if err := repo.SetAppliedVersion(ctx, created.ID, 2); err != nil {
		t.Fatalf("set version: %v", err)
	}
	final, _ := repo.Get(ctx, created.ID)
	if final.Status != "applied" || final.AppliedTargetVersion == nil || *final.AppliedTargetVersion != 2 {
		t.Fatalf("final: %+v", final)
	}
}
```

Note: reuse whatever seed helpers exist in `internal/store` tests; if none returns exactly these ids, add a small local helper in the test file that inserts a project→env→config and a user via the existing repos (`NewProjectRepo`, `NewEnvironmentRepo`, `NewConfigRepo`, `NewUserRepo`) and returns their ids. `seed.ApproverID` can be a second user.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/store/ -run TestPromotionRequestLifecycle`
Expected: FAIL — repo undefined.

- [ ] **Step 4: Implement the repo**

Create `internal/store/promotion_requests.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type PromotionSelection struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

type PromotionRequest struct {
	ID                   string
	ProjectID            string
	SourceConfigID       string
	SourceVersion        int
	TargetConfigID       *string
	TargetEnvID          string
	TargetName           string
	CreateTarget         bool
	Selections           []PromotionSelection
	Note                 string
	Status               string
	RequestedBy          string
	DecidedBy            *string
	DecisionNote         string
	AppliedTargetVersion *int
	CreatedAt            time.Time
	DecidedAt            *time.Time
}

type PromotionRequestRepo struct{ s *Store }

func NewPromotionRequestRepo(s *Store) *PromotionRequestRepo { return &PromotionRequestRepo{s: s} }

const promoReqCols = `id::text, project_id::text, source_config_id::text, source_version,
	target_config_id::text, target_env_id::text, target_name, create_target, selections,
	note, status, requested_by::text, decided_by::text, decision_note,
	applied_target_version, created_at, decided_at`

func scanPromoReq(row interface{ Scan(...any) error }) (*PromotionRequest, error) {
	var p PromotionRequest
	var selRaw []byte
	if err := row.Scan(&p.ID, &p.ProjectID, &p.SourceConfigID, &p.SourceVersion,
		&p.TargetConfigID, &p.TargetEnvID, &p.TargetName, &p.CreateTarget, &selRaw,
		&p.Note, &p.Status, &p.RequestedBy, &p.DecidedBy, &p.DecisionNote,
		&p.AppliedTargetVersion, &p.CreatedAt, &p.DecidedAt); err != nil {
		return nil, mapError(err)
	}
	if len(selRaw) > 0 {
		if err := json.Unmarshal(selRaw, &p.Selections); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

func (r *PromotionRequestRepo) Create(ctx context.Context, p *PromotionRequest) (*PromotionRequest, error) {
	sel, err := json.Marshal(p.Selections)
	if err != nil {
		return nil, err
	}
	var id string
	err = r.s.pool.QueryRow(ctx,
		`INSERT INTO promotion_requests
		   (project_id, source_config_id, source_version, target_config_id, target_env_id,
		    target_name, create_target, selections, note, requested_by)
		 VALUES ($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,$6,$7,$8,$9,$10::uuid)
		 RETURNING id::text`,
		p.ProjectID, p.SourceConfigID, p.SourceVersion, p.TargetConfigID, p.TargetEnvID,
		p.TargetName, p.CreateTarget, sel, p.Note, p.RequestedBy).Scan(&id)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, id)
}

func (r *PromotionRequestRepo) Get(ctx context.Context, id string) (*PromotionRequest, error) {
	return scanPromoReq(r.s.pool.QueryRow(ctx,
		`SELECT `+promoReqCols+` FROM promotion_requests WHERE id = $1::uuid`, id))
}

func (r *PromotionRequestRepo) ListByProject(ctx context.Context, projectID, status string) ([]*PromotionRequest, error) {
	return r.list(ctx, `WHERE project_id = $1::uuid`+statusClause(status, 2), projectID, status)
}

func (r *PromotionRequestRepo) ListByRequester(ctx context.Context, userID, status string) ([]*PromotionRequest, error) {
	return r.list(ctx, `WHERE requested_by = $1::uuid`+statusClause(status, 2), userID, status)
}

func statusClause(status string, n int) string {
	if status == "" {
		return ""
	}
	return ` AND status = $` + itoa(n)
}

func (r *PromotionRequestRepo) list(ctx context.Context, where string, args ...any) ([]*PromotionRequest, error) {
	// Drop a trailing empty status arg so the placeholder count matches.
	if len(args) == 2 {
		if s, ok := args[1].(string); ok && s == "" {
			args = args[:1]
		}
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+promoReqCols+` FROM promotion_requests `+where+
			` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*PromotionRequest
	for rows.Next() {
		p, err := scanPromoReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// ClaimForApply atomically CAS pending->applied (the claim). RowsAffected != 1
// means it was already decided/claimed -> ErrNotFound (handler maps to 409).
func (r *PromotionRequestRepo) ClaimForApply(ctx context.Context, id, approver string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE promotion_requests
		    SET status='applied', decided_by=$2::uuid, decided_at=now()
		  WHERE id=$1::uuid AND status='pending'`, id, approver)
}

func (r *PromotionRequestRepo) SetAppliedVersion(ctx context.Context, id string, version int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE promotion_requests SET applied_target_version=$2 WHERE id=$1::uuid AND status='applied'`,
		id, version)
}

// RevertToPending releases a claim when Apply failed (applied->pending).
func (r *PromotionRequestRepo) RevertToPending(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE promotion_requests
		    SET status='pending', decided_by=NULL, decided_at=NULL, applied_target_version=NULL
		  WHERE id=$1::uuid AND status='applied'`, id)
}

// Decide CAS pending->to for reject/cancel.
func (r *PromotionRequestRepo) Decide(ctx context.Context, id, to, decidedBy, note string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE promotion_requests
		    SET status=$2, decided_by=$3::uuid, decision_note=$4, decided_at=now()
		  WHERE id=$1::uuid AND status='pending'`, id, to, decidedBy, note)
}
```

Add a tiny `itoa` if not present in the package (check first — `strconv.Itoa` is fine; import `strconv` and use `strconv.Itoa(n)` instead of a custom `itoa`). Also confirm `execAffectingOne` returns `ErrNotFound` on 0 rows (per shared facts) — that is what the test asserts for the conflicting second claim.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/store/ -run TestPromotionRequestLifecycle`
Expected: PASS (needs Docker/testcontainers).

- [ ] **Step 6: Commit**

```bash
git add migrations/000021_promotion_requests.up.sql migrations/000021_promotion_requests.down.sql internal/store/promotion_requests.go internal/store/promotion_requests_test.go
git commit -m "feat(store): promotion_requests table + repo (migration 000021)"
```

---

### Task 3: Request lifecycle on `promote.Service`

**Files:**
- Modify: `internal/promote/service.go` (add `requests` repo to `New` + struct)
- Create: `internal/promote/requests.go`, `internal/promote/requests_test.go`

- [ ] **Step 1: Add the repo to the service**

In `internal/promote/service.go`, add a field to `Service`:

```go
	requests   *store.PromotionRequestRepo
```

and set it in `New`:

```go
		requests:   store.NewPromotionRequestRepo(st),
```

- [ ] **Step 2: Write the lifecycle test (fails first)**

Create `internal/promote/requests_test.go`. Reuse the existing promote test harness (`internal/promote/harness_test.go` — see how `service_test.go` builds a `*Service` + seeds project/env/config/secrets). The test: create a request, approve it, assert the target got a new version with the promoted key; a second approve conflicts:

```go
package promote

import (
	"context"
	"testing"
)

func TestRequestApproveApplies(t *testing.T) {
	h := newPromoteHarness(t) // existing harness: svc, ids, seeded source secret
	ctx := context.Background()

	reqID, err := h.svc.CreateRequest(ctx, CreateRequestInput{
		SourceConfigID: h.SourceConfigID,
		TargetEnvID:    h.TargetEnvID,
		TargetName:     "prod",
		CreateTarget:   true,
		SourceVersion:  h.SourceVersion,
		Selections:     []Selection{{Key: h.Key, Action: ActionSet}},
		Note:           "ship it",
		RequestedBy:    h.DevUserID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := h.svc.ApproveRequest(ctx, reqID, h.AdminUserID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if res.TargetVersion < 1 || len(res.Applied) != 1 {
		t.Fatalf("apply result: %+v", res)
	}
	// Second approve conflicts (already applied).
	if _, err := h.svc.ApproveRequest(ctx, reqID, h.AdminUserID); err == nil {
		t.Fatalf("second approve must conflict")
	}
}

func TestRequestRejectAndCancel(t *testing.T) {
	h := newPromoteHarness(t)
	ctx := context.Background()
	mk := func() string {
		id, err := h.svc.CreateRequest(ctx, CreateRequestInput{
			SourceConfigID: h.SourceConfigID, TargetEnvID: h.TargetEnvID, TargetName: "prod",
			CreateTarget: true, SourceVersion: h.SourceVersion,
			Selections: []Selection{{Key: h.Key, Action: ActionSet}}, RequestedBy: h.DevUserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	if err := h.svc.RejectRequest(ctx, mk(), h.AdminUserID, "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := h.svc.CancelRequest(ctx, mk(), h.DevUserID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}
```

If `newPromoteHarness` doesn't exist with those fields, add the missing seed values to the existing harness (or a thin local helper) — you need: a source config with one secret at a known version, a target env id, and two user ids.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/promote/ -run 'TestRequest'`
Expected: FAIL — `CreateRequest`/`ApproveRequest` undefined.

- [ ] **Step 4: Implement the lifecycle**

Create `internal/promote/requests.go`:

```go
package promote

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/store"
)

// ErrRequestConflict is returned when a request is not in the expected state
// (e.g. approve/reject/cancel on an already-decided request).
var ErrRequestConflict = errors.New("promotion request not pending")

type CreateRequestInput struct {
	SourceConfigID string
	TargetConfigID string // empty when creating
	TargetEnvID    string
	TargetName     string
	CreateTarget   bool
	SourceVersion  int
	Selections     []Selection
	Note           string
	RequestedBy    string // user id
}

// CreateRequest validates the pipeline step and persists a pending request.
func (s *Service) CreateRequest(ctx context.Context, in CreateRequestInput) (string, error) {
	projectID, srcEnv, err := s.projectAndEnv(ctx, in.SourceConfigID)
	if err != nil {
		return "", err
	}
	// Validate srcEnv -> targetEnv is the pipeline's next step (reuse validateStep).
	if err := s.validateStep(ctx, projectID, srcEnv, in.TargetEnvID); err != nil {
		return "", err
	}
	sels := make([]store.PromotionSelection, 0, len(in.Selections))
	for _, sel := range in.Selections {
		sels = append(sels, store.PromotionSelection{Key: sel.Key, Action: string(sel.Action)})
	}
	rec := &store.PromotionRequest{
		ProjectID:      projectID,
		SourceConfigID: in.SourceConfigID,
		SourceVersion:  in.SourceVersion,
		TargetEnvID:    in.TargetEnvID,
		TargetName:     in.TargetName,
		CreateTarget:   in.CreateTarget,
		Selections:     sels,
		Note:           in.Note,
		RequestedBy:    in.RequestedBy,
	}
	if in.TargetConfigID != "" {
		rec.TargetConfigID = &in.TargetConfigID
	}
	created, err := s.requests.Create(ctx, rec)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (s *Service) GetRequest(ctx context.Context, id string) (*store.PromotionRequest, error) {
	return s.requests.Get(ctx, id)
}

func (s *Service) ListRequestsByProject(ctx context.Context, projectID, status string) ([]*store.PromotionRequest, error) {
	return s.requests.ListByProject(ctx, projectID, status)
}

func (s *Service) ListRequestsByRequester(ctx context.Context, userID, status string) ([]*store.PromotionRequest, error) {
	return s.requests.ListByRequester(ctx, userID, status)
}

// ApproveRequest claims the request (CAS pending->applied), runs the existing
// Apply, then records the applied version. On Apply error it releases the claim
// back to pending. Authorization (secret:promote on target) and the four-eyes
// check are enforced by the caller (handler) BEFORE this is called.
func (s *Service) ApproveRequest(ctx context.Context, id, approver string) (ApplyResult, error) {
	req, err := s.requests.Get(ctx, id)
	if err != nil {
		return ApplyResult{}, err
	}
	if req.Status != "pending" {
		return ApplyResult{}, ErrRequestConflict
	}
	if err := s.requests.ClaimForApply(ctx, id, approver); err != nil {
		return ApplyResult{}, ErrRequestConflict // lost the race / already decided
	}
	target := ""
	if req.TargetConfigID != nil {
		target = *req.TargetConfigID
	}
	sels := make([]Selection, 0, len(req.Selections))
	for _, sel := range req.Selections {
		sels = append(sels, Selection{Key: sel.Key, Action: Action(sel.Action)})
	}
	res, err := s.Apply(ctx, ApplyRequest{
		SourceConfigID: req.SourceConfigID,
		TargetConfigID: target,
		TargetEnvID:    req.TargetEnvID,
		TargetName:     req.TargetName,
		CreateTarget:   req.CreateTarget,
		SourceVersion:  req.SourceVersion,
		Selections:     sels,
		Actor:          approver,
	})
	if err != nil {
		_ = s.requests.RevertToPending(ctx, id) // release the claim so it can be retried
		return ApplyResult{}, err
	}
	if verr := s.requests.SetAppliedVersion(ctx, id, res.TargetVersion); verr != nil {
		return res, verr
	}
	return res, nil
}

func (s *Service) RejectRequest(ctx context.Context, id, approver, note string) error {
	if err := s.requests.Decide(ctx, id, "rejected", approver, note); err != nil {
		return ErrRequestConflict
	}
	return nil
}

func (s *Service) CancelRequest(ctx context.Context, id, requester string) error {
	if err := s.requests.Decide(ctx, id, "cancelled", requester, ""); err != nil {
		return ErrRequestConflict
	}
	return nil
}
```

(If `validateStep`'s signature differs — verify `internal/promote/service.go:118` `validateStep(ctx, projectID, srcEnv, dstEnv string) error` — pass the target ENV id as `dstEnv`.)

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/promote/ -run 'TestRequest'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/promote/service.go internal/promote/requests.go internal/promote/requests_test.go
git commit -m "feat(promote): request lifecycle (create/approve/reject/cancel) reusing Apply"
```

---

### Task 4: REST handlers + routes

**Files:**
- Create: `internal/api/promotion_request_handlers.go`, `internal/api/promotion_request_e2e_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write the handlers**

Create `internal/api/promotion_request_handlers.go`. Follow the `handlePromoteApply` template. Six handlers:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/promote"
)

func promoReqJSON(p any) map[string]any { // helper to shape a value-free response
	return map[string]any{}
}

// POST /v1/promote/requests — file a request (promotion:request on SOURCE env).
func (s *Server) handlePromoteRequestCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string `json:"from_config"`
		To            string `json:"to_config"`
		ToEnv         string `json:"to_env"`
		ToName        string `json:"to_name"`
		Create        bool   `json:"create"`
		SourceVersion int    `json:"source_version"`
		Note          string `json:"note"`
		Selections    []struct {
			Key    string `json:"key"`
			Action string `json:"action"`
		} `json:"selections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if body.From == "" || body.ToEnv == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from_config and to_env are required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), body.From)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, srcRes); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	// Request rights are scoped to the SOURCE env.
	if !s.authorize(w, r, authz.PromotionRequest, srcRes, "promotion.request.create", "configs/"+body.From) {
		return
	}
	sels := make([]promote.Selection, 0, len(body.Selections))
	for _, sel := range body.Selections {
		sels = append(sels, promote.Selection{Key: sel.Key, Action: promote.Action(sel.Action)})
	}
	// Resolve target env id from the env scope (ToEnv is an env slug/id per the
	// promote convention — mirror handlePromoteApply's resolveScopeResource use).
	tgtRes, err := s.resolveScopeResource(r.Context(), "environment", body.ToEnv)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	id, err := s.promote.CreateRequest(r.Context(), promote.CreateRequestInput{
		SourceConfigID: body.From,
		TargetConfigID: body.To,
		TargetEnvID:    tgtRes.EnvID,
		TargetName:     body.ToName,
		CreateTarget:   body.Create,
		SourceVersion:  body.SourceVersion,
		Selections:     sels,
		Note:           body.Note,
		RequestedBy:    promoteActorUser(r),
	})
	if err != nil {
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "promotion.request.create", "promote/requests/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "pending"})
}
```

Then implement the remaining five in the same file:

- `handlePromoteRequestList` (`GET /v1/promote/requests`): query params `project` (required), `status`, `mine`. Authorize the caller can read the project (use `authz.SecretRead` or `ProjectRead` on the project resource — mirror how other project-scoped lists authorize). If `mine=true`, call `s.promote.ListRequestsByRequester(ctx, promoteActorUser(r), status)`; else `s.promote.ListRequestsByProject(ctx, projectID, status)` and, for each, keep only those the caller may approve OR requested (filter: `promoteActorUser(r) == req.RequestedBy` OR `s.can(r, authz.SecretPromote, authz.Resource{ProjectID: projectID, EnvID: req.TargetEnvID}) == nil`). Render value-free rows via `promoReqView` (below).
- `handlePromoteRequestGet` (`GET /v1/promote/requests/{id}`): load, authorize (requester or target-approver), render detail incl. a value-free diff via `s.promote.Preview(ctx, req.SourceConfigID, target, actor)` (only when a target exists; skip diff when creating). Never include values.
- `handlePromoteRequestApprove` (`POST /v1/promote/requests/{id}/approve`): load; authorize `authz.SecretPromote` on `{ProjectID, EnvID: req.TargetEnvID}`; **four-eyes**: `if promoteActorUser(r) == req.RequestedBy { writeError(w, 403, CodeForbidden, "cannot approve your own request"); return }`; call `s.promote.ApproveRequest(ctx, id, promoteActorUser(r))`; map `promote.ErrRequestConflict` → `409` `request_not_pending`, other Apply errors via `s.writePromoteError`; audit `promotion.request.approve`; return the ApplyResult shape (`{target_version, applied:[keys], skipped}`).
- `handlePromoteRequestReject` (`POST …/reject`): body `{note}`; same authz + four-eyes as approve; `s.promote.RejectRequest`; audit `promotion.request.reject`.
- `handlePromoteRequestCancel` (`POST …/cancel`): load; authorize the caller **is the requester** (`promoteActorUser(r) == req.RequestedBy` else `403`); `s.promote.CancelRequest`; audit `promotion.request.cancel`.

Add a value-free view helper:

```go
func promoReqView(p *store.PromotionRequest) map[string]any {
	keys := make([]string, 0, len(p.Selections))
	for _, s := range p.Selections {
		keys = append(keys, s.Key) // key NAMES only
	}
	m := map[string]any{
		"id": p.ID, "project_id": p.ProjectID, "source_config_id": p.SourceConfigID,
		"source_version": p.SourceVersion, "target_env_id": p.TargetEnvID,
		"target_name": p.TargetName, "create_target": p.CreateTarget,
		"keys": keys, "selections": p.Selections, "note": p.Note,
		"status": p.Status, "requested_by": p.RequestedBy,
		"created_at": p.CreatedAt,
	}
	if p.TargetConfigID != nil {
		m["target_config_id"] = *p.TargetConfigID
	}
	if p.DecidedBy != nil {
		m["decided_by"] = *p.DecidedBy
	}
	if p.AppliedTargetVersion != nil {
		m["applied_target_version"] = *p.AppliedTargetVersion
	}
	return m
}
```

(Delete the throwaway `promoReqJSON` stub above — it was only a placeholder to make the first code block self-contained; use `promoReqView` everywhere.) Import `store` and remove unused `chi` if the create handler doesn't need it (the `{id}` handlers use `chi.URLParam(r, "id")`). Confirm `CodeForbidden` exists (grep `errors.go`); if the constant differs, use the existing forbidden code.

- [ ] **Step 2: Wire the routes**

In `internal/api/server.go`, inside the existing `if s.promote != nil { r.Group(...) }` block (server.go:260), add after the existing promote routes:

```go
			r.Post("/v1/promote/requests", s.handlePromoteRequestCreate)
			r.Get("/v1/promote/requests", s.handlePromoteRequestList)
			r.Get("/v1/promote/requests/{id}", s.handlePromoteRequestGet)
			r.Post("/v1/promote/requests/{id}/approve", s.handlePromoteRequestApprove)
			r.Post("/v1/promote/requests/{id}/reject", s.handlePromoteRequestReject)
			r.Post("/v1/promote/requests/{id}/cancel", s.handlePromoteRequestCancel)
```

- [ ] **Step 3: Write the e2e + leak test**

Create `internal/api/promotion_request_e2e_test.go` using the full-server test harness (see `promotion_apply_e2e_test.go` / `promotion_e2e_test.go` for how they boot a server, create a project/env/config, seed secrets, and authenticate as users with specific roles). Cover: a developer (no target promote) files a request → 201; an admin approves → the target gets a new version and status=applied; self-approval by the requester → 403; a second approve → 409. Add a leak assertion that no seeded secret VALUE appears in any request response body or in the audit export (mirror the existing leak tests).

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/api/ -run 'PromoteRequest|PromotionRequest' -v`
Expected: PASS (testcontainers).

- [ ] **Step 5: Commit**

```bash
git add internal/api/promotion_request_handlers.go internal/api/promotion_request_e2e_test.go internal/api/server.go
git commit -m "feat(api): promotion request endpoints (create/list/get/approve/reject/cancel)"
```

---

### Task 5: CLI subcommands

**Files:**
- Modify: `cmd/janus/promotion_commands.go` (register subcommands on the existing promote cmd)
- Create: `cmd/janus/promotion_request_commands.go`, `cmd/janus/promotion_request_commands_test.go`

- [ ] **Step 1: Write the CLI test (fails first)**

Create `cmd/janus/promotion_request_commands_test.go` with an httptest stub for the six endpoints and assert the wire calls. Model it on the existing `promotion_commands_test.go` (see how it stubs `/v1/promote/*` and uses `runCLI`). Test `request` (POST body), `requests` (GET list renders an id), `approve`/`reject`/`cancel` (POST to the right path):

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubPromoReq(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	// binding resolution helpers may hit env/config lists — stub minimally as the
	// existing promotion_commands_test does; copy those stubs.
	mux.HandleFunc("POST /v1/promote/requests", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "rq1", "status": "pending"})
	})
	mux.HandleFunc("GET /v1/promote/requests", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"requests": []map[string]any{{"id": "rq1", "status": "pending", "keys": []string{"DB_URL"}}}})
	})
	mux.HandleFunc("POST /v1/promote/requests/rq1/approve", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"target_version": 2, "applied": []string{"DB_URL"}, "skipped": []string{}})
	})
	mux.HandleFunc("POST /v1/promote/requests/rq1/reject", func(w http.ResponseWriter, r *http.Request) { paths = append(paths, "POST "+r.URL.Path); w.WriteHeader(200) })
	mux.HandleFunc("POST /v1/promote/requests/rq1/cancel", func(w http.ResponseWriter, r *http.Request) { paths = append(paths, "POST "+r.URL.Path); w.WriteHeader(200) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestPromoteRequestCLI(t *testing.T) {
	ts, paths := stubPromoReq(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "dev", "--config", "default"}
	if _, err := runCLI(t, "", append([]string{"promote", "request", "--to", "prod", "--key", "DB_URL", "--note", "ship"}, a...)...); err != nil {
		t.Fatalf("request: %v", err)
	}
	out, err := runCLI(t, "", append([]string{"promote", "requests", "--project", "acme", "--address", ts.URL, "--token", "janus_svc_test"}...)...)
	if err != nil || !strings.Contains(out, "rq1") {
		t.Fatalf("requests: %q %v", out, err)
	}
	if _, err := runCLI(t, "", "promote", "approve", "rq1", "--address", ts.URL, "--token", "janus_svc_test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	for _, want := range []string{"POST /v1/promote/requests", "GET /v1/promote/requests", "POST /v1/promote/requests/rq1/approve"} {
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

(Match the exact binding-resolution stubs the existing `promotion_commands_test.go` uses — the `request` subcommand resolves the source config from the binding like `promote` does, so it needs the same `/v1/projects/{pid}/environments`+`/configs` stubs. Copy them.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/janus/ -run TestPromoteRequestCLI`
Expected: FAIL — unknown subcommand `request`.

- [ ] **Step 3: Implement the subcommands**

Create `cmd/janus/promotion_request_commands.go` with a function `addPromoteRequestSubcommands(cmd *cobra.Command)` that builds and attaches `request`, `requests`, `approve`, `reject`, `cancel`. `request` resolves the source binding (`resolveBinding`) and the source config id (`c.resolveConfigID(project, env, config)`), builds the POST body (`from_config`, `to_env`, selections from `--key`/`--all`, `note`), and prints the returned id. `requests` GETs `/v1/promote/requests?project=<slug-or-id>&status=&mine=` and prints a table (id, status, target, keys). `approve <id>`/`reject <id>`/`cancel <id>` POST to the respective path; `approve` prints the ApplyResult summary; `reject`/`cancel` TTY-confirm unless `--yes`. Use `newAPIClient`, `c.call`, `isTerminalCmd`, `promptLine` per the existing CLI patterns.

- [ ] **Step 4: Register the subcommands**

In `cmd/janus/promotion_commands.go`, at the end of `newPromoteCmd()` (before `return cmd`), add:

```go
	addPromoteRequestSubcommands(cmd)
```

(The parent keeps its `RunE` for direct `janus promote --to <env>`; cobra routes `janus promote request …` to the child.)

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./cmd/janus/ -run TestPromoteRequestCLI` then `go test ./cmd/janus/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/janus/promotion_request_commands.go cmd/janus/promotion_request_commands_test.go cmd/janus/promotion_commands.go
git commit -m "feat(cli): janus promote request/requests/approve/reject/cancel"
```

---

### Task 6: Web UI — Requests queue, review, request-instead

**Files:**
- Create: `web/src/…/promotion/RequestsPanel.tsx`, `RequestReview.tsx`, request API hooks, + tests
- Modify: the existing promotion page/route + nav to surface the Requests view and a pending badge; the direct-promote flow to offer "Request approval instead" when the user lacks target promote (or on 403).

**First:** read the existing promotion UI (`web/src` — find the promote components, the API client module, the TanStack Query hooks, and how msw mocks mirror handler wire shapes). Match the Nocturne token classes; no raw palette. `npm test -- --run` (not watch); `npm run smoke` checks both themes.

- [ ] **Step 1: Add API client + hooks (with msw-mocked tests)**

Add typed client functions and TanStack Query hooks for the six endpoints (`createPromotionRequest`, `listPromotionRequests`, `getPromotionRequest`, `approvePromotionRequest`, `rejectPromotionRequest`, `cancelPromotionRequest`), mirroring the wire shapes from Task 4 (`{id,status,keys,note,target_env_id,target_name,source_version,requested_by,...}`; approve returns `{target_version,applied,skipped}`). Write a hook test using the existing msw setup; the mocks MUST mirror the Go response shapes. Run `npm test -- --run <file>`; expect green.

- [ ] **Step 2: RequestsPanel (queue + my requests)**

A panel listing `pending` requests the viewer can approve (fetched with `?project=&status=pending`) plus a "My requests" section (`?mine=true`). Each row shows target env/config, key count, requester, note, status. Approve/Reject buttons on approvable rows (open the review), a Cancel action on the viewer's own pending rows. Value-free (key names only). Test rendering + the approve/reject/cancel mutations (await data-dependent content to avoid react-query/msw races). `npm test -- --run`.

- [ ] **Step 3: RequestReview (diff + decision)**

A review surface showing the value-free diff (added/changed/removed **key names**) from the request detail + the requester's note, with Approve (calls approve, shows the ApplyResult counts + skipped) and Reject (captures a note). Test both paths.

- [ ] **Step 4: "Request approval instead" entry**

In the existing direct-promote flow, when the user lacks target `secret:promote` (detect via a 403 on apply, or a capability probe), offer filing a request with the same selected keys (calls `createPromotionRequest`). Add a pending-count badge on the promotion nav entry, sourced from the pending list. Test the fallback path.

- [ ] **Step 5: Full web checks**

Run: `npm test -- --run` (all green) and `npm run smoke` (both themes).

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(web): promotion requests queue, review, and request-instead flow"
```

---

### Task 7: Full verification + docs + tracker + memory

**Files:** `docs/guides/managing-secrets.md` or the promotion doc; `docs/openapi.yaml`; `gaps.md`.

- [ ] **Step 1: Full build + tests + race + leak**

Run: `go build ./... && go test ./... && go test -race ./cmd/janus/... && go test ./internal/api/ -run 'Leak' && go test ./internal/promote/ -run 'Leak'`
Expected: all PASS (api/store/promote use testcontainers — run with Docker up).

- [ ] **Step 2: OpenAPI drift — document the 6 new routes**

The drift test `internal/api/openapi_drift_test.go` now fails (6 undocumented routes). Add path items for `/v1/promote/requests` (GET, POST), `/v1/promote/requests/{id}` (GET), `.../{id}/approve` (POST), `.../{id}/reject` (POST), `.../{id}/cancel` (POST) to `docs/openapi.yaml`, tag `promote`, value-free (no secret values; note the request stores key names only). Run `go test ./internal/api/ -run TestOpenAPINoDrift` until green.

- [ ] **Step 3: Security gates**

Run: `gosec ./cmd/janus/... ./internal/... ; govulncheck ./...`
Expected: no new gosec findings in the new files (the 4 pre-existing vendored `internal/crypto/shamir` findings are known/out of scope); govulncheck only the known local go1.25.11 stdlib-TLS artifact (repo pins go1.26.5).

- [ ] **Step 4: Docs**

Update the promotion how-to (find where Phase-A promotion is documented, e.g. `docs/guides/managing-secrets.md` or an ops doc) with a "Requesting a promotion (approval workflow)" section: who can request (`promotion:request`, developer+), who approves (`secret:promote` on target), the CLI verbs, and value-safety (requests carry key names only).

- [ ] **Step 5: Mark `gaps.md` item #11 done**

Strike the "Phase B: promotion approval workflow" priority item with `**[DONE 2026-07-16]**` and a one-line summary.

- [ ] **Step 6: Commit**

```bash
git add docs/ gaps.md
git commit -m "docs: document promotion approval workflow; mark gaps #11 done"
```

---

## Self-Review

**Spec coverage:**
- Capability-gap trigger, Phase A unchanged → no Phase-A code touched; approve gates on existing `SecretPromote` (Task 4).
- `promotion:request` new action, developer+, source-scoped → Task 1 (action) + Task 4 (authorized against the SOURCE resource).
- Data model (value-free) → Task 2 migration + repo; selections store key+action only.
- State machine pending→applied/rejected/cancelled, claim-first concurrency → Task 2 (`ClaimForApply`/`RevertToPending`/`Decide`) + Task 3 (`ApproveRequest`).
- Four-eyes → Task 4 approve/reject handlers (`requester != approver` → 403).
- REST/CLI/UI surfaces → Tasks 4/5/6.
- Value-safety + audit (`promotion.request.{create,approve,reject,cancel}`) → Task 4 + leak tests (Tasks 4/7).
- Non-goals respected (no notifications/quorum/scheduled-apply/TTL/policy-gate) → nothing implements them.

**Placeholder scan:** The one intentionally non-inlined body is Task 4's five secondary handlers (described precisely with authz/audit/response rules rather than full code) and Task 6's UI (described with the exact API contract + components + tests, to be matched against the existing promotion components and locked design system). The throwaway `promoReqJSON` stub is explicitly flagged for deletion. Everything security-critical (repo CAS SQL, service claim-first logic, create/approve handlers, authz, migration) is complete code.

**Type consistency:** `store.PromotionRequest`/`PromotionSelection` (Task 2) are consumed by `promote` (Task 3) and rendered by `promoReqView` (Task 4). `promote.CreateRequestInput`/`ApproveRequest`/`ErrRequestConflict` (Task 3) are called by the handlers (Task 4). `Selection{Key,Action}`/`Action("set"|"remove")` match the existing promote types. `ClaimForApply`→`SetAppliedVersion`/`RevertToPending` names are consistent across Tasks 2–3.

**Open items to confirm during implementation:** (a) the exact seed/harness helpers in `internal/store` and `internal/promote` tests (reuse, don't reinvent); (b) `validateStep`'s target argument (env id); (c) whether `ToEnv` in the promote body is a slug or id and reuse `resolveScopeResource("environment", …)` exactly as `handlePromoteApply` does; (d) the forbidden error code constant name.
