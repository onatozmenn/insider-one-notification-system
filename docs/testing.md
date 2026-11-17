# Testing

## 1. Strategy

The test suite has five layers. Each layer has a clear job; we do not duplicate the same assertion at every level.

| Layer | What it proves | Speed | Status |
| --- | --- | --- | --- |
| Unit | Pure rules: validation, state transitions, backoff, queue selection, template rendering. | Milliseconds | Implemented |
| Contract | Provider adapter behaviour against a controlled HTTP server. | Milliseconds | Implemented |
| Realtime | WebSocket handshake, snapshot frame, live status frame. | Milliseconds | Implemented |
| Integration | Repositories, migrations, queue, reconciler, and Redis pub/sub with real services. | Seconds | Implemented |
| Smoke | The composed stack creates and accepts a notification and a batch. | Tens of seconds | Implemented |

The current suite covers unit, provider contract, worker, service, API handler, WebSocket, tracing endpoint, repository, queue, reconciler, and Redis pub/sub tests. Repository integration tests and the Compose smoke target run with one command.

## 2. Unit tests

Packages and cases:

- `internal/notification`:
  - Valid SMS, email, and push payloads.
  - Missing required fields.
  - Content limits per channel.
  - Recipient formats.
  - Priority defaults and invalid values.
  - Batch size 0, 1, 1000, 1001.
  - Scheduled time in the past and too far ahead.
  - Template validation, rendering, missing variables, and channel limits.
  - Manual retry rules for `failed` and `dead_letter`.
  - Idempotency key replay, in-progress recovery, and conflict decisions.
- `internal/delivery`:
  - Status code classification: 202, 400, 408, 425, 429, 500, 503.
  - Network error classification.
  - Backoff bounds and jitter range.
  - `Retry-After` parsing.
  - Max attempts.
  - Queue selection by priority.
  - Rate limiter wait and cancellation decisions with a fake limiter.
- `internal/platform`:
  - Config validation and defaults.
  - Correlation ID generation and propagation.
  - OTLP endpoint normalization.
- `internal/realtime`:
  - Broadcast by notification ID.
  - Unsubscribe.
- `internal/api` WebSocket:
  - Snapshot frame on connect.
  - Live status frame.
  - Upgrade through the logging middleware.

Use table-driven tests. Use `t.Parallel()` where the code has no shared state.

## 3. Integration tests

Integration tests use real PostgreSQL and Redis. They are behind the `integration` build tag so `go test ./...` stays fast and dependency-free.

- `internal/notification/repository_integration_test.go`:
  - Insert and read a notification, with its first event.
  - Guarded transitions affect exactly one row and reject stale writes.
  - List filters, ordering, pagination, and total count.
  - Batch insert and rollback when one item fails.
  - Idempotency key conflict behaviour.
  - Templates, duplicate names, and the email-subject database constraint.
  - Retry and cancellation guards.
  - Reconcilable pending, due scheduled, and stale queued rows.
  - Stale processing at `max_attempts` moves to `dead_letter`.
  - Expired idempotency keys are deleted and can be reused.
- `internal/delivery/delivery_integration_test.go`:
  - Manual retries are not collapsed by the asynq uniqueness lock.
  - The reconciler enqueues old pending, due scheduled, and stale queued rows.
  - Queue depth polling.
  - Worker start and shutdown against Redis.
- `internal/realtime/hub_integration_test.go`:
  - A published Redis event reaches the WebSocket hub subscriber.

Isolation:

- Each test package creates its own PostgreSQL database and Redis instance in `TestMain`, so packages can run in parallel.
- Migrations run from the embedded `migrations.FS`.
- Every test truncates the application tables and flushes Redis before it runs.
- No test depends on execution order.

Dependencies:

- Locally: `testcontainers-go` starts `postgres:16-alpine` and `redis:7-alpine`.
- In CI: service containers are provided; the helper reads `TEST_DATABASE_URL` and `TEST_REDIS_ADDR`.
- If the environment variables are absent, the helper falls back to testcontainers.
- Docker must be running for local integration tests.

