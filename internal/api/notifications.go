package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/requestid"
)

// NotificationService is the application boundary used by the HTTP handlers.
type NotificationService interface {
	Create(ctx context.Context, input notification.CreateInput, idempotencyKey, correlationID string) (*notification.Notification, bool, error)
	CreateBatch(ctx context.Context, input notification.BatchInput, idempotencyKey, correlationID string) (*notification.Batch, bool, error)
	Get(ctx context.Context, id uuid.UUID) (*notification.Notification, error)
	List(ctx context.Context, filter notification.ListFilter) ([]notification.Notification, int, error)
	Cancel(ctx context.Context, id uuid.UUID) (*notification.Notification, error)
	GetBatch(ctx context.Context, id uuid.UUID) (*notification.Batch, error)
}

type notificationHandler struct {
	service NotificationService
	logger  *slog.Logger
}

func registerNotificationRoutes(r chi.Router, service NotificationService, log *slog.Logger) {
	h := &notificationHandler{service: service, logger: log}

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/notifications", h.create)
		r.Post("/notifications/batch", h.createBatch)
		r.Get("/notifications", h.list)
		r.Get("/notifications/{id}", h.get)
		r.Post("/notifications/{id}/cancel", h.cancel)
		r.Get("/batches/{id}", h.getBatch)
	})
}

func (h *notificationHandler) create(w http.ResponseWriter, r *http.Request) {
	var input notification.CreateInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_error", "Invalid JSON body", nil)
		return
	}

	key, err := readIdempotencyKey(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	created, replayed, err := h.service.Create(
		r.Context(),
		input,
		key,
		requestid.FromContext(r.Context()),
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/notifications/"+created.ID.String())
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}

	writeJSON(w, http.StatusAccepted, created)
}

func (h *notificationHandler) createBatch(w http.ResponseWriter, r *http.Request) {
	var input notification.BatchInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_error", "Invalid JSON body", nil)
		return
	}

	key, err := readIdempotencyKey(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	batch, replayed, err := h.service.CreateBatch(
		r.Context(),
		input,
		key,
		requestid.FromContext(r.Context()),
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	fillBatchLinks(batch)
	w.Header().Set("Location", batch.StatusURL)
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}

	writeJSON(w, http.StatusAccepted, batch)
}

func (h *notificationHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	n, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, n)
}

func (h *notificationHandler) list(w http.ResponseWriter, r *http.Request) {
	filter, err := parseListFilter(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items, total, err := h.service.List(r.Context(), filter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + filter.PageSize - 1) / filter.PageSize
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": items,
		"meta": map[string]int{
			"page":        filter.Page,
			"page_size":   filter.PageSize,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

func (h *notificationHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	n, err := h.service.Cancel(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, n)
}

func (h *notificationHandler) getBatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	batch, err := h.service.GetBatch(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	fillBatchLinks(batch)
	writeJSON(w, http.StatusOK, batch)
}

func (h *notificationHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *notification.ValidationError

	switch {
	case errors.As(err, &validationErr):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_error", "Request validation failed", validationErr.Details)
	case errors.Is(err, notification.ErrNotFound), errors.Is(err, notification.ErrBatchNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "Resource not found", nil)
	case errors.Is(err, notification.ErrNotCancellable):
		writeAPIError(w, http.StatusConflict, "not_cancellable", "Only pending or scheduled notifications can be cancelled", nil)
	case errors.Is(err, notification.ErrIdempotencyConflict):
		writeAPIError(w, http.StatusUnprocessableEntity, "idempotency_conflict", "Idempotency key reused with a different request body", nil)
	case errors.Is(err, notification.ErrIdempotencyInProgress):
		writeAPIError(w, http.StatusConflict, "idempotency_in_progress", "The original request is still in progress", nil)
	default:
		h.logger.Error("request failed", "error", err, "request_id", requestid.FromContext(r.Context()))
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Unexpected server error", nil)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain exactly one JSON object")
	}
	return nil
}

func readIdempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 255 {
		return "", &notification.ValidationError{Details: []notification.FieldError{
			{Field: "Idempotency-Key", Reason: "must be at most 255 characters"},
		}}
	}
	return key, nil
}

func pathUUID(r *http.Request, parameter string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, parameter))
	if err != nil {
		return uuid.Nil, &notification.ValidationError{Details: []notification.FieldError{
			{Field: parameter, Reason: "must be a valid UUID"},
		}}
	}
	return id, nil
}

func parseListFilter(r *http.Request) (notification.ListFilter, error) {
	query := r.URL.Query()
	filter := notification.ListFilter{}
	var details []notification.FieldError

	if value := query.Get("status"); value != "" {
		status := notification.Status(value)
		if !status.Valid() {
			details = append(details, notification.FieldError{Field: "status", Reason: "must be a valid status"})
		} else {
			filter.Status = &status
		}
	}

	if value := query.Get("channel"); value != "" {
		channel := notification.Channel(value)
		if !channel.Valid() {
			details = append(details, notification.FieldError{Field: "channel", Reason: "must be one of sms, email, push"})
		} else {
			filter.Channel = &channel
		}
	}

	if value := query.Get("batch_id"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			details = append(details, notification.FieldError{Field: "batch_id", Reason: "must be a valid UUID"})
		} else {
			filter.BatchID = &id
		}
	}

	if value := query.Get("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			details = append(details, notification.FieldError{Field: "from", Reason: "must be RFC3339"})
		} else {
			filter.From = &parsed
		}
	}

	if value := query.Get("to"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			details = append(details, notification.FieldError{Field: "to", Reason: "must be RFC3339"})
		} else {
			filter.To = &parsed
		}
	}

	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		details = append(details, notification.FieldError{Field: "to", Reason: "must be after from"})
	}

	if value := query.Get("page"); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil || page < 1 {
			details = append(details, notification.FieldError{Field: "page", Reason: "must be a positive integer"})
		} else {
			filter.Page = page
		}
	}

	if value := query.Get("page_size"); value != "" {
		size, err := strconv.Atoi(value)
		if err != nil || size < 1 {
			details = append(details, notification.FieldError{Field: "page_size", Reason: "must be a positive integer"})
		} else {
			filter.PageSize = size
		}
	}

	if len(details) > 0 {
		return notification.ListFilter{}, &notification.ValidationError{Details: details}
	}

	filter.Normalize()
	return filter, nil
}

func fillBatchLinks(batch *notification.Batch) {
	batch.StatusURL = "/api/v1/batches/" + batch.ID.String()
	batch.NotificationsURL = "/api/v1/notifications?batch_id=" + batch.ID.String()
}

type apiErrorResponse struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string                    `json:"code"`
	Message string                    `json:"message"`
	Details []notification.FieldError `json:"details"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details []notification.FieldError) {
	if details == nil {
		details = []notification.FieldError{}
	}
	writeJSON(w, status, apiErrorResponse{
		Error: apiErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
