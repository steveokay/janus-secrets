# Config Inheritance + Secret References Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Read-time resolution of config inheritance (same-environment, child-wins) and secret references (`${projects.<project>.<env>.<config>.KEY}` + local `${KEY}`) over the existing secret read path, resolve-by-default with `?raw=true` opt-out, strict per-target authorization for references, atomic failure, and per-deref audit.

**Architecture:** A new pure `internal/resolve` package composes over two ports — `RawReader` (config coordinate → raw decrypted values, implemented by `internal/secrets`) and `Authorizer` (per-target `secret:read` check, implemented by `internal/api`). It merges inheritance first, then expands references transitively with two cycle guards and a depth cap, returning resolved values plus a provenance list the API turns into audit events.

**Tech Stack:** Go 1.26.4, stdlib only in `internal/resolve`; `pgx`/`chi` at the edges; testcontainers for the store/api integration tests.

**Spec:** `docs/superpowers/specs/2026-07-05-inheritance-references-design.md`. Locked decisions: absolute+local grammar; caller-authorized references, transparent inheritance; resolve-by-default (`?raw=true` opts out); atomic failure; per-dereferenced-target audit.

---

## Existing integration points (verified)

- **`internal/secrets`**: `Service{st,projects,envs,configs,secrets,keyring}` (`service.go`); `NewService(st,kr)`. Read path: `RevealConfig`/`GetSecret`/`GetSecretVersion` call `s.secrets.GetLatest(ctx, cfg.ID) (store.ConfigVersion, map[string]store.SecretValue, error)`, then `s.decryptValue(proj, cfg.ID, sv, kek)`. `resolveProject(ctx, configID)` walks config→env→project. `zeroize([]byte)`. Errors: `ErrSealed`/`ErrNotFound`/`ErrDecrypt`/`ErrValidation` (`errors.go`).
- **`internal/store`**: `ConfigRepo.Get(id)`, `ConfigRepo.GetByName(envID, name)`, `EnvironmentRepo.GetBySlug(projID, slug)`, `ProjectRepo.GetBySlug(slug)`. `Config{ID,EnvironmentID,Name,InheritsFrom *string}` (`models.go`). `store.ErrNotFound`.
- **`internal/authz`**: `Engine.Can(ctx, auth.Principal, *TokenScope, Action, Resource) error`; `SecretRead`; `Resource{ProjectID,EnvID,ConfigID}`; `ErrForbidden`.
- **`internal/api`**: `Server{service *secrets.Service, ...}`. `s.can(r, action, res) error`, `s.authorize(w,r,action,res,auditAction,auditResource) bool`, `s.writeAuthzError(w,err)`, `s.record(r,action,resource,result,code,detail) error`, `s.configResource(r) (authz.Resource, cid string, err error)`, `PrincipalFrom(ctx)`. Handlers `handleSecretsList`/`handleSecretGet` (`secrets_handlers.go`). `writeServiceError` (`service_errors.go`). `writeError(w,status,code,msg)`, `CodeValidation`, `CodeForbidden`, `CodeInternal` (`errors.go`).
- **`cmd/janus`**: `run.go` (`GET .../secrets?reveal=true`), `secrets_download.go` (same), `secrets_cmd.go` (`get` at `.../secrets/{key}`, `list` at `.../secrets`). `c.call(method, path, body, &out)`.

## Package dependency direction (no cycles)

`internal/resolve` imports **stdlib only** (defines the ports + types). `internal/secrets` imports `internal/resolve` (implements `RawReader`, returns `resolve.RawConfig`). `internal/api` imports both. So: `resolve` (leaf) ← `secrets` ← `api`, and `resolve` ← `api`. No cycle.

## File structure

| File | Responsibility |
|------|----------------|
| `internal/resolve/errors.go` | error sentinels |
| `internal/resolve/types.go` | `Coord`, `RawConfig`, `Provenance`, `RawReader`, `Authorizer` |
| `internal/resolve/parse.go` (+ `_test`) | reference grammar → `[]segment` |
| `internal/resolve/inherit.go` (+ `_test`) | `resolveMerged` chain walk |
| `internal/resolve/resolve.go` (+ `_test`) | `Resolver.Resolve`/`ResolveKey`, expansion, cycles, depth, zeroize, provenance |
| `internal/secrets/rawread.go` (+ `_test`) | `ReadRaw`/`ReadRawByID` |
| `internal/api/resolve_adapter.go` | `apiAuthorizer`, `resolverFor`, sentinel mapping |
| `internal/api/secrets_handlers.go` | reveal resolve-by-default + `?raw`, single-key, masked origin, audit |
| `cmd/janus/run.go`, `secrets_download.go`, `secrets_cmd.go` | `--raw`, list `origin` |

---

## Task 1: resolve package — types + error sentinels

**Files:**
- Create: `internal/resolve/errors.go`, `internal/resolve/types.go`
- Test: `internal/resolve/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
package resolve

import (
	"errors"
	"testing"
)

func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{
		ErrInheritanceCycle, ErrBrokenInheritance, ErrReferenceCycle,
		ErrUnresolvedReference, ErrForbiddenReference, ErrReferenceDepth,
		ErrBadReferenceSyntax,
	}
	for i := range all {
		for j := range all {
			if i != j && errors.Is(all[i], all[j]) {
				t.Fatalf("sentinels %d and %d must be distinct", i, j)
			}
		}
	}
}

func TestCoordAndRawConfigZeroValues(t *testing.T) {
	var c Coord
	if c.Project != "" || c.Env != "" || c.Config != "" {
		t.Fatal("zero Coord must be empty")
	}
	rc := RawConfig{Values: map[string][]byte{}}
	if rc.InheritsFrom != nil || len(rc.Values) != 0 {
		t.Fatal("zero RawConfig unexpected")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolve/ -run 'TestSentinels|TestCoord' -count=1`
Expected: FAIL — package/types don't exist.

- [ ] **Step 3: Write the implementation**

`internal/resolve/errors.go`:

```go
// Package resolve composes config inheritance and read-time secret references
// over two ports (RawReader, Authorizer). It is pure: no crypto, no HTTP, no
// store/authz imports. Inheritance is merged first (child wins), then references
// are expanded transitively with cycle detection and a depth cap. Any
// unresolvable reference fails the whole resolution (atomic).
package resolve

import "errors"

var (
	// ErrInheritanceCycle: the inherits_from chain loops.
	ErrInheritanceCycle = errors.New("resolve: inheritance cycle")
	// ErrBrokenInheritance: a base config in the chain is missing or deleted.
	ErrBrokenInheritance = errors.New("resolve: broken inheritance base")
	// ErrReferenceCycle: reference expansion revisits a (config,key) frame.
	ErrReferenceCycle = errors.New("resolve: reference cycle")
	// ErrUnresolvedReference: a referenced project/env/config/key does not exist.
	ErrUnresolvedReference = errors.New("resolve: unresolved reference")
	// ErrForbiddenReference: caller lacks secret:read on a referenced target.
	ErrForbiddenReference = errors.New("resolve: forbidden reference")
	// ErrReferenceDepth: expansion exceeded the depth cap (backstop).
	ErrReferenceDepth = errors.New("resolve: reference depth exceeded")
	// ErrBadReferenceSyntax: a ${...} token is malformed.
	ErrBadReferenceSyntax = errors.New("resolve: bad reference syntax")
)
```