## 4. Contract tests

The provider adapter is tested with `httptest.Server`:

| Server behaviour | Expected adapter result |
| --- | --- |
| `202` with the expected JSON | Success, `provider_message_id` captured |
| `400` | Permanent error |
| `429` with `Retry-After: 2` | Transient error, retry delay 2 seconds |
| `500` | Transient error |
| Sleep longer than the client timeout | Transient timeout error |
| Invalid JSON with `202` | Success with an empty message ID, logged |

The real webhook.site endpoint is never used in tests. It is a demo dependency, not a test dependency.

## 5. Smoke test

`make smoke` runs `scripts/smoke` against a running stack. It expects a working provider endpoint: webhook.site or a local mock. Set `BASE_URL` when the gateway is not on `http://localhost:8080`.

1. Wait for `/readyz`.
2. Create one SMS notification.
3. Poll `GET /notifications/{id}` until `accepted` or timeout.
4. Create a batch of three.
5. Poll the batch summary until `completed` and all three are accepted.
6. Call `/api/v1/stats` and require `200` with counts.

The command exits non-zero and prints the failing status on error. The smoke target is not part of CI because it needs the composed stack; the unit and integration layers cover the same code paths in CI.

## 6. Commands

`make test` runs unit, integration, and coverage. Docker must be running for the integration tests if `TEST_DATABASE_URL` and `TEST_REDIS_ADDR` are not set.

```bash
# Unit, integration, and coverage
make test

# Unit only
make test-unit

# Integration only (testcontainers locally)
make test-integration

# Coverage for the domain packages, including integration tests
make coverage

# Race detector for everything
go test -race -tags=integration ./...

# One package
go test -tags=integration ./internal/notification/...

# Smoke a running stack
BASE_URL=http://localhost:8080 make smoke

# OpenAPI lint
make openapi-lint

# k6 load test, with the stack running
BASE_URL=http://localhost:8080 make load-test
```

Without Make:

```bash
go test ./...
go test -race -tags=integration ./...
go test -coverprofile=coverage.out -tags=integration ./internal/notification/... ./internal/delivery/...
go tool cover -func=coverage.out
BASE_URL=http://localhost:8080 go run ./scripts/smoke
npx --yes @redocly/cli@latest lint api/openapi.yaml
```

## 7. CI

`.github/workflows/ci.yml` runs on every push and pull request.

The `test` job has PostgreSQL 16 and Redis 7 service containers and sets `TEST_DATABASE_URL` and `TEST_REDIS_ADDR`:

1. `gofmt` check.
2. `go vet`.
3. `go test -race ./...`.
4. `go test -race -tags=integration ./...`.
5. Coverage for `internal/notification` and `internal/delivery`, printed to the log and uploaded as an artifact.
6. `go build ./...`.

The other jobs run `golangci-lint`, OpenAPI lint, Compose validation, and the Docker build. Cache Go modules and build cache. Fail fast on formatting and lint.

## 8. Coverage

- Target: 70% or more on `internal/notification` and `internal/delivery`.
- Current local result with the integration tag: `internal/notification` 76.1%, `internal/delivery` 75.2%, combined 75.8%.
- `make coverage` prints the per-function report and writes `coverage.out`.
- Do not chase 100%. Generated code and `main` packages are excluded.
- Coverage is reported, not enforced as a hard gate. A missing test for a core rule is a review blocker.

## 9. Test data

- Phone numbers use the non-routable fiction range `+15555550100`.
- Emails use `example.com`.
- Device tokens are random strings.
- Never use real personal data.
- The webhook.site URL is public; treat every payload as public.

## 10. What we do not test

- Real SMS, email, or push providers.
- webhook.site availability.
- Browser UI. The API and Swagger UI are the interface.
- Long-running soak tests in CI. A local k6 script is provided for load evidence.
