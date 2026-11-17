# ADR 0003: At-least-once delivery with two-layer idempotency

## Status

Accepted.

## Context

The provider is an external HTTP service. A worker can crash after the provider accepts a message but before the database update commits. The client can retry a request after a timeout. Both cases can create duplicates.

Exactly-once delivery across an external provider is not achievable without the provider's cooperation and a distributed transaction. The honest contract is at-least-once.

## Decision

Use at-least-once delivery and make duplicates unlikely and harmless:

### API layer

- Optional `Idempotency-Key` header on create endpoints.
- Store `(scope, key)` with a SHA-256 hash of the canonical request body.
- Same key and same body: replay the stored response with `Idempotent-Replayed: true`.
- Same key and different body: `422 idempotency_conflict`.
- Request still running: `409 idempotency_in_progress`.
- Keys expire after 24 hours.

### Worker layer

- asynq task ID is the notification ID; the task is unique for 24 hours.
- The status transition is a guarded update. Only one worker can move `queued` to `processing`.
- The provider request includes `Idempotency-Key: <notification_id>`.
- A duplicate task that arrives after `accepted` is ignored.

## Consequences

Positive:

- Client retries are safe.
- Worker retries do not send the same notification twice in normal operation.
- The behaviour is explicit and testable.

Negative:

- A crash in the narrow window between the provider call and the database commit can still produce a duplicate. The provider idempotency header is the only defence.
- Idempotency records add a table and a cleanup job.
- The API key is optional; without it, duplicate client requests are possible.

## Alternatives considered

- **Exactly-once:** rejected as unrealistic across a provider boundary.
- **API-only idempotency:** does not protect against worker retries.
- **Worker-only idempotency:** does not protect against client retries.
- **Provider-side deduplication only:** depends on a provider feature we do not control.
