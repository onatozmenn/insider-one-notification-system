# AGENTS.md

This file is the project constitution. It defines what we build, how we build it, and what we refuse to build. If a change conflicts with this file, update this file first and record the reason in `docs/decisions/`.

## 1. Project

**Insider One — Software Engineer Assessment 2026**

Build an event-driven notification system. It accepts notification requests, processes them asynchronously, delivers them through a simulated provider, and exposes status, metrics, and logs.

The reviewers are Insider One engineers. They will run the stack, read the code, and ask us to defend the design. Optimize for clarity and correctness, not feature count.

## 2. Definition of done

A reviewer can:

1. Run `docker compose up` and get a working stack.
2. Create a single notification and a batch of up to 1000.
3. Query by notification ID or batch ID.
4. List notifications with status, channel, and date filters plus pagination.
5. Cancel a pending notification.
6. Watch priority, rate limiting, retries, and idempotency in action.
7. Read `/metrics`, structured logs, `/healthz`, `/readyz`, and the OpenAPI docs.
8. Run the test suite with one command.
9. Follow the commit history and understand how the system was built.

## 3. Hard requirements

From the brief:

- Go 1.2x.
- Public GitHub repository with a clean commit history.
- `docker compose up` starts the system.
- OpenAPI/Swagger documentation.
- Versioned database migrations.
- Test suite runnable with one command.
- Provider simulation: `POST https://webhook.site/{uuid}` with `{ "to", "channel", "content" }`; expects `202` and `{ "messageId", "status", "timestamp" }`.
- Notification API: create, batch up to 1000, status by ID or batch ID, cancel pending, list with filters and pagination.
- Processing: async queue workers, 100 messages/second per channel, priority queues (high/normal/low), content validation, idempotency.
- Observability: metrics endpoint, structured logs with correlation IDs, health check.
- Delivery and retry design is ours.

## 4. Scope

### MVP

- HTTP API with validation and consistent errors.
- PostgreSQL schema and versioned migrations.
- Single and batch creation, status lookup, filtering, pagination, cancellation.
- Redis-backed queue with priority and async workers.
- Per-channel rate limiting at the provider boundary.
- Idempotency at the API and worker levels.
- Provider adapter with retry policy, backoff, and dead-letter handling.
- Metrics, structured logs, correlation IDs, health and readiness checks.
- `docker compose up`, OpenAPI spec, Swagger UI, README, tests, CI.

### Bonus (only after the MVP is green)

- Scheduled notifications.
- Template system with variable substitution.
- WebSocket status updates.
- Distributed tracing with OpenTelemetry and Jaeger.
- Dead-letter inspection and manual retry.
- Load test evidence with k6.
- Optional Prometheus/Grafana profile.

### Non-goals

- Microservices, Kubernetes, service mesh.
- A custom queue or scheduler. Use proven libraries.
- Exactly-once delivery. The contract is at-least-once.
- Real SMS, email, or push providers.
- A frontend dashboard. Swagger UI and metrics are enough.
- Authentication and multi-tenancy. They are not in the brief.
- Premature abstractions. No interface until there is a second implementation or a test seam.

## 5. Architecture

**Modular monolith, one codebase, two processes.**

- One Go module.
- `cmd/api`, `cmd/worker`, and `cmd/migrate` are thin entrypoints.
- `internal/` holds the domain and adapters.
- The API and worker share code but not memory. They communicate through PostgreSQL and Redis.
- A module can become a separate service later because its boundary is already explicit.

### Runtime topology

| Service | Responsibility |
| --- | --- |
| `gateway` | nginx reverse proxy. Single entry point for API, docs, metrics, health, WebSocket. |
| `api` | HTTP API, validation, persistence, enqueue, status queries, metrics, WebSocket hub. |
| `worker` | Queue consumer, rate limiting, provider calls, retries, status updates, event publishing. |
| `postgres` | Source of truth. |
| `redis` | Queue, rate limiter, pub/sub for real-time updates. |
| `migrate` | One-shot migration job. |
| `swagger-ui` | Serves the OpenAPI spec. |

The API and worker run from the same image with different commands. This is still a monolith: one codebase, one release artifact, clear module boundaries.

### Modules

| Package | Responsibility |
| --- | --- |
| `internal/notification` | Domain types, validation, state machine, service, repository interfaces. |
| `internal/delivery` | Enqueue, worker, priority, rate limiter, retry policy. |
| `internal/provider` | HTTP provider adapter and test double. |
| `internal/template` | Template rendering. Bonus. |
| `internal/realtime` | WebSocket hub and Redis pub/sub. Bonus. |
| `internal/platform` | Config, logging, metrics, PostgreSQL, Redis, HTTP server, shutdown. |

### Dependency rule

