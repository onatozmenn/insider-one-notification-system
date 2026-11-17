# ADR 0002: PostgreSQL for state, Redis and asynq for work

## Status

Accepted.

## Context

The system needs:

- Durable notification and batch records.
- An asynchronous queue with priority, retries, scheduling, and unique tasks.
- A per-channel rate limiter shared across worker replicas.
- A small operational footprint that fits a 12-hour assessment.

A database-only queue would require custom polling, locking, and visibility timeouts. Kafka or RabbitMQ would add a broker and a new operational model. A custom Redis Streams consumer would demonstrate depth but increase the risk of subtle bugs.

## Decision

- **PostgreSQL** is the source of truth for notifications, batches, events, idempotency keys, and templates.
- **Redis** holds asynq queues, rate limiter state, and pub/sub channels.
- **asynq** provides priority queues, retries, scheduled tasks, and unique task IDs.
- **redis_rate** provides a GCRA rate limiter in Redis.
- A reconciler re-enqueues `pending` rows after a crash or a short Redis outage.

## Consequences

Positive:

- Small stack: PostgreSQL and Redis only.
- asynq covers the queue features the brief asks for.
- The rate limiter is correct across worker replicas.
- The reconciler closes the gap between the database commit and the queue enqueue.

Negative:

- Redis is not the source of truth. A flush loses queued tasks.
- The system is at-least-once, not exactly-once.
- Two data stores need health checks and operational care.
- asynq retries are tied to Redis; a long outage delays delivery.

## Alternatives considered

- **Kafka:** strong log semantics, but too much operational weight for this scope.
- **RabbitMQ:** good routing and acknowledgements, but another broker to run and explain.
- **Custom Redis Streams:** more control, more code, more failure modes. Rejected under the "no risky custom infrastructure" rule.
- **PostgreSQL-only queue:** simpler stack, but polling and locking would be slower and harder to make fair.
- **Transactional outbox:** stronger than the reconciler, but adds a dispatcher and another table. The reconciler is a deliberate trade-off for the assessment.