`internal/resolve/types.go`:

```go
package resolve

import "context"

// Coord addresses a config by human names, as written in a reference.
type Coord struct{ Project, Env, Config string }

// RawConfig is a config's raw (un-resolved) decrypted state. Values are verbatim
// stored plaintext, ${...} intact. The consumer must treat Values as owned and
// zero them when done.
type RawConfig struct {
	ProjectID, EnvID, ConfigID string
	Project, Env, Config       string  // canonical names, for provenance/errors
	InheritsFrom               *string // parent config id, if any
	Values                     map[string][]byte
}

// Path is the human path project/env/config, for provenance and error messages.
func (rc RawConfig) Path() string { return rc.Project + "/" + rc.Env + "/" + rc.Config }

// RawReader returns raw decrypted config state, by coordinate (name lookup) or by
// id. Implemented by internal/secrets.
type RawReader interface {
	ReadRaw(ctx context.Context, coord Coord) (RawConfig, error)
	ReadRawByID(ctx context.Context, configID string) (RawConfig, error)
}

// Authorizer performs the strict per-target secret:read check for a reference.
// Implemented by internal/api. A nil Authorizer means "trusted caller" (checks
// skipped) — used only at internal call sites without a principal.
type Authorizer interface {
	CanReadSecrets(ctx context.Context, target RawConfig) error
}

// Provenance records a distinct target config read via a reference (for audit).
type Provenance struct{ ProjectID, EnvID, ConfigID, Path string }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/resolve/ -run 'TestSentinels|TestCoord' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/resolve/errors.go internal/resolve/types.go internal/resolve/types_test.go
git commit -m "feat(resolve): package skeleton — ports, types, error sentinels"
```

---

## Task 2: reference grammar parser

**Files:**
- Create: `internal/resolve/parse.go`
- Test: `internal/resolve/parse_test.go`

Parses a value into a sequence of literal and reference segments. Grammar: `${KEY}` = local ref; `${projects.<project>.<env>.<config>.KEY}` = absolute ref; `$$` = a literal `$` (so `$${KEY}` is literal `${KEY}`); an unterminated `${` or a malformed inner body → `ErrBadReferenceSyntax`.

- [ ] **Step 1: Write the failing test**

```go
package resolve

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseSegments(t *testing.T) {
	loc := func(k string) segment { return segment{ref: &ref{local: true, key: k}} }
	abs := func(p, e, c, k string) segment {
		return segment{ref: &ref{coord: Coord{p, e, c}, key: k}}
	}
	lit := func(s string) segment { return segment{literal: s} }

	cases := []struct {
		name string
		in   string
		want []segment
	}{
		{"plain", "hello", []segment{lit("hello")}},
		{"local", "${DB}", []segment{loc("DB")}},
		{"absolute", "${projects.billing.prod.api.KEY}", []segment{abs("billing", "prod", "api", "KEY")}},
		{"interleaved", "u://${U}:${P}@${projects.i.prod.db.HOST}/x", []segment{
			lit("u://"), loc("U"), lit(":"), loc("P"), lit("@"), abs("i", "prod", "db", "HOST"), lit("/x"),
		}},
		{"escape", "$${DB}", []segment{lit("$"), lit("{DB}")}},
		{"lone dollar", "cost is $5", []segment{lit("cost is "), lit("$"), lit("5")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSegments(tc.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseSegments(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseSegmentsErrors(t *testing.T) {
	for _, in := range []string{"${", "${unterminated", "${ }", "${a.b.c.d}", "${projects.a.b.KEY}", "${projects.a.b.c.d.e.KEY}"} {
		if _, err := parseSegments(in); !errors.Is(err, ErrBadReferenceSyntax) {
			t.Fatalf("parseSegments(%q): want ErrBadReferenceSyntax, got %v", in, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolve/ -run TestParseSegments -count=1`
Expected: FAIL — `parseSegments`/`segment`/`ref` undefined.

- [ ] **Step 3: Write the implementation**

`internal/resolve/parse.go`:

```go
package resolve

import (
	"fmt"
	"strings"
)

// ref is a parsed reference token. local ${KEY} sets local=true and key; an
// absolute ${projects.p.e.c.KEY} sets coord and key.
type ref struct {
	local bool
	coord Coord
	key   string
}

// segment is either literal text (ref == nil) or a reference (literal == "").
type segment struct {
	literal string
	ref     *ref
}

// parseSegments splits value into literal/reference segments. $$ is a literal $;
// ${...} is a reference; an unterminated ${ or malformed body is
// ErrBadReferenceSyntax.
func parseSegments(value string) ([]segment, error) {
	var out []segment
	for i := 0; i < len(value); {
		d := strings.IndexByte(value[i:], '$')
		if d < 0 {
			out = append(out, segment{literal: value[i:]})
			break
		}
		if d > 0 {
			out = append(out, segment{literal: value[i : i+d]})
		}
		j := i + d // position of '$'
		switch {
		case j+1 < len(value) && value[j+1] == '$':
			out = append(out, segment{literal: "$"})
			i = j + 2
		case j+1 < len(value) && value[j+1] == '{':
			end := strings.IndexByte(value[j+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated ${", ErrBadReferenceSyntax)
			}
			body := value[j+2 : j+2+end]
			rf, err := parseRefBody(body)
			if err != nil {
				return nil, err
			}
			out = append(out, segment{ref: rf})
			i = j + 2 + end + 1
		default:
			out = append(out, segment{literal: "$"})
			i = j + 1
		}
	}
	return out, nil
}

func parseRefBody(body string) (*ref, error) {
	parts := strings.Split(body, ".")
	switch {
	case len(parts) == 1:
		if !validSegmentToken(parts[0]) {
			return nil, fmt.Errorf("%w: bad local key %q", ErrBadReferenceSyntax, body)
		}
		return &ref{local: true, key: parts[0]}, nil
	case len(parts) == 5 && parts[0] == "projects":
		for _, p := range parts[1:] {
			if !validSegmentToken(p) {
				return nil, fmt.Errorf("%w: bad reference %q", ErrBadReferenceSyntax, body)
			}
		}
		return &ref{coord: Coord{Project: parts[1], Env: parts[2], Config: parts[3]}, key: parts[4]}, nil
	default:
		return nil, fmt.Errorf("%w: %q must be KEY or projects.p.e.c.KEY", ErrBadReferenceSyntax, body)
	}
}

// validSegmentToken permits non-empty tokens of letters, digits, '_' and '-'.
// (Existence and finer key/slug rules are enforced at resolution time by the
// store lookups; this only rejects obviously malformed tokens.)
func validSegmentToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		ok := c == '_' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/resolve/ -run TestParseSegments -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/resolve/parse.go internal/resolve/parse_test.go
git commit -m "feat(resolve): reference grammar parser (local/absolute/\$\$ escape)"
```

---

## Task 3: inheritance merge (chain walk)

**Files:**
- Create: `internal/resolve/inherit.go`
- Test: `internal/resolve/inherit_test.go`

`resolveMerged` walks `InheritsFrom` from the requested config up to its deepest ancestor, then merges deepest→child so the child wins. It uses a `fakeReader` in tests.

