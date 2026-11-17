package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Channel is the Redis pub/sub channel for status events.
const Channel = "notification.events"

// StatusEvent is published after a notification transition.
type StatusEvent struct {
	NotificationID string    `json:"notification_id"`
	Channel        string    `json:"channel"`
	Status         string    `json:"status"`
	AttemptCount   int       `json:"attempt_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Hub fans out events to WebSocket subscribers by notification ID.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan StatusEvent]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{subscribers: make(map[string]map[chan StatusEvent]struct{})}
}

// Subscribe returns a channel and a cancel function.
func (h *Hub) Subscribe(notificationID string) (<-chan StatusEvent, func()) {
	events := make(chan StatusEvent, 16)

	h.mu.Lock()
	if h.subscribers[notificationID] == nil {
		h.subscribers[notificationID] = make(map[chan StatusEvent]struct{})
	}
	h.subscribers[notificationID][events] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		if subscribers, ok := h.subscribers[notificationID]; ok {
			delete(subscribers, events)
			if len(subscribers) == 0 {
				delete(h.subscribers, notificationID)
			}
		}
		close(events)
	}

	return events, cancel
}

// Broadcast sends an event to all subscribers of its notification.
func (h *Hub) Broadcast(event StatusEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for events := range h.subscribers[event.NotificationID] {
		select {
		case events <- event:
		default:
			// A slow client should not block delivery for everyone else.
		}
	}
}

// Run subscribes to Redis and fans events out until the context is cancelled.
func (h *Hub) Run(ctx context.Context, client *redis.Client, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	for {
		pubsub := client.Subscribe(ctx, Channel)
		messages := pubsub.Channel()

		for {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case message, ok := <-messages:
				if !ok {
					_ = pubsub.Close()
					goto reconnect
				}

				var event StatusEvent
				if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
					logger.Warn("discard malformed status event", "error", err)
					continue
				}
				h.Broadcast(event)
			}
		}

	reconnect:
		logger.Warn("redis status subscription closed; reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// Publish sends a status event to Redis.
func Publish(ctx context.Context, client *redis.Client, event StatusEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return client.Publish(ctx, Channel, payload).Err()
}