- Domain packages do not import HTTP, Redis, or SQL code.
- Adapters depend on the domain, not the other way around.
- Cross-module calls go through exported interfaces or application services, never through another module's tables.
- `platform` contains infrastructure only. No business rules.

### Request flow

1. API validates the request and writes the notification plus a status event in one transaction.
2. API enqueues an asynq task with the notification ID and correlation ID.
3. Worker claims the row with a guarded update (`queued` to `processing`).
4. Worker waits for the per-channel rate limiter.
5. Worker calls the provider.
6. Worker records the result, schedules a retry, or marks a terminal state.
7. API serves status. WebSocket publishes the new state when enabled.

## 6. Technology choices

| Concern | Choice | Why |
| --- | --- | --- |
| Language | Go 1.26 | Matches the brief (1.2x). Pin the toolchain in `go.mod` and Dockerfile. |
| HTTP router | `chi` | Small, idiomatic, good middleware support. |
| Database | PostgreSQL 16 + `pgx/v5` | Transactions, constraints, indexes, JSON when useful. |
| Queue | `asynq` | Redis-backed, priority queues, retries, scheduling, unique tasks. |
| Rate limiting | `redis_rate` | GCRA in Redis. Correct across worker replicas. |
| Migrations | `golang-migrate` | Versioned SQL files, embedded in the binary, run by `migrate`. |
| Logging | `log/slog` | Standard library, JSON output, no extra dependency. |
| Metrics | Prometheus client | Standard format, easy to scrape. |
| Config | Environment variables | Twelve-factor, validated at startup. |
| WebSocket | `github.com/coder/websocket` | Maintained, simple API. Bonus. |
| Tracing | OpenTelemetry + Jaeger | W3C trace context, OTLP export. Bonus. |
| Tests | `testing` + `testcontainers-go` | Real PostgreSQL and Redis in integration tests. |
| API spec | OpenAPI 3.1 + Swagger UI | Hand-written spec, linted in CI. |
| Gateway | nginx | Predictable, easy to read, stable. |
| Container | Multi-stage, non-root | Small image, clear build. |

Every choice is mature and boring. The assessment is about design and reliability, not about reinventing infrastructure.

## 7. Domain rules

### Statuses

| Status | Meaning | Terminal |
| --- | --- | --- |
| `pending` | Created, not yet queued. | No |
| `scheduled` | Waiting for `scheduled_at`. Bonus. | No |
| `queued` | In Redis, waiting for a worker. | No |
| `processing` | Claimed by a worker, provider call in progress. | No |
| `accepted` | Provider returned 202. | Yes |
| `failed` | Permanent error. | Yes |
| `cancelled` | Cancelled before processing. | Yes |
| `dead_letter` | Retries exhausted. Needs attention. | Yes |
| `delivered` | Delivery receipt received. Bonus. | Yes |

`accepted` means the provider accepted the message. We do not claim delivery without a receipt.

### Transitions

- `pending` to `queued` on enqueue.
- `pending` or `scheduled` to `cancelled` on cancel.
- `scheduled` to `processing` when the scheduled task fires and a worker claims it.
- `queued` to `processing` when a worker claims the row.
- `processing` to `accepted` on provider 202.
- `processing` to `queued` on a transient error with retries left.
- `processing` to `failed` on a permanent error.
- `processing` to `dead_letter` when retries are exhausted.
- Every transition is a guarded SQL update. If zero rows change, the worker stops.

### Channels and limits

| Channel | Recipient | Required fields | Limits |
| --- | --- | --- | --- |
| `sms` | E.164, for example `+905551234567` | `content` | 160 characters |
| `email` | Basic RFC 5322 check | `subject`, `content` | subject 200, content 50,000 characters |
| `push` | Non-empty device token, max 512 | `content` | title 100, content 500 characters |

Priority is `high`, `normal`, or `low`. Default is `normal`.

A batch has at most 1000 notifications. Validation is all-or-nothing: if one item is invalid, the whole batch is rejected with item-level errors. This keeps retries and idempotency simple.

### Idempotency

Two layers:

1. API: optional `Idempotency-Key` header. The key and a hash of the request body are stored. A replay with the same body returns the original response and `Idempotent-Replayed: true`. A replay with a different body returns `422`.
2. Worker: the asynq task ID is the notification ID, and the task is unique for a fixed TTL. The database state transition is the final guard. A notification can only move from `queued` to `processing` once.

### Retries

- Transient: 408, 425, 429, 5xx, timeouts, connection errors.
- Permanent: other 4xx, invalid payload, unsupported channel.
- Backoff: exponential with full jitter. Base 1s, factor 2, cap 30s.
- Maximum attempts: 5 total, configurable.
- Respect `Retry-After` when the provider sends it.
- Exhausted retries move the notification to `dead_letter` and emit a metric.
- Provider timeout: 10 seconds per attempt.

