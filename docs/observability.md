# Observability

## 1. Metrics

Prometheus format at `/metrics` on the API (`8080`) and the worker (`9091`). The API exposes HTTP, create, and queue-depth metrics. The worker exposes provider, rate-limit, queue-wait, and processing metrics.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `notification_created_total` | counter | `channel`, `priority` | Notifications accepted by the API. |
| `notification_status_total` | counter | `channel`, `status` | Status transitions. |
| `notification_processing_duration_seconds` | histogram | `channel`, `status` | Time from claim to terminal state. |
| `notification_queue_wait_seconds` | histogram | `priority`, `channel` | Time between enqueue and claim. |
| `provider_request_total` | counter | `channel`, `result` | Provider calls by result: `success`, `transient_error`, `permanent_error`. |
| `provider_request_duration_seconds` | histogram | `channel` | Provider call latency. |
| `rate_limit_wait_seconds` | histogram | `channel` | Time spent waiting for a channel token. |
| `queue_depth` | gauge | `queue` | Pending tasks per asynq queue. |
| `http_requests_total` | counter | `method`, `route`, `status` | API requests. |
| `http_request_duration_seconds` | histogram | `method`, `route` | API latency. |
| `build_info` | gauge | `version`, `commit` | Build metadata. |

Rules:

- Labels are bounded. Do not put notification IDs, recipients, or error strings in labels.
- Histograms use buckets that fit the provider timeout: 10ms to 15s.
- The metrics registry is created once and passed explicitly. No package-level mutable state.

## 2. Stats endpoint

`GET /api/v1/stats` returns a small JSON view for humans and the README demo:

```json
{
  "queue_depth": {
    "high": 10,
    "normal": 42,
    "low": 300
  },
  "window": "5m",
  "counts": {
    "accepted": 1200,
    "failed": 3,
    "dead_letter": 1
  },
  "success_rate": 0.9967,
  "p95_latency_ms": 180
}
```

- Counts come from `notifications` for the last five minutes.
- Queue depth comes from asynq Inspector.
- Success rate is `accepted / (accepted + failed + dead_letter)`.
- p95 latency is computed from `accepted_at - created_at` for the window (end-to-end acceptance latency). The histogram `notification_processing_duration_seconds` measures claim-to-terminal time for worker diagnostics; it is not the `/stats` value.

The endpoint is read-only and does not touch the queue.

## 3. Logs

Structured JSON with `log/slog`. One line per event.

Example API log:

```json
{
  "time": "2026-11-17T17:00:00.123Z",
  "level": "INFO",
  "service": "api",
  "msg": "notification created",
  "request_id": "req_01J...",
  "notification_id": "0193b2f4-7c1a-7d3e-9a10-5f8c2d6e4b71",
  "batch_id": null,
  "channel": "sms",
  "priority": "high",
  "status": "queued"
}
```

Example worker log:

```json
{
  "time": "2026-11-17T17:00:01.456Z",
  "level": "INFO",
  "service": "worker",
  "msg": "provider accepted notification",
  "request_id": "req_01J...",
  "notification_id": "0193b2f4-7c1a-7d3e-9a10-5f8c2d6e4b71",
  "channel": "sms",
  "attempt": 1,
  "duration_ms": 123,
  "provider_message_id": "3f2b9c1e-8a4d-4f6e-9b2a-1c7d5e8f0a3b",
  "recipient": "+1555***100"
}
```

Rules:

- Required fields: `time`, `level`, `service`, `msg`.
- Correlation fields when available: `request_id`, `notification_id`, `batch_id`.
- Worker fields: `channel`, `attempt`, `duration_ms`, `error`.
- Mask recipients. If the rule would reveal the full value, print `***` instead.
  - Phone: keep the country code and the last 3 digits (`+1555***100`).
  - Email: keep the first character and the domain only when the local part has at least 3 characters; otherwise use `***@domain`.
  - Device token: keep the first 6 characters only when the token is longer than 12 characters; otherwise use `***`.
- Never log full payloads. Content may contain personal data.
- Errors include a stable `error_code` and a short message. Stack traces are for local development only.

## 4. Correlation IDs

- The API reads `X-Request-ID` or generates one.
- The ID is returned in the response and stored in `notifications.correlation_id`.
- The worker reads it from the task payload and logs it.
- The provider request includes `X-Request-ID`.
- The gateway passes the header through. If the client sends none, the API generates one.

