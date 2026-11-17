//go:build integration

package realtime

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/testinfra"
)

func TestHubRunReceivesPublishedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	redisInfra, err := testinfra.StartRedis(ctx)
	require.NoError(t, err)
	t.Cleanup(redisInfra.Close)

	hub := NewHub()
	go hub.Run(ctx, redisInfra.Client, slog.New(slog.NewTextHandler(io.Discard, nil)))

	events, unsubscribe := hub.Subscribe("notification-integration")
	defer unsubscribe()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	expected := StatusEvent{
		NotificationID: "notification-integration",
		Channel:        "sms",
		Status:         "accepted",
		AttemptCount:   1,
		UpdatedAt:      time.Now().UTC(),
	}

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for a Redis pub/sub event")
		case <-ticker.C:
			require.NoError(t, Publish(ctx, redisInfra.Client, expected))
		case event := <-events:
			assert.Equal(t, expected.NotificationID, event.NotificationID)
			assert.Equal(t, expected.Channel, event.Channel)
			assert.Equal(t, expected.Status, event.Status)
			assert.Equal(t, expected.AttemptCount, event.AttemptCount)
			return
		}
	}
}
