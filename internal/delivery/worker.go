package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
	"github.com/onatozmenn/insider-one-notification-system/internal/provider"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

const defaultLease = 2 * time.Minute

// WorkerRepository is the persistence boundary used by the worker.
type WorkerRepository interface {
	Get(ctx context.Context, id uuid.UUID) (*notification.Notification, error)
	Claim(ctx context.Context, id uuid.UUID, now time.Time, lease time.Duration) (*notification.Notification, error)
	MarkAccepted(ctx context.Context, id uuid.UUID, providerMessageID string, now time.Time) (*notification.Notification, error)
	MarkFailed(ctx context.Context, id uuid.UUID, message string, now time.Time) (*notification.Notification, error)
	MarkDeadLetter(ctx context.Context, id uuid.UUID, message string, now time.Time) (*notification.Notification, error)
	MarkQueuedForRetry(ctx context.Context, id uuid.UUID, message string, nextAttempt time.Time, now time.Time) (*notification.Notification, error)
}

// WorkerProvider is the provider boundary used by the worker.
type WorkerProvider interface {
	Send(ctx context.Context, message provider.Message) (provider.Result, error)
}

// WorkerLimiter is the rate limiter boundary used by the worker.
type WorkerLimiter interface {
	Allow(ctx context.Context, key string, limit redis_rate.Limit) (*redis_rate.Result, error)
}

// WorkerOptions are the delivery worker dependencies.
type WorkerOptions struct {
	Redis         asynq.RedisConnOpt
	Repository    WorkerRepository
	Provider      WorkerProvider
	Limiter       WorkerLimiter
	Metrics       *metrics.Metrics
	Publisher     func(ctx context.Context, event realtime.StatusEvent) error
	Logger        *slog.Logger
	Concurrency   int
	RatePerSecond int
	Burst         int
	Lease         time.Duration
}

// Worker consumes delivery tasks and calls the provider.
type Worker struct {
	redis         asynq.RedisConnOpt
	repository    WorkerRepository
	provider      WorkerProvider
	limiter       WorkerLimiter
	metrics       *metrics.Metrics
	publisher     func(ctx context.Context, event realtime.StatusEvent) error
	logger        *slog.Logger
	concurrency   int
	ratePerSecond int
	burst         int
	lease         time.Duration
}

// NewWorker creates a delivery worker.
func NewWorker(options WorkerOptions) *Worker {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Concurrency < 1 {
		options.Concurrency = 50
	}
	if options.RatePerSecond < 1 {
		options.RatePerSecond = 100
	}
	if options.Burst < 1 {
		options.Burst = 1
	}
	if options.Lease <= 0 {
		options.Lease = defaultLease
	}

	return &Worker{
		redis:         options.Redis,
		repository:    options.Repository,
		provider:      options.Provider,
		limiter:       options.Limiter,
		metrics:       options.Metrics,
		publisher:     options.Publisher,
		logger:        options.Logger,
		concurrency:   options.Concurrency,
		ratePerSecond: options.RatePerSecond,
		burst:         options.Burst,
		lease:         options.Lease,
	}
}

// Start runs the asynq server until the context is cancelled.
func (w *Worker) Start(ctx context.Context) error {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskTypeDeliver, w.handle)

	server := asynq.NewServer(w.redis, asynq.Config{
		Concurrency: w.concurrency,
		Queues: map[string]int{
			QueueHigh:   6,
			QueueNormal: 3,
			QueueLow:    1,
		},
		RetryDelayFunc: RetryDelay,
		LogLevel:       asynq.WarnLevel,
	})

	if err := server.Start(mux); err != nil {
		return fmt.Errorf("start asynq server: %w", err)
	}

	<-ctx.Done()
	server.Shutdown()
	return nil
}

