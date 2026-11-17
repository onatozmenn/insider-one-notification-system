# Demo

A short walkthrough for a reviewer. It assumes the Compose stack is running and `PROVIDER_BASE_URL` points to a webhook.site URL configured to return `202`.

Set a base URL once:

```bash
export BASE_URL=http://localhost:8080
```

If you changed `GATEWAY_PORT`, update the value.

## 1. Health and docs

```bash
curl -s "$BASE_URL/healthz"
curl -s "$BASE_URL/readyz"
```

Expected:

```json
{"status":"ok"}
{"checks":{"postgres":"ok","redis":"ok"},"status":"ok"}
```

Open `$BASE_URL/docs` for Swagger UI.

## 2. Create a notification

```bash
curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-single-1' \
  -d '{
    "recipient": "+15555550100",
    "channel": "sms",
    "content": "Demo delivery",
    "priority": "high"
  }'
```

Expected: `202` with `"status":"queued"`. Save the `id` and poll:

```bash
export NOTIFICATION_ID=<id>
curl -s "$BASE_URL/api/v1/notifications/$NOTIFICATION_ID"
```

Expected after a moment: `"status":"accepted"`, `"attempt_count":1`, and a `provider_message_id` from webhook.site.

## 3. Idempotent replay

Send the same request with the same `Idempotency-Key`. The response should include:

```text
Idempotent-Replayed: true
```

and the same notification ID. Send the same key with a different body to get `422 idempotency_conflict`.

## 4. Batch

```bash
curl -s -X POST "$BASE_URL/api/v1/notifications/batch" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-batch-1' \
  -d '{
    "notifications": [
      {"recipient": "+15555550101", "channel": "sms", "content": "Batch one"},
      {"recipient": "onat@example.com", "channel": "email", "subject": "Batch", "content": "Batch two"},
      {"recipient": "device-token-demo", "channel": "push", "subject": "Batch", "content": "Batch three"}
    ]
  }'
```

Expected: `202` with a `batch_id`, `total: 3`, and a `status_url`. Poll the batch:

```bash
export BATCH_ID=<batch_id>
curl -s "$BASE_URL/api/v1/batches/$BATCH_ID"
```

Expected: counts move from `queued` to `accepted` and the derived status becomes `completed`.

## 5. Failure, retries, and dead letter

1. Create a second webhook.site URL.
2. Configure its response to `500`.
3. Set `PROVIDER_BASE_URL` to that URL in `.env`.
4. Recreate the worker and API:

```bash
docker compose up -d api worker
```

Create a notification and poll it:

```bash
curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-failure-1' \
  -d '{"recipient":"+15555550100","channel":"sms","content":"Failure demo"}'
```

Expected: the worker logs each retry and the notification reaches `dead_letter` after the configured attempts. With `MAX_ATTEMPTS=3`:

```json
{
  "status": "dead_letter",
  "attempt_count": 3,
  "last_error": "provider returned 500"
}
```

Restore `PROVIDER_BASE_URL` to the working URL and recreate the API and worker.

## 6. Scheduled notification

```bash
SCHEDULED_AT=$(date -u -d '+5 seconds' +%Y-%m-%dT%H:%M:%SZ)  # GNU date
# macOS: SCHEDULED_AT=$(date -u -v+5S +%Y-%m-%dT%H:%M:%SZ)

curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-scheduled-1' \
  -d "{\"recipient\":\"+15555550100\",\"channel\":\"sms\",\"content\":\"Scheduled demo\",\"scheduled_at\":\"$SCHEDULED_AT\"}"
```

Expected: `scheduled` immediately, `accepted` after the scheduled time.

To test cancellation, create a notification scheduled 30 seconds ahead, call `/cancel` immediately, and verify it stays `cancelled` after the scheduled time with no `provider_message_id`.

## 7. Templates

Create a template:

```bash
curl -s -X POST "$BASE_URL/api/v1/templates" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "welcome",
    "channel": "sms",
    "content": "Hello {{name}}, your code is {{code}}"
  }'
```

Send a notification that uses it:

```bash
curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -d '{
    "recipient": "+15555550100",
    "channel": "sms",
    "template": "welcome",
    "variables": {"name": "Onat", "code": "ABC"}
  }'
```

Expected: `202` and `"content":"Hello Onat, your code is ABC"`. Remove `code` from `variables` and expect `422` with `variables.code` in `details`.

## 8. Dead-letter retry

After the failure demo, keep the dead-letter notification ID and call:

```bash
export DEAD_LETTER_ID=<id-from-section-5>
curl -s -X POST "$BASE_URL/api/v1/notifications/$DEAD_LETTER_ID/retry"
```

Expected: `202` with `"status":"queued"` and `"attempt_count":0`. Once the working provider URL is restored, poll the notification until `accepted` with `attempt_count:1`.

## 9. WebSocket updates

Install [websocat](https://github.com/vi/websocat) or use any WebSocket client:

```bash
SCHEDULED_AT=$(date -u -d '+8 seconds' +%Y-%m-%dT%H:%M:%SZ)
curl -s -X POST "$BASE_URL/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -d "{\"recipient\":\"+15555550100\",\"channel\":\"sms\",\"content\":\"WebSocket demo\",\"scheduled_at\":\"$SCHEDULED_AT\"}"

# Copy the id from the response, then:
export NOTIFICATION_ID=<id>
websocat "ws://localhost:8080/api/v1/ws?notification_id=$NOTIFICATION_ID"
```

Expected frames: `snapshot` with `scheduled`, then `status` events for `processing` and `accepted`. If the gateway port is not `8080`, change the URL.

## 10. Distributed tracing

```bash
# Add OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 to .env
docker compose --profile tracing up -d jaeger
docker compose up -d api worker
```

Create a notification, then open `http://localhost:16686`. Select the `api` or `worker` service and open the trace. Expected spans in one trace: `POST /api/v1/notifications`, `queue.enqueue`, `notification.process`, and `provider.send`.

## 11. Load test

With [k6](https://k6.io) installed and the stack running:

```bash
BASE_URL=http://localhost:8080 k6 run scripts/load-test.js
# or
BASE_URL=http://localhost:8080 make load-test
```

The script runs 20 virtual users for 30 seconds, creates SMS notifications with unique idempotency keys, and fails if more than 1% of requests error or p95 request latency exceeds 300 ms. The queue and the 100/s channel limiter absorb the burst.

## 12. Prometheus and Grafana

```bash
docker compose --profile metrics up -d prometheus grafana
```

- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000`, `admin` / `admin`

Open the provisioned `Notification System` dashboard. Run the load test or the demo requests and watch queue depth, status transitions, provider latency, and rate-limit wait.

## 13. Stats and metrics

```bash
curl -s "$BASE_URL/api/v1/stats"
curl -s "$BASE_URL/metrics" | grep -E 'queue_depth|http_requests_total'
curl -s http://localhost:9091/metrics | grep -E 'provider_request_total|notification_status_total|rate_limit_wait_seconds'
```

Expected: queue depth, accepted/failed/dead-letter counts, success rate, p95 latency, and worker provider metrics.

## 14. Clean up

```bash
docker compose down -v
```

This removes the local database and Redis volumes.
