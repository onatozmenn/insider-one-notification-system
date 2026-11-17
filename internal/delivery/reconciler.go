package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
)

// Reconciler re-enqueues rows whose queue task may be missing after a crash,
// a Redis outage, or an exhausted transport retry budget.
type Reconciler struct {
	repository   *notification.Repository
	queue        *Queue
	logger       *slog.Logger
	interval     time.Duration
	pendingAfter time.Duration
	queuedAfter  time.Duration
}

// NewReconciler creates a reconciler with safe defaults.
func NewReconciler(repository *notification.Repository, queue *Queue, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		repository:   repository,
		queue:        queue,
		logger:       logger,
		interval:     30 * time.Second,
		pendingAfter: time.Minute,
		queuedAfter:  15 * time.Minute,
	}
}

// Run processes pending rows until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.runOnce(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Reconciler) runOnce(ctx context.Context) {
	now := time.Now().UTC()
	pendingBefore := now.Add(-r.pendingAfter)
	queuedBefore := now.Add(-r.queuedAfter)

	items, err := r.repository.ListReconcilable(ctx, pendingBefore, now, queuedBefore, 100)
	if err != nil {
		r.logger.Warn("reconciler failed to list notifications", "error", err)
		return
	}
	if len(items) == 0 {
		return
	}

	enqueued := 0
	queued := 0
	for _, n := range items {
		// Due scheduled and stale queued rows already have the right status.
		// Pending rows move to queued only after a successful enqueue.
		if err := r.queue.ReconcileEnqueue(ctx, n.ID, n.Priority, n.CorrelationID); err != nil {
			r.logger.Warn("reconciler failed to enqueue notification",
				"notification_id", n.ID,
				"status", n.Status,
				"error", err,
			)
			continue
		}
		enqueued++

		if n.Status != notification.StatusPending {
			continue
		}

		if _, err := r.repository.MarkQueued(ctx, n.ID); err != nil {
			r.logger.Warn("reconciler failed to mark notification queued",
				"notification_id", n.ID,
				"error", err,
			)
			continue
		}
		queued++
	}

	r.logger.Info("reconciler pass",
		"candidates", len(items),
		"enqueued", enqueued,
		"queued", queued,
	)
}
