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

lint:
	go vet ./...

build: web-build
	go build -o bin/janus ./cmd/janus

cover:
	go test -coverprofile=crypto.out ./internal/crypto
	go tool cover -func=crypto.out | tail -1

dev:
	@echo "Run these in two terminals (same-origin via Vite's /v1 proxy):"
	@echo "  1) cd web && npm run dev      # Vite dev server on :5173, proxies /v1 -> :8200"
	@echo "  2) make dev-up                # Go server + Postgres on :8200"

dev-up: build
	docker compose up -d --build
	./scripts/dev-unseal.sh

migrate:
	JANUS_DATABASE_URL=$${JANUS_DATABASE_URL:-postgres://janus:janus-dev@127.0.0.1:5432/janus?sslmode=disable} \
		go run ./cmd/janus migrate
