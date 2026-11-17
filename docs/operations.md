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

The first build takes a few minutes. If port `8080` is already in use, set `GATEWAY_PORT=8090` in `.env` and use `http://localhost:8090` below.

When the stack is healthy:

| URL | What |
| --- | --- |
| `http://localhost:8080` | Gateway |
| `http://localhost:8080/docs` | Swagger UI |
| `http://localhost:8080/openapi.yaml` | OpenAPI spec |
| `http://localhost:8080/healthz` | Liveness |
| `http://localhost:8080/readyz` | Readiness |
| `http://localhost:8080/metrics` | API Prometheus metrics |
| `http://localhost:9091/metrics` | Worker Prometheus metrics |
| `http://localhost:8080/api/v1/stats` | Human-readable stats |
| `http://localhost:8081` | API directly, bypassing the gateway |
| `ws://localhost:8080/api/v1/ws?notification_id=<id>` | WebSocket status updates |
| `http://localhost:16686` | Jaeger UI, `tracing` profile |
| `http://localhost:9090` | Prometheus, `metrics` profile |
| `http://localhost:3000` | Grafana, `metrics` profile (`admin` / `admin`) |

Optional profiles:

```bash
# Queue inspection UI at http://localhost:8083
docker compose --profile tools up -d

# Jaeger UI at http://localhost:16686
docker compose --profile tracing up -d jaeger

# Prometheus and Grafana
docker compose --profile metrics up -d prometheus grafana
```

## 3. Services

| Service | Port | Notes |
| --- | --- | --- |
| `gateway` | `8080` (`GATEWAY_PORT`) | nginx. Single entry point. |
| `api` | `8081` (`API_PORT`) | Direct API access for debugging. |
| `swagger-ui` | internal | OpenAPI UI, reached through `/docs`; spec at `/openapi.yaml`. |
| `postgres` | internal | Not exposed by default. |
| `redis` | internal | Not exposed by default. |
| `migrate` | none | Runs once and exits. |
| `worker` | `9091` (`WORKER_METRICS_PORT`) | Worker metrics only. No inbound API traffic. |
| `asynqmon` | `8083` | Optional `tools` profile. |
| `jaeger` | `16686`, OTLP `4318` | Optional `tracing` profile. |
| `prometheus` | `9090` | Optional `metrics` profile. |
| `grafana` | `3000` | Optional `metrics` profile. |

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
| `RATE_LIMIT_BURST` | `1` | Per-channel burst. Keep at 1 to enforce 100/s. |
| `MAX_ATTEMPTS` | `5` | Total attempts per notification. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Enables tracing when set. |
| `WS_ENABLED` | `true` | Enables the WebSocket hub. |
| `WORKER_METRICS_ADDR` | `:9090` | Worker metrics listen address inside the container. |
| `WORKER_METRICS_PORT` | `9091` | Compose host port for worker metrics. |

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
docker compose run --rm --entrypoint /app/migrate migrate up

# Roll back one migration
docker compose run --rm --entrypoint /app/migrate migrate down 1

# Check the current version
docker compose run --rm --entrypoint /app/migrate migrate version
```

The `migrate` service runs automatically before the API and worker start. It uses an advisory lock so two starts do not race.

## 7. Common commands

```bash
make up             # docker compose up --build
make down           # docker compose down
make logs           # follow logs
make test           # current unit and contract suite
make test-unit      # same suite, explicit target
make lint           # golangci-lint
make fmt            # gofmt
make build          # local binaries
make migrate-up
make migrate-down
make migrate-version
make openapi-lint
```

## 8. Local development

Run PostgreSQL and Redis with compose, then run the binaries:

The Compose dev override publishes PostgreSQL on `5433` and Redis on `6379` for host-run binaries:

```bash
docker compose -f docker-compose.yml -f compose.dev.yml up -d postgres redis
```

Run the API and worker in separate terminals:

```bash
# Terminal 1
go run ./cmd/migrate up
go run ./cmd/api

# Terminal 2
go run ./cmd/worker
```

For host runs, set:

```bash
export DATABASE_URL=postgres://postgres:postgres@localhost:5433/notifications?sslmode=disable
export REDIS_ADDR=localhost:6379
export PROVIDER_BASE_URL=https://webhook.site/<your-uuid>
```

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
4. Retry with `POST /api/v1/notifications/{id}/retry`. The endpoint resets the row, clears the error, and enqueues a fresh task ID. The archived asynq task stays archived; delete it with asynqmon if the archive should be cleaned up.
5. If Redis is unavailable during the retry, the row stays `pending`; the reconciler enqueues it when Redis returns. The API still returns `202`.

### Provider errors

1. Confirm `PROVIDER_BASE_URL` is reachable.
2. Confirm the webhook.site URL has not expired.
3. Check for `429` and `Retry-After`.
4. Check DNS and outbound network from the worker container.

### Redis is down

- `/readyz` returns `503`.
- The API accepts creates as `pending` and returns `202`. The reconciler enqueues them when Redis returns.
- The worker cannot consume.
- Use an `Idempotency-Key` if the client may retry a create during the outage.
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
- Docker healthchecks mark a container unhealthy, but they do not route traffic. The gateway re-resolves the API service through Docker DNS every 10 seconds, and nginx returns `502` or `503` while the API is down. A production deployment would use a load balancer that watches readiness directly.

## 12. Security notes

- No authentication in the assessment. The API is intended for a local demo.
- No real personal data. Recipients are fake.
- The webhook.site URL is public.
- Containers run as non-root.
- Secrets stay in `.env`, never in git.