- [ ] **Step 1: Write the failing test**

```go
package resolve

import (
	"context"
	"errors"
	"testing"
)

// fakeReader serves RawConfigs from an in-memory map keyed by config id, plus a
// coord index for ReadRaw. Missing id → notFoundErr.
type fakeReader struct {
	byID    map[string]RawConfig
	byCoord map[Coord]string // coord → config id
}

var errFakeNotFound = errors.New("fake: not found")

func (f *fakeReader) ReadRawByID(_ context.Context, id string) (RawConfig, error) {
	rc, ok := f.byID[id]
	if !ok {
		return RawConfig{}, errFakeNotFound
	}
	return rc, nil
}
func (f *fakeReader) ReadRaw(_ context.Context, c Coord) (RawConfig, error) {
	id, ok := f.byCoord[c]
	if !ok {
		return RawConfig{}, errFakeNotFound
	}
	return f.ReadRawByID(context.Background(), id)
}

func strptr(s string) *string { return &s }

func TestResolveMergedChildWins(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"base":   {ConfigID: "base", Values: map[string][]byte{"A": []byte("1"), "B": []byte("2")}},
		"branch": {ConfigID: "branch", InheritsFrom: strptr("base"), Values: map[string][]byte{"B": []byte("20"), "C": []byte("3")}},
	}}
	merged, self, err := resolveMerged(context.Background(), r, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if self.ConfigID != "branch" {
		t.Fatalf("self = %s", self.ConfigID)
	}
	want := map[string]string{"A": "1", "B": "20", "C": "3"} // child B wins
	if len(merged) != len(want) {
		t.Fatalf("merged keys = %v", merged)
	}
	for k, v := range want {
		if string(merged[k]) != v {
			t.Fatalf("merged[%s] = %q, want %q", k, merged[k], v)
		}
	}
}

func TestResolveMergedCycle(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"a": {ConfigID: "a", InheritsFrom: strptr("b"), Values: map[string][]byte{}},
		"b": {ConfigID: "b", InheritsFrom: strptr("a"), Values: map[string][]byte{}},
	}}
	if _, _, err := resolveMerged(context.Background(), r, "a"); !errors.Is(err, ErrInheritanceCycle) {
		t.Fatalf("want ErrInheritanceCycle, got %v", err)
	}
}

func TestResolveMergedBrokenBaseVsMissingRoot(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"branch": {ConfigID: "branch", InheritsFrom: strptr("gone"), Values: map[string][]byte{}},
	}}
	// Missing base → ErrBrokenInheritance.
	if _, _, err := resolveMerged(context.Background(), r, "branch"); !errors.Is(err, ErrBrokenInheritance) {
		t.Fatalf("broken base: want ErrBrokenInheritance, got %v", err)
	}
	// Missing root → original reader error propagates (not ErrBrokenInheritance).
	if _, _, err := resolveMerged(context.Background(), r, "gone"); !errors.Is(err, errFakeNotFound) {
		t.Fatalf("missing root: want errFakeNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolve/ -run TestResolveMerged -count=1`
Expected: FAIL — `resolveMerged` undefined.

- [ ] **Step 3: Write the implementation**

`internal/resolve/inherit.go`:

```go
package resolve

import (
	"context"
	"fmt"
)

// resolveMerged returns configID's inheritance-merged raw values (child wins) and
// the requested config's own RawConfig (identity). It walks InheritsFrom with a
// cycle guard. A missing requested config propagates the reader's error; a
// missing/unreadable *base* in the chain is ErrBrokenInheritance.
//
// The returned map's []byte values are the reader's decrypted plaintext; the
// caller owns them and must zero them when done.
func resolveMerged(ctx context.Context, reader RawReader, configID string) (map[string][]byte, RawConfig, error) {
	seen := map[string]bool{}
	var chain []RawConfig
	id := configID
	for id != "" {
		if seen[id] {
			return nil, RawConfig{}, fmt.Errorf("%w: at config %s", ErrInheritanceCycle, id)
		}
		seen[id] = true
		rc, err := reader.ReadRawByID(ctx, id)
		if err != nil {
			if len(chain) == 0 {
				return nil, RawConfig{}, err // requested config missing: propagate (→ 404)
			}
			return nil, RawConfig{}, fmt.Errorf("%w: base %s", ErrBrokenInheritance, id)
		}
		chain = append(chain, rc)
		if rc.InheritsFrom != nil {
			id = *rc.InheritsFrom
		} else {
			id = ""
		}
	}
	// chain[0] = requested (child); chain[len-1] = deepest ancestor. Apply
	// ancestor→child so the child's keys win.
	merged := make(map[string][]byte)
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Values {
			merged[k] = v
		}
	}
	return merged, chain[0], nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/resolve/ -run TestResolveMerged -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/resolve/inherit.go internal/resolve/inherit_test.go
git commit -m "feat(resolve): inheritance merge (child-wins chain walk, cycle, broken base)"
```

---

## Task 4: reference expansion + Resolve orchestration

**Files:**
- Create: `internal/resolve/resolve.go`
- Test: `internal/resolve/resolve_test.go`

`Resolver.Resolve` merges inheritance, then expands every value's references transitively. Local `${KEY}` resolves within the same merged map; absolute `${projects.p.e.c.KEY}` reads the target config (coordinate lookup → inheritance merge), checks the `Authorizer`, recurses, and records provenance. A `(configID,key)` frame stack detects reference cycles; a depth cap is a backstop. Any failure is atomic and zeroizes partial output. Every returned value is a fresh copy so source maps can be zeroed.

- [ ] **Step 1: Write the failing test**

