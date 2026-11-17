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

	// TransportMaxRetries bounds asynq retries for infrastructure errors. The
	// provider attempt budget is tracked in PostgreSQL with max_attempts, so
	// Redis or database failures cannot consume a delivery attempt.
	TransportMaxRetries = 100
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

// EnqueueFresh submits a recovery task with a new task ID and no uniqueness
// lock. It is used when an archived task still owns the notification ID.
func (q *Queue) EnqueueFresh(
	ctx context.Context,
	id uuid.UUID,
	priority notification.Priority,
	correlationID string,
) error {
	taskID := fmt.Sprintf("%s:recover:%d", id, time.Now().UnixNano())
	return q.enqueue(ctx, taskID, id, priority, correlationID, nil, 0)
}

// ReconcileEnqueue ensures that a task exists for the notification. Archived
// tasks retain their task ID, so they are deleted before the same ID is used
// again. A completed task is left alone and a fresh recovery task is created.
func (q *Queue) ReconcileEnqueue(
	ctx context.Context,
	id uuid.UUID,
	priority notification.Priority,
	correlationID string,
) error {
	queueName := queueForPriority(priority)
	info, err := q.inspector.GetTaskInfo(queueName, id.String())
	switch {
	case err == nil:
		switch info.State {
		case asynq.TaskStateArchived:
			if err := q.inspector.DeleteTask(queueName, id.String()); err != nil {
				return fmt.Errorf("delete archived task: %w", err)
			}
		case asynq.TaskStateCompleted:
			return q.EnqueueFresh(ctx, id, priority, correlationID)
		default:
			return nil
		}
	case errors.Is(err, asynq.ErrTaskNotFound), errors.Is(err, asynq.ErrQueueNotFound):
		// No task exists; enqueue below.
	default:
		return fmt.Errorf("inspect queue task: %w", err)
	}

	if err := q.Enqueue(ctx, id, priority, correlationID, nil); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return q.EnqueueFresh(ctx, id, priority, correlationID)
		}
		return err
	}

	// A unique lock can survive an archived task even after DeleteTask. In that
	// case Enqueue treats the duplicate as success but creates no task.
	if _, err := q.inspector.GetTaskInfo(queueName, id.String()); errors.Is(err, asynq.ErrTaskNotFound) {
		return q.EnqueueFresh(ctx, id, priority, correlationID)
	} else if err != nil && !errors.Is(err, asynq.ErrQueueNotFound) {
		return fmt.Errorf("verify reconciled task: %w", err)
	}
	return nil
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