func (w *Worker) handle(ctx context.Context, task *asynq.Task) (handleErr error) {
	var payload TaskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode task payload: %w", err)
	}

	id, err := uuid.Parse(payload.NotificationID)
	if err != nil {
		return fmt.Errorf("parse notification id: %w", err)
	}

	if payload.TraceParent != "" {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{
			"traceparent": payload.TraceParent,
		})
	}

	ctx, span := otel.Tracer("worker").Start(ctx, "notification.process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "asynq"),
			attribute.String("notification.id", id.String()),
		),
	)
	defer func() {
		if handleErr != nil {
			var retryAfter *RetryAfterError
			if !errors.As(handleErr, &retryAfter) {
				span.RecordError(handleErr)
				span.SetStatus(codes.Error, handleErr.Error())
			}
		}
		span.End()
	}()

	n, err := w.repository.Get(ctx, id)
	if errors.Is(err, notification.ErrNotFound) {
		w.logger.Warn("notification not found; dropping task", "notification_id", id)
		return nil
	}
	if err != nil {
		return fmt.Errorf("load notification: %w", err)
	}
	if n.Status.Terminal() || n.Status == notification.StatusCancelled {
		return nil
	}

	if n.Status == notification.StatusScheduled && n.ScheduledAt != nil && n.ScheduledAt.After(time.Now()) {
		return &RetryAfterError{Delay: time.Until(*n.ScheduledAt)}
	}

	if n.Status == notification.StatusProcessing {
		if remaining := w.lease - time.Since(n.UpdatedAt); remaining > 0 {
			return &RetryAfterError{Delay: remaining}
		}
	}

	waitStarted := time.Now()
	waitCtx, waitSpan := otel.Tracer("worker").Start(ctx, "rate_limit.wait",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("notification.channel", string(n.Channel)),
		),
	)
	waitErr := w.waitForToken(waitCtx, n.Channel)
	rateLimitWait := time.Since(waitStarted)
	waitSpan.SetAttributes(attribute.Int64("rate_limit.wait_ms", rateLimitWait.Milliseconds()))
	if waitErr != nil {
		waitSpan.RecordError(waitErr)
		waitSpan.SetStatus(codes.Error, waitErr.Error())
	}
	waitSpan.End()
	if waitErr != nil {
		return waitErr
	}

	claimed, err := w.repository.Claim(ctx, id, time.Now().UTC(), w.lease)
	if errors.Is(err, notification.ErrNotClaimable) || errors.Is(err, notification.ErrNotFound) {
		current, getErr := w.repository.Get(ctx, id)
		if getErr == nil && current.Status == notification.StatusProcessing {
			if remaining := w.lease - time.Since(current.UpdatedAt); remaining > 0 {
				return &RetryAfterError{Delay: remaining, Err: err}
			}
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim notification: %w", err)
	}

	w.metrics.ObserveQueueWait(string(claimed.Priority), string(claimed.Channel), time.Since(claimed.CreatedAt))
	w.metrics.ObserveRateLimitWait(string(claimed.Channel), rateLimitWait)
	w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusProcessing))
	w.publishStatus(ctx, claimed)

	sendCtx, sendSpan := otel.Tracer("worker").Start(ctx, "provider.send",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider.channel", string(claimed.Channel)),
			attribute.String("notification.id", claimed.ID.String()),
		),
	)

	started := time.Now()
	result, sendErr := w.provider.Send(sendCtx, provider.Message{
		ID:            claimed.ID.String(),
		To:            claimed.Recipient,
		Channel:       string(claimed.Channel),
		Content:       claimed.Content,
		CorrelationID: claimed.CorrelationID,
	})
	duration := time.Since(started)

	if sendErr != nil {
		sendSpan.RecordError(sendErr)
		sendSpan.SetStatus(codes.Error, sendErr.Error())
	} else {
		sendSpan.SetStatus(codes.Ok, "")
	}
	sendSpan.End()

	if sendErr == nil {
		w.metrics.ObserveProvider(string(claimed.Channel), "success", duration)
		w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusAccepted))
		w.metrics.ObserveProcessing(string(claimed.Channel), string(notification.StatusAccepted), time.Since(claimed.UpdatedAt))

		accepted, err := w.repository.MarkAccepted(ctx, claimed.ID, result.MessageID, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("mark accepted: %w", err)
		}
		w.publishStatus(ctx, accepted)
		w.logger.Info("notification accepted",
			"request_id", claimed.CorrelationID,
			"notification_id", claimed.ID,
			"batch_id", claimed.BatchID,
			"channel", claimed.Channel,
			"attempt", claimed.AttemptCount,
			"duration_ms", duration.Milliseconds(),
			"rate_limit_wait_ms", rateLimitWait.Milliseconds(),
			"provider_message_id", result.MessageID,
		)
		return nil
	}

	var providerErr *provider.Error
	if !errors.As(sendErr, &providerErr) {
		w.metrics.ObserveProvider(string(claimed.Channel), "transient_error", duration)
		w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusQueued))

		nextAttempt := time.Now().UTC().Add(RetryDelay(retryCount(ctx), sendErr, task))
		retried, err := w.repository.MarkQueuedForRetry(ctx, claimed.ID, sendErr.Error(), nextAttempt, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("mark retry: %w", err)
		}
		w.publishStatus(ctx, retried)
		w.logger.Warn("notification retry scheduled",
			"request_id", claimed.CorrelationID,
			"notification_id", claimed.ID,
			"channel", claimed.Channel,
			"attempt", claimed.AttemptCount,
			"error", sendErr,
		)
		return sendErr
	}

	message := providerErr.Message
	if message == "" {
		message = sendErr.Error()
	}

	if !providerErr.Transient {
		w.metrics.ObserveProvider(string(claimed.Channel), "permanent_error", duration)
		w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusFailed))
		w.metrics.ObserveProcessing(string(claimed.Channel), string(notification.StatusFailed), time.Since(claimed.UpdatedAt))

		failed, err := w.repository.MarkFailed(ctx, claimed.ID, message, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		w.publishStatus(ctx, failed)
		w.logger.Warn("notification failed permanently",
			"request_id", claimed.CorrelationID,
			"notification_id", claimed.ID,
			"channel", claimed.Channel,
			"attempt", claimed.AttemptCount,
			"error", message,
		)
		return nil
	}

	w.metrics.ObserveProvider(string(claimed.Channel), "transient_error", duration)

	if isLastAttempt(ctx) {
		w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusDeadLetter))
		w.metrics.ObserveProcessing(string(claimed.Channel), string(notification.StatusDeadLetter), time.Since(claimed.UpdatedAt))

		dead, err := w.repository.MarkDeadLetter(ctx, claimed.ID, message, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("mark dead letter: %w", err)
		}
		w.publishStatus(ctx, dead)
		w.logger.Warn("notification exhausted retries",
			"request_id", claimed.CorrelationID,
			"notification_id", claimed.ID,
			"channel", claimed.Channel,
			"attempt", claimed.AttemptCount,
			"error", message,
		)
		return nil
	}

	w.metrics.IncStatus(string(claimed.Channel), string(notification.StatusQueued))

	nextAttempt := time.Now().UTC().Add(RetryDelay(retryCount(ctx), sendErr, task))
	retried, err := w.repository.MarkQueuedForRetry(ctx, claimed.ID, message, nextAttempt, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark retry: %w", err)
	}
	w.publishStatus(ctx, retried)
	w.logger.Warn("notification retry scheduled",
		"request_id", claimed.CorrelationID,
		"notification_id", claimed.ID,
		"channel", claimed.Channel,
		"attempt", claimed.AttemptCount,
		"error", message,
	)
	return sendErr
}

