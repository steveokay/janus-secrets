# React SPA — Core Editor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the first slice of the Janus web UI — a Vite React-TS SPA embedded in the Go binary that lets an operator unseal, log in, navigate Project → Environment → Config, and edit secrets (masked list, audited reveal, dirty-buffer batched "Save as vN").

**Architecture:** A new `web/` Vite app builds to static assets that a new `internal/web` package embeds via `go:embed` and serves same-origin through the existing chi server (SPA fallback for deep links, CSP headers). The seal gate is narrowed so static assets load while sealed (for the unseal screen). React Router v6 + TanStack Query + a hand-written typed `fetch` client; no new server API endpoints (only the static handler + a `RequireUnsealed` tweak). Tests: Vitest + React Testing Library + MSW on the web side, Go httptest on the serve side.

**Tech Stack:** Go 1.26 (`go:embed`, chi), React 18 + TypeScript + Vite + Tailwind, TanStack Query, React Router v6, Vitest + Testing Library + MSW.

---

## Conventions & prerequisites

- **Module:** `github.com/steveokay/janus-secrets`. **Node:** use `npm` (lockfile `web/package-lock.json`). Run web commands from `web/`.
- **Existing API contracts this plan consumes** (no changes needed to them):
  - `GET /v1/sys/seal-status` → `{initialized, sealed, type, threshold?, shares?, progress?}` (`threshold`/`shares`/`progress` only for `type:"shamir"`).
  - `POST /v1/sys/unseal` body `{share}` (shamir, one per call) or `{}` (kms); `POST /v1/sys/unseal/reset` (shamir).
  - `POST /v1/auth/login` `{email,password}` → sets httpOnly cookie, body `{...user}`; `POST /v1/auth/logout`; `GET /v1/auth/me` → `{...user}`; `POST /v1/auth/password` `{current_password,new_password}`.
  - `GET /v1/projects` → `{projects:[{id,slug,name}]}`; `POST /v1/projects` `{slug,name}` → `{id,slug,name}`.
  - `GET /v1/projects/{pid}/environments` → `{environments:[{id,slug,name}]}`; `POST` same shape.
  - `GET /v1/projects/{pid}/environments/{eid}/configs` → `{configs:[{id,environment_id,name,inherits_from,created_at}]}`; `POST` `{name,inherits_from?}`.
  - `GET /v1/configs/{cid}/secrets` (masked) → `{secrets:{KEY:{value_version,created_at,origin}}}`.
  - `GET /v1/configs/{cid}/secrets?reveal=true` → `{version,secrets:{KEY:"value"}}`; add `&raw=true` for own stored values verbatim.
  - `GET /v1/configs/{cid}/secrets/{key}` → `{key,value}` (resolved); add `?raw=true` for verbatim.
  - `PUT /v1/configs/{cid}/secrets` `{message,changes:[{key,value,delete}]}` → `{version,id,created_at}` (≥1 change, no dup keys; delete via `{key,delete:true}`).
  - Error envelope on failure: `{"error":{"code","message"}}` with the HTTP status.
- **Gates (every task keeps them green):** `go build ./...`, `go vet ./...`, `go test ./... -count=1`, and (from `web/`) `npm run test -- --run`. Full sweep at the end adds `gosec -exclude-dir=internal/crypto/shamir ./...` (0 issues) and `govulncheck ./...` (0 affecting).
- **TDD:** every task writes the failing test first, confirms it fails, implements, confirms it passes, commits.

## File structure

**Go (serve side):**
- `internal/web/embed.go` — `//go:embed all:dist` + `Handler()` (assets + SPA fallback + CSP).
- `internal/web/dist/index.html` — committed dev placeholder (real Vite build overwrites in Docker/`make build`).
- `internal/web/embed_test.go` — deep-link shell, real-asset, CSP.
- `internal/api/middleware.go` — narrow `RequireUnsealed` to gate only `/v1/*` (static always served).
- `internal/api/server.go` — add `MountUI(http.Handler)` (sets router `NotFound`).
- `internal/api/ui_mount_test.go` — mount a stub UI; assert fallback vs `/v1` vs sealed behavior.
- `cmd/janus/.../boot.go` (or wherever `api.New` is called) — `srv.MountUI(web.Handler())`.

**Web app (`web/`):**
- Config: `package.json`, `vite.config.ts`, `tsconfig.json`, `tsconfig.node.json`, `tailwind.config.js`, `postcss.config.js`, `index.html`, `vitest.config.ts`, `src/test/setup.ts`, `src/test/msw.ts`.
- `src/main.tsx`, `src/App.tsx`, `src/index.css`.
- `src/lib/api.ts` — typed fetch client + `ApiError`.
- `src/lib/queryClient.ts` — Query client + global 401/503 routing.
- `src/auth/AuthProvider.tsx`, `src/auth/LoginPage.tsx`.
- `src/unseal/UnsealPage.tsx`.
- `src/shell/AppLayout.tsx`, `src/shell/TopBar.tsx`, `src/shell/Sidebar.tsx`, `src/shell/Placeholder.tsx`.
- `src/secrets/nav.ts` (query hooks), `src/secrets/SecretEditor.tsx`, `src/secrets/dirty.ts` (buffer logic), `src/structure/CreateForms.tsx`.
- Co-located `*.test.tsx` / `*.test.ts`.

**Docs:** `docs/web.md` (new), `status.md` (milestone entry), `README.md` (UI note).

---

## Task 1: `internal/web` — embed + SPA-fallback handler

**Files:**
- Create: `internal/web/dist/index.html`
- Create: `internal/web/embed.go`
- Create: `internal/web/embed_test.go`

- [ ] **Step 1: Commit the dev placeholder shell**

Create `internal/web/dist/index.html` (the real Vite build overwrites this in Docker / `make build`; committing one keeps `go build` working without a Node build):

```html
<!doctype html>
<html>
  <head><meta charset="utf-8" /><title>Janus</title></head>
  <body>
    <div id="root"></div>
    <p>UI not built — run <code>make build</code> (or <code>make dev</code>).</p>
  </body>
</html>
```

- [ ] **Step 2: Write the failing test**

Create `internal/web/embed_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesShellForDeepLink(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/projects/abc/configs/def", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("deep-link status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), `id="root"`) {
		t.Fatal("shell body missing #root")
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("missing/weak CSP: %q", csp)
	}
}

func TestHandlerServesRealAsset(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/index.html", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("index.html status = %d, want 200", rr.Code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestHandler -v`
Expected: FAIL — `undefined: Handler`.

- [ ] **Step 4: Implement `Handler`**

Create `internal/web/embed.go`:

```go
// Package web embeds the built React SPA and serves it same-origin from the
// Go binary. It holds no secrets and no business logic — only static assets and
// an SPA fallback so client-side routing owns deep links.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded SPA: a real asset when the request path matches a
// built file, otherwise index.html (so a deep link like /projects/x/configs/y
// loads the shell and React Router takes over). Every response carries a
// restrictive CSP; the app is self-contained so 'self' is sufficient.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; failure is a build bug
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if p := strings.TrimPrefix(r.URL.Path, "/"); p != "" && p != "index.html" {
			if f, err := sub.Open(p); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestHandler -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add internal/web/
git commit -m "feat(web): embed SPA with go:embed + SPA-fallback handler and CSP"
```

---

## Task 2: Serve the SPA — narrow the seal gate + mount UI fallback

**Files:**
- Modify: `internal/api/middleware.go` (the `RequireUnsealed` body)
- Modify: `internal/api/server.go` (add `MountUI`)
- Create: `internal/api/ui_mount_test.go`
- Modify: the boot path that calls `api.New` (wire `srv.MountUI(web.Handler())`)

- [ ] **Step 1: Write the failing test**

Create `internal/api/ui_mount_test.go`. It builds a minimal sealed and unsealed server via the existing test harness and mounts a stub UI, asserting the fallback and seal behavior. (Use the same construction the harness uses; `newSealedServer`/`newTestServer` names below mirror `harness_test.go` — adjust to the actual helper names when implementing.)

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func stubUI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("STUB-UI"))
	})
}

