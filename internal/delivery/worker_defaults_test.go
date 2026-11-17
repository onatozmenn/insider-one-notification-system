package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

func TestNewWorkerDefaults(t *testing.T) {
	worker := NewWorker(WorkerOptions{})

	assert.Equal(t, 50, worker.concurrency)
	assert.Equal(t, 100, worker.ratePerSecond)
	assert.Equal(t, 1, worker.burst)
	assert.Equal(t, defaultLease, worker.lease)
	assert.NotNil(t, worker.logger)
}

func TestPublishStatusWithoutPublisher(t *testing.T) {
	worker := NewWorker(WorkerOptions{})
	worker.publishStatus(context.Background(), &notification.Notification{
		ID:      uuid.New(),
		Channel: notification.ChannelSMS,
		Status:  notification.StatusAccepted,
	})
}

func TestPublishStatusRecordsChannelAndAttempt(t *testing.T) {
	var published bool
	worker := NewWorker(WorkerOptions{
		Publisher: func(_ context.Context, event realtime.StatusEvent) error {
			published = true
			assert.Equal(t, "accepted", event.Status)
			assert.Equal(t, 2, event.AttemptCount)
			return nil
		},
	})

	worker.publishStatus(context.Background(), &notification.Notification{
		ID:           uuid.New(),
		Channel:      notification.ChannelSMS,
		Status:       notification.StatusAccepted,
		AttemptCount: 2,
		UpdatedAt:    time.Now().UTC(),
	})
	assert.True(t, published)
}

func TestPublishStatusLogsPublisherError(t *testing.T) {
	worker := NewWorker(WorkerOptions{
		Publisher: func(context.Context, realtime.StatusEvent) error {
			return errors.New("publish failed")
		},
	})

	worker.publishStatus(context.Background(), &notification.Notification{
		ID:      uuid.New(),
		Channel: notification.ChannelSMS,
		Status:  notification.StatusAccepted,
	})
}

func TestRetryHelpersWithoutTaskContext(t *testing.T) {
	ctx := context.Background()
	assert.Zero(t, retryCount(ctx))
	assert.False(t, isLastAttempt(ctx))
}
