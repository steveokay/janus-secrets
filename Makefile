.PHONY: dev dev-up test test-modules lint build migrate cover web-deps web-build web-test

web-deps:
	cd web && npm ci

# `-- --run` was Vitest's watch-mode escape hatch; the suite is plain
# `node --test` now and the flag is inert.
web-test:
	cd web && npm test

# Build the SPA and stage it where go:embed picks it up.
web-build:
	cd web && npm ci && npm run build
	rm -rf internal/web/dist
	mkdir -p internal/web/dist
	cp -r web/dist/. internal/web/dist/

test: test-modules
	go test -race ./...
	cd web && npm test

# The nested modules. `go test ./...` above covers the ROOT module only — the
# `./...` pattern does not descend into a directory with its own go.mod — so
# without this `make test` was green while sdk/go and terraform-provider-janus
# went entirely unrun. CI checks all of them (see the go-modules / sdk-ts /
# sdk-python jobs); keep this in step with those so a local run means what a
# contributor thinks it means.
test-modules:
	cd sdk/go && go test -race ./...
	cd terraform-provider-janus && go test -race ./...
	cd sdk/ts && npm test
	cd sdk/python && python -m unittest discover -s tests -t . -q

lint:
	go vet ./...

build: web-build
	go build -o bin/janus ./cmd/janus

cover:
	go test -coverprofile=crypto.out ./internal/crypto
	go tool cover -func=crypto.out | tail -1

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
