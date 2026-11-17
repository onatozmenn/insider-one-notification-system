//go:build integration

package notification

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/testinfra"
)

var (
	integrationPostgres *testinfra.Postgres
	integrationPool     *pgxpool.Pool
	integrationRepo     *Repository
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	postgres, err := testinfra.StartPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start test postgres: %v\n", err)
		os.Exit(1)
	}

	integrationPostgres = postgres
	integrationPool = postgres.Pool
	integrationRepo = NewRepository(postgres.Pool)

	code := m.Run()
	postgres.Close()
	os.Exit(code)
}

func TestRepositoryCreateAndGet(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	n := integrationNotification()
	created, err := integrationRepo.Create(ctx, n, []byte(`{"source":"test"}`), nil)
	require.NoError(t, err)
	require.NotNil(t, created)

	got, err := integrationRepo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, StatusPending, got.Status)
	assert.Equal(t, ChannelSMS, got.Channel)
	assert.Equal(t, "integration test", got.Content)
	assert.Equal(t, 0, got.AttemptCount)

	var events int
	require.NoError(t, integrationPool.QueryRow(ctx,
		`SELECT count(*) FROM notification_events WHERE notification_id = $1`, n.ID,
	).Scan(&events))
	assert.Equal(t, 1, events)
}

func TestRepositoryGuardedTransitions(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	n := integrationNotification()
	_, err := integrationRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	claimed, err := integrationRepo.Claim(ctx, n.ID, time.Now().UTC(), 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, claimed.Status)
	assert.Equal(t, 1, claimed.AttemptCount)

	_, err = integrationRepo.Claim(ctx, n.ID, time.Now().UTC(), 2*time.Minute)
	require.ErrorIs(t, err, ErrNotClaimable)

	accepted, err := integrationRepo.MarkAccepted(ctx, n.ID, "provider-message-1", time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, accepted.Status)
	assert.Equal(t, "provider-message-1", *accepted.ProviderMessageID)

	_, err = integrationRepo.MarkFailed(ctx, n.ID, "too late", time.Now().UTC())
	require.ErrorIs(t, err, ErrNotClaimable)

	var events int
	require.NoError(t, integrationPool.QueryRow(ctx,
		`SELECT count(*) FROM notification_events WHERE notification_id = $1`, n.ID,
	).Scan(&events))
	assert.Equal(t, 3, events)
}

func TestRepositoryListFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	base := time.Now().UTC().Add(-time.Minute)
	first := integrationNotification()
	first.ID = mustUUID(t)
	first.CreatedAt = base
	first.UpdatedAt = base
	_, err := integrationRepo.Create(ctx, first, []byte(`{}`), nil)
	require.NoError(t, err)

	second := integrationNotification()
	second.ID = mustUUID(t)
	second.CreatedAt = base.Add(time.Second)
	second.UpdatedAt = base.Add(time.Second)
	_, err = integrationRepo.Create(ctx, second, []byte(`{}`), nil)
	require.NoError(t, err)
	_, err = integrationRepo.Claim(ctx, second.ID, time.Now().UTC(), 2*time.Minute)
	require.NoError(t, err)
	_, err = integrationRepo.MarkAccepted(ctx, second.ID, "msg-2", time.Now().UTC())
	require.NoError(t, err)

	third := integrationNotification()
	third.ID = mustUUID(t)
	third.Channel = ChannelEmail
	third.Recipient = "onat@example.com"
	third.Subject = stringPointer("Integration")
	third.CreatedAt = base.Add(2 * time.Second)
	third.UpdatedAt = base.Add(2 * time.Second)
	_, err = integrationRepo.Create(ctx, third, []byte(`{}`), nil)
	require.NoError(t, err)

	sms := ChannelSMS
	items, total, err := integrationRepo.List(ctx, ListFilter{Channel: &sms})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, items, 2)

	accepted := StatusAccepted
	items, total, err = integrationRepo.List(ctx, ListFilter{Status: &accepted})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, second.ID, items[0].ID)

	items, total, err = integrationRepo.List(ctx, ListFilter{Page: 1, PageSize: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, items, 2)
	assert.Equal(t, third.ID, items[0].ID)
	assert.Equal(t, second.ID, items[1].ID)

	from := base.Add(2 * time.Second)
	items, total, err = integrationRepo.List(ctx, ListFilter{From: &from})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, third.ID, items[0].ID)
}

