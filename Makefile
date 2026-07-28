# Janus — build, run, and the local mirrors of every CI gate.
#
# Any target below that has a counterpart in .github/workflows/ci.yml runs the
# SAME command with the SAME flags. Keep the two in step: a `make ci` that is
# quietly a subset of the real gates is worse than no target at all, because a
# green local run then reads as a promise it cannot keep.
#
# `make help` lists everything.

.PHONY: help dev dev-up build build-fast go-build clean migrate \
        test test-modules cover lint vuln sec helm-test ci \
        e2e e2e-up e2e-down \
        web-deps web-bundle web-build web-check web-test

# On Windows `go build -o bin/janus` writes a PE either way; giving it the .exe
# name is what makes it runnable from cmd/PowerShell, which resolve commands
# through PATHEXT. Git Bash/MSYS appends .exe when resolving a command, so
# `bin/janus` in scripts/dev-unseal.sh still finds it.
EXE :=
ifeq ($(OS),Windows_NT)
EXE := .exe
endif

# Stamp the same three variables goreleaser sets (.goreleaser.yaml). Without
# this a locally built binary reports `janus dev (commit none, built unknown)`,
# which makes it indistinguishable from any other hand-built binary — exactly
# the thing you need to know when a dev instance misbehaves.
VPKG    := github.com/steveokay/janus-secrets/internal/version
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Date=$(DATE)

E2E_COMPOSE := docker compose -p janus-e2e -f docker-compose.yml -f web/tests/e2e/docker-compose.e2e.yml
E2E_URL     := http://localhost:8231

help:
	@echo "build      build the SPA, embed it, and build bin/janus$(EXE) (version-stamped)"
	@echo "build-fast same, but skips 'npm ci' - only when node_modules matches package-lock"
	@echo "dev        print the two-terminal hot-reload workflow"
	@echo "dev-up     build, bring up docker compose, and unseal the dev instance"
	@echo "migrate    apply migrations to the local db"
	@echo ""
	@echo "ci         every CI gate, in CI's order (build lint test cover vuln sec helm-test)"
	@echo "test       go tests (root + nested modules) + web typecheck + web unit tests"
	@echo "cover      internal/crypto coverage; FAILS below 100.0%, as CI does"
	@echo "lint       go vet across the root module AND the nested modules"
	@echo "vuln/sec   govulncheck / gosec, pinned to the versions CI uses"
	@echo "helm-test  helm lint + render every seal mode + reject invalid seal configs"
	@echo "e2e        throwaway stack up -> Playwright -> down -v (destructive by design)"
	@echo "clean      remove bin/, build output, and coverage profiles"

# --- web ---------------------------------------------------------------------

web-deps:
	cd web && npm ci

web-check:
	cd web && npm run check

# `-- --run` was Vitest's watch-mode escape hatch; the suite is plain
# `node --test` now and the flag is inert.
web-test:
	cd web && npm test

# Build the SPA and stage it where go:embed picks it up. Assumes node_modules
# is already installed — `web-build` is the target that guarantees that.
web-bundle:
	cd web && npm run build
	rm -rf internal/web/dist
	mkdir -p internal/web/dist
	cp -r web/dist/. internal/web/dist/

web-build: web-deps web-bundle

# --- build -------------------------------------------------------------------

go-build:
	go build -ldflags "$(LDFLAGS)" -o bin/janus$(EXE) ./cmd/janus

build: web-build go-build

# The `npm ci` in web-build wipes and reinstalls node_modules, which is both
# the slow step and the one that trips EPERM on Windows. Use this when the
# dependency tree is already in step with package-lock.json and you only
# changed source.
build-fast: web-bundle go-build

clean:
	rm -rf bin web/dist crypto.out internal/web/dist/assets internal/web/dist/favicon.svg
	@# internal/web/dist/index.html is a tracked placeholder that the build
	@# overwrites; put the committed one back rather than leaving a dirty tree.
	-git checkout -- internal/web/dist/index.html

# --- test & gates ------------------------------------------------------------

# `go test` defaults to a 10-MINUTE timeout and internal/api under -race sits
# right on that line, so CI raised it to 30m. Locally it must match, or a slow
# machine reports a timeout panic that looks nothing like the clock it is.
test: test-modules
	go test -race -timeout 30m ./...
	cd web && npm run check
	cd web && npm test

# The nested modules. `go test ./...` above covers the ROOT module only — the
# `./...` pattern does not descend into a directory with its own go.mod — so
# without this `make test` was green while sdk/go and terraform-provider-janus
# went entirely unrun. CI checks all of them (see the go-modules / sdk-ts /
# sdk-python jobs); keep this in step with those so a local run means what a
# contributor thinks it means.
test-modules:
	cd sdk/go && go test -race -timeout 15m ./...
	cd terraform-provider-janus && go test -race -timeout 15m ./...
	cd sdk/ts && npm test
	cd sdk/python && python -m unittest discover -s tests -t . -q

lint:
	go vet ./...
	cd sdk/go && go vet ./...
	cd terraform-provider-janus && go vet ./...

