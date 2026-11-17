# Testing

## 1. Strategy

The test suite has four layers. Each layer has a clear job; we do not duplicate the same assertion at every level.

| Layer | What it proves | Speed | Status |
| --- | --- | --- | --- |
| Unit | Pure rules: validation, state transitions, backoff, queue selection. | Milliseconds | Implemented |
| Contract | Provider adapter behaviour against a controlled HTTP server. | Milliseconds | Implemented |
| Integration | Repositories, worker pipeline, and migrations with real PostgreSQL and Redis. | Seconds | Next |
| Smoke | The composed stack accepts a notification and reaches the provider. | Tens of seconds | Next |

The current suite covers unit, provider contract, worker, service, and API handler tests. Repository integration tests and the Compose smoke target are the next milestone.

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
  - Template fields rejected until the bonus feature lands.
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

Use table-driven tests. Use `t.Parallel()` where the code has no shared state.

## 3. Integration tests (next)

The target integration coverage uses real PostgreSQL and Redis. It does not mock the database. This layer is not implemented yet.

- `internal/notification/repository_test.go`:
  - Insert and read a notification.
  - Guarded transitions affect exactly one row.
  - List filters, ordering, pagination, and total count.
  - Batch insert is atomic.
  - Idempotency row conflict behaviour.
- `internal/api`:
  - `httptest` server with the real router.
  - Create, get, list, cancel, batch.
  - Error shapes and status codes.
  - Request ID echo.
  - Idempotent replay and conflict.
- `internal/delivery`:
  - Enqueue and consume with a fake provider.
  - Retry path with a provider that fails twice then succeeds.
  - Dead-letter path with a permanent failure.
  - Cancellation before the provider call.
  - Rate limiter shared across two consumers.
- `migrations`:
  - Up from an empty database.
  - Down and up again.
  - Constraints reject invalid rows.

Isolation:

- Each test gets a unique schema or database.
- `t.Cleanup` drops it.
- Redis keys use a test prefix and are deleted after the test.
- No test depends on execution order.

Dependencies:

- Locally: `testcontainers-go` starts PostgreSQL and Redis.
- In CI: service containers are provided and the test reads `TEST_DATABASE_URL` and `TEST_REDIS_URL`.
- If the environment variables are absent, the test helper falls back to testcontainers.

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

## 5. Smoke test (next)

The planned smoke target runs against the composed stack and overrides `PROVIDER_BASE_URL` with a local mock provider, so it never depends on the expiring webhook.site URL. Until it lands, use [demo.md](demo.md) for the manual walkthrough.

1. Wait for `/readyz`.
2. Create one SMS notification.
3. Poll `GET /notifications/{id}` until `accepted` or timeout.
4. Create a batch of 10.
5. Poll the batch summary until all notifications are terminal.
6. Assert the queue drains and `/stats` returns numbers.

The script fails with a non-zero exit code and prints the last status and logs on failure.

## 6. Commands

The unit targets are available now. Integration and smoke targets are still in progress.

```bash
# Everything currently implemented
make test

# Unit only
make test-unit

# Race detector
go test -race ./...

# One package
go test ./internal/notification/...

# OpenAPI lint
make openapi-lint
```

Without Make:

```bash
go test ./...
go test -race ./...
npx --yes @redocly/cli@latest lint api/openapi.yaml
```

## 7. CI

`.github/workflows/ci.yml` runs on every push and pull request:

1. `gofmt` check.
2. `go vet`.
3. `go test -race ./...`.
4. `go build ./...`.
5. `golangci-lint`.
6. OpenAPI lint.
7. Docker build for the API/worker image.

Cache Go modules and build cache. Fail fast on formatting and lint.

## 8. Coverage

- Target: 70% or more on `internal/notification` and `internal/delivery`.
- Do not chase 100%. Generated code and `main` packages are excluded.
- Coverage is reported locally, not enforced as a hard gate. A missing test for a core rule is a review blocker.

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
- Long-running soak tests in CI. A local k6 script is a bonus.
