.PHONY: dev test lint build migrate cover

test:
	go test -race ./...

lint:
	go vet ./...

build:
	go build -o bin/keyhaven ./cmd/keyhaven

cover:
	go test -coverprofile=crypto.out ./internal/crypto
	go tool cover -func=crypto.out | tail -1

dev:
	@echo "make dev: not yet implemented (arrives with the API milestone)"; exit 1

migrate:
	@echo "make migrate: not yet implemented (arrives with the store milestone)"; exit 1
