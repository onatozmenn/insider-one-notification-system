# Documentation

This folder explains the design behind the notification system. Start with the project constitution, then read the architecture and the area you need.

| Document | What it answers |
| --- | --- |
| [../AGENTS.md](../AGENTS.md) | Project rules, scope, stack, and definition of done. |
| [architecture.md](architecture.md) | How the system is put together and why. |
| [domain-model.md](domain-model.md) | Data model, statuses, transitions, indexes. |
| [api.md](api.md) | HTTP contract, examples, errors. |
| [reliability.md](reliability.md) | Queue, rate limiting, retries, idempotency, failure handling. |
| [observability.md](observability.md) | Metrics, logs, health, tracing, WebSocket events. |
| [testing.md](testing.md) | Test strategy and commands. |
| [operations.md](operations.md) | Docker Compose, configuration, migrations, runbook. |
| [demo.md](demo.md) | Step-by-step demo and expected results. |
| [roadmap.md](roadmap.md) | Milestones, issues, commit plan. |
| [decisions/](decisions/) | Architecture decision records. |

## Reading order

1. `AGENTS.md`
2. `architecture.md`
3. `domain-model.md`
4. `reliability.md`
5. `api.md`
6. The rest as needed.

## Status

The core MVP is implemented. The documents describe the target design; when code and docs disagree, fix one of them in the same pull request. Design changes go through an ADR.
