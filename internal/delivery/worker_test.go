package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/provider"
)

type fakeWorkerRepository struct {
	getFn                func(context.Context, uuid.UUID) (*notification.Notification, error)
	claimFn              func(context.Context, uuid.UUID, time.Time, time.Duration) (*notification.Notification, error)
	markAcceptedFn       func(context.Context, uuid.UUID, string, time.Time) (*notification.Notification, error)
	markFailedFn         func(context.Context, uuid.UUID, string, time.Time) (*notification.Notification, error)
	markDeadLetterFn     func(context.Context, uuid.UUID, string, time.Time) (*notification.Notification, error)
	markQueuedForRetryFn func(context.Context, uuid.UUID, string, time.Time, time.Time) (*notification.Notification, error)
}

func (f *fakeWorkerRepository) Get(ctx context.Context, id uuid.UUID) (*notification.Notification, error) {
	if f.getFn == nil {
		return nil, errors.New("unexpected Get call")
	}
	return f.getFn(ctx, id)
}

func (f *fakeWorkerRepository) Claim(ctx context.Context, id uuid.UUID, now time.Time, lease time.Duration) (*notification.Notification, error) {
	if f.claimFn == nil {
		return nil, errors.New("unexpected Claim call")
	}
	return f.claimFn(ctx, id, now, lease)
}

func (f *fakeWorkerRepository) MarkAccepted(ctx context.Context, id uuid.UUID, providerMessageID string, now time.Time) (*notification.Notification, error) {
	if f.markAcceptedFn == nil {
		return nil, errors.New("unexpected MarkAccepted call")
	}
	return f.markAcceptedFn(ctx, id, providerMessageID, now)
}

func (f *fakeWorkerRepository) MarkFailed(ctx context.Context, id uuid.UUID, message string, now time.Time) (*notification.Notification, error) {
	if f.markFailedFn == nil {
		return nil, errors.New("unexpected MarkFailed call")
	}
	return f.markFailedFn(ctx, id, message, now)
}

func (f *fakeWorkerRepository) MarkDeadLetter(ctx context.Context, id uuid.UUID, message string, now time.Time) (*notification.Notification, error) {
	if f.markDeadLetterFn == nil {
		return nil, errors.New("unexpected MarkDeadLetter call")
	}
	return f.markDeadLetterFn(ctx, id, message, now)
}

func (f *fakeWorkerRepository) MarkQueuedForRetry(ctx context.Context, id uuid.UUID, message string, nextAttempt, now time.Time) (*notification.Notification, error) {
	if f.markQueuedForRetryFn == nil {
		return nil, errors.New("unexpected MarkQueuedForRetry call")
	}
	return f.markQueuedForRetryFn(ctx, id, message, nextAttempt, now)
}

type fakeWorkerProvider struct {
	sendFn func(context.Context, provider.Message) (provider.Result, error)
	calls  int
}

func (f *fakeWorkerProvider) Send(ctx context.Context, message provider.Message) (provider.Result, error) {
	f.calls++
	if f.sendFn == nil {
		return provider.Result{}, nil
	}
	return f.sendFn(ctx, message)
}

type fakeWorkerLimiter struct {
	allowFn func(context.Context, string, redis_rate.Limit) (*redis_rate.Result, error)
	calls   int
}

func (f *fakeWorkerLimiter) Allow(ctx context.Context, key string, limit redis_rate.Limit) (*redis_rate.Result, error) {
	f.calls++
	if f.allowFn == nil {
		return &redis_rate.Result{Allowed: 1}, nil
	}
	return f.allowFn(ctx, key, limit)
}

func testWorker(repository WorkerRepository, providerClient WorkerProvider, limiter WorkerLimiter) *Worker {
	return NewWorker(WorkerOptions{
		Repository:    repository,
		Provider:      providerClient,
		Limiter:       limiter,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		RatePerSecond: 100,
		Burst:         1,
		Lease:         time.Minute,
	})
}

func testTask(t *testing.T, id uuid.UUID) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(TaskPayload{NotificationID: id.String(), CorrelationID: "req-1"})
	require.NoError(t, err)
	return asynq.NewTask(TaskTypeDeliver, payload)
}

func queuedNotification(id uuid.UUID) *notification.Notification {
	return &notification.Notification{
		ID:            id,
		Recipient:     "+15555550100",
		Channel:       notification.ChannelSMS,
		Content:       "hello",
		Priority:      notification.PriorityHigh,
		Status:        notification.StatusQueued,
		CorrelationID: "req-1",
	}
}

func processingNotification(id uuid.UUID) *notification.Notification {
	return &notification.Notification{
		ID:            id,
		Recipient:     "+15555550100",
		Channel:       notification.ChannelSMS,
		Content:       "hello",
		Priority:      notification.PriorityHigh,
		Status:        notification.StatusProcessing,
		AttemptCount:  1,
		CorrelationID: "req-1",
	}
}