This makes one request traceable across gateway, API, PostgreSQL, Redis, worker, and provider.

## 5. Health checks

| Endpoint | Purpose | Behaviour |
| --- | --- | --- |
| `/healthz` | Liveness | `200` if the process is running. No dependency checks. |
| `/readyz` | Readiness | `200` if PostgreSQL and Redis respond within 1 second. `503` with a JSON body otherwise. |

`/readyz` response when unhealthy:

```json
{
  "status": "unavailable",
  "checks": {
    "postgres": "timeout",
    "redis": "ok"
  }
}
```

The gateway routes both endpoints to the API. Docker healthchecks use the same endpoints.

## 6. Queue depth

A background goroutine in the API polls asynq Inspector every 5 seconds:

- `high`, `normal`, `low` pending counts.
- Active tasks.
- Scheduled tasks.
- Retry tasks.
- Archived tasks (dead letters).

The values feed `queue_depth` and `/api/v1/stats`. The poller uses a short timeout and logs failures without crashing the process.

## 7. Tracing

- OpenTelemetry with OTLP HTTP export. `OTEL_EXPORTER_OTLP_ENDPOINT` enables it; an empty value is a no-op.
- Spans:
  - HTTP request (API server span, named by route).
  - `queue.enqueue` (API producer span).
  - `notification.process` (worker consumer span).
  - `rate_limit.wait` (worker internal span with the wait duration).
  - `provider.send` (worker client span).
- W3C `traceparent` is injected into the asynq task payload and the provider request headers, so the API request, queue hop, worker, and provider call share one trace.
- Jaeger all-in-one runs under the `tracing` compose profile. The OTLP HTTP endpoint is `http://jaeger:4318`; the exporter appends `/v1/traces`.
- Tracing is optional. The system works without an exporter, and a tracing setup failure is logged without stopping the process.
- Run it with:

```bash
# .env: OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
docker compose --profile tracing up -d jaeger
docker compose up -d api worker
```

Open `http://localhost:16686`, choose the `api` or `worker` service, and inspect a trace.

## 8. WebSocket

- Endpoint: `GET /api/v1/ws?notification_id=<id>`. The gateway upgrades the connection.
- The API subscribes to Redis pub/sub channel `notification.events`.
- The worker publishes `{notification_id, channel, status, attempt_count, updated_at}` after each delivery transition: claim, acceptance, retry, permanent failure, and dead letter.
- The hub filters events by the requested notification ID and sends JSON frames.
- On connect, the client receives a snapshot of the current notification, then updates.
- A 2-second poll of the notification row is a fallback when pub/sub is unavailable or a transition is missed. The poll stops when the connection closes. This costs one indexed read per connection every two seconds.
- Frames are best-effort. There is no replay buffer; a reconnecting client should re-read the notification. `WS_ENABLED=false` disables the route and hub.

## 9. Grafana

`docker compose --profile metrics up -d prometheus grafana` starts:

- Prometheus at `http://localhost:9090`, scraping `api:8080` and `worker:9090` every 5 seconds.
- Grafana at `http://localhost:3000` (`admin` / `admin`) with a provisioned `Notification System` dashboard:
  - queue depth by queue,
  - provider requests by result and request rate by channel,
  - status transitions by status,
  - success rate over the selected window,
  - provider p95 latency by channel,
  - rate-limit wait p95 by channel,
  - processing p95 by channel and status.

The dashboard file is `deploy/grafana/dashboards/notification-system.json`.

## 10. Alerts (production view)

These are not implemented in the assessment, but the metrics support them:

| Alert | Condition |
| --- | --- |
| Queue backlog | `queue_depth{queue="high"} > 1000` for 5 minutes. |
| Dead letters | `increase(notification_status_total{status="dead_letter"}[5m]) > 10`. |
| Provider errors | `sum(rate(provider_request_total{result="transient_error"}[5m])) / sum(rate(provider_request_total[5m])) > 0.05` for 5 minutes. |
| Latency | p95 `provider_request_duration_seconds` above 2 seconds. |
| Readiness | `/readyz` fails for 1 minute. |

## 11. Privacy

- No real personal data in demos or tests.
- Recipients are masked in logs.
- The webhook.site URL is public.
- Metrics never contain recipient values.
