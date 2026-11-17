# Reliability

## 1. Delivery contract

The system is **at-least-once while PostgreSQL has accepted the work and Redis still holds the task**. A notification may be sent more than once if a worker crashes after the provider accepted it and before the database update commits. We reduce duplicate sends with two layers of idempotency, but we do not claim exactly-once delivery.

A Redis flush or data-loss event can drop `queued` tasks. The reconciler only recovers `pending` rows; it cannot reconstruct tasks that were only in Redis. Production would use a transactional outbox or a durable queue with stronger guarantees. This limitation is deliberate for the assessment.

Ordering is not guaranteed. A batch may finish out of order.

## 2. Queue design

- Queue: asynq on Redis.
- Queues: `high`, `normal`, `low`.
- Weights: `high` 6, `normal` 3, `low` 1.
- Worker concurrency: 50 by default, configurable.

Why weighted queues instead of strict priority: strict priority starves `low` during a burst. Weighted scheduling delays low-priority work without letting it disappear. The trade-off is that a high-priority message can wait behind a small amount of normal traffic.

Task payload:

```json
{
  "notification_id": "0193b2f4-7c1a-7d3e-9a10-5f8c2d6e4b71",
  "correlation_id": "req_01J..."
}
```

The task ID is the notification ID. The task is unique while it is queued (24-hour TTL). The unique lock is not a permanent tombstone: after the task completes, a re-enqueue with the same ID can succeed, but the worker sees the terminal status and stops. The database state guard is the final defense. Manual retries use a fresh task ID (`<notification_id>:retry:<n>`) so the uniqueness window does not block an operator retry.

## 3. Worker lifecycle

1. Dequeue a task.
2. Read the row status without claiming. If it is terminal or `cancelled`, stop.
3. Wait for a channel token from the rate limiter. The row stays `queued` during this wait, so the processing lease is not running.
4. Claim the row with a guarded update. It accepts `pending`, `queued`, a due `scheduled`, or a stale `processing` row and moves it to `processing`. The same update increments `attempt_count` and sets `updated_at`.
5. If no row changes, stop. Another worker, a cancellation, or a terminal state won.
6. Call the provider with a 10-second timeout.
7. Record the result:
   - `202`: `processing` to `accepted`.
   - Transient error with retries left: `processing` to `queued`, set `next_attempt_at`.
   - Permanent error: `processing` to `failed`.
   - Last attempt with a transient error: `processing` to `dead_letter`.
8. Insert a `notification_events` row in the same transaction as the status update.
9. Publish the event to Redis pub/sub for WebSocket clients.
10. Acknowledge the task.

`attempt_count` counts every provider attempt, including success and permanent failure. It increments at claim time, after the rate-limit wait, not only on retries.

When a retry is scheduled, the worker returns an error. asynq retries the task with the configured delay. The database status is `queued`, which matches the fact that the task is waiting in Redis.

### Stale claims

A worker can crash after claiming a row but before recording a result. The row stays `processing` and a retried task would normally fail the `queued` guard. The claim condition includes:

```sql
status = 'processing' AND updated_at < now() - interval '2 minutes'
```

The lease is longer than the provider timeout, so a live worker is not preempted. If the original worker is alive but stalled longer than the lease, a duplicate provider call is possible. The provider idempotency header and the at-least-once contract cover that case.

## 4. Rate limiting

- Limit: 100 provider requests per second per channel.
- Implementation: `redis_rate` (GCRA) with a Redis key per channel.
- Burst: 1. The queue absorbs bursts; the limiter smooths the rate. A larger burst would allow more than 100 requests in a sliding one-second window.
- Shared across all worker replicas.
- Workers wait for a token. They do not drop work and they do not bypass the limit.
- The wait time is recorded in `rate_limit_wait_seconds`.

Key format:

```text
ratelimit:provider:{channel}
```

If Redis is unavailable, the worker cannot get a token. The task fails, asynq retries it, and readiness reports the problem. Creates are still accepted and persisted as `pending`; the reconciler enqueues them when Redis returns.

## 5. Retry policy

| Setting | Value |
| --- | --- |
| Maximum attempts | 5 total (1 initial + 4 retries) |
| Base delay | 1 second |
| Factor | 2 |
| Cap | 30 seconds |
| Jitter | Full jitter: `random(0, min(cap, base * 2^attempt))` |
| Provider timeout | 10 seconds per attempt |
| Retry-After | Honoured when the provider sends it |

Example delays for attempts 0 to 3: 0 to 1s, 0 to 2s, 0 to 4s, 0 to 8s.

## 6. Error classification

| Condition | Class | Action |
| --- | --- | --- |
| `202` | Success | Mark `accepted`. |
| `408`, `425`, `429` | Transient | Retry. Honour `Retry-After`. |
| Any `5xx` | Transient | Retry. |
| Network timeout, connection reset, DNS failure | Transient | Retry. |
| Other `4xx` | Permanent | Mark `failed`. |
| Invalid URL or unsupported channel | Permanent | Mark `failed`. |
| Context cancelled by shutdown | Transient | Return the task to the queue. |

