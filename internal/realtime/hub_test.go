package realtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHubBroadcast(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe("notification-1")
	defer unsubscribe()

	hub.Broadcast(StatusEvent{
		NotificationID: "notification-1",
		Channel:        "sms",
		Status:         "accepted",
		AttemptCount:   1,
	})

	select {
	case event := <-events:
		assert.Equal(t, "notification-1", event.NotificationID)
		assert.Equal(t, "accepted", event.Status)
		assert.Equal(t, 1, event.AttemptCount)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestHubIgnoresOtherNotifications(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe("notification-1")
	defer unsubscribe()

	hub.Broadcast(StatusEvent{NotificationID: "notification-2", Status: "accepted"})

	select {
	case event := <-events:
		t.Fatalf("unexpected event: %+v", event)
	case <-time.After(50 * time.Millisecond):
		// Expected: no event for a different notification.
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()
	_, unsubscribe := hub.Subscribe("notification-1")
	unsubscribe()
	unsubscribe() // must be idempotent

	hub.Broadcast(StatusEvent{NotificationID: "notification-1", Status: "accepted"})
	require.Empty(t, hub.subscribers["notification-1"])
}
