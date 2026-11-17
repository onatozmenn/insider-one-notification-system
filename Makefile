.PHONY: up down logs build test test-unit test-integration lint fmt migrate-up migrate-down openapi-lint smoke

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f

build:
	go build ./...

test:
	go test -race ./...

test-unit:
	go test -race ./...

test-integration:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

migrate-up:
	docker compose run --rm migrate

migrate-down:
	docker compose run --rm migrate down 1

openapi-lint:
	@echo "OpenAPI lint lands with M1-7"

smoke:
	@echo "Smoke test lands with M5-3"
