# Operations

## 1. Prerequisites

- Docker Desktop with Docker Compose v2.
- Go 1.26 for local development.
- Make is optional. Every command has a plain `go` or `docker compose` equivalent.

## 2. Quickstart

```bash
git clone https://github.com/onatozmenn/insider-one-notification-system.git
cd insider-one-notification-system
docker compose up --build
```

The first build takes a few minutes. When the stack is healthy:

| URL | What |
| --- | --- |
| `http://localhost:8080` | Gateway |
| `http://localhost:8080/docs` | Swagger UI |
| `http://localhost:8080/openapi.yaml` | OpenAPI spec |
| `http://localhost:8080/healthz` | Liveness |
| `http://localhost:8080/readyz` | Readiness |
| `http://localhost:8080/metrics` | Prometheus metrics |
| `http://localhost:8080/api/v1/stats` | Human-readable stats |
| `http://localhost:8081` | API directly, bypassing the gateway |

Optional profiles:

```bash
# Queue inspection UI at http://localhost:8083
docker compose --profile tools up

# Jaeger UI at http://localhost:16686
docker compose --profile tracing up
```

## 3. Services

| Service | Port | Notes |
| --- | --- | --- |
| `gateway` | `8080` | nginx. Single entry point. |
| `api` | `8081` | Direct API access for debugging. |
| `swagger-ui` | internal | Reached through `/docs`. |
| `postgres` | internal | Not exposed by default. |
| `redis` | internal | Not exposed by default. |
| `migrate` | none | Runs once and exits. |
| `worker` | none | No inbound traffic. |
| `asynqmon` | `8083` | Optional `tools` profile. |
| `jaeger` | `16686` | Optional `tracing` profile. |

## 4. Configuration

Copy `.env.example` to `.env` for local overrides. Docker Compose reads `.env` automatically.

| Variable | Default | Purpose |
| --- | --- | --- |
| `APP_ENV` | `development` | Log format and debug behaviour. |
| `HTTP_ADDR` | `:8080` | API listen address. |
| `DATABASE_URL` | compose value | PostgreSQL connection string. |
| `REDIS_ADDR` | `redis:6379` | Redis address. |
| `REDIS_PASSWORD` | empty | Redis password, if any. |
| `PROVIDER_BASE_URL` | demo webhook.site URL | Provider endpoint. |
| `PROVIDER_TIMEOUT` | `10s` | Per-attempt provider timeout. |
| `WORKER_CONCURRENCY` | `50` | asynq worker concurrency. |
| `RATE_LIMIT_PER_SECOND` | `100` | Per-channel provider limit. |
| `RATE_LIMIT_BURST` | `100` | Per-channel burst. |
| `MAX_ATTEMPTS` | `5` | Total attempts per notification. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Enables tracing when set. |
| `WS_ENABLED` | `true` | Enables the WebSocket hub. |

Do not commit `.env`. The repository contains `.env.example` only.

## 5. Provider setup

The assessment uses webhook.site as a fake provider.

1. Open `https://webhook.site`.
2. Create a new URL. The UUID is the mailbox, not a user ID.
3. Set the response to:
   - Status: `202`
   - Content type: `application/json`
   - Body:

```json
{
  "messageId": "3f2b9c1e-8a4d-4f6e-9b2a-1c7d5e8f0a3b",
  "status": "accepted",
  "timestamp": "2026-11-17T17:30:00Z"
}
```

4. Put the URL in `PROVIDER_BASE_URL`.

The demo URL in `.env.example` expires after seven days. Create a fresh one before a long-lived demo. Tests use a local `httptest` server and do not depend on webhook.site.

## 6. Migrations

Migrations live in `migrations/` and are embedded in the `migrate` command.

```bash
# Apply all migrations
docker compose run --rm migrate up

# Roll back one migration
docker compose run --rm migrate down 1

# Check the current version
docker compose run --rm migrate version
```

The `migrate` service runs automatically before the API and worker start. It uses an advisory lock so two starts do not race.

## 7. Common commands

```bash
make up             # docker compose up --build
make down           # docker compose down
make logs           # follow logs
make test           # unit + integration
make test-unit      # unit only
make test-integration
make lint           # golangci-lint
make fmt            # gofmt + goimports
make build          # local binaries
make migrate-up
make migrate-down
make smoke          # end-to-end smoke test
make openapi-lint
```

## 8. Local development

Run PostgreSQL and Redis with compose, then run the binaries:

```bash
docker compose up -d postgres redis
go run ./cmd/migrate up
go run ./cmd/api
go run ./cmd/worker
```

Set `DATABASE_URL`, `REDIS_ADDR`, and `PROVIDER_BASE_URL` in your shell or `.env`.

## 9. Runbook

### Queue is growing

1. Check `GET /api/v1/stats` and `queue_depth`.
2. Open asynqmon (`--profile tools`) and inspect the `high`, `normal`, `low` queues.
3. Check worker logs for provider errors or rate-limit waits.
4. Check provider latency and error rate.
5. Scale workers by increasing `WORKER_CONCURRENCY` or adding replicas.

### Dead letters

1. Query:

```sql
SELECT id, channel, attempt_count, last_error, updated_at
FROM notifications
WHERE status = 'dead_letter'
ORDER BY updated_at DESC
LIMIT 50;
```

2. Inspect the provider response in the logs by `notification_id`.
3. Fix the cause: bad recipient, provider outage, invalid payload.
4. Retry with `POST /api/v1/notifications/{id}/retry` (bonus) or update the row and enqueue a task.

### Provider errors

1. Confirm `PROVIDER_BASE_URL` is reachable.
2. Confirm the webhook.site URL has not expired.
3. Check for `429` and `Retry-After`.
4. Check DNS and outbound network from the worker container.

### Redis is down

- `/readyz` returns `503`.
- The API can still insert notifications as `pending`.
- The worker cannot consume.
- When Redis returns, the reconciler enqueues pending rows.
- Restart Redis and watch the queue drain.

### PostgreSQL is down

- `/readyz` returns `503`.
- The API returns `503 service_unavailable`.
- The worker cannot update state; tasks retry.
- Restart PostgreSQL and confirm migrations are current.

### Reset everything

```bash
docker compose down -v
docker compose up --build
```

This deletes the database and Redis volumes. Use it only for local development.

## 10. Logs

```bash
docker compose logs -f api
docker compose logs -f worker
docker compose logs -f gateway
```

Logs are JSON. Pipe them through `jq` for readability:

```bash
docker compose logs -f api | jq .
```

## 11. Health checks

- `GET /healthz`: process is alive.
- `GET /readyz`: PostgreSQL and Redis respond.
- Docker healthchecks use these endpoints. A failing readiness check keeps the container out of the gateway pool.

## 12. Security notes

- No authentication in the assessment. The API is intended for a local demo.
- No real personal data. Recipients are fake.
- The webhook.site URL is public.
- Containers run as non-root.
- Secrets stay in `.env`, never in git.