### Rate limiting

- 100 provider requests per second per channel.
- Shared across worker replicas through Redis.
- Workers wait for a token; they do not drop work.
- Record `rate_limit_wait_seconds` so the cost is visible.

### Cancellation

Only `pending` and `scheduled` notifications can be cancelled. Anything else returns `409 Conflict`. Workers re-check the status before calling the provider to close the race.

### Delivery contract

At-least-once. We prevent duplicate sends with idempotency keys and state guards. We do not promise exactly-once.

## 8. API rules

- Base path: `/api/v1`.
- JSON only. Timestamps are RFC3339 in UTC.
- IDs are UUIDv7, generated by the server.
- Every request gets an `X-Request-ID`. If the client sends one, we keep it.
- Errors use one shape:
  `{"error":{"code":"validation_error","message":"...","details":[{"field":"recipient","reason":"..."}]}}`
- Create returns `202 Accepted` with a `Location` header.
- Batch create returns `202 Accepted` with `batch_id`, `total`, and a status URL.
- List supports `status`, `channel`, `batch_id`, `from`, `to`, `page`, and `page_size`.
- `page_size` defaults to 20 and is capped at 100.
- OpenAPI is the contract. Handlers must match it.

## 9. Observability rules

- Metrics:
  - `notification_created_total{channel,priority}`
  - `notification_status_total{channel,status}`
  - `notification_processing_duration_seconds{channel,status}`
  - `notification_queue_wait_seconds{priority,channel}`
  - `provider_request_total{channel,result}`
  - `provider_request_duration_seconds{channel}`
  - `rate_limit_wait_seconds{channel}`
  - `queue_depth{queue}`
  - `http_requests_total{method,route,status}`
  - `http_request_duration_seconds{method,route}`
- `/api/v1/stats` returns queue depth, recent success/failure counts, success rate, and p95 latency in JSON.
- Logs are JSON, one line per event. Include `request_id`, `notification_id`, `batch_id`, `channel`, `attempt`, `duration_ms`, and `error`.
- Mask recipients in logs. Never log full phone numbers, emails, or device tokens.
- `/healthz` checks the process. `/readyz` checks PostgreSQL and Redis.
- Queue depth is polled from asynq Inspector every 5 seconds.
- Traces propagate W3C `traceparent`. Bonus.

## 10. Testing rules

- Unit tests: validation, retry classification, backoff, idempotency, template rendering, rate limiter.
- Integration tests: repositories, API handlers, migrations, worker pipeline.
- Provider tests: `httptest` server returning 202, 400, 429, 500, and timeouts.
- Race detector in CI: `go test -race ./...`.
- `make test` runs everything. Integration tests use testcontainers locally and the compose services in CI.
- Coverage target: 70%+ on domain packages. Do not chase 100%.
- Every bug fix gets a regression test.

## 11. Code rules

- `gofmt`, `goimports`, `golangci-lint`.
- Context first. No `context.TODO()` in production paths.
- Wrap errors with `%w`. Map domain errors to HTTP codes in one place.
- Small interfaces at the consumer side.
- No global state except immutable config and metric registration.
- SQL is explicit and lives in repository files. No ORM.
- Comments explain why, not what. Exported identifiers have doc comments.
- Keep files under roughly 400 lines where practical.
- No commented-out code. No TODOs without an issue.
- English everywhere: code, comments, docs, commits, PRs.

## 12. Git and GitHub

- Conventional Commits: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`.
- One issue per meaningful change. Every PR links its issue with `Closes #12`.
- Branch names: `feat/notification-api`, `fix/retry-classification`, `docs/architecture`.
- Small PRs. Include what changed, how it was tested, and evidence when useful.
- CI must pass before merge. Squash merge keeps `main` clean.
- CodeRabbit and cubic review pull requests. Address comments or explain why not.
- No secrets in the repo. `.env.example` only.
- `main` is always runnable.

## 13. Security and data

- No real personal data. Use fake recipients in demos and tests.
- The webhook.site URL is public. Treat everything sent to it as public.
- Validate input, limit body size, set timeouts, and parameterize queries.
- Run containers as non-root.
- Do not commit `.env`, credentials, or provider secrets.

## 14. Working agreement

- Docs first, then code. Architecture decisions go to `docs/decisions/`.
- MVP before bonuses. No bonus work while an MVP item is red.
- Prefer boring, proven solutions.
- Write the smallest failing test before fixing a bug.
- Keep the reviewer in mind: clear names, clear errors, clear README.
- If a decision is not in this file, write an ADR before changing the architecture.

## 15. Open decisions

- [x] Repository name: `insider-one-notification-system`.
- [ ] Optional Prometheus/Grafana profile. Not required by the brief.
- [ ] Load test with k6. Useful evidence, not required.
