# Roadmap

## 1. Rules

- MVP before bonuses. No bonus work while an MVP item is red.
- One issue per meaningful change. One pull request per issue.
- Every PR must keep `main` runnable.
- The definition of done in [AGENTS.md](../AGENTS.md) applies to every item.

### Current status (2026-11-17)

- M0 complete.
- M1 complete, including OpenAPI and Swagger UI.
- M2 complete.
- M3 complete: idempotency, cancellation races, failure injection, stale claims, and repository integration tests are implemented.
- M4 complete.
- M5 complete: Compose, README, unit/integration tests, CI, OpenAPI validation, and the v1.0.0 release are in place. `make test` runs unit, integration, and coverage; `make smoke` checks the composed stack.
- M6 complete: scheduled notifications, templates, WebSocket updates, distributed tracing, dead-letter retry, the k6 load test, and the Prometheus/Grafana profile are implemented.

## 2. Milestones

### M0 — Foundation

| Issue | Title | Acceptance |
| --- | --- | --- |
| M0-1 | Project constitution and docs | `AGENTS.md`, `docs/`, `CONTRIBUTING.md`, templates. |
| M0-2 | Go module and command skeleton | `cmd/api`, `cmd/worker`, `cmd/migrate` build. |
| M0-3 | Config, logging, health | Env config, JSON logs, `/healthz`, `/readyz`. |
| M0-4 | Compose skeleton | PostgreSQL, Redis, API, worker, migrate, gateway start. |
| M0-5 | CI pipeline | Format, lint, vet, unit tests, build. |

### M1 — API and persistence

| Issue | Title | Acceptance |
| --- | --- | --- |
| M1-1 | Database schema and migrations | Tables, constraints, indexes, up/down. |
| M1-2 | Domain model and validation | Channel rules, batch limits, typed errors. |
| M1-3 | Create and persist a single notification | 202, transaction, event row, API idempotency. Enqueue arrives in M2-1. |
| M1-4 | Get and list notifications | Filters, pagination, stable order. |
| M1-5 | Cancel notification | 200 for pending/scheduled, 409 otherwise. |
| M1-6 | Batch create and summary | Up to 1000, atomic validation, derived status. |
| M1-7 | OpenAPI and Swagger UI | Spec matches handlers, docs served. |

### M2 — Processing

| Issue | Title | Acceptance |
| --- | --- | --- |
| M2-1 | Queue client and enqueue | asynq client, task payload, task IDs, move to `queued`. |
| M2-2 | Worker and state guards | Claim, transition, event, ack. |
| M2-3 | Provider adapter | 202 success, typed errors, timeout. |
| M2-4 | Priority queues | `high`, `normal`, `low` with weights. |
| M2-5 | Per-channel rate limiting | 100/s shared through Redis, burst 1. |
| M2-6 | Retry and dead letter | Classification, backoff, max attempts. |
| M2-7 | Reconciler | Pending, due scheduled, and stale queued rows enqueued after a crash or Redis outage. |

### M3 — Reliability

| Issue | Title | Acceptance |
| --- | --- | --- |
| M3-1 | Worker idempotency | Unique task, guarded transition, duplicate test. |
| M3-2 | Cancellation races | Worker re-checks status before the provider call. |
| M3-3 | Failure injection tests | Provider 400, 429, 500, timeout, duplicate task. |
| M3-4 | Stale claim recovery | A crashed worker's `processing` row is reclaimed after the lease. |

### M4 — Observability

| Issue | Title | Acceptance |
| --- | --- | --- |
| M4-1 | Prometheus metrics | Counters, histograms, gauges, bounded labels. |
| M4-2 | Stats endpoint | Queue depth, counts, success rate, p95. |
| M4-3 | Structured logs and correlation IDs | Request ID end to end, masked recipients. |
| M4-4 | Health and queue depth | Readiness checks, 5-second inspector poll. |

### M5 — Delivery

| Issue | Title | Acceptance |
| --- | --- | --- |
| M5-1 | Full Docker Compose | One command starts the stack; healthchecks pass. |
| M5-2 | README and examples | Quickstart, architecture, curl examples. |
| M5-3 | Test suite and CI | `make test`, race detector, integration services. |
| M5-4 | OpenAPI validation | Lint in CI, Swagger UI works. |
| M5-5 | Release v1.0.0 | Tag, release notes, demo evidence. |

### M6 — Bonuses

| Issue | Title | Acceptance |
| --- | --- | --- |
| M6-1 | Scheduled notifications | `scheduled_at`, asynq `ProcessAt`, cancel. |
| M6-2 | Templates | Create/list, variable substitution, missing variable error. |
| M6-3 | WebSocket updates | Snapshot + live status events. |
| M6-4 | Distributed tracing | OTel spans, Jaeger profile. |
| M6-5 | Dead-letter retry | API endpoint resets and re-enqueues. |
| M6-6 | Load test | k6 script, 100/s per channel evidence. |
| M6-7 | Metrics profile | Prometheus + Grafana dashboard. |

## 3. Commit plan

Use Conventional Commits. Examples:

```text
docs: add project constitution and design docs
chore: scaffold Go module and service commands
feat: add PostgreSQL schema and migrations
feat: add notification validation and creation API
feat: add notification queries and cancellation
feat: add batch creation and batch summary
feat: add asynq queue and worker pipeline
feat: add provider adapter with retry classification
feat: add per-channel rate limiting
feat: add idempotency keys and replay handling
feat: add Prometheus metrics and stats endpoint
feat: add message templates and dead-letter retry
feat: stream status updates over WebSocket
feat: add OpenTelemetry tracing and Jaeger profile
feat: add k6 load test and Prometheus/Grafana profile
test: add integration tests for worker pipeline
ci: add lint, test, and build workflow
docs: add README quickstart and API examples
chore: release v1.0.0
```

Each commit should compile. Do not mix refactors with features.

## 4. Pull request order

1. Docs and constitution.
2. Module skeleton and config.
3. Database schema and migrations.
4. Domain validation.
5. Create and query API.
6. Batch and cancel.
7. Queue and worker.
8. Provider and retries.
9. Rate limiting.
10. Idempotency.
11. Observability.
12. Docker Compose and README.
13. Tests and CI hardening.
14. Bonuses in the order above.

## 5. Timeline

| Day | Focus |
| --- | --- |
| Day 1 | Docs, repo, issues, skeleton, schema, create/query API. |
| Day 2 | Queue, worker, provider, rate limit, retry, idempotency, observability. |
| Day 3 | Compose, OpenAPI, tests, README, bonuses, release, final review. |

The assessment suggests 12 hours of work. The three-day window includes review and buffer time. If time runs short, stop at a green MVP and document the remaining bonuses as future work.

## 6. Review checklist

- Does the PR close an issue?
- Does it keep `main` runnable?
- Are tests included?
- Are metrics and logs added for new behaviour?
- Is the OpenAPI spec updated?
- Is the README still accurate?
- Are errors mapped to the right HTTP status?
- Are recipients masked in logs?
- Is there any new secret or personal data?
- Can the design be explained in one minute?
