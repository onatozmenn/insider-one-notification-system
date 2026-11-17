.PHONY: up down logs build test test-unit test-integration coverage lint fmt migrate-up migrate-down migrate-version openapi-lint smoke load-test

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f

build:
	go build ./...

test: test-unit test-integration coverage

test-unit:
	go test -race ./...

test-integration:
	go test -race -tags=integration ./...

coverage:
	go test -tags=integration -coverprofile=coverage.out ./internal/notification/... ./internal/delivery/...
	go tool cover -func=coverage.out

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
	go run ./scripts/smoke

load-test:
	k6 run scripts/load-test.js