func TestRepositoryBatch(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	batch := integrationBatch()
	items := []*Notification{
		integrationNotification(),
		integrationNotification(),
	}
	for _, item := range items {
		item.BatchID = &batch.ID
	}

	created, err := integrationRepo.CreateBatch(ctx, batch, items, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, created.Total)
	assert.Equal(t, 2, created.Counts[StatusPending])
	assert.Equal(t, BatchStatusPending, created.Status)

	stored, err := integrationRepo.GetBatch(ctx, batch.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, stored.Total)
	assert.Equal(t, 2, stored.Counts[StatusPending])

	for _, item := range items {
		got, err := integrationRepo.Get(ctx, item.ID)
		require.NoError(t, err)
		require.NotNil(t, got.BatchID)
		assert.Equal(t, batch.ID, *got.BatchID)
	}
}

func TestRepositoryBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	batch := integrationBatch()
	duplicateID := mustUUID(t)
	items := []*Notification{
		integrationNotification(),
		integrationNotification(),
	}
	items[0].ID = duplicateID
	items[1].ID = duplicateID
	items[0].BatchID = &batch.ID
	items[1].BatchID = &batch.ID

	_, err := integrationRepo.CreateBatch(ctx, batch, items, nil)
	require.Error(t, err)

	_, err = integrationRepo.GetBatch(ctx, batch.ID)
	require.ErrorIs(t, err, ErrBatchNotFound)

	var notifications int
	require.NoError(t, integrationPool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&notifications))
	assert.Zero(t, notifications)
}

func TestRepositoryIdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	first := integrationNotification()
	record := &IdempotencyRecord{
		Scope:          ScopeSingle,
		Key:            "integration-key",
		RequestHash:    "hash-1",
		State:          IdempotencyCompleted,
		ResourceID:     &first.ID,
		ResponseStatus: 202,
	}
	_, err := integrationRepo.Create(ctx, first, []byte(`{}`), record)
	require.NoError(t, err)

	second := integrationNotification()
	_, err = integrationRepo.Create(ctx, second, []byte(`{}`), &IdempotencyRecord{
		Scope:          ScopeSingle,
		Key:            "integration-key",
		RequestHash:    "hash-2",
		State:          IdempotencyCompleted,
		ResourceID:     &second.ID,
		ResponseStatus: 202,
	})
	require.Error(t, err)
	assert.True(t, IsIdempotencyConflict(err))

	stored, err := integrationRepo.GetIdempotency(ctx, ScopeSingle, "integration-key")
	require.NoError(t, err)
	require.NotNil(t, stored.ResourceID)
	assert.Equal(t, first.ID, *stored.ResourceID)
	assert.Equal(t, "hash-1", stored.RequestHash)

	var notifications int
	require.NoError(t, integrationPool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&notifications))
	assert.Equal(t, 1, notifications)
}