func TestMountUIFallbackAndSealGate(t *testing.T) {
	// Unsealed server: UI fallback serves non-/v1 paths; /v1/sys still works.
	s := newTestServerUnsealed(t) // harness helper: unsealed keyring, minimal deps
	s.MountUI(stubUI())

	// (a) deep link → stub UI
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/projects/x/configs/y", nil))
	if rr.Code != 200 || rr.Body.String() != "STUB-UI" {
		t.Fatalf("deep link: got %d %q, want 200 STUB-UI", rr.Code, rr.Body.String())
	}
	// (b) API path is not shadowed by the UI fallback
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/sys/seal-status", nil))
	if rr.Code != 200 {
		t.Fatalf("seal-status status = %d, want 200", rr.Code)
	}

	// Sealed server: static UI still served, but a non-sys /v1 path is 503.
	sealed := newTestServerSealed(t) // harness helper: sealed keyring
	sealed.MountUI(stubUI())
	rr = httptest.NewRecorder()
	sealed.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/login", nil))
	if rr.Code != 200 || rr.Body.String() != "STUB-UI" {
		t.Fatalf("sealed UI: got %d %q, want 200 STUB-UI", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	sealed.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/configs/abc/secrets", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed API: status = %d, want 503", rr.Code)
	}
}
```

> **Note for implementer:** if the harness lacks `newTestServerUnsealed`/`newTestServerSealed`, add thin wrappers in `ui_mount_test.go` over whatever `harness_test.go` already provides (it constructs `*Server` with a `*crypto.Keyring`; seal/unseal it via `keyring.Seal()` / the unseal path). Do NOT invent new production seams for the test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestMountUIFallbackAndSealGate -v`
Expected: FAIL — `s.MountUI undefined` (and, once that compiles, the sealed `/login` case returns 503 because the old gate blocks non-`/v1` paths).

- [ ] **Step 3: Narrow `RequireUnsealed`**

In `internal/api/middleware.go`, replace the guard body so only `/v1/*` API paths (except `/v1/sys/`) are sealed-gated; everything else (the embedded SPA + assets) is always served:

```go
func RequireUnsealed(kr *crypto.Keyring) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Non-API paths (the embedded SPA and its assets) and /v1/sys/* are
			// served regardless of seal state: the UI must load while sealed to
			// present the unseal screen.
			if !strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1/sys/") {
				next.ServeHTTP(w, r)
				return
			}
			if kr.Sealed() {
				writeError(w, http.StatusServiceUnavailable, CodeSealed,
					"server is sealed; unseal via /v1/sys/unseal")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Add `MountUI` to the server**

In `internal/api/server.go`, after the `Handler()` method:

```go
// MountUI installs h as the router's fallback for any route the /v1 API does not
// match — i.e. the embedded SPA and its assets. Call after New, before serving.
// nil is a no-op (unit-test servers with no UI keep chi's default 404).
func (s *Server) MountUI(h http.Handler) {
	if h == nil {
		return
	}
	s.router.NotFound(h.ServeHTTP)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestMountUIFallbackAndSealGate -v`
Expected: PASS.
Then run the whole API package to catch any test that assumed a sealed non-`/v1` path returned 503: `go test ./internal/api/ -count=1`
Expected: PASS (fix any stale assertion by updating it to the new, correct behavior — sealed static paths are served, not 503).

- [ ] **Step 6: Wire the UI into boot**

Find where production constructs the server (`api.New(...)` — in the server bootstrap under `cmd/janus`/`internal/api` Boot). Immediately after the `srv := api.New(...)` line, add:

```go
srv.MountUI(web.Handler())
```

and add the import `"github.com/steveokay/janus-secrets/internal/web"`. Run `go build ./...`.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ cmd/
git commit -m "feat(api): serve embedded SPA as fallback; gate seal only on /v1 API paths"
```

---

## Task 3: Scaffold the Vite React-TS app

**Files (all under `web/`):** `package.json`, `vite.config.ts`, `vitest.config.ts`, `tsconfig.json`, `tailwind.config.js`, `postcss.config.js`, `index.html`, `.gitignore`, `src/main.tsx`, `src/App.tsx`, `src/index.css`, `src/test/setup.ts`, `src/App.test.tsx`. Also modify root `.gitignore`.

- [ ] **Step 1: Create the project config files**

`web/package.json`:

```json
{
  "name": "janus-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview",
    "typecheck": "tsc --noEmit",
    "test": "vitest"
  },
  "dependencies": {
    "@tanstack/react-query": "^5.51.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.26.0"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.4.8",
    "@testing-library/react": "^16.0.0",
    "@testing-library/user-event": "^14.5.2",
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "autoprefixer": "^10.4.19",
    "jsdom": "^24.1.1",
    "msw": "^2.3.5",
    "postcss": "^8.4.40",
    "tailwindcss": "^3.4.7",
    "typescript": "^5.5.4",
    "vite": "^5.4.0",
    "vitest": "^2.0.5"
  }
}
```

`web/vite.config.ts` (dev proxies `/v1` to the Go server so it's same-origin):

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist' },
  server: { proxy: { '/v1': 'http://localhost:8200' } },
})
```

`web/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
  },
})
```

`web/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src"]
}
```

`web/tailwind.config.js`:

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: { extend: {} },
  plugins: [],
}
```

`web/postcss.config.js`:

```js
export default { plugins: { tailwindcss: {}, autoprefixer: {} } }
```

