package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
)

var testNotificationID = uuid.MustParse("0193b2f4-7c1a-7d3e-9a10-5f8c2d6e4b71")

type fakeNotificationService struct {
	createFn      func(context.Context, notification.CreateInput, string, string) (*notification.Notification, bool, error)
	createBatchFn func(context.Context, notification.BatchInput, string, string) (*notification.Batch, bool, error)
	getFn         func(context.Context, uuid.UUID) (*notification.Notification, error)
	listFn        func(context.Context, notification.ListFilter) ([]notification.Notification, int, error)
	cancelFn      func(context.Context, uuid.UUID) (*notification.Notification, error)
	getBatchFn    func(context.Context, uuid.UUID) (*notification.Batch, error)
	statsFn       func(context.Context, time.Time) (notification.Stats, error)
}

func (f *fakeNotificationService) Create(
	ctx context.Context,
	input notification.CreateInput,
	key string,
	correlationID string,
) (*notification.Notification, bool, error) {
	if f.createFn == nil {
		return nil, false, errors.New("unexpected Create call")
	}
	return f.createFn(ctx, input, key, correlationID)
}

func (f *fakeNotificationService) CreateBatch(
	ctx context.Context,
	input notification.BatchInput,
	key string,
	correlationID string,
) (*notification.Batch, bool, error) {
	if f.createBatchFn == nil {
		return nil, false, errors.New("unexpected CreateBatch call")
	}
	return f.createBatchFn(ctx, input, key, correlationID)
}

func (f *fakeNotificationService) Get(ctx context.Context, id uuid.UUID) (*notification.Notification, error) {
	if f.getFn == nil {
		return nil, errors.New("unexpected Get call")
	}
	return f.getFn(ctx, id)
}

func (f *fakeNotificationService) List(
	ctx context.Context,
	filter notification.ListFilter,
) ([]notification.Notification, int, error) {
	if f.listFn == nil {
		return nil, 0, errors.New("unexpected List call")
	}
	return f.listFn(ctx, filter)
}

func (f *fakeNotificationService) Cancel(ctx context.Context, id uuid.UUID) (*notification.Notification, error) {
	if f.cancelFn == nil {
		return nil, errors.New("unexpected Cancel call")
	}
	return f.cancelFn(ctx, id)
}

func (f *fakeNotificationService) GetBatch(ctx context.Context, id uuid.UUID) (*notification.Batch, error) {
	if f.getBatchFn == nil {
		return nil, errors.New("unexpected GetBatch call")
	}
	return f.getBatchFn(ctx, id)
}

func (f *fakeNotificationService) Stats(ctx context.Context, since time.Time) (notification.Stats, error) {
	if f.statsFn == nil {
		return notification.Stats{}, errors.New("unexpected Stats call")
	}
	return f.statsFn(ctx, since)
}

func testRouter(service NotificationService) http.Handler {
	return NewRouter(Dependencies{
		Logger: testLogger(),
		Readiness: ReadinessChecks{
			Postgres: func(context.Context) error { return nil },
			Redis:    func(context.Context) error { return nil },
		},
		Notifications: service,
	})
}