```go
package resolve

import (
	"context"
	"errors"
	"testing"
)

// denyAuthorizer denies a specific config id; allows the rest.
type denyAuthorizer struct{ denyID string }

func (d denyAuthorizer) CanReadSecrets(_ context.Context, t RawConfig) error {
	if t.ConfigID == d.denyID {
		return ErrForbiddenReference
	}
	return nil
}

func newReader() *fakeReader {
	return &fakeReader{byID: map[string]RawConfig{}, byCoord: map[Coord]string{}}
}
func (f *fakeReader) put(rc RawConfig, coord Coord) {
	f.byID[rc.ConfigID] = rc
	f.byCoord[coord] = rc.ConfigID
}

func TestResolveLocalAndAbsolute(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ProjectID: "pB", EnvID: "eB", ConfigID: "cB", Project: "billing", Env: "prod", Config: "api",
		Values: map[string][]byte{"HOST": []byte("db.internal")}}, Coord{"billing", "prod", "api"})
	r.put(RawConfig{ProjectID: "pA", EnvID: "eA", ConfigID: "cA", Project: "app", Env: "prod", Config: "web",
		Values: map[string][]byte{
			"USER": []byte("svc"),
			"URL":  []byte("u://${USER}@${projects.billing.prod.api.HOST}/x"),
		}}, Coord{"app", "prod", "web"})

	res := &Resolver{reader: r, authz: nil}
	out, prov, err := res.Resolve(context.Background(), "cA")
	if err != nil {
		t.Fatal(err)
	}
	if string(out["URL"]) != "u://svc@db.internal/x" {
		t.Fatalf("URL = %q", out["URL"])
	}
	if len(prov) != 1 || prov[0].ConfigID != "cB" {
		t.Fatalf("provenance = %+v", prov)
	}
}

func TestResolveForbiddenReference(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cB", Project: "billing", Env: "prod", Config: "api",
		Values: map[string][]byte{"K": []byte("v")}}, Coord{"billing", "prod", "api"})
	r.put(RawConfig{ConfigID: "cA", Project: "app", Env: "prod", Config: "web",
		Values: map[string][]byte{"X": []byte("${projects.billing.prod.api.K}")}}, Coord{"app", "prod", "web"})

	res := &Resolver{reader: r, authz: denyAuthorizer{denyID: "cB"}}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrForbiddenReference) {
		t.Fatalf("want ErrForbiddenReference, got %v", err)
	}
}

func TestResolveReferenceCycle(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"A": []byte("${projects.b.p.y.B}")}}, Coord{"a", "p", "x"})
	r.put(RawConfig{ConfigID: "cB", Project: "b", Env: "p", Config: "y",
		Values: map[string][]byte{"B": []byte("${projects.a.p.x.A}")}}, Coord{"b", "p", "y"})
	res := &Resolver{reader: r, authz: nil}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrReferenceCycle) {
		t.Fatalf("want ErrReferenceCycle, got %v", err)
	}
}

func TestResolveUnresolvedReference(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"A": []byte("${MISSING}")}}, Coord{"a", "p", "x"})
	res := &Resolver{reader: r, authz: nil}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("local missing key: want ErrUnresolvedReference, got %v", err)
	}
}

func TestResolveKeyBinaryPassthrough(t *testing.T) {
	// A value that is exactly one reference yields the target's exact bytes.
	r := newReader()
	bin := []byte{0x00, 0xff, 0x10}
	r.put(RawConfig{ConfigID: "cB", Project: "b", Env: "p", Config: "y",
		Values: map[string][]byte{"BIN": bin}}, Coord{"b", "p", "y"})
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"K": []byte("${projects.b.p.y.BIN}")}}, Coord{"a", "p", "x"})
	res := &Resolver{reader: r, authz: nil}
	got, _, err := res.ResolveKey(context.Background(), "cA", "K")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bin) {
		t.Fatalf("binary passthrough = %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolve/ -run TestResolve -count=1`
Expected: FAIL — `Resolver`/`Resolve`/`ResolveKey` undefined.

- [ ] **Step 3: Write the implementation**

`internal/resolve/resolve.go`:

```go
package resolve

import (
	"context"
	"fmt"
)

// maxDepth caps reference expansion as a backstop beyond strict cycle detection.
const maxDepth = 32

// Resolver expands inheritance + references over its two ports.
type Resolver struct {
	reader RawReader
	authz  Authorizer // nil = trusted caller (checks skipped)
}

// New builds a Resolver. authz may be nil for trusted internal call sites.
func New(reader RawReader, authz Authorizer) *Resolver {
	return &Resolver{reader: reader, authz: authz}
}

// frame is one (config,key) on the resolution stack, for cycle detection.
type frame struct{ configID, key string }

// run carries per-Resolve mutable state.
type run struct {
	r    *Resolver
	prov map[string]Provenance // configID → provenance (dedup)
}

// Resolve returns rootConfigID's fully merged + dereferenced values, plus the
// distinct target configs read via references. Atomic: any failure returns an
// error and no values (partial output is zeroized).
func (rv *Resolver) Resolve(ctx context.Context, rootConfigID string) (map[string][]byte, []Provenance, error) {
	merged, self, err := resolveMerged(ctx, rv.reader, rootConfigID)
	defer zeroizeMap(merged)
	if err != nil {
		return nil, nil, err
	}
	st := &run{r: rv, prov: map[string]Provenance{}}
	out := make(map[string][]byte, len(merged))
	for k := range merged {
		v, err := st.resolveKey(ctx, self, merged, k, nil)
		if err != nil {
			zeroizeMap(out)
			return nil, nil, err
		}
		out[k] = v
	}
	return out, st.provList(), nil
}

// ResolveKey resolves a single key of rootConfigID (used by single-key reveals).
func (rv *Resolver) ResolveKey(ctx context.Context, rootConfigID, key string) ([]byte, []Provenance, error) {
	merged, self, err := resolveMerged(ctx, rv.reader, rootConfigID)
	defer zeroizeMap(merged)
	if err != nil {
		return nil, nil, err
	}
	if _, ok := merged[key]; !ok {
		return nil, nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, key, self.Path())
	}
	st := &run{r: rv, prov: map[string]Provenance{}}
	v, err := st.resolveKey(ctx, self, merged, key, nil)
	if err != nil {
		return nil, nil, err
	}
	return v, st.provList(), nil
}

// resolveKey expands one key's value in the context of cfg's merged map. stack
// holds the ancestry of (config,key) frames for cycle detection. It always
// returns a fresh []byte so source maps can be zeroized independently.
func (st *run) resolveKey(ctx context.Context, cfg RawConfig, merged map[string][]byte, key string, stack []frame) ([]byte, error) {
	if len(stack) >= maxDepth {
		return nil, fmt.Errorf("%w: at %s/%s", ErrReferenceDepth, cfg.Path(), key)
	}
	fr := frame{configID: cfg.ConfigID, key: key}
	for _, s := range stack {
		if s == fr {
			return nil, fmt.Errorf("%w: %s/%s", ErrReferenceCycle, cfg.Path(), key)
		}
	}
	raw, ok := merged[key]
	if !ok {
		return nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, key, cfg.Path())
	}
	segs, err := parseSegments(string(raw))
	if err != nil {
		return nil, err
	}
	// Full-slice expression forces a copy-on-append so sibling references at this
	// level cannot alias each other's frame in a shared backing array.
	next := append(stack[:len(stack):len(stack)], fr)

	// Fast path: a value that is exactly one reference returns the target's exact
	// bytes (binary passthrough).
	if len(segs) == 1 && segs[0].ref != nil {
		return st.resolveRef(ctx, cfg, merged, segs[0].ref, next)
	}
	var buf []byte
	for _, seg := range segs {
		if seg.ref == nil {
			buf = append(buf, seg.literal...)
			continue
		}
		v, err := st.resolveRef(ctx, cfg, merged, seg.ref, next)
		if err != nil {
			return nil, err
		}
		buf = append(buf, v...)
		zeroize(v)
	}
	if buf == nil {
		buf = []byte{} // non-nil empty for an empty value
	}
	return buf, nil
}

// resolveRef resolves one reference token to a fresh []byte.
func (st *run) resolveRef(ctx context.Context, cfg RawConfig, merged map[string][]byte, rf *ref, stack []frame) ([]byte, error) {
	if rf.local {
		return st.resolveKey(ctx, cfg, merged, rf.key, stack)
	}
	target, err := st.r.reader.ReadRaw(ctx, rf.coord)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.%s.%s.%s", ErrUnresolvedReference, rf.coord.Project, rf.coord.Env, rf.coord.Config, rf.key)
	}
	if st.r.authz != nil {
		if err := st.r.authz.CanReadSecrets(ctx, target); err != nil {
			return nil, fmt.Errorf("%w: %s.%s.%s.%s", ErrForbiddenReference, rf.coord.Project, rf.coord.Env, rf.coord.Config, rf.key)
		}
	}
	tMerged, tSelf, err := resolveMerged(ctx, st.r.reader, target.ConfigID)
	defer zeroizeMap(tMerged)
	if err != nil {
		return nil, err
	}
	if _, ok := tMerged[rf.key]; !ok {
		return nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, rf.key, tSelf.Path())
	}
	st.prov[tSelf.ConfigID] = Provenance{ProjectID: tSelf.ProjectID, EnvID: tSelf.EnvID, ConfigID: tSelf.ConfigID, Path: tSelf.Path()}
	return st.resolveKey(ctx, tSelf, tMerged, rf.key, stack)
}

func (st *run) provList() []Provenance {
	out := make([]Provenance, 0, len(st.prov))
	for _, p := range st.prov {
		out = append(out, p)
	}
	return out
}

func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
func zeroizeMap(m map[string][]byte) {
	for _, v := range m {
		zeroize(v)
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/resolve/ -count=1`
Expected: PASS (all resolve tests).

