package delivery

import (
	"context"
	"time"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
)

// PollQueueDepth updates the queue_depth gauge every interval.
func PollQueueDepth(
	ctx context.Context,
	queue *Queue,
	appMetrics *metrics.Metrics,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	poll := func() {
		for name, depth := range queue.Depth(ctx) {
			appMetrics.SetQueueDepth(name, depth)
		}
	}

	poll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