func TestWorkerAcceptsNotification(t *testing.T) {
	id := uuid.New()
	var acceptedMessageID string

	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			return queuedNotification(id), nil
		},
		claimFn: func(context.Context, uuid.UUID, time.Time, time.Duration) (*notification.Notification, error) {
			return processingNotification(id), nil
		},
		markAcceptedFn: func(_ context.Context, _ uuid.UUID, providerMessageID string, _ time.Time) (*notification.Notification, error) {
			acceptedMessageID = providerMessageID
			return &notification.Notification{ID: id, Status: notification.StatusAccepted}, nil
		},
	}
	var sentMessage provider.Message
	providerClient := &fakeWorkerProvider{
		sendFn: func(_ context.Context, message provider.Message) (provider.Result, error) {
			sentMessage = message
			return provider.Result{MessageID: "msg-1", Status: "accepted"}, nil
		},
	}

	worker := testWorker(repository, providerClient, &fakeWorkerLimiter{})
	err := worker.handle(context.Background(), testTask(t, id))

	require.NoError(t, err)
	assert.Equal(t, id.String(), sentMessage.ID)
	assert.Equal(t, "+15555550100", sentMessage.To)
	assert.Equal(t, "sms", sentMessage.Channel)
	assert.Equal(t, "hello", sentMessage.Content)
	assert.Equal(t, "msg-1", acceptedMessageID)
}

func TestWorkerSkipsTerminalNotification(t *testing.T) {
	id := uuid.New()
	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			n := queuedNotification(id)
			n.Status = notification.StatusAccepted
			return n, nil
		},
	}
	providerClient := &fakeWorkerProvider{}
	worker := testWorker(repository, providerClient, &fakeWorkerLimiter{})

	err := worker.handle(context.Background(), testTask(t, id))

	require.NoError(t, err)
	assert.Equal(t, 0, providerClient.calls)
}

func TestWorkerMarksPermanentFailure(t *testing.T) {
	id := uuid.New()
	var capturedMessage string

	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			return queuedNotification(id), nil
		},
		claimFn: func(context.Context, uuid.UUID, time.Time, time.Duration) (*notification.Notification, error) {
			return processingNotification(id), nil
		},
		markFailedFn: func(_ context.Context, _ uuid.UUID, message string, _ time.Time) (*notification.Notification, error) {
			capturedMessage = message
			return &notification.Notification{ID: id, Status: notification.StatusFailed}, nil
		},
	}
	providerClient := &fakeWorkerProvider{
		sendFn: func(context.Context, provider.Message) (provider.Result, error) {
			return provider.Result{}, &provider.Error{Transient: false, Message: "provider returned 400", StatusCode: 400}
		},
	}

	worker := testWorker(repository, providerClient, &fakeWorkerLimiter{})
	err := worker.handle(context.Background(), testTask(t, id))

	require.NoError(t, err)
	assert.Equal(t, "provider returned 400", capturedMessage)
}

func TestWorkerSchedulesTransientRetry(t *testing.T) {
	id := uuid.New()
	retried := false

	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			return queuedNotification(id), nil
		},
		claimFn: func(context.Context, uuid.UUID, time.Time, time.Duration) (*notification.Notification, error) {
			return processingNotification(id), nil
		},
		markQueuedForRetryFn: func(_ context.Context, _ uuid.UUID, message string, _ time.Time, _ time.Time) (*notification.Notification, error) {
			retried = true
			assert.Equal(t, "provider returned 500", message)
			return &notification.Notification{ID: id, Status: notification.StatusQueued}, nil
		},
	}
	providerClient := &fakeWorkerProvider{
		sendFn: func(context.Context, provider.Message) (provider.Result, error) {
			return provider.Result{}, &provider.Error{Transient: true, Message: "provider returned 500", StatusCode: 500}
		},
	}

	worker := testWorker(repository, providerClient, &fakeWorkerLimiter{})
	err := worker.handle(context.Background(), testTask(t, id))

	require.Error(t, err)
	assert.True(t, retried)
}

func TestWorkerWaitsForRateLimit(t *testing.T) {
	id := uuid.New()
	calls := 0

	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			return queuedNotification(id), nil
		},
		claimFn: func(context.Context, uuid.UUID, time.Time, time.Duration) (*notification.Notification, error) {
			return processingNotification(id), nil
		},
		markAcceptedFn: func(context.Context, uuid.UUID, string, time.Time) (*notification.Notification, error) {
			return &notification.Notification{ID: id, Status: notification.StatusAccepted}, nil
		},
	}
	providerClient := &fakeWorkerProvider{
		sendFn: func(context.Context, provider.Message) (provider.Result, error) {
			return provider.Result{MessageID: "msg-1"}, nil
		},
	}
	limiter := &fakeWorkerLimiter{
		allowFn: func(context.Context, string, redis_rate.Limit) (*redis_rate.Result, error) {
			calls++
			if calls == 1 {
				return &redis_rate.Result{Allowed: 0, RetryAfter: time.Millisecond}, nil
			}
			return &redis_rate.Result{Allowed: 1}, nil
		},
	}

	worker := testWorker(repository, providerClient, limiter)
	err := worker.handle(context.Background(), testTask(t, id))

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestWorkerDefersWhenProcessingLeaseIsFresh(t *testing.T) {
	id := uuid.New()
	repository := &fakeWorkerRepository{
		getFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			n := processingNotification(id)
			n.UpdatedAt = time.Now()
			return n, nil
		},
	}

	worker := testWorker(repository, &fakeWorkerProvider{}, &fakeWorkerLimiter{})
	err := worker.handle(context.Background(), testTask(t, id))

	require.Error(t, err)
	var retryAfter *RetryAfterError
	require.ErrorAs(t, err, &retryAfter)
	assert.Greater(t, retryAfter.Delay, time.Duration(0))
}