- [ ] **Step 5: Commit**

```bash
git add internal/resolve/resolve.go internal/resolve/resolve_test.go
git commit -m "feat(resolve): transitive reference expansion, cycles, depth cap, provenance"
```

---

## Task 5: internal/secrets raw-read port

**Files:**
- Create: `internal/secrets/rawread.go`
- Test: `internal/secrets/rawread_test.go`

Implements `resolve.RawReader` on `*secrets.Service`: `ReadRawByID(configID)` and `ReadRaw(coord)` return a config's decrypted raw values (verbatim) plus identity + `InheritsFrom`. Reuses the existing decrypt path (`GetLatest` + `decryptValue`) and the store's slug/name lookups.

- [ ] **Step 1: Write the failing test**

Reuse the package's testcontainers harness (see `internal/secrets/harness_test.go` for `newTestService`/how a project+env+config+secret is seeded; mirror an existing `secrets_test.go` setup). The test seeds a project (slug `app`), env (slug `prod`), config (`web`) with a secret `K=v`, then:

```go
func TestReadRawByCoordAndID(t *testing.T) {
	svc, _ := newTestService(t) // returns *Service (+ store) per the harness
	ctx := context.Background()
	// ... seed project app / env prod / config web / secret K=v via svc + store ...
	// (use the same helpers the existing secrets_test.go uses; capture configID)

	rc, err := svc.ReadRaw(ctx, resolve.Coord{Project: "app", Env: "prod", Config: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if string(rc.Values["K"]) != "v" || rc.Config != "web" || rc.ConfigID == "" {
		t.Fatalf("ReadRaw = %+v", rc)
	}
	byID, err := svc.ReadRawByID(ctx, rc.ConfigID)
	if err != nil || string(byID.Values["K"]) != "v" {
		t.Fatalf("ReadRawByID = %+v %v", byID, err)
	}
	// Unknown coordinate → store.ErrNotFound (mapped to resolve.ErrUnresolved at api).
	if _, err := svc.ReadRaw(ctx, resolve.Coord{Project: "nope", Env: "prod", Config: "web"}); err == nil {
		t.Fatal("unknown project must error")
	}
}
```