func TestCreateNotification(t *testing.T) {
	service := &fakeNotificationService{
		createFn: func(_ context.Context, input notification.CreateInput, key, _ string) (*notification.Notification, bool, error) {
			require.Equal(t, "+15555550100", input.Recipient)
			require.Equal(t, notification.ChannelSMS, input.Channel)
			require.Equal(t, "demo-key", key)
			return &notification.Notification{ID: testNotificationID, Status: notification.StatusPending}, false, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications",
		strings.NewReader(`{"recipient":"+15555550100","channel":"sms","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "demo-key")
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "/api/v1/notifications/"+testNotificationID.String(), rec.Header().Get("Location"))
	assert.Contains(t, rec.Body.String(), testNotificationID.String())
}

func TestCreateNotificationReplayHeader(t *testing.T) {
	service := &fakeNotificationService{
		createFn: func(context.Context, notification.CreateInput, string, string) (*notification.Notification, bool, error) {
			return &notification.Notification{ID: testNotificationID, Status: notification.StatusPending}, true, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications",
		strings.NewReader(`{"recipient":"+15555550100","channel":"sms","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "true", rec.Header().Get("Idempotent-Replayed"))
}

func TestCreateNotificationValidationError(t *testing.T) {
	service := &fakeNotificationService{
		createFn: func(context.Context, notification.CreateInput, string, string) (*notification.Notification, bool, error) {
			return nil, false, &notification.ValidationError{Details: []notification.FieldError{
				{Field: "recipient", Reason: "must be a valid E.164 phone number"},
			}}
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications",
		strings.NewReader(`{"recipient":"bad","channel":"sms","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"validation_error"`)
	assert.Contains(t, rec.Body.String(), `"field":"recipient"`)
}

func TestCreateNotificationRejectsLongIdempotencyKey(t *testing.T) {
	service := &fakeNotificationService{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications",
		strings.NewReader(`{"recipient":"+15555550100","channel":"sms","content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("a", 256))
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Idempotency-Key")
}

func TestListNotifications(t *testing.T) {
	service := &fakeNotificationService{
		listFn: func(_ context.Context, filter notification.ListFilter) ([]notification.Notification, int, error) {
			require.NotNil(t, filter.Status)
			require.Equal(t, notification.StatusPending, *filter.Status)
			require.NotNil(t, filter.Channel)
			require.Equal(t, notification.ChannelSMS, *filter.Channel)
			require.Equal(t, 2, filter.Page)
			require.Equal(t, 5, filter.PageSize)
			return []notification.Notification{{ID: testNotificationID}}, 7, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?status=pending&channel=sms&page=2&page_size=5", nil)
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"total":7`)
	assert.Contains(t, rec.Body.String(), `"total_pages":2`)
}

func TestGetNotificationRejectsInvalidID(t *testing.T) {
	service := &fakeNotificationService{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/not-a-uuid", nil)
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), `"field":"id"`)
}

func TestCancelNotificationConflict(t *testing.T) {
	service := &fakeNotificationService{
		cancelFn: func(context.Context, uuid.UUID) (*notification.Notification, error) {
			return nil, notification.ErrNotCancellable
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+testNotificationID.String()+"/cancel", nil)
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"not_cancellable"`)
}

type fakeQueueDepth struct {
	depth map[string]int
}

func (f fakeQueueDepth) Depth(context.Context) map[string]int {
	return f.depth
}

func TestStats(t *testing.T) {
	service := &fakeNotificationService{
		statsFn: func(context.Context, time.Time) (notification.Stats, error) {
			return notification.Stats{
				Counts: map[notification.Status]int{
					notification.StatusAccepted: 10,
					notification.StatusFailed:   1,
				},
				SuccessRate:  0.9,
				P95LatencyMS: 150,
			}, nil
		},
	}

	router := NewRouter(Dependencies{
		Logger:        testLogger(),
		Readiness:     ReadinessChecks{},
		Notifications: service,
		QueueDepth:    fakeQueueDepth{depth: map[string]int{"high": 3, "normal": 2}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"window":"5m"`)
	assert.Contains(t, rec.Body.String(), `"high":3`)
	assert.Contains(t, rec.Body.String(), `"success_rate":0.9`)
}

func TestCreateBatch(t *testing.T) {
	service := &fakeNotificationService{
		createBatchFn: func(_ context.Context, input notification.BatchInput, _, _ string) (*notification.Batch, bool, error) {
			require.Len(t, input.Notifications, 2)
			return &notification.Batch{
				ID:     testNotificationID,
				Total:  2,
				Status: notification.BatchStatusPending,
				Counts: map[notification.Status]int{notification.StatusPending: 2},
			}, false, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/batch",
		strings.NewReader(`{"notifications":[
			{"recipient":"+15555550100","channel":"sms","content":"one"},
			{"recipient":"onat@example.com","channel":"email","subject":"Two","content":"two"}
		]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testRouter(service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status_url":"/api/v1/batches/`+testNotificationID.String()+`"`)
}
