# ADR 0006: Recovery sweep and a separate transport retry budget

## Status

Accepted.

## Context

The MVP used a single reconciler query for `pending` rows. That covered a crash between the database commit and the first enqueue, but four gaps remained:

- A scheduled row whose initial enqueue failed stayed `scheduled` forever; the reconciler only read `pending`.
- Redis or database errors were returned to asynq. If they consumed the asynq retry budget, the task was archived while the row stayed `queued`; the reconciler did not read `queued`.
- `Claim` incremented `attempt_count` when reclaiming a stale `processing` row. At `attempt_count = max_attempts`, the database constraint rejected the update, leaving the row stuck in `processing`.
- Expired idempotency rows were ignored by reads but still held the primary key. A key could not be reused after its 24-hour TTL, and expired rows were never deleted.

Together these meant the documented at-least-once recovery did not cover every state where the Redis task and the PostgreSQL row could disagree.

## Decision

- The reconciler selects three classes of rows every 30 seconds:
  - `pending` older than one minute;
  - `scheduled` whose `scheduled_at` is due;
  - `queued` whose `updated_at` is older than 15 minutes. A partial index on `(updated_at) WHERE status = 'queued'` supports this scan.
- Re-enqueue uses the notification ID as the asynq task ID. Duplicates are collapsed while the task exists, and the database state guards remain the final authority. Pending rows are marked `queued` after a successful enqueue; due scheduled and stale queued rows keep their status.
- asynq transport retries are separate from provider attempts. `TransportMaxRetries` is 100 for infrastructure failures; provider attempts are still bounded by `notifications.attempt_count` and `max_attempts`. Backoff uses the database attempt count, not the asynq retry count.
- A stale `processing` row at `max_attempts` is moved to `dead_letter` with `last_error = "processing lease expired after final attempt"`. `Claim` never reclaims a stale row at the attempt limit.
- Expired idempotency rows are deleted when read, and an hourly API cleanup removes the rest. Reusing a key after 24 hours now creates a new resource.
- The delivery contract remains at-least-once. A provider call that was in flight during a crash can still be duplicated.

## Consequences

Positive:

- A lost task, a failed initial enqueue, or a Redis flush no longer leaves a row permanently stuck.
- Infrastructure failures do not consume the provider retry budget.
- A worker crash on the final attempt has a terminal outcome instead of violating a constraint.
- Idempotency TTL behavior matches the documentation.

Negative:

- The reconciler can enqueue a duplicate task while the original task is still active or waiting. The DB guards make this harmless for delivery but can create extra asynq work.
- The 15-minute queued threshold is a heuristic. A very slow rate-limit backlog may trigger redundant enqueues; the unique task lock collapses most of them.
- This is still not a transactional outbox. A durable outbox would make the database commit and queue publication atomic at the cost of a dispatcher and another table.

## Alternatives considered

- **Only mark failed scheduled rows as `pending`:** rejected because a pending row with a future `scheduled_at` would be delivered immediately by the worker.
- **Use the asynq retry budget for everything:** rejected because infrastructure errors and provider errors have different retry semantics.
- **Allow `attempt_count > max_attempts` for stale claims:** rejected because it weakens the invariant and hides the exhausted state.
- **Keep expired idempotency rows and delete on insert conflict:** rejected because the read path would still return `not found` and the conflict path would need a second special case. Deleting on read is simpler.
- **A full outbox:** stronger, but a larger change than this assessment needs. The broader reconciler makes the existing trade-off explicit and testable.