func (w *Worker) publishStatus(ctx context.Context, n *notification.Notification) {
	if w.publisher == nil || n == nil {
		return
	}

	event := realtime.StatusEvent{
		NotificationID: n.ID.String(),
		Channel:        string(n.Channel),
		Status:         string(n.Status),
		AttemptCount:   n.AttemptCount,
		UpdatedAt:      n.UpdatedAt,
	}
	if err := w.publisher(ctx, event); err != nil {
		w.logger.Warn("publish status event failed",
			"notification_id", n.ID,
			"status", n.Status,
			"error", err,
		)
	}
}

func (w *Worker) waitForToken(ctx context.Context, channel notification.Channel) error {
	limit := redis_rate.Limit{
		Rate:   w.ratePerSecond,
		Period: time.Second,
		Burst:  w.burst,
	}
	key := rateLimitKey(channel)

	for {
		result, err := w.limiter.Allow(ctx, key, limit)
		if err != nil {
			return fmt.Errorf("rate limiter: %w", err)
		}
		if result.Allowed > 0 {
			return nil
		}

		timer := time.NewTimer(result.RetryAfter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func rateLimitKey(channel notification.Channel) string {
	return "ratelimit:provider:" + string(channel)
}

func retryCount(ctx context.Context) int {
	count, ok := asynq.GetRetryCount(ctx)
	if !ok {
		return 0
	}
	return count
}

func isLastAttempt(ctx context.Context) bool {
	retries, ok := asynq.GetRetryCount(ctx)
	if !ok {
		return false
	}
	maxRetries, ok := asynq.GetMaxRetry(ctx)
	if !ok {
		return false
	}
	return retries >= maxRetries
}
