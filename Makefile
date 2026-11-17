.PHONY: up down logs build test test-unit test-integration lint fmt migrate-up migrate-down migrate-version openapi-lint smoke

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
	@echo "Integration tests are planned; run 'make test' for the current suite."

lint:
	golangci-lint run

fmt:
	gofmt -w .

migrate-up:
	docker compose run --rm --entrypoint /app/migrate migrate up

migrate-down:
	docker compose run --rm --entrypoint /app/migrate migrate down 1

migrate-version:
	docker compose run --rm --entrypoint /app/migrate migrate version

openapi-lint:
	npx --yes @redocly/cli@latest lint api/openapi.yaml

smoke:
	@echo "Smoke test is planned; use docs/demo.md for a manual walkthrough."