> Implementer note: match the exact harness constructor + seeding helpers used by the existing `internal/secrets/*_test.go`. The assertions above are the contract; adapt the setup lines to the real harness.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/secrets/ -run TestReadRaw -count=1`
Expected: FAIL — `ReadRaw`/`ReadRawByID` undefined.

- [ ] **Step 3: Write the implementation**

`internal/secrets/rawread.go`:

```go
package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ReadRaw implements resolve.RawReader: it resolves a coordinate (project slug →
// env slug → config name) to a config and returns its raw decrypted values.
func (s *Service) ReadRaw(ctx context.Context, coord resolve.Coord) (resolve.RawConfig, error) {
	proj, err := s.projects.GetBySlug(ctx, coord.Project)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	env, err := s.envs.GetBySlug(ctx, proj.ID, coord.Env)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	cfg, err := s.configs.GetByName(ctx, env.ID, coord.Config)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	return s.rawFor(ctx, proj, env, cfg)
}

// ReadRawByID implements resolve.RawReader by config id (used to walk an
// inheritance chain, whose links are stored as config ids).
func (s *Service) ReadRawByID(ctx context.Context, configID string) (resolve.RawConfig, error) {
	cfg, err := s.configs.Get(ctx, configID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	env, err := s.envs.Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	return s.rawFor(ctx, proj, env, cfg)
}

// rawFor decrypts every live secret in cfg's latest version into a raw map.
func (s *Service) rawFor(ctx context.Context, proj *store.Project, env *store.Environment, cfg *store.Config) (resolve.RawConfig, error) {
	_, state, err := s.secrets.GetLatest(ctx, cfg.ID)
	if err != nil {
		return resolve.RawConfig{}, mapStoreErr(err)
	}
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return resolve.RawConfig{}, err
	}
	defer zeroize(kek)
	values := make(map[string][]byte, len(state))
	for key, sv := range state {
		pt, err := s.decryptValue(proj, cfg.ID, sv, kek)
		if err != nil {
			for _, v := range values {
				zeroize(v)
			}
			return resolve.RawConfig{}, err
		}
		values[key] = pt
	}
	return resolve.RawConfig{
		ProjectID: proj.ID, EnvID: env.ID, ConfigID: cfg.ID,
		Project: proj.Slug, Env: env.Slug, Config: cfg.Name,
		InheritsFrom: cfg.InheritsFrom, Values: values,
	}, nil
}
```

> Note: confirm `store.Project.Slug`, `store.Environment.Slug`, `store.Config.Name`, and `store.Config.InheritsFrom` field names against `internal/store/models.go` (they were verified present). `mapStoreErr` and `unwrapProjectKEK`/`decryptValue`/`zeroize` already exist in the package.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/secrets/ -run TestReadRaw -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/rawread.go internal/secrets/rawread_test.go
git commit -m "feat(secrets): ReadRaw/ReadRawByID raw-read port for resolution"
```

---

## Task 6: api authorizer adapter + resolver construction + error mapping

**Files:**
- Create: `internal/api/resolve_adapter.go`
- Modify: `internal/api/service_errors.go`
- Test: `internal/api/resolve_adapter_test.go`

The API implements `resolve.Authorizer` with a per-request adapter that reuses `s.can`, constructs a `resolve.Resolver` bound to the request principal, and maps the new sentinels in `writeServiceError`.

- [ ] **Step 1: Write the failing test**

```go
package api

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/resolve"
)

func TestWriteServiceErrorResolveSentinels(t *testing.T) {
	s := &Server{}
	cases := []struct {
		err  error
		code int
	}{
		{resolve.ErrForbiddenReference, 403},
		{resolve.ErrInheritanceCycle, 409},
		{resolve.ErrBrokenInheritance, 409},
		{resolve.ErrReferenceCycle, 409},
		{resolve.ErrUnresolvedReference, 422},
		{resolve.ErrReferenceDepth, 422},
		{resolve.ErrBadReferenceSyntax, 400},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.writeServiceError(rec, tc.err)
		if rec.Code != tc.code {
			t.Fatalf("%v → %d, want %d", tc.err, rec.Code, tc.code)
		}
	}
}

func TestWriteServiceErrorWrappedResolve(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.writeServiceError(rec, errors.New("x")) // default
	if rec.Code != 500 {
		t.Fatalf("default → %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestWriteServiceErrorResolve -count=1`
Expected: FAIL — sentinels not mapped (403/409/422/400 come out 500).

- [ ] **Step 3: Write the implementation**

Add to `internal/api/service_errors.go` imports `"github.com/steveokay/janus-secrets/internal/resolve"` and these cases **before** the `default`:

```go
	case errors.Is(err, resolve.ErrForbiddenReference):
		writeError(w, http.StatusForbidden, CodeForbidden, "forbidden reference")
	case errors.Is(err, resolve.ErrInheritanceCycle), errors.Is(err, resolve.ErrBrokenInheritance),
		errors.Is(err, resolve.ErrReferenceCycle):
		writeError(w, http.StatusConflict, "conflict", "unresolvable configuration")
	case errors.Is(err, resolve.ErrUnresolvedReference), errors.Is(err, resolve.ErrReferenceDepth):
		writeError(w, http.StatusUnprocessableEntity, "unresolved_reference", "unresolved reference")
	case errors.Is(err, resolve.ErrBadReferenceSyntax):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid reference syntax")
```

`internal/api/resolve_adapter.go`:

```go
package api

import (
	"context"
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestWriteServiceErrorResolve -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/resolve_adapter.go internal/api/service_errors.go internal/api/resolve_adapter_test.go
git commit -m "feat(api): resolve authorizer adapter + sentinel error mapping"
```

---

## Task 7: reveal handlers — resolve by default, ?raw=true, per-deref audit

**Files:**
- Modify: `internal/api/secrets_handlers.go`
- Test: `internal/api/resolve_reveal_e2e_test.go`

The full-config reveal (`?reveal=true`) and single-key reveal now resolve by default via `resolverFor(r)`; `?raw=true` bypasses resolution (verbatim). Each distinct dereferenced target emits its own `secret.reveal`.

- [ ] **Step 1: Write the failing e2e test**

Using the api testcontainers harness (`authStackFull`, `login`, `doAuthed`; see existing `secrets_*_e2e_test.go`): create two projects/configs where config `app/prod/web` has `URL=${projects.billing.prod.api.HOST}` and `billing/prod/api` has `HOST=db.internal`, grant the caller read on both, then:

```go
func TestResolvedRevealAndRaw(t *testing.T) {
	// ... harness setup: seed billing/prod/api HOST=db.internal and
	//     app/prod/web URL=${projects.billing.prod.api.HOST}; owner cookie ...
	base := ts.URL + "/v1/configs/" + webCID + "/secrets"

	var resolved struct{ Secrets map[string]string `json:"secrets"` }
	if code := doAuthed(t, "GET", base+"?reveal=true", cookie, "", "", &resolved); code != 200 {
		t.Fatalf("resolved reveal: %d", code)
	}
	if resolved.Secrets["URL"] != "db.internal" {
		t.Fatalf("URL resolved = %q", resolved.Secrets["URL"])
	}
	var raw struct{ Secrets map[string]string `json:"secrets"` }
	if code := doAuthed(t, "GET", base+"?reveal=true&raw=true", cookie, "", "", &raw); code != 200 {
		t.Fatalf("raw reveal: %d", code)
	}
	if raw.Secrets["URL"] != "${projects.billing.prod.api.HOST}" {
		t.Fatalf("URL raw = %q", raw.Secrets["URL"])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestResolvedRevealAndRaw -count=1 -v`
Expected: FAIL — resolved reveal returns the literal `${...}` (no resolution yet).

- [ ] **Step 3: Write the implementation**

Rewrite the `reveal` branch of `handleSecretsList` (replace lines that call `RevealConfig` + build `out`) so raw uses the verbatim path and the default resolves + audits provenance:

```go
	if r.URL.Query().Get("reveal") == "true" {
		if !s.authorize(w, r, authz.SecretRead, res, "secret.reveal", "configs/"+cid+"/secrets") {
			return
		}
		raw := r.URL.Query().Get("raw") == "true"
		if raw {
			cv, all, err := s.service.RevealConfig(r.Context(), cid)
			if err != nil {
				s.writeServiceError(w, err)
				return
			}
			out := make(map[string]string, len(all))
			for k, sec := range all {
				out[k] = string(sec.Value)
			}
			if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets", "success", "", "raw"); err != nil {
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"version": cv.Version, "secrets": out})
			return
		}
		values, prov, err := s.resolverFor(r).Resolve(r.Context(), cid)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		out := make(map[string]string, len(values))
		for k, v := range values {
			out[k] = string(v)
		}
		if err := s.recordReveal(r, cid, "all", prov); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		// version metadata still comes from the config's own latest version.
		cv, _, verr := s.service.RevealConfig(r.Context(), cid)
		version := 0
		if verr == nil {
			version = cv.Version
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version, "secrets": out})
		return
	}
```

> Simplification note: to avoid a second decrypt for the version number, the implementer may instead have `resolve` return the root version, or fetch it via a lightweight `s.service.LatestVersion(cid)` helper. If adding a helper is cleaner, add `func (s *secrets.Service) LatestVersion(ctx, configID) (int, error)` (wraps `GetLatest`, returns `cv.Version`) and use it here and drop the extra `RevealConfig`. Keep whichever keeps one decrypt on the hot path.

Add the shared audit helper (records the primary reveal plus one event per distinct dereferenced target) — put it in `resolve_adapter.go`:

```go
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
```

Update `handleSecretGet` to resolve by default (latest only) and honor `?raw=true`:

```go
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.SecretRead, res, "secret.reveal", "configs/"+cid+"/secrets/"+key) {
		return
	}
	raw := r.URL.Query().Get("raw") == "true"
	if v := r.URL.Query().Get("version"); v != "" {
		// Historical reads are always raw (a past version is a stored artifact).
		vv, convErr := strconv.Atoi(v)
		if convErr != nil || vv < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation, "version must be a positive integer")
			return
		}
		sec, err := s.service.GetSecretVersion(r.Context(), cid, key, vv)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets/"+key, "success", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(sec.Value), "value_version": sec.ValueVersion})
		return
	}
	if raw {
		sec, err := s.service.GetSecret(r.Context(), cid, key)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+cid+"/secrets/"+key, "success", "", "raw"); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(sec.Value)})
		return
	}
	val, prov, err := s.resolverFor(r).ResolveKey(r.Context(), cid, key)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.recordReveal(r, cid, "key "+key, prov); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": string(val)})
```

> The single-key resolved response omits `value_version` (a resolved value can compose multiple versions across configs); the raw and historical paths keep it.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestResolvedRevealAndRaw -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/secrets_handlers.go internal/api/resolve_adapter.go internal/api/resolve_reveal_e2e_test.go
# (+ internal/secrets/*.go if a LatestVersion helper was added)
git commit -m "feat(api): resolve reveals by default; ?raw=true opt-out; per-deref audit"
```

---

## Task 8: masked list — inherited keys with origin markers

**Files:**
- Create: `internal/secrets/merged_meta.go`
- Modify: `internal/api/secrets_handlers.go` (masked branch of `handleSecretsList`)
- Test: `internal/secrets/merged_meta_test.go`, extend `resolve_reveal_e2e_test.go`

The masked list now shows own **and inherited** keys, each with an `origin` of `own`, `inherited`, or `overridden`. This is a metadata-only inheritance merge (no decryption, not audited).

- [ ] **Step 1: Write the failing test**

`internal/secrets/merged_meta_test.go` (testcontainers; seed a base config `B` with keys `A,B` and a branch config inheriting from `B` with keys `B,C`):

```go
func TestListMergedOrigins(t *testing.T) {
	// ... seed base (A,B) + branch inherits_from base (B override, C) ...
	metas, err := svc.ListSecretsMerged(context.Background(), branchCID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range metas {
		got[m.Key] = m.Origin
	}
	if got["A"] != "inherited" || got["B"] != "overridden" || got["C"] != "own" {
		t.Fatalf("origins = %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/secrets/ -run TestListMergedOrigins -count=1`
Expected: FAIL — `ListSecretsMerged`/`MergedMeta` undefined.

- [ ] **Step 3: Write the implementation**

`internal/secrets/merged_meta.go`:

```go
package secrets

import (
	"context"
	"time"
)

// MergedMeta is one key in the inheritance-merged masked view. Origin is
// "own" (defined only here), "inherited" (only from a base), or "overridden"
// (defined here and also in a base).
type MergedMeta struct {
	Key          string
	ValueVersion int
	CreatedAt    time.Time
	Origin       string
}

// ListSecretsMerged returns the masked, inheritance-merged key set for configID.
// Metadata only — no decryption, no audit. Child metadata wins; a key present in
// both this config and an ancestor is "overridden".
func (s *Service) ListSecretsMerged(ctx context.Context, configID string) ([]MergedMeta, error) {
	// Walk the inherits_from chain (cycle-guarded) collecting each config's key
	// metadata; nearer configs win, and a key seen at multiple levels is
	// "overridden".
	seen := map[string]bool{}
	var chainCV []map[string]storeMetaEntry
	id := configID
	own := map[string]bool{}
	first := true
	for id != "" {
		if seen[id] {
			return nil, ErrConflict // inheritance cycle → surfaced as conflict (409)
		}
		seen[id] = true
		cfg, err := s.configs.Get(ctx, id)
		if err != nil {
			if first {
				return nil, mapStoreErr(err)
			}
			return nil, ErrConflict // broken base
		}
		_, state, err := s.secrets.GetLatest(ctx, id)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		lvl := map[string]storeMetaEntry{}
		for k, sv := range state {
			lvl[k] = storeMetaEntry{vv: sv.ValueVersion, at: sv.CreatedAt}
			if first {
				own[k] = true
			}
		}
		chainCV = append(chainCV, lvl)
		first = false
		if cfg.InheritsFrom != nil {
			id = *cfg.InheritsFrom
		} else {
			id = ""
		}
	}
	// Merge ancestor→child (index len-1 .. 0); child wins for value_version/created_at.
	merged := map[string]storeMetaEntry{}
	presentAtMultiple := map[string]int{}
	for i := len(chainCV) - 1; i >= 0; i-- {
		for k, e := range chainCV[i] {
			merged[k] = e
			presentAtMultiple[k]++
		}
	}
	out := make([]MergedMeta, 0, len(merged))
	for k, e := range merged {
		origin := "inherited"
		if own[k] {
			if presentAtMultiple[k] > 1 {
				origin = "overridden"
			} else {
				origin = "own"
			}
		}
		out = append(out, MergedMeta{Key: k, ValueVersion: e.vv, CreatedAt: e.at, Origin: origin})
	}
	return out, nil
}

type storeMetaEntry struct {
	vv int
	at time.Time
}
```

> Confirm `store.SecretValue` exposes `ValueVersion int` and `CreatedAt time.Time` (used already by `ListSecrets`); adjust field names if the harness reveals otherwise.

Modify the masked branch of `handleSecretsList` to use it:

```go
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	metas, err := s.service.ListSecretsMerged(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	masked := make(map[string]any, len(metas))
	for _, m := range metas {
		masked[m.Key] = map[string]any{
			"value_version": m.ValueVersion,
			"created_at":    m.CreatedAt.UTC().Format(time.RFC3339),
			"origin":        m.Origin,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": masked})
```

> This drops the top-level `version` from the masked response (a merged view spans multiple config versions). If a consumer needs the config's own version, it is available on the resolved/raw reveal. Update any masked-list test that asserted `version` accordingly.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/secrets/ -run TestListMergedOrigins -count=1` then `go test ./internal/api/ -run 'TestSecrets|TestResolved' -count=1`
Expected: PASS (fix any masked-list test that asserted the removed `version` field).

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/merged_meta.go internal/api/secrets_handlers.go internal/secrets/merged_meta_test.go internal/api/resolve_reveal_e2e_test.go
git commit -m "feat: masked list shows inherited keys with own/inherited/overridden origin"
```

---

## Task 9: forbidden-reference + inheritance e2e (RBAC + atomic failure)

**Files:**
- Test: `internal/api/resolve_rbac_e2e_test.go`

Prove the strict-reference and transparent-inheritance rules end to end.

- [ ] **Step 1: Write the failing/locking test**

```go
func TestResolveReferenceRBACAndInheritance(t *testing.T) {
	// Seed: billing/prod/api HOST=secret ; app/prod/web URL=${projects.billing.prod.api.HOST}.
	// Make a developer with read on app only (not billing) via makeUser + a
	// project/env membership grant (mirror secrets_rbac_e2e_test.go).

	// (a) developer resolving app/prod/web → 403 (forbidden reference), whole read fails.
	if code := doAuthed(t, "GET", appSecrets+"?reveal=true", devCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("forbidden ref: want 403, got %d", code)
	}
	// (b) developer can still read app raw (no deref) → 200.
	if code := doAuthed(t, "GET", appSecrets+"?reveal=true&raw=true", devCookie, "", "", nil); code != 200 {
		t.Fatalf("raw read: want 200, got %d", code)
	}
	// (c) inheritance is transparent: a token scoped to a branch config reads
	//     inherited base values without a base grant.
	//     Seed base+branch in the same env; mint a config-scoped token on the branch;
	//     resolved reveal returns an inherited key. → 200 with the inherited value.
}
```

- [ ] **Step 2: Run to verify it fails/passes**

Run: `go test ./internal/api/ -run TestResolveReferenceRBACAndInheritance -count=1 -v`
Expected: PASS once Tasks 7–8 are in; if (a) is not 403 or (c) not 200, fix the handler/authorizer.

- [ ] **Step 3: (no new impl expected)** — this task locks behavior from Tasks 6–8. If it fails, the defect is in the authorizer wiring or handler; fix there.

- [ ] **Step 4: Commit**

```bash
git add internal/api/resolve_rbac_e2e_test.go
git commit -m "test(api): reference RBAC (forbidden→403 atomic) + transparent inheritance e2e"
```

---

## Task 10: audit trail + leak e2e

**Files:**
- Test: `internal/api/resolve_audit_leak_test.go`

Prove the per-deref audit trail and no-leak-on-failure.

- [ ] **Step 1: Write the test**

```go
func TestResolveAuditTrailAndLeak(t *testing.T) {
	// Seed app/prod/web URL=${projects.billing.prod.api.HOST}; billing/prod/api HOST=<sentinel>.
	// Owner reveals app/prod/web resolved → 200.
	// (a) Audit: iterate audit rows (store.NewAuditRepo(srv.st).Iterate) and assert
	//     two secret.reveal rows: resource configs/<webCID>/secrets (primary) and
	//     configs/<billingCID>/secrets (detail contains "via reference").
	// (b) Leak: with slog redirected into a syncBuffer, drive a FAILED resolve
	//     (reference to a nonexistent key) and assert the response body is a generic
	//     422 that does not contain the sentinel, and the sentinel never appears in logs.
}
```

Mirror `leak_test.go` (`syncBuffer`, `slog.SetDefault`, `store.NewAuditRepo(srv.st).Iterate`).

- [ ] **Step 2: Run**

Run: `go test ./internal/api/ -run TestResolveAuditTrailAndLeak -count=1 -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/resolve_audit_leak_test.go
git commit -m "test(api): per-deref audit trail + no-leak-on-failed-resolution e2e"
```

---

## Task 11: CLI — `--raw` on run/download/get; list origin

**Files:**
- Modify: `cmd/janus/run.go`, `cmd/janus/secrets_download.go`, `cmd/janus/secrets_cmd.go`
- Test: extend `cmd/janus/secrets_cmd_test.go` (or a new `resolve_cli_test.go`)

`run`, `secrets download`, and `secrets get` resolve by default (they already call `?reveal=true` / the reveal endpoint); add a `--raw` bool that appends `raw=true`. `secrets list` prints an `ORIGIN` column.

- [ ] **Step 1: Write the failing test**

A unit test that builds the command and asserts the query string / column. Match how existing `secrets_cmd_test.go` exercises the commands (fake server or URL assertion). Contract:

```go
func TestSecretsGetRawFlagAppendsRaw(t *testing.T) {
	// with --raw, the GET path for `secrets get KEY` ends with "?raw=true"
	// without --raw, it has no raw param
}
func TestSecretsListShowsOrigin(t *testing.T) {
	// list output includes an ORIGIN column with own/inherited/overridden
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/janus/ -run 'TestSecretsGetRawFlag|TestSecretsListShowsOrigin' -count=1`
Expected: FAIL — no `--raw` flag / no origin column.

- [ ] **Step 3: Write the implementation**

- In `secrets_cmd.go` `get`: add `var raw bool`, `cmd.Flags().BoolVar(&raw, "raw", false, "return the stored value verbatim (do not resolve references)")`, and build the path as `.../secrets/<key>` plus `?raw=true` when `raw`.
- In `secrets_cmd.go` `list`: request `.../secrets` (unchanged), and when printing, add an `ORIGIN` column from each entry's `origin` field.
- In `run.go` and `secrets_download.go`: add a `--raw` bool that switches the query to `?reveal=true&raw=true`. (Default stays resolved, which is what these consumers want.)

> Show the exact edits by reading each file first; keep stdout-is-data / stderr-is-diagnostics discipline. `run --raw` is mainly for debugging (injecting verbatim `${...}` is rarely useful) — document it as such in the flag help.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/janus/ -run 'TestSecretsGetRawFlag|TestSecretsListShowsOrigin' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/run.go cmd/janus/secrets_download.go cmd/janus/secrets_cmd.go cmd/janus/secrets_cmd_test.go
git commit -m "feat(cli): --raw on run/download/get; secrets list shows origin"
```

---

## Task 12: docs + full gate sweep

**Files:**
- Create: `docs/references.md`
- Modify: `docs/data-model.md`, `docs/operations.md`, `README.md`, `status.md`, `docs/cli.md`

- [ ] **Step 1: Write docs**

- `docs/references.md` — the reference grammar (`${projects.<project>.<env>.<config>.KEY}`, local `${KEY}`, `$$` escape), inheritance (same-environment, child-wins, `origin` markers), resolution rules (transitive, cycle detection, depth cap, atomic failure), the raw-vs-resolved read surface (`?raw=true`), the strict-reference / transparent-inheritance authorization model, and the per-deref audit behavior. Derive from the spec.
- `docs/data-model.md` — replace the "config inheritance and secret references are deferred" note with a summary + link to `references.md`.
- `docs/operations.md` — add a short "Inheritance & references" subsection under Secrets API (resolve-by-default, `?raw=true`, `origin` in masked list, the 403-on-forbidden-reference behavior).
- `docs/cli.md` — document `--raw` and the `secrets list` origin column.
- `README.md` — mark the open Phase-1 item done in the roadmap; note references/inheritance under the data-model bullet.
- `status.md` — add the Milestone-11 entry (Phase 1 · final item) mirroring the M8/M9 format: scope, task list, verification; flip the open Phase-1 checkbox and the "one open line item" narrative.

- [ ] **Step 2: Full gate sweep**

```bash
go build ./...
go vet ./...
go test ./... -count=1
gosec -exclude-dir=internal/crypto/shamir ./...
govulncheck ./...
```
Expected: build/vet clean; every package `ok` (Docker-backed suites run); `gosec` 0 issues (justify any new `#nosec`); `govulncheck` 0 affecting; `internal/crypto`/`internal/authz`/`internal/audit` stay 100%.

- [ ] **Step 3: Commit**

```bash
git add docs/references.md docs/data-model.md docs/operations.md docs/cli.md README.md status.md
git commit -m "docs: config inheritance + secret references; status M11; full gate sweep"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** grammar (T2); inheritance merge + cycle/broken-base (T3); reference expansion + transitive + cycle + depth + provenance + zeroization (T4); raw-read port (T5); strict per-target authz (T6 adapter + T9 e2e); resolve-by-default + `?raw` + single-key + error mapping (T6–T7); atomic failure (T4 + T9); per-deref audit (T7 helper + T10); masked-list origin (T8); CLI `--raw` + origin (T11); docs + gate (T12). Every spec section maps to a task.
- **Type consistency:** `resolve.Coord`, `RawConfig` (with `Path()`), `Provenance`, `RawReader.ReadRaw/ReadRawByID`, `Authorizer.CanReadSecrets`, `Resolver.New/Resolve/ResolveKey`, `secrets.ReadRaw/ReadRawByID`, `secrets.ListSecretsMerged`/`MergedMeta{Key,ValueVersion,CreatedAt,Origin}`, `api.apiAuthorizer`/`resolverFor`/`recordReveal` are defined once and reused with the same signatures.
- **Green at every commit:** the pure `resolve` package (T1–T4) lands before the `secrets` port (T5) that returns its types; the api adapter (T6) precedes the handler rewrite (T7); the masked-list change (T8) is independent; tests-only tasks (T9–T10) lock behavior already implemented.
- **Verify-before-you-code reminders** are inline where a task reuses an existing symbol whose exact name must be confirmed (secrets test harness constructor; `store.SecretValue`/`Config`/`Project`/`Environment` field names; CLI command wiring). Trust `go build`/`go test` over gopls "undefined" diagnostics for new-in-branch symbols.
