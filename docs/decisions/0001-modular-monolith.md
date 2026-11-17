# ADR 0001: Modular monolith, not microservices

## Status

Accepted.

## Context

The assessment asks for an event-driven notification system with an API, asynchronous workers, retries, rate limiting, and observability. The time budget is 12 hours. One engineer builds and defends the result.

Microservices would add service discovery, network retries, distributed tracing across process boundaries, and multiple deployment units. None of that is required to demonstrate the design skills the assessment is looking for. A single process would be simpler, but it would mix HTTP handling, queue consumption, and delivery in one runtime and make scaling the worker independently impossible.

## Decision

Build a modular monolith:

- One Go module and one container image.
- Three commands: `api`, `worker`, `migrate`.
- Clear internal packages with explicit dependencies.
- API and worker share code but not memory. They communicate through PostgreSQL and Redis.
- The domain does not import HTTP, Redis, or SQL packages.

## Consequences

Positive:

- One repository, one build, one deployment artifact.
- Local development and review are straightforward.
- The API and worker scale independently by running more replicas.
- Module boundaries keep the option to extract a service later.

Negative:

- A shared database schema couples modules at the storage level.
- A bad migration affects both processes.
- There is no process boundary to enforce discipline; code review and package rules must do it.

## Alternatives considered

- **Microservices:** rejected for operational overhead and time budget.
- **Single process:** rejected because worker scaling and failure isolation would be worse.
- **Serverless functions:** rejected because queue semantics, rate limiting, and local reproducibility would be harder to demonstrate.
