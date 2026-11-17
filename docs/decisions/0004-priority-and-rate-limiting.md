# ADR 0004: Weighted priority queues and a shared per-channel rate limiter

## Status

Accepted.

## Context

The brief requires:

- Priority queues: `high`, `normal`, `low`.
- Maximum 100 messages per second per channel.
- Burst traffic such as flash sales.

Strict priority is easy but starves `low` during a sustained burst. A single FIFO queue ignores priority. An in-process rate limiter is fast but wrong when the worker scales to more than one replica. Dropping work when the limit is reached violates the delivery contract.

## Decision

- Use three asynq queues with weights: `high` 6, `normal` 3, `low` 1.
- Use `redis_rate` (GCRA) with one key per channel: 100 requests/second, burst 1.
- Workers wait for a token instead of dropping work. The token wait happens before the row is claimed, so the `processing` lease only covers the provider call.
- Record `rate_limit_wait_seconds` per channel.
- Set the worker task timeout high enough to absorb a long wait (10 minutes).

A burst of 100 would allow 100 immediate requests plus the 100/second refill, which can exceed the cap in a sliding one-second window. GCRA needs a burst of at least 1; burst 1 enforces the cap and lets the queue absorb bursts.

## Consequences

Positive:

- Low-priority work is delayed, not starved.
- The limit is correct across worker replicas.
- Bursts are absorbed by the queue.
- The wait cost is visible in metrics.

Negative:

- A high-priority message can wait behind some normal traffic.
- A large low-priority burst can occupy workers while waiting for tokens. Concurrency and task timeouts must be tuned.
- Redis is on the hot path for every provider call.
- The rate limiter adds latency even when the provider is fast.

## Alternatives considered

- **Strict priority:** rejected because of starvation.
- **Single FIFO queue:** rejected because it ignores priority.
- **In-process `golang.org/x/time/rate`:** simpler, but incorrect with multiple worker replicas.
- **Drop or reject on limit:** rejected because it loses accepted work.
- **Provider-side rate limiting:** the provider is a simulation; we cannot rely on it.
