//go:build integration

package delivery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/testinfra"
)

var (
	deliveryPostgres *testinfra.Postgres
	deliveryRedis    *testinfra.Redis
	deliveryRepo     *notification.Repository
	deliveryQueue    *Queue
	deliveryLogger   = slog.New(slog.NewTextHandler(io.Discard, nil))
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	postgres, err := testinfra.StartPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start test postgres: %v\n", err)
		os.Exit(1)
	}
	redisInfra, err := testinfra.StartRedis(ctx)
	if err != nil {
		postgres.Close()
		fmt.Fprintf(os.Stderr, "start test redis: %v\n", err)
		os.Exit(1)
	}
	cancel()

	deliveryPostgres = postgres
	deliveryRedis = redisInfra
	deliveryRepo = notification.NewRepository(postgres.Pool)
	deliveryQueue = NewQueue(asynq.RedisClientOpt{Addr: redisInfra.Addr}, TransportMaxRetries, deliveryLogger)

	code := m.Run()

	_ = deliveryQueue.Close()
	redisInfra.Close()
	postgres.Close()
	os.Exit(code)
}

func TestQueueRetriesAreNotDeduplicated(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	n := deliveryNotification()
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	require.NoError(t, deliveryQueue.EnqueueRetry(ctx, n.ID, n.Priority, n.CorrelationID))
	require.NoError(t, deliveryQueue.EnqueueRetry(ctx, n.ID, n.Priority, n.CorrelationID))

	waitForQueueDepth(t, deliveryQueue, 2)
}

func TestReconcilerEnqueuesPendingRows(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	n := deliveryNotification()
	n.CreatedAt = time.Now().UTC().Add(-2 * time.Minute)
	n.UpdatedAt = n.CreatedAt
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	reconciler := NewReconciler(deliveryRepo, deliveryQueue, deliveryLogger)
	reconciler.runOnce(ctx)

	stored, err := deliveryRepo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, notification.StatusQueued, stored.Status)

	waitForQueueDepth(t, deliveryQueue, 1)
}

func TestReconcilerEnqueuesDueScheduledRow(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	now := time.Now().UTC()
	scheduledAt := now.Add(-time.Second)
	n := deliveryNotification()
	n.Status = notification.StatusScheduled
	n.ScheduledAt = &scheduledAt
	n.CreatedAt = now.Add(-2 * time.Minute)
	n.UpdatedAt = n.CreatedAt
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	NewReconciler(deliveryRepo, deliveryQueue, deliveryLogger).runOnce(ctx)

	stored, err := deliveryRepo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, notification.StatusScheduled, stored.Status)
	waitForQueueDepth(t, deliveryQueue, 1)
}

func TestReconcilerRequeuesStaleQueuedRow(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	now := time.Now().UTC()
	n := deliveryNotification()
	n.Status = notification.StatusQueued
	n.CreatedAt = now.Add(-30 * time.Minute)
	n.UpdatedAt = now.Add(-20 * time.Minute)
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	NewReconciler(deliveryRepo, deliveryQueue, deliveryLogger).runOnce(ctx)

	stored, err := deliveryRepo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, notification.StatusQueued, stored.Status)
	waitForQueueDepth(t, deliveryQueue, 1)
}

func TestReconcilerRecoversArchivedTask(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	now := time.Now().UTC()
	n := deliveryNotification()
	n.Status = notification.StatusQueued
	n.CreatedAt = now.Add(-30 * time.Minute)
	n.UpdatedAt = now.Add(-20 * time.Minute)
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	require.NoError(t, deliveryQueue.Enqueue(ctx, n.ID, n.Priority, n.CorrelationID, nil))
	require.NoError(t, deliveryQueue.Inspector().ArchiveTask(QueueNormal, n.ID.String()))

	info, err := deliveryQueue.Inspector().GetTaskInfo(QueueNormal, n.ID.String())
	require.NoError(t, err)
	require.Equal(t, asynq.TaskStateArchived, info.State)

	NewReconciler(deliveryRepo, deliveryQueue, deliveryLogger).runOnce(ctx)

	waitForQueueDepth(t, deliveryQueue, 1)

	info, err = deliveryQueue.Inspector().GetTaskInfo(QueueNormal, n.ID.String())
	require.NoError(t, err)
	assert.NotEqual(t, asynq.TaskStateArchived, info.State)
}