`web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Janus</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`web/.gitignore`:

```
node_modules/
dist/
```

Append to the **root** `.gitignore` (keep the committed placeholder, ignore built assets):

```
# Built SPA assets (the placeholder index.html is committed; real build output is not)
/internal/web/dist/*
!/internal/web/dist/index.html
```

- [ ] **Step 2: Create the source + test files**

`web/src/index.css`:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```

`web/src/main.tsx`:

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
```

`web/src/App.tsx` (minimal; grown in Task 8):

```tsx
export default function App() {
  return <div>Janus</div>
}
```

`web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom'
```

`web/src/App.test.tsx` (the failing test):

```tsx
import { render, screen } from '@testing-library/react'
import App from './App'

test('renders the app root', () => {
  render(<App />)
  expect(screen.getByText('Janus')).toBeInTheDocument()
})
```

- [ ] **Step 3: Install and run the test to verify it passes**

Run (from `web/`): `npm install` then `npm run test -- --run`
Expected: 1 passing test. (The test is trivially green once `App` renders "Janus" — this task's real deliverable is a working toolchain: Vite + TS + Tailwind + Vitest + jsdom all resolve and run.)

- [ ] **Step 4: Verify build + typecheck**

Run (from `web/`): `npm run build && npm run typecheck`
Expected: `dist/` produced, no type errors.

- [ ] **Step 5: Commit**

```bash
git add web/ .gitignore
git commit -m "chore(web): scaffold Vite + React-TS + Tailwind + Vitest project"
```

---

## Task 4: Typed API client + `ApiError`

**Files:**
- Create: `web/src/lib/api.ts`
- Create: `web/src/test/msw.ts` (shared MSW server)
- Create: `web/src/lib/api.test.ts`

- [ ] **Step 1: Write the failing test**

`web/src/test/msw.ts` (shared across suites):

```ts
import { setupServer } from 'msw/node'
export const server = setupServer()
```

Wire MSW lifecycle in `web/src/test/setup.ts` (append below the jest-dom import):

```ts
import { afterAll, afterEach, beforeAll } from 'vitest'
import { server } from './msw'

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
```

`web/src/lib/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { api, ApiError } from './api'

test('GET returns parsed JSON', async () => {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ email: 'a@b.io' })))
  await expect(api.get('/v1/auth/me')).resolves.toEqual({ email: 'a@b.io' })
})

test('error envelope becomes a typed ApiError', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 }),
    ),
  )
  await expect(api.get('/v1/projects')).rejects.toMatchObject({
    name: 'ApiError',
    status: 403,
    code: 'forbidden',
  } satisfies Partial<ApiError>)
})

test('POST sends JSON body and credentials', async () => {
  let sawBody: unknown
  let sawCreds: string | null = null
  server.use(
    http.post('/v1/projects', async ({ request }) => {
      sawBody = await request.json()
      sawCreds = request.credentials
      return HttpResponse.json({ id: 'p1' }, { status: 200 })
    }),
  )
  await api.post('/v1/projects', { slug: 'x', name: 'X' })
  expect(sawBody).toEqual({ slug: 'x', name: 'X' })
  expect(sawCreds).toBe('include')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/lib/api.test.ts`
Expected: FAIL — cannot resolve `./api`.

- [ ] **Step 3: Implement the client**

`web/src/lib/api.ts`:

```ts
// Thin typed fetch client. Every call is same-origin with the session cookie;
// failures parse the server's {error:{code,message}} envelope into ApiError.
export class ApiError extends Error {
  readonly name = 'ApiError'
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'include',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const e = data?.error
    throw new ApiError(res.status, e?.code ?? 'error', e?.message ?? res.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/lib/api.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/api.test.ts web/src/test/msw.ts web/src/test/setup.ts
git commit -m "feat(web): typed fetch client with ApiError + MSW test harness"
```

---

## Task 5: Query client + typed endpoint functions

**Files:**
- Create: `web/src/lib/endpoints.ts` (typed wrappers + response types)
- Create: `web/src/lib/queryClient.ts` (QueryClient + global 401/503 routing)
- Create: `web/src/lib/endpoints.test.ts`

- [ ] **Step 1: Write the failing test**

`web/src/lib/endpoints.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { endpoints } from './endpoints'

test('sealStatus parses the shamir shape', async () => {
  server.use(
    http.get('/v1/sys/seal-status', () =>
      HttpResponse.json({ initialized: true, sealed: true, type: 'shamir', threshold: 3, shares: 5, progress: 1 }),
    ),
  )
  await expect(endpoints.sealStatus()).resolves.toMatchObject({ sealed: true, threshold: 3, progress: 1 })
})

test('listProjects unwraps the projects array', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 's', name: 'N' }] })),
  )
  await expect(endpoints.listProjects()).resolves.toEqual([{ id: 'p1', slug: 's', name: 'N' }])
})

test('saveSecrets posts the batch and returns the version', async () => {
  let sawBody: any
  server.use(
    http.put('/v1/configs/c1/secrets', async ({ request }) => {
      sawBody = await request.json()
      return HttpResponse.json({ version: 4, id: 'v4', created_at: '2026-07-06T00:00:00Z' })
    }),
  )
  const changes = [{ key: 'A', value: '1' }, { key: 'B', delete: true }]
  await expect(endpoints.saveSecrets('c1', changes, 'msg')).resolves.toMatchObject({ version: 4 })
  expect(sawBody).toEqual({ message: 'msg', changes })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/lib/endpoints.test.ts`
Expected: FAIL — cannot resolve `./endpoints`.

- [ ] **Step 3: Implement endpoints + query client**

`web/src/lib/endpoints.ts`:

```ts
import { api } from './api'

export interface SealStatus {
  initialized: boolean
  sealed: boolean
  type: 'shamir' | 'awskms'
  threshold?: number
  shares?: number
  progress?: number
}
export interface User { email: string }
export interface Project { id: string; slug: string; name: string }
export interface Environment { id: string; slug: string; name: string }
export interface Config { id: string; environment_id: string; name: string; inherits_from: string | null; created_at: string }
export interface MaskedSecret { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden' }
export interface SecretChange { key: string; value?: string; delete?: boolean }
export interface VersionResult { version: number; id: string; created_at: string }

export const endpoints = {
  // sys / auth
  sealStatus: () => api.get<SealStatus>('/v1/sys/seal-status'),
  unsealShare: (share: string) => api.post<SealStatus>('/v1/sys/unseal', { share }),
  unsealKms: () => api.post<SealStatus>('/v1/sys/unseal', {}),
  unsealReset: () => api.post<SealStatus>('/v1/sys/unseal/reset'),
  me: () => api.get<User>('/v1/auth/me'),
  login: (email: string, password: string) => api.post<User>('/v1/auth/login', { email, password }),
  logout: () => api.post<void>('/v1/auth/logout'),
  changePassword: (current_password: string, new_password: string) =>
    api.post<void>('/v1/auth/password', { current_password, new_password }),

  // structure
  listProjects: () => api.get<{ projects: Project[] }>('/v1/projects').then((r) => r.projects),
  createProject: (slug: string, name: string) => api.post<Project>('/v1/projects', { slug, name }),
  listEnvironments: (pid: string) =>
    api.get<{ environments: Environment[] }>(`/v1/projects/${pid}/environments`).then((r) => r.environments),
  createEnvironment: (pid: string, slug: string, name: string) =>
    api.post<Environment>(`/v1/projects/${pid}/environments`, { slug, name }),
  listConfigs: (pid: string, eid: string) =>
    api.get<{ configs: Config[] }>(`/v1/projects/${pid}/environments/${eid}/configs`).then((r) => r.configs),
  createConfig: (pid: string, eid: string, name: string, inherits_from?: string) =>
    api.post<Config>(`/v1/projects/${pid}/environments/${eid}/configs`, { name, inherits_from }),

  // secrets
  maskedSecrets: (cid: string) =>
    api.get<{ secrets: Record<string, MaskedSecret> }>(`/v1/configs/${cid}/secrets`).then((r) => r.secrets),
  revealKey: (cid: string, key: string) =>
    api.get<{ key: string; value: string }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}`),
  revealAll: (cid: string) =>
    api.get<{ version: number; secrets: Record<string, string> }>(`/v1/configs/${cid}/secrets?reveal=true`),
  rawOwnValues: (cid: string) =>
    api
      .get<{ version: number; secrets: Record<string, string> }>(`/v1/configs/${cid}/secrets?reveal=true&raw=true`)
      .then((r) => r.secrets),
  saveSecrets: (cid: string, changes: SecretChange[], message: string) =>
    api.put<VersionResult>(`/v1/configs/${cid}/secrets`, { message, changes }),
}
```

`web/src/lib/queryClient.ts`:

```ts
import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

// onAuthEvent is set by the app root; the client calls it on global auth/seal
// signals so a single place owns "redirect to /login" and "redirect to /unseal".
let onAuthEvent: (kind: 'unauthorized' | 'sealed') => void = () => {}
export function setAuthEventHandler(fn: (kind: 'unauthorized' | 'sealed') => void) {
  onAuthEvent = fn
}

function route(err: unknown) {
  if (err instanceof ApiError) {
    if (err.status === 401) onAuthEvent('unauthorized')
    else if (err.status === 503) onAuthEvent('sealed')
  }
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError) && count < 2,
      staleTime: 5_000,
    },
  },
})
queryClient.getQueryCache().subscribe((e) => {
  if (e.type === 'updated' && e.query.state.status === 'error') route(e.query.state.error)
})
queryClient.getMutationCache().subscribe((e) => {
  if (e?.mutation?.state.status === 'error') route(e.mutation.state.error)
})
```

- [ ] **Step 4: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/lib/endpoints.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/endpoints.ts web/src/lib/endpoints.test.ts web/src/lib/queryClient.ts
git commit -m "feat(web): typed endpoint fns + query client with global 401/503 routing"
```

---

## Task 6: Auth context + login page

**Files:**
- Create: `web/src/test/render.tsx` (shared provider-aware render helper)
- Create: `web/src/auth/AuthProvider.tsx`
- Create: `web/src/auth/LoginPage.tsx`
- Create: `web/src/auth/LoginPage.test.tsx`

- [ ] **Step 1: Create the shared render helper**

`web/src/test/render.tsx`:

```tsx
import { ReactElement, ReactNode } from 'react'
import { render } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '../auth/AuthProvider'

export function renderApp(ui: ReactElement, { route = '/', withAuth = true }: { route?: string; withAuth?: boolean } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (node: ReactNode) => (withAuth ? <AuthProvider>{node}</AuthProvider> : node)
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{wrap(ui)}</MemoryRouter>
    </QueryClientProvider>,
  )
}
```

- [ ] **Step 2: Write the failing test**

`web/src/auth/LoginPage.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { LoginPage } from './LoginPage'

function mockMe(status: number) {
  server.use(http.get('/v1/auth/me', () =>
    status === 200 ? HttpResponse.json({ email: 'me@corp.io' }) : new HttpResponse(null, { status }),
  ))
}

test('successful login triggers /me refresh', async () => {
  mockMe(401) // initial mount: not logged in
  let loggedIn = false
  server.use(
    http.post('/v1/auth/login', () => { loggedIn = true; return HttpResponse.json({ email: 'me@corp.io' }) }),
  )
  renderApp(<LoginPage />, { withAuth: true })
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'pw')
  mockMe(200) // after login, /me now succeeds
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  await waitFor(() => expect(loggedIn).toBe(true))
})

test('401 shows a generic error (no enumeration)', async () => {
  mockMe(401)
  server.use(http.post('/v1/auth/login', () => new HttpResponse(JSON.stringify({ error: { code: 'unauthorized', message: 'x' } }), { status: 401 })))
  renderApp(<LoginPage />)
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'bad')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/invalid email or password/i)
})

test('429 shows a rate-limit message', async () => {
  mockMe(401)
  server.use(http.post('/v1/auth/login', () => new HttpResponse(null, { status: 429 })))
  renderApp(<LoginPage />)
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'pw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
})
```

- [ ] **Step 3: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/auth/LoginPage.test.tsx`
Expected: FAIL — cannot resolve `./AuthProvider` / `./LoginPage`.

- [ ] **Step 4: Implement `AuthProvider` and `LoginPage`**

`web/src/auth/AuthProvider.tsx`:

```tsx
import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { endpoints, User } from '../lib/endpoints'
import { ApiError } from '../lib/api'

interface AuthState {
  user: User | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}
const Ctx = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  async function refresh() {
    setLoading(true)
    try {
      setUser(await endpoints.me())
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setUser(null)
      else throw e
    } finally {
      setLoading(false)
    }
  }
  async function logout() {
    await endpoints.logout()
    setUser(null)
  }
  useEffect(() => { void refresh() }, [])

  return <Ctx.Provider value={{ user, loading, refresh, logout }}>{children}</Ctx.Provider>
}

export function useAuth() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth used outside AuthProvider')
  return c
}
```

`web/src/auth/LoginPage.tsx`:

```tsx
import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useAuth } from './AuthProvider'

export function LoginPage() {
  const { refresh } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await endpoints.login(email, password)
      await refresh()
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) setError('Too many attempts — wait a moment and try again.')
      else setError('Invalid email or password.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} aria-label="login" className="mx-auto mt-24 flex w-80 flex-col gap-3">
      <h1 className="text-xl font-semibold">Sign in to Janus</h1>
      <label className="flex flex-col text-sm">Email
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required className="rounded border p-2" />
      </label>
      <label className="flex flex-col text-sm">Password
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required className="rounded border p-2" />
      </label>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <button type="submit" disabled={busy} className="rounded bg-blue-600 p-2 text-white disabled:opacity-50">
        {busy ? 'Signing in…' : 'Sign in'}
      </button>
    </form>
  )
}
```

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/auth/LoginPage.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Add a change-password form (spec: auth includes change password)**

Write the failing test `web/src/auth/ChangePassword.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ChangePasswordForm } from './ChangePassword'

test('posts current + new password and calls onDone', async () => {
  let body: any
  server.use(http.post('/v1/auth/password', async ({ request }) => { body = await request.json(); return new HttpResponse(null, { status: 204 }) }))
  const onDone = vi.fn()
  renderApp(<ChangePasswordForm onDone={onDone} onClose={() => {}} />, { withAuth: false })
  await userEvent.type(screen.getByLabelText(/current password/i), 'old')
  await userEvent.type(screen.getByLabelText(/new password/i), 'newpw')
  await userEvent.click(screen.getByRole('button', { name: /change password/i }))
  await waitFor(() => expect(onDone).toHaveBeenCalled())
  expect(body).toEqual({ current_password: 'old', new_password: 'newpw' })
})
```

Run: `npm run test -- --run src/auth/ChangePassword.test.tsx` → FAIL (unresolved). Then implement `web/src/auth/ChangePassword.tsx`:

```tsx
import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'

export function ChangePasswordForm({ onDone, onClose }: { onDone: () => void; onClose: () => void }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(''); setBusy(true)
    try { await endpoints.changePassword(current, next); onDone() }
    catch (err) { setError(err instanceof ApiError ? err.message : 'Could not change password.') }
    finally { setBusy(false) }
  }
  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/30">
      <form onSubmit={submit} className="w-80 rounded bg-white p-4 shadow">
        <h2 className="mb-3 text-lg font-semibold">Change password</h2>
        <label className="mb-2 flex flex-col text-sm">Current password
          <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required className="rounded border p-1" />
        </label>
        <label className="mb-2 flex flex-col text-sm">New password
          <input type="password" value={next} onChange={(e) => setNext(e.target.value)} required className="rounded border p-1" />
        </label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Change password</button>
        </div>
      </form>
    </div>
  )
}
```

Run the test again → PASS. (Wire a "Change password" item into the `TopBar` user area in Task 8 that toggles this form.)

- [ ] **Step 7: Commit**

```bash
git add web/src/auth/
git commit -m "feat(web): auth context + login + change-password form"
```

---

## Task 7: Unseal screen

**Files:**
- Create: `web/src/unseal/UnsealPage.tsx`
- Create: `web/src/unseal/UnsealPage.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/unseal/UnsealPage.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { UnsealPage } from './UnsealPage'

test('shamir: submitting shares advances progress then calls onUnsealed', async () => {
  const status = { initialized: true, sealed: true, type: 'shamir', threshold: 2, shares: 3, progress: 0 }
  server.use(
    http.get('/v1/sys/seal-status', () => HttpResponse.json(status)),
    http.post('/v1/sys/unseal', () => {
      status.progress += 1
      if (status.progress >= status.threshold) status.sealed = false
      return HttpResponse.json(status)
    }),
  )
  const onUnsealed = vi.fn()
  renderApp(<UnsealPage onUnsealed={onUnsealed} />, { withAuth: false })

  await screen.findByText(/0 of 2/i)
  await userEvent.type(screen.getByLabelText(/unseal key share/i), 'share-1')
  await userEvent.click(screen.getByRole('button', { name: /submit share/i }))
  await screen.findByText(/1 of 2/i)
  await userEvent.type(screen.getByLabelText(/unseal key share/i), 'share-2')
  await userEvent.click(screen.getByRole('button', { name: /submit share/i }))
  await waitFor(() => expect(onUnsealed).toHaveBeenCalled())
})

test('kms: auto-unsealed status calls onUnsealed without a share input', async () => {
  server.use(http.get('/v1/sys/seal-status', () =>
    HttpResponse.json({ initialized: true, sealed: false, type: 'awskms' })))
  const onUnsealed = vi.fn()
  renderApp(<UnsealPage onUnsealed={onUnsealed} />, { withAuth: false })
  await waitFor(() => expect(onUnsealed).toHaveBeenCalled())
  expect(screen.queryByLabelText(/unseal key share/i)).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/unseal/UnsealPage.test.tsx`
Expected: FAIL — cannot resolve `./UnsealPage`.

- [ ] **Step 3: Implement `UnsealPage`**

`web/src/unseal/UnsealPage.tsx`:

```tsx
import { FormEvent, useEffect, useState } from 'react'
import { endpoints, SealStatus } from '../lib/endpoints'

// UnsealPage drives a sealed server to unsealed. Shares live only in local state
// and are cleared immediately after each submit — never persisted or logged.
export function UnsealPage({ onUnsealed }: { onUnsealed: () => void }) {
  const [status, setStatus] = useState<SealStatus | null>(null)
  const [share, setShare] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function apply(s: SealStatus) {
    setStatus(s)
    if (!s.sealed) onUnsealed()
  }
  useEffect(() => { endpoints.sealStatus().then(apply).catch(() => setError('Could not read seal status.')) }, [])
  // KMS servers auto-unseal; poll until unsealed.
  useEffect(() => {
    if (status?.type !== 'awskms' || !status.sealed) return
    const t = setInterval(() => endpoints.sealStatus().then(apply).catch(() => {}), 1500)
    return () => clearInterval(t)
  }, [status?.type, status?.sealed])

  async function submitShare(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    const s = share
    setShare('') // clear before the await; never keep the share around
    try {
      apply(await endpoints.unsealShare(s))
    } catch {
      setError('That share was rejected.')
    } finally {
      setBusy(false)
    }
  }
  async function reset() {
    setError('')
    try { setStatus(await endpoints.unsealReset()) } catch { setError('Reset failed.') }
  }

  if (!status) return <p className="mt-24 text-center">Loading…</p>
  if (status.type === 'awskms')
    return <p className="mt-24 text-center">Waiting for KMS auto-unseal…</p>

  return (
    <form onSubmit={submitShare} className="mx-auto mt-24 flex w-96 flex-col gap-3">
      <h1 className="text-xl font-semibold">Unseal Janus</h1>
      <p className="text-sm text-gray-500">
        {(status.progress ?? 0)} of {status.threshold} shares submitted
      </p>
      <label className="flex flex-col text-sm">Unseal key share
        <input type="password" autoComplete="off" value={share} onChange={(e) => setShare(e.target.value)} required className="rounded border p-2" />
      </label>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="rounded bg-blue-600 p-2 text-white disabled:opacity-50">Submit share</button>
        <button type="button" onClick={reset} className="rounded border p-2">Reset</button>
      </div>
    </form>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/unseal/UnsealPage.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/unseal/
git commit -m "feat(web): unseal screen (shamir shares + progress + reset; kms auto)"
```

---

## Task 8: App shell, router & bootstrap guards

**Files:**
- Create: `web/src/shell/AppLayout.tsx`, `web/src/shell/TopBar.tsx`
- Rewrite: `web/src/App.tsx` (router + bootstrap)
- Create: `web/src/App.test.tsx` (replace the Task-3 smoke test)

- [ ] **Step 1: Rewrite the failing test**

Replace `web/src/App.test.tsx` with routing/guard coverage:

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { render } from '@testing-library/react'
import { afterEach } from 'vitest'
import App from './App'
import { server } from './test/msw'
import { queryClient } from './lib/queryClient'

// App uses the real queryClient singleton; clear it between tests to avoid
// one test's cached projects/seal-status bleeding into the next.
afterEach(() => queryClient.clear())

function boot(seal: object, me: number) {
  server.use(
    http.get('/v1/sys/seal-status', () => HttpResponse.json(seal)),
    http.get('/v1/auth/me', () => (me === 200 ? HttpResponse.json({ email: 'me@corp.io' }) : new HttpResponse(null, { status: me }))),
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
  )
}

test('sealed server routes to the unseal screen', async () => {
  boot({ initialized: true, sealed: true, type: 'shamir', threshold: 2, shares: 3, progress: 0 }, 401)
  render(<App />)
  expect(await screen.findByText(/unseal janus/i)).toBeInTheDocument()
})

test('unsealed + unauthenticated routes to login', async () => {
  boot({ initialized: true, sealed: false, type: 'shamir' }, 401)
  render(<App />)
  expect(await screen.findByRole('button', { name: /sign in/i })).toBeInTheDocument()
})

test('unsealed + authenticated shows the app shell', async () => {
  boot({ initialized: true, sealed: false, type: 'shamir' }, 200)
  render(<App />)
  expect(await screen.findByText(/me@corp\.io/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/App.test.tsx`
Expected: FAIL (old App renders only "Janus").

- [ ] **Step 3: Implement the shell + top bar**

`web/src/shell/TopBar.tsx`:

```tsx
import { useAuth } from '../auth/AuthProvider'

export function TopBar({ sealed }: { sealed: boolean }) {
  const { user, logout } = useAuth()
  return (
    <header className="flex items-center justify-between border-b px-4 py-2">
      <span className="font-semibold text-blue-700">Janus</span>
      <div className="flex items-center gap-4 text-sm">
        <span>{sealed ? '🔒 sealed' : '🔓 unsealed'}</span>
        {user && (
          <span className="flex items-center gap-2">
            {user.email}
            <button onClick={() => void logout()} className="rounded border px-2 py-0.5">Log out</button>
          </span>
        )}
      </div>
    </header>
  )
}
```

`web/src/shell/AppLayout.tsx`:

```tsx
import { ReactNode } from 'react'
import { TopBar } from './TopBar'

export function AppLayout({ sidebar, children }: { sidebar: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-screen flex-col">
      <TopBar sealed={false} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-64 shrink-0 overflow-y-auto border-r p-3">{sidebar}</aside>
        <main className="min-w-0 flex-1 overflow-y-auto p-4">{children}</main>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Implement `App` (bootstrap + router)**

Rewrite `web/src/App.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient, setAuthEventHandler } from './lib/queryClient'
import { endpoints, SealStatus } from './lib/endpoints'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { LoginPage } from './auth/LoginPage'
import { UnsealPage } from './unseal/UnsealPage'
import { AppLayout } from './shell/AppLayout'

function Gate() {
  const { user, loading } = useAuth()
  const [seal, setSeal] = useState<SealStatus | null>(null)
  const navigate = useNavigate()

  useEffect(() => { endpoints.sealStatus().then(setSeal).catch(() => setSeal(null)) }, [])
  useEffect(() => {
    setAuthEventHandler((kind) => {
      if (kind === 'sealed') endpoints.sealStatus().then(setSeal)
      else navigate('/login')
    })
  }, [navigate])

  if (!seal || loading) return <p className="mt-24 text-center">Loading…</p>
  if (seal.initialized === false)
    return <p className="mt-24 text-center">Server not initialized — run <code>janus init</code>.</p>
  if (seal.sealed) return <UnsealPage onUnsealed={() => endpoints.sealStatus().then(setSeal)} />
  if (!user) return <LoginPage />

  return (
    <AppLayout sidebar={<nav className="text-sm text-gray-500">Projects</nav>}>
      <Routes>
        <Route path="/" element={<p>Select a project.</p>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppLayout>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <Gate />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
```

> **Note:** the router tree grows in later tasks (Task 9 adds project/config routes into the `<Routes>` block; Task 8 leaves a single placeholder route so the shell renders). Tests use MSW; no real server needed.

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/App.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/App.tsx web/src/App.test.tsx web/src/shell/
git commit -m "feat(web): app shell + router + bootstrap guards (sealed/login/app)"
```

---

## Task 9: Project switcher + environment/config tree

**Files:**
- Create: `web/src/secrets/nav.ts` (query hooks)
- Create: `web/src/shell/Sidebar.tsx`
- Create: `web/src/shell/Sidebar.test.tsx`
- Modify: `web/src/App.tsx` (render `<Sidebar />` + config route)

- [ ] **Step 1: Write the failing test**

`web/src/shell/Sidebar.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Sidebar } from './Sidebar'

test('renders projects, then the selected project’s env → config tree', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'Prod' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: '' }] })),
  )
  renderApp(<Sidebar />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('Acme')).toBeInTheDocument()
  expect(await screen.findByText('prod')).toBeInTheDocument() // env label
  expect(await screen.findByRole('link', { name: /^prod$/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/shell/Sidebar.test.tsx`
Expected: FAIL — cannot resolve `./Sidebar`.

- [ ] **Step 3: Implement nav hooks + Sidebar**

`web/src/secrets/nav.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'

export const useProjects = () => useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
export const useEnvironments = (pid?: string) =>
  useQuery({ queryKey: ['envs', pid], queryFn: () => endpoints.listEnvironments(pid!), enabled: !!pid })
export const useConfigs = (pid?: string, eid?: string) =>
  useQuery({ queryKey: ['configs', pid, eid], queryFn: () => endpoints.listConfigs(pid!, eid!), enabled: !!pid && !!eid })
```

`web/src/shell/Sidebar.tsx`:

```tsx
import { Link, useParams, useNavigate } from 'react-router-dom'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'

function EnvConfigs({ pid, eid, name }: { pid: string; eid: string; name: string }) {
  const configs = useConfigs(pid, eid)
  return (
    <li>
      <div className="mt-1 text-xs uppercase tracking-wide text-gray-400">{name}</div>
      <ul className="ml-2">
        {configs.data?.map((c) => (
          <li key={c.id}>
            <Link to={`/projects/${pid}/configs/${c.id}`} className="block rounded px-1 py-0.5 hover:bg-gray-100">
              {c.name}
              {c.inherits_from && <span className="ml-1 text-xs text-blue-500">↳</span>}
            </Link>
          </li>
        ))}
      </ul>
    </li>
  )
}

export function Sidebar() {
  const { projectId } = useParams()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)

  return (
    <nav className="text-sm">
      <select
        value={projectId ?? ''}
        onChange={(e) => navigate(`/projects/${e.target.value}`)}
        className="mb-3 w-full rounded border p-1"
        aria-label="project"
      >
        <option value="" disabled>Select a project…</option>
        {projects.data?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
      {projectId && (
        <ul>
          {envs.data?.map((e) => <EnvConfigs key={e.id} pid={projectId} eid={e.id} name={e.name} />)}
        </ul>
      )}
      <div className="mt-4 border-t pt-2 text-gray-400">
        <Link to={`/projects/${projectId ?? ''}/audit`} className="block">Audit</Link>
        <Link to="/tokens" className="block">Tokens</Link>
        <Link to="/members" className="block">Members</Link>
        <div className="mt-2 border-t pt-2">
          <Link to="/transit" className="block">Transit</Link>
          <Link to="/settings" className="block">Settings</Link>
        </div>
      </div>
    </nav>
  )
}
```

The test asserts the `Acme` option, the `Prod` env label, and the `prod` config link. (The `select` option text `Acme` and the env label `Prod` differ from the config link `prod`, so the queries are all exercised.)

- [ ] **Step 4: Wire `Sidebar` into `App`**

In `web/src/App.tsx`, replace the placeholder sidebar and routes inside the authenticated branch:

```tsx
import { Sidebar } from './shell/Sidebar'
import { SecretEditor } from './secrets/SecretEditor' // added in Task 12
// ...
return (
  <AppLayout sidebar={<Sidebar />}>
    <Routes>
      <Route path="/" element={<p>Select a project.</p>} />
      <Route path="/projects/:projectId" element={<p>Select a config.</p>} />
      <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
      <Route path="/tokens" element={<p>Coming soon.</p>} />
      <Route path="/members" element={<p>Coming soon.</p>} />
      <Route path="/transit" element={<p>Coming soon.</p>} />
      <Route path="/settings" element={<p>Coming soon.</p>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  </AppLayout>
)
```

> **Ordering note:** `SecretEditor` does not exist until Task 12. To keep the build green at *this* commit, temporarily point the config route at `<p>Editor…</p>` and swap in `<SecretEditor />` during Task 12 (the Task-12 commit updates this import). The placeholders (`Coming soon.`) become real `Placeholder` components in Task 14.

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/shell/Sidebar.test.tsx` then `npm run test -- --run` (whole suite) and `npm run typecheck`.
Expected: PASS; no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/secrets/nav.ts web/src/shell/Sidebar.tsx web/src/shell/Sidebar.test.tsx web/src/App.tsx
git commit -m "feat(web): project switcher + env/config tree sidebar"
```

---

## Task 10: Lightweight structure creation (project / environment / config)

**Files:**
- Create: `web/src/structure/CreateForms.tsx` (three small dialog forms + a `useCreate*` mutation each)
- Create: `web/src/structure/CreateForms.test.tsx`
- Modify: `web/src/shell/Sidebar.tsx` (a "＋" affordance opens the relevant form)

- [ ] **Step 1: Write the failing test**

`web/src/structure/CreateForms.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { CreateProjectForm } from './CreateForms'

test('creating a project posts slug+name and calls onCreated', async () => {
  let body: any
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
    http.post('/v1/projects', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'p1', slug: body.slug, name: body.name })
    }),
  )
  const onCreated = vi.fn()
  renderApp(<CreateProjectForm onCreated={onCreated} onClose={() => {}} />, { withAuth: false })
  await userEvent.type(screen.getByLabelText(/slug/i), 'acme')
  await userEvent.type(screen.getByLabelText(/name/i), 'Acme')
  await userEvent.click(screen.getByRole('button', { name: /create/i }))
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' })))
  expect(body).toEqual({ slug: 'acme', name: 'Acme' })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/structure/CreateForms.test.tsx`
Expected: FAIL — cannot resolve `./CreateForms`.

- [ ] **Step 3: Implement the forms**

`web/src/structure/CreateForms.tsx`:

```tsx
import { FormEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, Project, Environment, Config } from '../lib/endpoints'
import { ApiError } from '../lib/api'

function Dialog({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/30">
      <div className="w-80 rounded bg-white p-4 shadow">
        <h2 className="mb-3 text-lg font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  )
}

function useSubmit<T>(fn: () => Promise<T>, onDone: (v: T) => void) {
  const [error, setError] = useState('')
  const m = useMutation({
    mutationFn: fn,
    onSuccess: onDone,
    onError: (e) => setError(e instanceof ApiError ? e.message : 'Failed to create.'),
  })
  return { error, submit: (e: FormEvent) => { e.preventDefault(); setError(''); m.mutate() }, busy: m.isPending }
}

export function CreateProjectForm({ onCreated, onClose }: { onCreated: (p: Project) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createProject(slug, name),
    (p) => { void qc.invalidateQueries({ queryKey: ['projects'] }); onCreated(p) },
  )
  return (
    <Dialog title="New project">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Slug<input aria-label="slug" value={slug} onChange={(e) => setSlug(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
        </div>
      </form>
    </Dialog>
  )
}

export function CreateEnvironmentForm({ pid, onCreated, onClose }: { pid: string; onCreated: (e: Environment) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createEnvironment(pid, slug, name),
    (e) => { void qc.invalidateQueries({ queryKey: ['envs', pid] }); onCreated(e) },
  )
  return (
    <Dialog title="New environment">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Slug<input aria-label="slug" value={slug} onChange={(e) => setSlug(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
        </div>
      </form>
    </Dialog>
  )
}

export function CreateConfigForm({ pid, eid, bases, onCreated, onClose }: { pid: string; eid: string; bases: Config[]; onCreated: (c: Config) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [base, setBase] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createConfig(pid, eid, name, base || undefined),
    (c) => { void qc.invalidateQueries({ queryKey: ['configs', pid, eid] }); onCreated(c) },
  )
  return (
    <Dialog title="New config">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Inherits from (same environment, optional)
          <select aria-label="inherits from" value={base} onChange={(e) => setBase(e.target.value)} className="w-full rounded border p-1">
            <option value="">— none —</option>
            {bases.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
        </div>
      </form>
    </Dialog>
  )
}
```

> The config form only offers same-environment bases (the caller passes that env's configs as `bases`); the server independently enforces the same-environment rule (M11), so this is UX guidance, not the security boundary.

- [ ] **Step 4: Wire "＋" affordances into the Sidebar**

In `Sidebar.tsx`, add local state to open each form (a "＋ Project" button near the switcher; a "＋" per environment for configs; a "＋ Env" under the tree). On `onCreated`, navigate to the new resource. Keep it minimal — the forms own their own submission. (Exact JSX left to the implementer; the forms and their props are fully specified above.)

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/structure/CreateForms.test.tsx` then `npm run test -- --run` and `npm run typecheck`.
Expected: PASS; no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/structure/ web/src/shell/Sidebar.tsx
git commit -m "feat(web): lightweight create forms for project/environment/config"
```

---

## Task 11: Dirty-buffer logic (pure module)

**Files:**
- Create: `web/src/secrets/dirty.ts`
- Create: `web/src/secrets/dirty.test.ts`

- [ ] **Step 1: Write the failing test**

`web/src/secrets/dirty.test.ts`:

```ts
import { emptyBuffer, setValue, removeKey, addKey, summarize, toChanges, isDirty } from './dirty'

// original = the config's own raw values (what a save diffs against)
const original = { DB_URL: 'postgres://a', LOG_LEVEL: 'info' }

test('editing an existing key marks it changed', () => {
  const b = setValue(emptyBuffer(), 'LOG_LEVEL', 'debug')
  expect(isDirty(b)).toBe(true)
  expect(summarize(b, original)).toEqual({ added: 0, changed: 1, removed: 0 })
  expect(toChanges(b, original)).toEqual([{ key: 'LOG_LEVEL', value: 'debug' }])
})

test('setting a key back to its original value is not a change', () => {
  const b = setValue(setValue(emptyBuffer(), 'LOG_LEVEL', 'debug'), 'LOG_LEVEL', 'info')
  expect(summarize(b, original)).toEqual({ added: 0, changed: 0, removed: 0 })
  expect(toChanges(b, original)).toEqual([])
})

test('adding a new key and removing an existing one', () => {
  let b = addKey(emptyBuffer(), 'FEATURE_X', 'on')
  b = removeKey(b, 'DB_URL')
  expect(summarize(b, original)).toEqual({ added: 1, changed: 0, removed: 1 })
  expect(toChanges(b, original)).toEqual(
    expect.arrayContaining([{ key: 'FEATURE_X', value: 'on' }, { key: 'DB_URL', delete: true }]),
  )
})

test('removing a key that never existed contributes nothing', () => {
  const b = removeKey(emptyBuffer(), 'NOPE')
  expect(toChanges(b, original)).toEqual([])
  expect(isDirty(b)).toBe(false)
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/secrets/dirty.test.ts`
Expected: FAIL — cannot resolve `./dirty`.

- [ ] **Step 3: Implement the pure buffer**

`web/src/secrets/dirty.ts`:

```ts
import { SecretChange } from '../lib/endpoints'

// A Buffer records intended edits keyed by secret name. `null` value = deletion.
export type Buffer = Record<string, { value: string | null }>
type Original = Record<string, string>

export const emptyBuffer = (): Buffer => ({})
export const setValue = (b: Buffer, key: string, value: string): Buffer => ({ ...b, [key]: { value } })
export const addKey = setValue // adding and editing both set a value; save() diffs vs original
export const removeKey = (b: Buffer, key: string): Buffer => ({ ...b, [key]: { value: null } })
export const revert = (b: Buffer, key: string): Buffer => { const { [key]: _drop, ...rest } = b; return rest }

// A buffer entry is "effective" only if it actually differs from the original.
function effective(b: Buffer, original: Original): Array<{ key: string; value: string | null }> {
  const out: Array<{ key: string; value: string | null }> = []
  for (const [key, { value }] of Object.entries(b)) {
    const had = key in original
    if (value === null) { if (had) out.push({ key, value: null }) } // delete only a key that exists
    else if (!had || original[key] !== value) out.push({ key, value }) // add or real change
  }
  return out
}

export const isDirty = (b: Buffer, original: Original = {}): boolean => effective(b, original).length > 0

export function summarize(b: Buffer, original: Original) {
  let added = 0, changed = 0, removed = 0
  for (const e of effective(b, original)) {
    if (e.value === null) removed++
    else if (e.key in original) changed++
    else added++
  }
  return { added, changed, removed }
}

export function toChanges(b: Buffer, original: Original): SecretChange[] {
  return effective(b, original).map((e) => (e.value === null ? { key: e.key, delete: true } : { key: e.key, value: e.value }))
}
```

> **Note:** `isDirty(b)` in the test is called with no `original`, which defaults to `{}`; a lone edit still counts as dirty (an "add" vs an empty original). The `removeKey('NOPE')` case is not dirty because deleting a key absent from the (empty) original yields no effective change. `summarize`/`toChanges` always take the real `original`.

- [ ] **Step 4: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/secrets/dirty.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/secrets/dirty.ts web/src/secrets/dirty.test.ts
git commit -m "feat(web): pure dirty-buffer logic (summary + batch mapping)"
```

---

## Task 12: Secret editor — masked list, origin badges, audited reveal

**Files:**
- Create: `web/src/secrets/SecretEditor.tsx`
- Create: `web/src/secrets/SecretEditor.test.tsx`
- Modify: `web/src/App.tsx` (point the config route at `<SecretEditor />`)

- [ ] **Step 1: Write the failing test**

`web/src/secrets/SecretEditor.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

function seed() {
  server.use(
    http.get('/v1/configs/c1/secrets', () =>
      HttpResponse.json({ secrets: {
        DB_URL: { value_version: 3, created_at: '', origin: 'own' },
        SENTRY_DSN: { value_version: 1, created_at: '', origin: 'inherited' },
      } })),
    http.get('/v1/configs/c1/secrets?reveal=true&raw=true', () =>
      HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })),
  )
}

test('renders masked rows with origin badges; no reveal on load', async () => {
  let revealed = false
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => { revealed = true; return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' }) }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('DB_URL')).toBeInTheDocument()
  expect(screen.getByText('inherited')).toBeInTheDocument()
  expect(screen.getByText('own')).toBeInTheDocument()
  expect(screen.queryByText('postgres://a')).toBeNull() // masked by default
  expect(revealed).toBe(false) // masked list must not call the audited reveal
})

test('clicking reveal fetches the audited value and shows it', async () => {
  seed()
  let revealed = false
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => { revealed = true; return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' }) }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal db_url/i }))
  await waitFor(() => expect(revealed).toBe(true))
  expect(await screen.findByText('postgres://a')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/secrets/SecretEditor.test.tsx`
Expected: FAIL — cannot resolve `./SecretEditor`.

- [ ] **Step 3: Implement the editor (read + reveal only; editing arrives in Task 13)**

`web/src/secrets/SecretEditor.tsx`:

```tsx
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { endpoints, MaskedSecret } from '../lib/endpoints'

const badge: Record<MaskedSecret['origin'], string> = {
  own: 'bg-green-100 text-green-700',
  inherited: 'bg-blue-100 text-blue-700',
  overridden: 'bg-amber-100 text-amber-700',
}

export function SecretEditor() {
  const { configId } = useParams()
  const cid = configId!
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  // Revealed plaintext lives ONLY here — never in the query cache; cleared on unmount.
  const [revealed, setRevealed] = useState<Record<string, string>>({})

  async function reveal(key: string) {
    const r = await endpoints.revealKey(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
  }

  if (masked.isLoading) return <p>Loading…</p>
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  const rows = Object.entries(masked.data ?? {})

  return (
    <table className="w-full text-sm">
      <thead><tr className="text-left text-gray-400"><th>KEY</th><th>VALUE</th><th>ORIGIN</th><th>v</th></tr></thead>
      <tbody>
        {rows.map(([key, meta]) => (
          <tr key={key} className="border-t">
            <td className="py-1 font-mono">{key}</td>
            <td className="py-1 font-mono">
              {key in revealed ? revealed[key] : '•••••••'}
              {!(key in revealed) && (
                <button onClick={() => void reveal(key)} aria-label={`reveal ${key}`} className="ml-2 text-gray-400">👁</button>
              )}
            </td>
            <td className="py-1"><span className={`rounded px-1.5 ${badge[meta.origin]}`}>{meta.origin}</span></td>
            <td className="py-1 text-gray-400">{meta.value_version}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Step 4: Point the config route at the editor**

In `web/src/App.tsx`, ensure the import `import { SecretEditor } from './secrets/SecretEditor'` is present and the route `/projects/:projectId/configs/:configId` renders `<SecretEditor />` (replacing the Task-9 temporary placeholder).

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/secrets/SecretEditor.test.tsx` then `npm run test -- --run` and `npm run typecheck`.
Expected: PASS; no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/secrets/SecretEditor.tsx web/src/secrets/SecretEditor.test.tsx web/src/App.tsx
git commit -m "feat(web): secret editor masked list + origin badges + audited reveal"
```

---

## Task 13: Secret editor — edit, add/delete, batched Save as vN

**Files:**
- Modify: `web/src/secrets/SecretEditor.tsx` (edit buffer + save + unsaved guard)
- Create: `web/src/secrets/SecretEditor.save.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/secrets/SecretEditor.save.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

test('editing a value then Save posts one batch and shows the new version', async () => {
  let put: any
  server.use(
    http.get('/v1/configs/c1/secrets', () =>
      HttpResponse.json({ secrets: { LOG_LEVEL: { value_version: 1, created_at: '', origin: 'own' } } })),
    http.get('/v1/configs/c1/secrets?reveal=true&raw=true', () =>
      HttpResponse.json({ version: 1, secrets: { LOG_LEVEL: 'info' } })),
    http.put('/v1/configs/c1/secrets', async ({ request }) => {
      put = await request.json()
      return HttpResponse.json({ version: 2, id: 'v2', created_at: '' })
    }),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('LOG_LEVEL')
  await userEvent.click(screen.getByRole('button', { name: /edit log_level/i }))
  const input = screen.getByRole('textbox', { name: /value for log_level/i })
  await userEvent.clear(input)
  await userEvent.type(input, 'debug')
  expect(screen.getByText(/1 changed/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save as v2/i }))
  await waitFor(() => expect(put).toEqual({ message: '', changes: [{ key: 'LOG_LEVEL', value: 'debug' }] }))
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/secrets/SecretEditor.save.test.tsx`
Expected: FAIL (no edit button / no save button yet).

- [ ] **Step 3: Extend the editor with editing + save**

Extend `web/src/secrets/SecretEditor.tsx`. Load the config's own raw values (for editing), keep a dirty `Buffer`, render edit inputs, compute the pending summary, and Save as the next version. Full replacement:

```tsx
import { useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, MaskedSecret } from '../lib/endpoints'
import { Buffer, emptyBuffer, setValue, removeKey, addKey, summarize, toChanges, isDirty } from './dirty'

const badge: Record<MaskedSecret['origin'], string> = {
  own: 'bg-green-100 text-green-700',
  inherited: 'bg-blue-100 text-blue-700',
  overridden: 'bg-amber-100 text-amber-700',
}

export function SecretEditor() {
  const { configId } = useParams()
  const cid = configId!
  const qc = useQueryClient()
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  const raw = useQuery({ queryKey: ['config', cid, 'raw'], queryFn: () => endpoints.rawOwnValues(cid) })
  const [buffer, setBuffer] = useState<Buffer>(emptyBuffer())
  const [editing, setEditing] = useState<Record<string, boolean>>({})
  const [revealed, setRevealed] = useState<Record<string, string>>({})

  const original = raw.data ?? {}
  const version = masked.data ? Math.max(0, ...Object.values(masked.data).map((m) => m.value_version)) : 0
  const summary = useMemo(() => summarize(buffer, original), [buffer, original])
  const dirty = isDirty(buffer, original)

  const save = useMutation({
    mutationFn: () => endpoints.saveSecrets(cid, toChanges(buffer, original), ''),
    onSuccess: () => {
      setBuffer(emptyBuffer())
      setEditing({})
      void qc.invalidateQueries({ queryKey: ['config', cid] })
    },
  })

  async function reveal(key: string) {
    const r = await endpoints.revealKey(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
  }
  function valueOf(key: string): string {
    return key in buffer ? (buffer[key].value ?? '') : (original[key] ?? '')
  }

  if (masked.isLoading || raw.isLoading) return <p>Loading…</p>
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  const rows = Object.entries(masked.data ?? {})

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <span className="text-sm text-gray-500">
          {dirty ? `Pending: +${summary.added} added · ${summary.changed} changed · ${summary.removed} removed` : `${rows.length} keys`}
        </span>
        <button
          onClick={() => save.mutate()}
          disabled={!dirty || save.isPending}
          className="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-40"
        >
          {save.isPending ? 'Saving…' : `Save as v${version + 1}`}
        </button>
      </div>
      {save.isError && <p role="alert" className="mb-2 text-sm text-red-600">Save failed.</p>}
      <table className="w-full text-sm">
        <thead><tr className="text-left text-gray-400"><th>KEY</th><th>VALUE</th><th>ORIGIN</th><th>v</th></tr></thead>
        <tbody>
          {rows.map(([key, meta]) => {
            const removedRow = key in buffer && buffer[key].value === null
            return (
              <tr key={key} className={`border-t ${removedRow ? 'line-through opacity-50' : ''}`}>
                <td className="py-1 font-mono">{key}</td>
                <td className="py-1 font-mono">
                  {editing[key] ? (
                    <input
                      aria-label={`value for ${key}`}
                      value={valueOf(key)}
                      onChange={(e) => setBuffer((b) => setValue(b, key, e.target.value))}
                      className="w-full rounded border p-1"
                    />
                  ) : (
                    <>
                      {key in revealed ? revealed[key] : '•••••••'}
                      {meta.origin !== 'inherited' && (
                        <button aria-label={`edit ${key}`} onClick={() => setEditing((s) => ({ ...s, [key]: true }))} className="ml-2 text-gray-400">✎</button>
                      )}
                      {!(key in revealed) && (
                        <button aria-label={`reveal ${key}`} onClick={() => void reveal(key)} className="ml-1 text-gray-400">👁</button>
                      )}
                      {meta.origin !== 'inherited' && !removedRow && (
                        <button aria-label={`remove ${key}`} onClick={() => setBuffer((b) => removeKey(b, key))} className="ml-1 text-red-400">✕</button>
                      )}
                    </>
                  )}
                </td>
                <td className="py-1"><span className={`rounded px-1.5 ${badge[meta.origin]}`}>{meta.origin}</span></td>
                <td className="py-1 text-gray-400">{meta.value_version}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <AddKeyRow onAdd={(k, v) => setBuffer((b) => addKey(b, k, v))} />
    </div>
  )
}

function AddKeyRow({ onAdd }: { onAdd: (key: string, value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  return (
    <div className="mt-3 flex gap-2">
      <input aria-label="new key" placeholder="NEW_KEY" value={key} onChange={(e) => setKey(e.target.value)} className="rounded border p-1 font-mono" />
      <input aria-label="new value" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} className="rounded border p-1 font-mono" />
      <button
        disabled={!key}
        onClick={() => { onAdd(key, value); setKey(''); setValue('') }}
        className="rounded border px-2 disabled:opacity-40"
      >
        ＋ Add key
      </button>
    </div>
  )
}
```

- [ ] **Step 4: Add the unsaved-changes navigation guard**

Add a `beforeunload` guard while the buffer is dirty (below the `dirty` computation, before the early returns):

```tsx
import { useEffect } from 'react'
// ...
useEffect(() => {
  if (!dirty) return
  const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }
  window.addEventListener('beforeunload', h)
  return () => window.removeEventListener('beforeunload', h)
}, [dirty])
```

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run src/secrets/SecretEditor.save.test.tsx` then `npm run test -- --run` and `npm run typecheck`.
Expected: PASS; no type errors. (The reveal test from Task 12 still passes; `save as v2` appears because the single key is `value_version:1` → `version+1 = 2`.)

- [ ] **Step 6: Commit**

```bash
git add web/src/secrets/SecretEditor.tsx web/src/secrets/SecretEditor.save.test.tsx
git commit -m "feat(web): editor dirty buffer, add/edit/delete, batched Save as vN + guard"
```

---

## Task 14: Placeholders for deferred areas + empty states

**Files:**
- Create: `web/src/shell/Placeholder.tsx`
- Create: `web/src/shell/Placeholder.test.tsx`
- Modify: `web/src/App.tsx` (use `<Placeholder>` for the deferred routes; empty-state copy)

- [ ] **Step 1: Write the failing test**

`web/src/shell/Placeholder.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { Placeholder } from './Placeholder'

test('names the feature and marks it as coming later', () => {
  render(<Placeholder feature="Audit viewer" />)
  expect(screen.getByText(/audit viewer/i)).toBeInTheDocument()
  expect(screen.getByText(/coming in a later/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `web/`): `npm run test -- --run src/shell/Placeholder.test.tsx`
Expected: FAIL — cannot resolve `./Placeholder`.

- [ ] **Step 3: Implement `Placeholder`**

`web/src/shell/Placeholder.tsx`:

```tsx
export function Placeholder({ feature }: { feature: string }) {
  return (
    <div className="mt-16 text-center text-gray-500">
      <p className="text-lg">{feature}</p>
      <p className="text-sm">Coming in a later Phase-2 slice.</p>
    </div>
  )
}
```

- [ ] **Step 4: Use it for the deferred routes + empty states**

In `web/src/App.tsx`, replace the deferred-route placeholders and add empty-state copy:

```tsx
import { Placeholder } from './shell/Placeholder'
// ...
<Route path="/" element={<div className="mt-16 text-center text-gray-500">Select or create a project to begin.</div>} />
<Route path="/projects/:projectId" element={<div className="mt-16 text-center text-gray-500">Select a config from the sidebar.</div>} />
<Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
<Route path="/projects/:projectId/audit" element={<Placeholder feature="Audit viewer" />} />
<Route path="/tokens" element={<Placeholder feature="Token management" />} />
<Route path="/members" element={<Placeholder feature="Member management" />} />
<Route path="/transit" element={<Placeholder feature="Transit UI" />} />
<Route path="/settings" element={<Placeholder feature="Settings" />} />
<Route path="*" element={<Navigate to="/" replace />} />
```

- [ ] **Step 5: Run test to verify it passes**

Run (from `web/`): `npm run test -- --run` (whole suite) and `npm run typecheck`.
Expected: PASS; no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/shell/Placeholder.tsx web/src/shell/Placeholder.test.tsx web/src/App.tsx
git commit -m "feat(web): coming-soon placeholders for deferred areas + empty states"
```

---

## Task 15: Build wiring, docs, and full gate sweep

**Files:**
- Modify: `Dockerfile` (add web build stage; copy dist before `go build`)
- Modify: `Makefile` (web-build/test targets; `build` and `test` include web; `dev`)
- Create: `docs/web.md`
- Modify: `README.md`, `status.md`

- [ ] **Step 1: Add the web build stage to the Dockerfile**

Replace `Dockerfile` with (adds a `web` stage; the Go stage copies the built `dist` over the committed placeholder before building, so the binary embeds the real UI):

```dockerfile
# --- web build stage ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/web/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /janus ./cmd/janus

FROM alpine:3.21
RUN adduser -D -u 10001 janus
USER janus
COPY --from=build /janus /usr/local/bin/janus
EXPOSE 8200
ENTRYPOINT ["janus"]
CMD ["server"]
```

- [ ] **Step 2: Update the Makefile**

Replace the `test`, `build`, and `dev` targets and add web targets (keep `dev-up`, `migrate`, `cover`, `lint` as they are; extend `.PHONY`):

```makefile
.PHONY: dev dev-up test lint build migrate cover web-deps web-build web-test

web-deps:
	cd web && npm ci

web-test:
	cd web && npm run test -- --run

# Build the SPA and stage it where go:embed picks it up.
web-build:
	cd web && npm ci && npm run build
	rm -rf internal/web/dist
	mkdir -p internal/web/dist
	cp -r web/dist/. internal/web/dist/

test:
	go test -race ./...
	cd web && npm run test -- --run

build: web-build
	go build -o bin/janus ./cmd/janus

dev:
	@echo "Run these in two terminals (same-origin via Vite's /v1 proxy):"
	@echo "  1) cd web && npm run dev      # Vite dev server on :5173, proxies /v1 -> :8200"
	@echo "  2) make dev-up                # Go server + Postgres on :8200"
```

> **Note:** `make build` overwrites the committed `internal/web/dist/index.html` placeholder with the real build output; that shows as a modified file in `git status` — do **not** commit it (the root `.gitignore` already ignores everything under `internal/web/dist` except the placeholder). Run `git checkout -- internal/web/dist/index.html` to restore the placeholder if needed. Everyday dev uses `make dev` (no embed).

- [ ] **Step 3: Verify a full embedded build works**

Run: `make build` then `./bin/janus --help` (or the server's help) to confirm the binary builds with embedded assets. Restore the placeholder afterward:
`git checkout -- internal/web/dist/index.html`
Expected: binary builds; no build errors.

- [ ] **Step 4: Write `docs/web.md`**

Create `docs/web.md` documenting: the `web/` app + `internal/web` embed, the SPA-fallback + CSP, the seal-gate narrowing, dev (`make dev` / Vite proxy) vs prod (embedded) serving, the milestone's scope (core editor) and what's deferred, and the test stack (Vitest + RTL + MSW; Go `internal/web` test). Cross-link `docs/references.md` for the origin badges and the spec at `docs/superpowers/specs/2026-07-06-spa-core-editor-design.md`.

- [ ] **Step 5: Update README + status**

- `README.md`: under the UI/roadmap area, note the web UI now exists (core editor slice) and mark Phase-2 sub-project B milestone 1 in progress/done; point at `docs/web.md`.
- `status.md`: add a "Phase 2 · Sub-project B — React SPA (milestone 1: core editor)" entry mirroring the M10/M11 format — scope, the 15-task checklist all checked, and a verification block.

- [ ] **Step 6: Full gate sweep**

Run and confirm all green:

```bash
go build ./... && go vet ./...
go test ./... -count=1
cd web && npm run test -- --run && npm run typecheck && npm run build && cd ..
gosec -exclude-dir=internal/crypto/shamir ./...      # expect 0 issues
govulncheck ./...                                     # expect 0 affecting
git checkout -- internal/web/dist/index.html          # restore placeholder if make build ran
```

Expected: Go build/vet/test pass; web tests + typecheck + build pass; gosec 0 issues; govulncheck 0 affecting. `internal/crypto`/`internal/authz`/`internal/audit` coverage unchanged at 100% (no changes there).

- [ ] **Step 7: Commit**

```bash
git add Dockerfile Makefile docs/web.md README.md status.md
git commit -m "build(web): Dockerfile web stage + Makefile wiring; docs + status; gate sweep"
```

---

## Notes for the implementer

- **No new `/v1` endpoints.** The only server changes are `internal/web` (new), the `RequireUnsealed` narrowing, `MountUI`, and the boot wiring. Everything else consumes existing endpoints.
- **Secret plaintext & Shamir shares never enter the Query cache or storage** — they live in ephemeral component state (`revealed` map, `share` field) and are cleared. Do not "cache" a revealed value in a query.
- **Editing targets raw own-values** (`?reveal=true&raw=true`), not resolved values; inherited rows are not editable in place.
- **Save uses only the batch endpoint** (`PUT …/secrets` with the full `changes` array incl. `{key,delete:true}`); do not call the per-key or delete endpoints.
- **Keep the build green at each commit.** The one forward-reference (Task 9's route → `SecretEditor` from Task 12) is handled by the temporary placeholder noted in Task 9.
- Trust `go build`/`go test` and `npm run test`/`typecheck` over editor diagnostics (stale gopls/TS-server false positives for new-in-branch symbols are common in this repo).