The provider adapter returns a typed error with `Transient bool`, `RetryAfter time.Duration`, and `Message string`. The worker uses that type; it does not inspect raw strings.

## 7. Dead letter

When the last attempt fails with a transient error, the notification moves to `dead_letter`. The task is archived by asynq. The event row stores the last error and attempt count.

Operators can inspect dead letters through the API (bonus) or directly in PostgreSQL:

```sql
SELECT id, channel, attempt_count, last_error, updated_at
FROM notifications
WHERE status = 'dead_letter'
ORDER BY updated_at DESC;
```

A manual retry:

1. Sets `status` to `pending`, resets `attempt_count` to 0, and clears `last_error`.
2. Enqueues a fresh task with ID `<notification_id>:retry:<n>`.
3. Moves the row to `queued`.

If the enqueue fails, the row stays `pending` and the reconciler retries it. The archived task stays in the asynq archive; it is not requeued. Use asynq Inspector to delete it if the archive should be cleaned up.

## 8. Idempotency

Two layers:

### API layer

- Optional `Idempotency-Key` header.
- Row in `idempotency_keys` with scope, key, request hash, state, response.
- In-progress requests return `409`.
- Completed requests replay the stored response with `Idempotent-Replayed: true`.
- Different body with the same key returns `422`.

### Worker layer

- asynq task ID is the notification ID, unique for 24 hours. Manual retries use a fresh task ID.
- The state transition is a guarded update. Only one worker can claim a row for a given attempt.
- The provider request includes `Idempotency-Key: <notification_id>` so a real provider could deduplicate. webhook.site ignores it, but the header is correct.
- If a duplicate task arrives after the notification is `accepted`, the worker sees the status and stops.
- A duplicate task that arrives while the row is `processing` waits for the lease to expire, then may retry. This is the at-least-once trade-off.

## 9. Cancellation

- Only `pending` and `scheduled` can be cancelled.
- Cancellation is a guarded update: `UPDATE ... WHERE status IN ('pending', 'scheduled')`.
- A scheduled asynq task is not deleted. When it fires, the worker sees `cancelled` and stops. This avoids a race between deleting the task and the worker claiming it.
- `queued` and `processing` return `409`. A message in Redis may already be on its way to the provider.

## 10. Reconciler

The API commits a notification as `pending`, then enqueues it and marks it `queued`. If the process dies between those steps, the row stays `pending`.

A background loop in the worker runs every 30 seconds:

```sql
SELECT id FROM notifications
WHERE status = 'pending' AND created_at < now() - interval '1 minute'
ORDER BY created_at
LIMIT 100;
```

It enqueues each row and moves it to `queued`. The same loop recovers from a short Redis outage: rows stay `pending` until Redis is back.

This is not a full transactional outbox. It covers the failure window with much less code, and the trade-off is documented in [decisions/0002](decisions/0002-postgres-redis-asynq.md).

## 11. Scheduled notifications (bonus)

- `scheduled_at` must be in the future and at most 30 days ahead.
- The notification is inserted as `scheduled`.
- The task is enqueued with `asynq.ProcessAt(scheduled_at)`.
- When the task fires, the worker claims `scheduled` to `processing`.
- Cancellation sets `cancelled`; the worker skips the task when it fires.
- Redis must be persistent enough to hold scheduled tasks. In the assessment, Redis runs with AOF enabled.

## 12. Failure scenarios

| Scenario | What happens | What the reviewer sees |
| --- | --- | --- |
| API dies after commit, before enqueue | Row stays `pending` | Reconciler enqueues it within 30 seconds |
| Worker dies before provider call | Task retries | Attempt count increases |
| Worker dies after provider 202, before DB update | Task retries; provider may receive a duplicate | Provider idempotency header; DB guard on the next attempt |
| Provider returns 500 | Retry with backoff | `queued` then `processing` again |
| Provider returns 400 | `failed` | No retry |
| Provider is slow | 10-second timeout, then retry | `last_error` records the timeout |
| Redis is down | API persists creates as `pending` and returns 202; readiness fails; worker cannot consume | `pending` rows; reconciler enqueues after Redis returns |
| PostgreSQL is down | API returns 503; worker retries | `/readyz` reports the failure |
| Queue is flooded | Rate limiter throttles; queue depth grows | `/stats` and `/metrics` show depth and wait time |
| Duplicate API request | Idempotency key replays the response | `Idempotent-Replayed: true` |
| Cancel a queued notification | `409` | Documented behaviour |

## 13. What we do not guarantee

- Exactly-once delivery.
- Ordering within a batch.
- Delivery receipts from the provider. `accepted` means the provider accepted the message.
- Infinite retries. Five attempts, then a dead letter.
- SLA numbers. The targets are design goals, not contractual promises.
