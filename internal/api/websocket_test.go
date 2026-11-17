package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

func TestWebSocketSnapshotAndStatus(t *testing.T) {
	snapshotTime := time.Now().UTC()
	service := &fakeNotificationService{
		getFn: func(_ context.Context, id uuid.UUID) (*notification.Notification, error) {
			return &notification.Notification{
				ID:        id,
				Channel:   notification.ChannelSMS,
				Status:    notification.StatusQueued,
				UpdatedAt: snapshotTime,
			}, nil
		},
	}
	hub := realtime.NewHub()

	router := NewRouter(Dependencies{
		Logger:        testLogger(),
		Readiness:     ReadinessChecks{},
		Notifications: service,
		Realtime:      hub,
	})
	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/v1/ws?notification_id=" + testNotificationID.String()

	connection, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer func() {
		_ = connection.Close(websocket.StatusNormalClosure, "test done")
	}()

	_, payload, err := connection.Read(ctx)
	require.NoError(t, err)

	var snapshot struct {
		Type         string                     `json:"type"`
		Notification *notification.Notification `json:"notification"`
	}
	require.NoError(t, json.Unmarshal(payload, &snapshot))
	assert.Equal(t, "snapshot", snapshot.Type)
	require.NotNil(t, snapshot.Notification)
	assert.Equal(t, testNotificationID, snapshot.Notification.ID)

	hub.Broadcast(realtime.StatusEvent{
		NotificationID: testNotificationID.String(),
		Channel:        "sms",
		Status:         "accepted",
		AttemptCount:   1,
		UpdatedAt:      snapshotTime.Add(time.Second),
	})

	_, payload, err = connection.Read(ctx)
	require.NoError(t, err)

	var status struct {
		Type  string                `json:"type"`
		Event *realtime.StatusEvent `json:"event"`
	}
	require.NoError(t, json.Unmarshal(payload, &status))
	assert.Equal(t, "status", status.Type)
	require.NotNil(t, status.Event)
	assert.Equal(t, "accepted", status.Event.Status)
}
