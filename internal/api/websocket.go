package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

type wsEnvelope struct {
	Type         string                     `json:"type"`
	Notification *notification.Notification `json:"notification,omitempty"`
	Event        *realtime.StatusEvent      `json:"event,omitempty"`
}

func registerWebSocketRoute(
	r chi.Router,
	service NotificationService,
	hub *realtime.Hub,
	log *slog.Logger,
) {
	if service == nil || hub == nil {
		return
	}

	r.Get("/api/v1/ws", func(w http.ResponseWriter, r *http.Request) {
		rawID := r.URL.Query().Get("notification_id")
		id, err := uuid.Parse(rawID)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_error",
				"notification_id must be a valid UUID", nil)
			return
		}

		// Subscribe before reading the snapshot so a transition between the
		// read and the subscription is not lost.
		events, unsubscribe := hub.Subscribe(id.String())
		defer unsubscribe()

		snapshot, err := service.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, notification.ErrNotFound) {
				writeAPIError(w, http.StatusNotFound, "not_found", "Resource not found", nil)
				return
			}
			log.Error("websocket snapshot failed", "notification_id", id, "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Unexpected server error", nil)
			return
		}

		// Auth and browser origin policy are out of scope for this assessment.
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			log.Warn("websocket upgrade failed", "notification_id", id, "error", err)
			return
		}
		defer func() {
			_ = connection.Close(websocket.StatusNormalClosure, "closed")
		}()

		// The client is not expected to send frames; CloseRead detects a peer
		// close and returns a context that is canceled with the connection.
		ctx := connection.CloseRead(r.Context())

		if err := writeWebSocket(ctx, connection, wsEnvelope{
			Type:         "snapshot",
			Notification: snapshot,
		}); err != nil {
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if !event.UpdatedAt.After(snapshot.UpdatedAt) {
					continue
				}
				if err := writeWebSocket(ctx, connection, wsEnvelope{
					Type:  "status",
					Event: &event,
				}); err != nil {
					return
				}
			}
		}
	})
}

func writeWebSocket(ctx context.Context, connection *websocket.Conn, envelope wsEnvelope) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return connection.Write(writeCtx, websocket.MessageText, payload)
}