func TestRepositoryTemplates(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	subject := "Welcome"
	template := &Template{
		ID:        mustUUID(t),
		Name:      "welcome",
		Channel:   ChannelEmail,
		Subject:   &subject,
		Content:   "Hello {{name}}",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	created, err := integrationRepo.CreateTemplate(ctx, template)
	require.NoError(t, err)
	assert.Equal(t, template.ID, created.ID)

	byID, err := integrationRepo.GetTemplate(ctx, template.ID)
	require.NoError(t, err)
	assert.Equal(t, "welcome", byID.Name)

	byName, err := integrationRepo.GetTemplateByName(ctx, "welcome")
	require.NoError(t, err)
	assert.Equal(t, template.ID, byName.ID)

	all, err := integrationRepo.ListTemplates(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	_, err = integrationRepo.CreateTemplate(ctx, &Template{
		ID:        mustUUID(t),
		Name:      "welcome",
		Channel:   ChannelSMS,
		Content:   "duplicate",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	require.ErrorIs(t, err, ErrTemplateExists)

	_, err = integrationRepo.CreateTemplate(ctx, &Template{
		ID:        mustUUID(t),
		Name:      "email-without-subject",
		Channel:   ChannelEmail,
		Content:   "this must fail",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	require.Error(t, err, "the database must reject an email template without a subject")
}

func TestRepositoryRetryAndCancel(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	retry := integrationNotification()
	_, err := integrationRepo.Create(ctx, retry, []byte(`{}`), nil)
	require.NoError(t, err)
	_, err = integrationRepo.Claim(ctx, retry.ID, time.Now().UTC(), 2*time.Minute)
	require.NoError(t, err)
	_, err = integrationRepo.MarkDeadLetter(ctx, retry.ID, "provider returned 500", time.Now().UTC())
	require.NoError(t, err)

	retried, err := integrationRepo.Retry(ctx, retry.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, retried.Status)
	assert.Zero(t, retried.AttemptCount)
	assert.Nil(t, retried.LastError)

	_, err = integrationRepo.Retry(ctx, retry.ID)
	require.ErrorIs(t, err, ErrNotRetryable)

	cancel := integrationNotification()
	_, err = integrationRepo.Create(ctx, cancel, []byte(`{}`), nil)
	require.NoError(t, err)

	cancelled, err := integrationRepo.Cancel(ctx, cancel.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, cancelled.Status)

	_, err = integrationRepo.Cancel(ctx, cancel.ID)
	require.ErrorIs(t, err, ErrNotCancellable)

	queued := integrationNotification()
	queued.Status = StatusQueued
	_, err = integrationRepo.Create(ctx, queued, []byte(`{}`), nil)
	require.NoError(t, err)

	cancelledQueued, err := integrationRepo.Cancel(ctx, queued.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, cancelledQueued.Status)
}

func TestRepositoryReconcilableRows(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	now := time.Now().UTC()
	scheduledAt := now.Add(-time.Second)
	oldPending := integrationNotification()
	oldPending.CreatedAt = now.Add(-2 * time.Minute)
	oldPending.UpdatedAt = oldPending.CreatedAt
	_, err := integrationRepo.Create(ctx, oldPending, []byte(`{}`), nil)
	require.NoError(t, err)

	dueScheduled := integrationNotification()
	dueScheduled.Status = StatusScheduled
	dueScheduled.ScheduledAt = &scheduledAt
	dueScheduled.CreatedAt = now.Add(-2 * time.Minute)
	dueScheduled.UpdatedAt = dueScheduled.CreatedAt
	_, err = integrationRepo.Create(ctx, dueScheduled, []byte(`{}`), nil)
	require.NoError(t, err)

	staleQueued := integrationNotification()
	staleQueued.Status = StatusQueued
	staleQueued.CreatedAt = now.Add(-30 * time.Minute)
	staleQueued.UpdatedAt = now.Add(-20 * time.Minute)
	_, err = integrationRepo.Create(ctx, staleQueued, []byte(`{}`), nil)
	require.NoError(t, err)

	currentQueued := integrationNotification()
	currentQueued.Status = StatusQueued
	currentQueued.CreatedAt = now
	currentQueued.UpdatedAt = now
	_, err = integrationRepo.Create(ctx, currentQueued, []byte(`{}`), nil)
	require.NoError(t, err)

	items, err := integrationRepo.ListReconcilable(
		ctx,
		now.Add(-time.Minute),
		now,
		now.Add(-15*time.Minute),
		now.Add(-3*time.Minute),
		10,
	)
	require.NoError(t, err)
	require.Len(t, items, 3)

	ids := map[uuid.UUID]bool{}
	for _, item := range items {
		ids[item.ID] = true
	}
	assert.True(t, ids[oldPending.ID])
	assert.True(t, ids[dueScheduled.ID])
	assert.True(t, ids[staleQueued.ID])
	assert.False(t, ids[currentQueued.ID])
}

func TestRepositoryMarkExhaustedLease(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	n := integrationNotification()
	_, err := integrationRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)
	_, err = integrationRepo.Claim(ctx, n.ID, time.Now().UTC(), 2*time.Minute)
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = integrationPool.Exec(ctx,
		`UPDATE notifications SET attempt_count = 5, updated_at = $2 WHERE id = $1`,
		n.ID, now.Add(-3*time.Minute),
	)
	require.NoError(t, err)

	_, err = integrationRepo.Claim(ctx, n.ID, now, 2*time.Minute)
	require.ErrorIs(t, err, ErrNotClaimable, "a stale row at max_attempts must not be reclaimed")

	dead, err := integrationRepo.MarkExhaustedLease(ctx, n.ID, now, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, StatusDeadLetter, dead.Status)
	assert.Equal(t, 5, dead.AttemptCount)
	require.NotNil(t, dead.LastError)
	assert.Equal(t, "processing lease expired after final attempt", *dead.LastError)

	_, err = integrationRepo.MarkExhaustedLease(ctx, n.ID, now, 2*time.Minute)
	require.ErrorIs(t, err, ErrNotClaimable)
}

func TestRepositoryExpiredIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	first := integrationNotification()
	_, err := integrationRepo.Create(ctx, first, []byte(`{}`), &IdempotencyRecord{
		Scope:          ScopeSingle,
		Key:            "expiring-key",
		RequestHash:    "hash-1",
		State:          IdempotencyCompleted,
		ResourceID:     &first.ID,
		ResponseStatus: 202,
	})
	require.NoError(t, err)

	_, err = integrationPool.Exec(ctx,
		`UPDATE idempotency_keys SET expires_at = now() - interval '1 hour'
		 WHERE scope = $1 AND key = $2`,
		ScopeSingle, "expiring-key",
	)
	require.NoError(t, err)

	_, err = integrationRepo.GetIdempotency(ctx, ScopeSingle, "expiring-key")
	require.ErrorIs(t, err, ErrNotFound)

	var remaining int
	require.NoError(t, integrationPool.QueryRow(ctx,
		`SELECT count(*) FROM idempotency_keys WHERE scope = $1 AND key = $2`,
		ScopeSingle, "expiring-key",
	).Scan(&remaining))
	assert.Zero(t, remaining, "an expired key must be removed so it can be reused")

	second := integrationNotification()
	_, err = integrationRepo.Create(ctx, second, []byte(`{}`), &IdempotencyRecord{
		Scope:          ScopeSingle,
		Key:            "expiring-key",
		RequestHash:    "hash-2",
		State:          IdempotencyCompleted,
		ResourceID:     &second.ID,
		ResponseStatus: 202,
	})
	require.NoError(t, err)

	_, err = integrationPool.Exec(ctx,
		`UPDATE idempotency_keys SET expires_at = now() - interval '1 hour'
		 WHERE scope = $1 AND key = $2`,
		ScopeSingle, "expiring-key",
	)
	require.NoError(t, err)

	deleted, err := integrationRepo.DeleteExpiredIdempotency(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestRepositoryBatchOfThousand(t *testing.T) {
	ctx := context.Background()
	resetIntegrationDB(t)

	now := time.Now().UTC()
	batch := &Batch{
		ID:        mustUUID(t),
		Total:     1000,
		Status:    BatchStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	items := make([]*Notification, 1000)
	for i := range items {
		n := integrationNotification()
		n.BatchID = &batch.ID
		n.Recipient = fmt.Sprintf("+1555555%04d", i)
		n.CreatedAt = now
		n.UpdatedAt = now
		items[i] = n
	}

	created, err := integrationRepo.CreateBatch(ctx, batch, items, nil)
	require.NoError(t, err)
	assert.Equal(t, 1000, created.Total)
	assert.Equal(t, 1000, created.Counts[StatusPending])

	var stored int
	require.NoError(t, integrationPool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE batch_id = $1`, batch.ID,
	).Scan(&stored))
	assert.Equal(t, 1000, stored)
}

func resetIntegrationDB(t *testing.T) {
	t.Helper()
	require.NoError(t, integrationPostgres.Reset(context.Background()))
}

func integrationNotification() *Notification {
	now := time.Now().UTC()
	return &Notification{
		ID:            mustUUID(nil),
		Recipient:     "+15555550100",
		Channel:       ChannelSMS,
		Content:       "integration test",
		Priority:      PriorityNormal,
		Status:        StatusPending,
		AttemptCount:  0,
		MaxAttempts:   5,
		CorrelationID: "req-integration",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func integrationBatch() *Batch {
	now := time.Now().UTC()
	return &Batch{
		ID:        mustUUID(nil),
		Total:     2,
		Status:    BatchStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func mustUUID(t *testing.T) uuid.UUID {
	if t != nil {
		t.Helper()
	}
	id, err := uuid.NewV7()
	if err != nil {
		if t != nil {
			t.Fatalf("generate uuid: %v", err)
		}
		panic(err)
	}
	return id
}