# CLAUDE.md makes both of these build failures. They were only ever enforced in
# CI, so the first time you saw a finding was after pushing.
#
# GOTOOLCHAIN is not decoration here. `go run <tool>@latest` resolves in its own
# module context, so go.mod's `toolchain` line does NOT apply and the scan runs
# against whatever stdlib your local `go` happens to be. On a machine one patch
# release behind, `make vuln` reports a stdlib CVE that CI (which builds on
# `stable`) does not have — a finding about the developer's laptop, dressed up
# as a finding about this repo.
TOOLCHAIN := $(shell awk '/^toolchain /{print $$2}' go.mod)

vuln:
	GOTOOLCHAIN=$(TOOLCHAIN) go run golang.org/x/vuln/cmd/govulncheck@latest ./...

sec:
	GOTOOLCHAIN=$(TOOLCHAIN) go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -exclude-dir=internal/crypto/shamir ./...

# Prints AND enforces. Reporting the number without failing on it is how a
# 100%-coverage gate silently becomes a suggestion.
cover:
	go test -coverprofile=crypto.out ./internal/crypto
	@pct=$$(go tool cover -func=crypto.out | tail -1 | awk '{print $$3}'); \
	echo "internal/crypto coverage: $$pct"; \
	if [ "$$pct" != "100.0%" ]; then \
		echo "coverage gate failed: internal/crypto must be 100.0%" >&2; exit 1; \
	fi

# The chart had no CI at all until three defects survived to a real deployment.
# This mirrors the `helm` job; each render GREPS for the configured value,
# because piping to /dev/null is precisely what hid a validator checking a
# values key that did not exist (gcpkms.keyName vs the real gcpkms.key).
# CI additionally asserts the API Service selector with a python/pyyaml step;
# the grep below covers the same regression without the extra dependency.
helm-test:
	@command -v helm >/dev/null || { echo "helm not found - install it to run this gate" >&2; exit 1; }
	helm lint deploy/helm/janus --set seal.type=shamir
	@set -eu; \
	helm template t deploy/helm/janus --set seal.type=shamir > /dev/null; \
	helm template t deploy/helm/janus --set seal.type=awskms \
		--set seal.awskms.keyArn=arn:aws:kms:us-east-1:1:key/x \
		| grep -q 'arn:aws:kms:us-east-1:1:key/x'; \
	helm template t deploy/helm/janus --set seal.type=gcpkms \
		--set seal.gcpkms.key=projects/p/locations/l/keyRings/r/cryptoKeys/k \
		| grep -q 'projects/p/locations/l/keyRings/r/cryptoKeys/k'; \
	helm template t deploy/helm/janus --set seal.type=azurekv \
		--set seal.azurekv.vaultUrl=https://v.vault.azure.net \
		--set seal.azurekv.keyName=k \
		| grep -q 'https://v.vault.azure.net'; \
	echo "ok: every seal mode renders its configured value"
	@set -eu; \
	for args in "seal.type=bogus" "seal.type=awskms" "seal.type=gcpkms" "seal.type=azurekv"; do \
		if helm template t deploy/helm/janus --set $$args > /dev/null 2>&1; then \
			echo "expected a template failure for: $$args" >&2; exit 1; \
		fi; \
	done; \
	echo "ok: invalid and incomplete seal configs are rejected"
	@set -eu; \
	helm template t deploy/helm/janus --set seal.type=shamir --set postgresql.enabled=true \
		--show-only templates/service.yaml \
		| sed -n '/^  selector:/,$$p' | grep -q 'app.kubernetes.io/component: server'; \
	echo "ok: the API Service selector pins component=server"

# Everything CI enforces, in CI's order. Needs Docker (integration tests),
# Node, and helm.
ci: build lint test cover vuln sec helm-test
	@echo "all local gates passed"

# --- browser E2E --------------------------------------------------------------
#
# DESTRUCTIVE BY DESIGN: the suite runs the one-shot init ceremony and hard-
# destroys projects, so it gets its own compose project name, its own volume,
# and port :8231. Never point it at :8210. First run only:
#   cd web && npx playwright install --with-deps chromium
# See web/tests/e2e/README.md.

e2e-up:
	$(E2E_COMPOSE) up -d --build
	@echo "waiting for $(E2E_URL) (it boots SEALED — the suite unseals it)"
	curl -sf --retry 30 --retry-all-errors --retry-delay 2 $(E2E_URL)/v1/sys/seal-status

e2e-down:
	$(E2E_COMPOSE) down -v
	rm -rf web/tests/e2e/.state

# Every run needs a fresh stack, so tear down first and always tear down after.
e2e:
	$(MAKE) e2e-down || true
	$(MAKE) e2e-up
	cd web && JANUS_E2E_BASE_URL=$(E2E_URL) npm run test:e2e; status=$$?; \
		cd .. && $(MAKE) e2e-down; exit $$status

# --- dev ---------------------------------------------------------------------

dev:
	@echo "Run these in two terminals (same-origin via Vite's /v1 proxy):"
	@echo "  1) cd web && npm run dev      # Vite dev server on :5173, proxies /v1 -> :8210"
	@echo "  2) make dev-up                # Go server on :8210, Postgres on :5433"

dev-up: build
	docker compose up -d --build
	./scripts/dev-unseal.sh

migrate:
	JANUS_DATABASE_URL=$${JANUS_DATABASE_URL:-postgres://janus:janus-dev@127.0.0.1:5433/janus?sslmode=disable} \
		go run ./cmd/janus migrate
