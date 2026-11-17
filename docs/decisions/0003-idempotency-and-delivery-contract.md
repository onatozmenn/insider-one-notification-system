# ADR 0003: At-least-once delivery with two-layer idempotency

## Status

Accepted.

## Context

The provider is an external HTTP service. A worker can crash after the provider accepts a message but before the database update commits. The client can retry a request after a timeout. Both cases can create duplicates.

Exactly-once delivery across an external provider is not achievable without the provider's cooperation and a distributed transaction. The honest contract is at-least-once while PostgreSQL holds the work and Redis holds the task. A Redis flush can drop queued tasks; the reconciler only recovers `pending` rows.

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

- asynq task ID is the notification ID; the task is unique while it is queued (24-hour TTL). The unique lock is not a permanent tombstone; after processing, the database status guard is the final defense. Manual retries use a fresh task ID (`<notification_id>:retry:<n>`).
- The status transition is a guarded update. Only one worker can claim a row for a given attempt.
- The claim accepts `pending`, `queued`, a due `scheduled`, or a stale `processing` row. The stale lease is longer than the provider timeout.
- The provider request includes `Idempotency-Key: <notification_id>`.
- A duplicate task that arrives after `accepted` is ignored. A duplicate task that arrives while the row is `processing` waits for the lease to expire, then may retry.

## Consequences

Positive:

- Client retries are safe.
- Worker retries do not send the same notification twice in normal operation.
- The behaviour is explicit and testable.

Negative:

- A crash in the narrow window between the provider call and the database commit can still produce a duplicate. The provider idempotency header is the only defence.
- A stale-lease recovery can duplicate a call if the original worker is alive but stalled. The lease is longer than the provider timeout to make this rare.
- A Redis flush can drop queued tasks; the reconciler cannot recover them.
- Idempotency records add a table and a cleanup job.
- TaskID uniqueness only covers queued tasks; it is not a 24-hour deduplication window after completion.
- The API key is optional; without it, duplicate client requests are possible.

## Alternatives considered

- **Exactly-once:** rejected as unrealistic across a provider boundary.
- **API-only idempotency:** does not protect against worker retries.
- **Worker-only idempotency:** does not protect against client retries.
- **Provider-side deduplication only:** depends on a provider feature we do not control.