func TestReconcilerRequeuesProcessingRowWithLostTask(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	now := time.Now().UTC()
	n := deliveryNotification()
	n.Status = notification.StatusProcessing
	n.AttemptCount = 1
	n.CreatedAt = now.Add(-30 * time.Minute)
	n.UpdatedAt = now.Add(-5 * time.Minute)
	_, err := deliveryRepo.Create(ctx, n, []byte(`{}`), nil)
	require.NoError(t, err)

	NewReconciler(deliveryRepo, deliveryQueue, deliveryLogger).runOnce(ctx)

	stored, err := deliveryRepo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, notification.StatusProcessing, stored.Status)
	waitForQueueDepth(t, deliveryQueue, 1)
}

func TestRateLimiterIsSharedAcrossWorkerClients(t *testing.T) {
	ctx := context.Background()
	resetDeliveryState(t)

	limiterA := redis_rate.NewLimiter(deliveryRedis.Client)
	limiterB := redis_rate.NewLimiter(deliveryRedis.Client)
	limit := redis_rate.Limit{Rate: 1, Period: 10 * time.Second, Burst: 1}
	key := "ratelimit:provider:sms"

	first, err := limiterA.Allow(ctx, key, limit)
	require.NoError(t, err)
	require.Greater(t, first.Allowed, 0)

	second, err := limiterB.Allow(ctx, key, limit)
	require.NoError(t, err)
	assert.Zero(t, second.Allowed, "a second worker client must share the Redis limit")
	assert.Greater(t, second.RetryAfter, time.Duration(0))
}

func waitForQueueDepth(t *testing.T, queue *Queue, want int) {
	t.Helper()

	// In production the worker registers these queues in asynq:queues at
	// startup. The test has no worker, so register them explicitly.
	require.NoError(t, deliveryRedis.Client.SAdd(context.Background(), "asynq:queues",
		QueueHigh, QueueNormal, QueueLow).Err())

	deadline := time.Now().Add(5 * time.Second)
	var depths map[string]int
	for time.Now().Before(deadline) {
		depths = queue.Depth(context.Background())
		if depths[QueueNormal] == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("normal queue depth = %d, want %d (depths: %v)", depths[QueueNormal], want, depths)
}

func TestPollQueueDepth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	PollQueueDepth(ctx, deliveryQueue, metrics.New(), time.Millisecond)
}

func TestWorkerStartsAndShutsDown(t *testing.T) {
	resetDeliveryState(t)

	worker := NewWorker(WorkerOptions{
		Redis:      asynq.RedisClientOpt{Addr: deliveryRedis.Addr},
		Repository: &fakeWorkerRepository{},
		Provider:   &fakeWorkerProvider{},
		Limiter:    &fakeWorkerLimiter{},
		Logger:     deliveryLogger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func resetDeliveryState(t *testing.T) {
	t.Helper()
	require.NoError(t, deliveryPostgres.Reset(context.Background()))
	require.NoError(t, deliveryRedis.Client.FlushDB(context.Background()).Err())
	require.NoError(t, deliveryRedis.Client.SAdd(context.Background(), "asynq:queues",
		QueueHigh, QueueNormal, QueueLow).Err())
}

func deliveryNotification() *notification.Notification {
	now := time.Now().UTC()
	return &notification.Notification{
		ID:            mustDeliveryUUID(),
		Recipient:     "+15555550100",
		Channel:       notification.ChannelSMS,
		Content:       "delivery integration",
		Priority:      notification.PriorityNormal,
		Status:        notification.StatusPending,
		MaxAttempts:   5,
		CorrelationID: "req-delivery-integration",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func mustDeliveryUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id
}
