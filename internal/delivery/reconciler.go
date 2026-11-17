package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
)

// Reconciler re-enqueues pending rows after a crash or a Redis outage.
type Reconciler struct {
	repository   *notification.Repository
	queue        *Queue
	logger       *slog.Logger
	interval     time.Duration
	pendingAfter time.Duration
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
	cutoff := time.Now().UTC().Add(-r.pendingAfter)

	pending, err := r.repository.ListPending(ctx, cutoff, 100)
	if err != nil {
		r.logger.Warn("reconciler failed to list pending notifications", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	queued := 0
	for _, n := range pending {
		if err := r.queue.Enqueue(ctx, n.ID, n.Priority, n.CorrelationID, n.ScheduledAt); err != nil {
			r.logger.Warn("reconciler failed to enqueue notification",
				"notification_id", n.ID,
				"error", err,
			)
			continue
		}

		if n.ScheduledAt != nil {
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

	r.logger.Info("reconciler pass", "pending", len(pending), "queued", queued)
}
