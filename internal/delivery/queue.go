package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
)

const (
	// TaskTypeDeliver is the asynq task type for a notification.
	TaskTypeDeliver = "notification:deliver"

	// QueueHigh, QueueNormal, and QueueLow are the priority queues.
	QueueHigh   = "high"
	QueueNormal = "normal"
	QueueLow    = "low"
)

// TaskPayload is the data carried by a delivery task.
type TaskPayload struct {
	NotificationID string `json:"notification_id"`
	CorrelationID  string `json:"correlation_id"`
}

// Queue submits delivery tasks to Redis.
type Queue struct {
	client     *asynq.Client
	inspector  *asynq.Inspector
	maxRetries int
	logger     *slog.Logger
}

// NewQueue creates a queue client.
func NewQueue(redis asynq.RedisConnOpt, maxRetries int, logger *slog.Logger) *Queue {
	if maxRetries < 0 {
		maxRetries = 0
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Queue{
		client:     asynq.NewClient(redis),
		inspector:  asynq.NewInspector(redis),
		maxRetries: maxRetries,
		logger:     logger,
	}
}

// Close releases the queue client.
func (q *Queue) Close() error {
	return q.client.Close()
}

// Inspector exposes queue inspection for stats and operations.
func (q *Queue) Inspector() *asynq.Inspector {
	return q.inspector
}

// Enqueue submits one notification. The task ID is the notification ID, so
// duplicate enqueues are collapsed while the task is queued.
func (q *Queue) Enqueue(
	ctx context.Context,
	id uuid.UUID,
	priority notification.Priority,
	correlationID string,
	scheduledAt *time.Time,
) error {
	payload, err := json.Marshal(TaskPayload{
		NotificationID: id.String(),
		CorrelationID:  correlationID,
	})
	if err != nil {
		return fmt.Errorf("marshal task payload: %w", err)
	}

	options := []asynq.Option{
		asynq.TaskID(id.String()),
		asynq.Unique(24 * time.Hour),
		asynq.Queue(queueForPriority(priority)),
		asynq.MaxRetry(q.maxRetries),
		asynq.Timeout(10 * time.Minute),
	}
	if scheduledAt != nil {
		options = append(options, asynq.ProcessAt(*scheduledAt))
	}

	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(TaskTypeDeliver, payload, options...))
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}
	return nil
}

func queueForPriority(priority notification.Priority) string {
	switch priority {
	case notification.PriorityHigh:
		return QueueHigh
	case notification.PriorityLow:
		return QueueLow
	default:
		return QueueNormal
	}
}
