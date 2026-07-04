.PHONY: dev dev-up test lint build migrate cover

test:
	go test -race ./...

lint:
	go vet ./...

build:
	go build -o bin/janus ./cmd/janus

cover:
	go test -coverprofile=crypto.out ./internal/crypto
	go tool cover -func=crypto.out | tail -1

dev:
	@echo "make dev: hot-reload arrives with the web UI milestone; use 'make dev-up'"; exit 1

dev-up: build
	docker compose up -d --build
	./scripts/dev-unseal.sh

migrate:
	JANUS_DATABASE_URL=$${JANUS_DATABASE_URL:-postgres://janus:janus-dev@127.0.0.1:5432/janus?sslmode=disable} \
		go run ./cmd/janus migrate
