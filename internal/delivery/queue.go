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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

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
	TraceParent    string `json:"traceparent,omitempty"`
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

// Depth returns the pending + active + scheduled + retry size of each queue.
func (q *Queue) Depth(_ context.Context) map[string]int {
	depths := make(map[string]int, 3)
	for _, name := range []string{QueueHigh, QueueNormal, QueueLow} {
		info, err := q.inspector.GetQueueInfo(name)
		if err != nil {
			continue
		}
		depths[name] = info.Pending + info.Active + info.Scheduled + info.Retry
	}
	return depths
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
	return q.enqueue(ctx, id.String(), id, priority, correlationID, scheduledAt, 24*time.Hour)
}

// EnqueueRetry submits a failed or dead-letter notification with a fresh task ID.
// Uniqueness is disabled: asynq deduplicates on task type and payload, so a
// quick retry would otherwise collide with the previous attempt.
func (q *Queue) EnqueueRetry(
	ctx context.Context,
	id uuid.UUID,
	priority notification.Priority,
	correlationID string,
) error {
	taskID := fmt.Sprintf("%s:retry:%d", id, time.Now().UnixNano())
	return q.enqueue(ctx, taskID, id, priority, correlationID, nil, 0)
}

func (q *Queue) enqueue(
	ctx context.Context,
	taskID string,
	id uuid.UUID,
	priority notification.Priority,
	correlationID string,
	scheduledAt *time.Time,
	uniqueTTL time.Duration,
) error {
	queueName := queueForPriority(priority)
	ctx, span := otel.Tracer("queue").Start(ctx, "queue.enqueue",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "asynq"),
			attribute.String("queue.name", queueName),
		),
	)
	defer span.End()

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	payload, err := json.Marshal(TaskPayload{
		NotificationID: id.String(),
		CorrelationID:  correlationID,
		TraceParent:    carrier.Get("traceparent"),
	})
	if err != nil {
		return fmt.Errorf("marshal task payload: %w", err)
	}

	options := []asynq.Option{
		asynq.TaskID(taskID),
		asynq.Queue(queueForPriority(priority)),
		asynq.MaxRetry(q.maxRetries),
		asynq.Timeout(10 * time.Minute),
	}
	if uniqueTTL > 0 {
		options = append(options, asynq.Unique(uniqueTTL))
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
