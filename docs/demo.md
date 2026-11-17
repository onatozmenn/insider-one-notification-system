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

## 7. Stats and metrics

```bash
curl -s "$BASE_URL/api/v1/stats"
curl -s "$BASE_URL/metrics" | grep -E 'queue_depth|http_requests_total'
curl -s http://localhost:9091/metrics | grep -E 'provider_request_total|notification_status_total|rate_limit_wait_seconds'
```

Expected: queue depth, accepted/failed/dead-letter counts, success rate, p95 latency, and worker provider metrics.

## 8. Clean up

```bash
docker compose down -v
```

This removes the local database and Redis volumes.
