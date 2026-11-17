# ADR 0005: Bonus features without changing the core architecture

## Status

Accepted.

## Context

The MVP is a modular monolith with PostgreSQL as the source of truth and Redis for the queue, rate limiter, and pub/sub. The bonus features add templates, WebSocket updates, distributed tracing, dead-letter retry, a load test, and a metrics profile. Each could be built as a separate service, a new datastore, or a scheduled job. The assessment values design clarity and a runnable stack more than feature count.

## Decision

- **Templates** live in the notification domain (`internal/notification/template.go`) and in a `templates` table added by migration `000002`. Rendering happens in the API before validation, so the worker only ever sees a normal notification row. There is no template reference on the notification, which keeps the delivery path unchanged.
- **WebSocket updates** use Redis pub/sub channel `notification.events`. The worker publishes after each delivery transition; the API hub keeps in-memory subscribers and filters by notification ID. A 2-second database poll backs up the subscription when pub/sub is unavailable or a transition is missed. Frames are best-effort with no replay buffer. A client that misses a frame re-reads the notification over HTTP. This keeps the API stateless and lets any replica serve a connection.
- **Distributed tracing** uses OpenTelemetry with OTLP HTTP. The API starts a server span, the enqueue step injects `traceparent` into the asynq payload, the worker extracts it, and the provider client injects it into the outgoing request headers. Tracing is enabled only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Jaeger is an optional Compose profile, not a dependency.
- **Dead-letter retry** resets a `failed` or `dead_letter` row and enqueues a fresh task with a new task ID. The original asynq archive entry is left untouched. The database status guard remains the final authority.
- **Load testing** is a standalone k6 script against the public API. It is not part of CI, because it needs a running stack and a provider endpoint.
- **Prometheus and Grafana** are optional Compose profiles. Prometheus scrapes the API and worker metrics endpoints; Grafana is provisioned from a checked-in dashboard. Neither is required by `docker compose up`.

## Consequences

Positive:

- The API and worker remain the only application processes.
- Templates and retries reuse existing validation, idempotency, and state guards.
- Telemetry and dashboards can be turned on without changing application code.
- The gateway remains the single entry point, including WebSocket upgrades.

Negative:

- WebSocket events can still be missed while the client is disconnected. The 2-second poll recovers transitions while connected, but there is no replay buffer.
- Tracing adds a dependency tree to the binary even when disabled (it is a no-op exporter).
- Template changes are not versioned; a notification stores the rendered output, so history is preserved but templates cannot be re-rendered later.
- The Grafana dashboard is a demo view, not a production SLO dashboard.

## Alternatives considered

- **A separate template or realtime service:** rejected for the assessment; it adds deployment and network failure modes without proving anything new.
- **Persisting WebSocket events for replay:** requires a new event stream or table and an offset protocol. Overkill for status updates that can be re-read from the notification row.
- **Embedding Jaeger or Prometheus in the default Compose stack:** rejected because `docker compose up` should stay small and fast.
- **Manual SQL for retries:** rejected because it bypasses the API contract and the state guards.
