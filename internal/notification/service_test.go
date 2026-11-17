package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	createFn            func(context.Context, *Notification, []byte, *IdempotencyRecord) (*Notification, error)
	createBatchFn       func(context.Context, *Batch, []*Notification, *IdempotencyRecord) (*Batch, error)
	getFn               func(context.Context, uuid.UUID) (*Notification, error)
	listFn              func(context.Context, ListFilter) ([]Notification, int, error)
	cancelFn            func(context.Context, uuid.UUID) (*Notification, error)
	getBatchFn          func(context.Context, uuid.UUID) (*Batch, error)
	getIdempotencyFn    func(context.Context, string, string) (*IdempotencyRecord, error)
	markQueuedFn        func(context.Context, uuid.UUID) (*Notification, error)
	retryFn             func(context.Context, uuid.UUID) (*Notification, error)
	statsFn             func(context.Context, time.Time) (Stats, error)
	createTemplateFn    func(context.Context, *Template) (*Template, error)
	getTemplateFn       func(context.Context, uuid.UUID) (*Template, error)
	getTemplateByNameFn func(context.Context, string) (*Template, error)
	listTemplatesFn     func(context.Context) ([]Template, error)
}

func (f *fakeStore) Create(ctx context.Context, n *Notification, detail []byte, idem *IdempotencyRecord) (*Notification, error) {
	if f.createFn == nil {
		return nil, errors.New("unexpected Create call")
	}
	return f.createFn(ctx, n, detail, idem)
}

func (f *fakeStore) CreateBatch(ctx context.Context, batch *Batch, items []*Notification, idem *IdempotencyRecord) (*Batch, error) {
	if f.createBatchFn == nil {
		return nil, errors.New("unexpected CreateBatch call")
	}
	return f.createBatchFn(ctx, batch, items, idem)
}

func (f *fakeStore) Get(ctx context.Context, id uuid.UUID) (*Notification, error) {
	if f.getFn == nil {
		return nil, errors.New("unexpected Get call")
	}
	return f.getFn(ctx, id)
}

func (f *fakeStore) List(ctx context.Context, filter ListFilter) ([]Notification, int, error) {
	if f.listFn == nil {
		return nil, 0, errors.New("unexpected List call")
	}
	return f.listFn(ctx, filter)
}

func (f *fakeStore) Cancel(ctx context.Context, id uuid.UUID) (*Notification, error) {
	if f.cancelFn == nil {
		return nil, errors.New("unexpected Cancel call")
	}
	return f.cancelFn(ctx, id)
}

func (f *fakeStore) GetBatch(ctx context.Context, id uuid.UUID) (*Batch, error) {
	if f.getBatchFn == nil {
		return nil, errors.New("unexpected GetBatch call")
	}
	return f.getBatchFn(ctx, id)
}

func (f *fakeStore) GetIdempotency(ctx context.Context, scope, key string) (*IdempotencyRecord, error) {
	if f.getIdempotencyFn == nil {
		return nil, ErrNotFound
	}
	return f.getIdempotencyFn(ctx, scope, key)
}

func (f *fakeStore) MarkQueued(ctx context.Context, id uuid.UUID) (*Notification, error) {
	if f.markQueuedFn == nil {
		return nil, errors.New("unexpected MarkQueued call")
	}
	return f.markQueuedFn(ctx, id)
}

func (f *fakeStore) Retry(ctx context.Context, id uuid.UUID) (*Notification, error) {
	if f.retryFn == nil {
		return nil, errors.New("unexpected Retry call")
	}
	return f.retryFn(ctx, id)
}

func (f *fakeStore) Stats(ctx context.Context, since time.Time) (Stats, error) {
	if f.statsFn == nil {
		return Stats{}, nil
	}
	return f.statsFn(ctx, since)
}

func (f *fakeStore) CreateTemplate(ctx context.Context, template *Template) (*Template, error) {
	if f.createTemplateFn == nil {
		return nil, errors.New("unexpected CreateTemplate call")
	}
	return f.createTemplateFn(ctx, template)
}

func (f *fakeStore) GetTemplate(ctx context.Context, id uuid.UUID) (*Template, error) {
	if f.getTemplateFn == nil {
		return nil, errors.New("unexpected GetTemplate call")
	}
	return f.getTemplateFn(ctx, id)
}

func (f *fakeStore) GetTemplateByName(ctx context.Context, name string) (*Template, error) {
	if f.getTemplateByNameFn == nil {
		return nil, ErrTemplateNotFound
	}
	return f.getTemplateByNameFn(ctx, name)
}

func (f *fakeStore) ListTemplates(ctx context.Context) ([]Template, error) {
	if f.listTemplatesFn == nil {
		return []Template{}, nil
	}
	return f.listTemplatesFn(ctx)
}

type fakeEnqueuer struct {
	enqueueFn      func(context.Context, uuid.UUID, Priority, string, *time.Time) error
	enqueueRetryFn func(context.Context, uuid.UUID, Priority, string) error
	calls          int
	retryCalls     int
}

func (f *fakeEnqueuer) Enqueue(
	ctx context.Context,
	id uuid.UUID,
	priority Priority,
	correlationID string,
	scheduledAt *time.Time,
) error {
	f.calls++
	if f.enqueueFn == nil {
		return nil
	}
	return f.enqueueFn(ctx, id, priority, correlationID, scheduledAt)
}

func (f *fakeEnqueuer) EnqueueRetry(
	ctx context.Context,
	id uuid.UUID,
	priority Priority,
	correlationID string,
) error {
	f.retryCalls++
	if f.enqueueRetryFn == nil {
		return nil
	}
	return f.enqueueRetryFn(ctx, id, priority, correlationID)
}

func fixedNow() time.Time {
	return time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC)
}

func TestServiceCreate(t *testing.T) {
	var (
		captured *Notification
		idem     *IdempotencyRecord
	)

	store := &fakeStore{
		createFn: func(_ context.Context, n *Notification, _ []byte, record *IdempotencyRecord) (*Notification, error) {
			captured = n
			idem = record
			return n, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	n, replayed, err := service.Create(context.Background(), validSMS(), "", "req-1")
	require.NoError(t, err)
	assert.False(t, replayed)
	require.NotNil(t, captured)
	assert.Equal(t, StatusPending, n.Status)
	assert.Equal(t, PriorityNormal, n.Priority)
	assert.Equal(t, 5, n.MaxAttempts)
	assert.Equal(t, "req-1", n.CorrelationID)
	assert.Equal(t, fixedNow(), n.CreatedAt)
	assert.Nil(t, idem)
}

func TestServiceCreateEnqueues(t *testing.T) {
	store := &fakeStore{
		createFn: func(_ context.Context, n *Notification, _ []byte, _ *IdempotencyRecord) (*Notification, error) {
			return n, nil
		},
		markQueuedFn: func(_ context.Context, id uuid.UUID) (*Notification, error) {
			return &Notification{ID: id, Status: StatusQueued}, nil
		},
	}
	enqueuer := &fakeEnqueuer{}
	service := NewService(store, 5, fixedNow).WithEnqueuer(enqueuer)

	n, replayed, err := service.Create(context.Background(), validSMS(), "", "req-1")
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, 1, enqueuer.calls)
	assert.Equal(t, StatusQueued, n.Status)
}

func TestServiceCreateEnqueueFailureLeavesPending(t *testing.T) {
	store := &fakeStore{
		createFn: func(_ context.Context, n *Notification, _ []byte, _ *IdempotencyRecord) (*Notification, error) {
			return n, nil
		},
	}
	enqueuer := &fakeEnqueuer{
		enqueueFn: func(context.Context, uuid.UUID, Priority, string, *time.Time) error {
			return errors.New("redis down")
		},
	}
	service := NewService(store, 5, fixedNow).WithEnqueuer(enqueuer)

	n, replayed, err := service.Create(context.Background(), validSMS(), "", "req-1")
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, StatusPending, n.Status)
}

func TestServiceCreateWithTemplate(t *testing.T) {
	template := &Template{
		Name:    "welcome",
		Channel: ChannelSMS,
		Content: "Hello {{name}}",
	}
	var captured *Notification

	store := &fakeStore{
		getTemplateByNameFn: func(context.Context, string) (*Template, error) {
			return template, nil
		},
		createFn: func(_ context.Context, n *Notification, _ []byte, _ *IdempotencyRecord) (*Notification, error) {
			captured = n
			return n, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	n, replayed, err := service.Create(context.Background(), CreateInput{
		Recipient: "+15555550100",
		Channel:   ChannelSMS,
		Template:  "welcome",
		Variables: map[string]string{"name": "Onat"},
	}, "", "req-1")

	require.NoError(t, err)
	assert.False(t, replayed)
	require.NotNil(t, captured)
	assert.Equal(t, "Hello Onat", n.Content)
	assert.Equal(t, "Hello Onat", captured.Content)
}

func TestServiceCreateMissingTemplate(t *testing.T) {
	store := &fakeStore{
		getTemplateByNameFn: func(context.Context, string) (*Template, error) {
			return nil, ErrTemplateNotFound
		},
	}
	service := NewService(store, 5, fixedNow)

	_, _, err := service.Create(context.Background(), CreateInput{
		Recipient: "+15555550100",
		Channel:   ChannelSMS,
		Template:  "missing",
	}, "", "req-1")

	require.ErrorIs(t, err, ErrTemplateNotFound)
}

func TestServiceCreateTemplateMissingVariable(t *testing.T) {
	store := &fakeStore{
		getTemplateByNameFn: func(context.Context, string) (*Template, error) {
			return &Template{Name: "welcome", Channel: ChannelSMS, Content: "Hello {{name}}"}, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	_, _, err := service.Create(context.Background(), CreateInput{
		Recipient: "+15555550100",
		Channel:   ChannelSMS,
		Template:  "welcome",
	}, "", "req-1")

	require.Error(t, err)
	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	assert.Contains(t, fieldNames(validationErr), "variables.name")
}

func TestServiceRetry(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		retryFn: func(_ context.Context, id uuid.UUID) (*Notification, error) {
			return &Notification{ID: id, Status: StatusPending, Priority: PriorityHigh, CorrelationID: "req-1"}, nil
		},
		markQueuedFn: func(_ context.Context, id uuid.UUID) (*Notification, error) {
			return &Notification{ID: id, Status: StatusQueued}, nil
		},
	}
	enqueuer := &fakeEnqueuer{}
	service := NewService(store, 5, fixedNow).WithEnqueuer(enqueuer)

	n, err := service.Retry(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusQueued, n.Status)
	assert.Equal(t, 1, enqueuer.retryCalls)
}

func TestServiceRetryNotRetryable(t *testing.T) {
	store := &fakeStore{
		retryFn: func(context.Context, uuid.UUID) (*Notification, error) {
			return nil, ErrNotRetryable
		},
	}
	service := NewService(store, 5, fixedNow)

	_, err := service.Retry(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotRetryable)
}

func TestServiceCreateTemplate(t *testing.T) {
	store := &fakeStore{
		createTemplateFn: func(_ context.Context, template *Template) (*Template, error) {
			return template, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	template, err := service.CreateTemplate(context.Background(), TemplateInput{
		Name:    "welcome",
		Channel: ChannelSMS,
		Content: "Hello",
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, template.ID)
	assert.Equal(t, "welcome", template.Name)
	assert.Equal(t, ChannelSMS, template.Channel)
	assert.Nil(t, template.Subject)
	assert.Equal(t, fixedNow(), template.CreatedAt)
}

func TestServiceCreateIdempotentReplay(t *testing.T) {
	input := validSMS()
	validated, err := ValidateCreate(input, fixedNow())
	require.NoError(t, err)

	resourceID := uuid.New()
	record := &IdempotencyRecord{
		Scope:       ScopeSingle,
		Key:         "key-1",
		RequestHash: hashValue(validated),
		State:       IdempotencyCompleted,
		ResourceID:  &resourceID,
	}

	store := &fakeStore{
		getIdempotencyFn: func(context.Context, string, string) (*IdempotencyRecord, error) {
			return record, nil
		},
		getFn: func(context.Context, uuid.UUID) (*Notification, error) {
			return &Notification{ID: resourceID, Status: StatusPending}, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	n, replayed, err := service.Create(context.Background(), input, "key-1", "req-1")
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, resourceID, n.ID)
}

func TestServiceCreateIdempotencyConflict(t *testing.T) {
	input := validSMS()
	resourceID := uuid.New()
	record := &IdempotencyRecord{
		Scope:       ScopeSingle,
		Key:         "key-1",
		RequestHash: "different-hash",
		State:       IdempotencyCompleted,
		ResourceID:  &resourceID,
	}

	store := &fakeStore{
		getIdempotencyFn: func(context.Context, string, string) (*IdempotencyRecord, error) {
			return record, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	_, _, err := service.Create(context.Background(), input, "key-1", "req-1")
	require.ErrorIs(t, err, ErrIdempotencyConflict)
}

func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, 5, fixedNow)

	_, _, err := service.Create(context.Background(), CreateInput{}, "", "req-1")
	require.Error(t, err)

	var validationErr *ValidationError
	assert.True(t, errors.As(err, &validationErr))
}

func TestServiceCreateBatch(t *testing.T) {
	var (
		capturedBatch *Batch
		capturedItems []*Notification
	)

	store := &fakeStore{
		createBatchFn: func(_ context.Context, batch *Batch, items []*Notification, _ *IdempotencyRecord) (*Batch, error) {
			capturedBatch = batch
			capturedItems = items
			return batch, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	batch, replayed, err := service.CreateBatch(context.Background(), BatchInput{
		Notifications: []CreateInput{validSMS(), validSMS()},
	}, "", "req-1")
	require.NoError(t, err)
	assert.False(t, replayed)
	require.NotNil(t, capturedBatch)
	assert.Len(t, capturedItems, 2)
	assert.Equal(t, 2, batch.Total)
	assert.Equal(t, BatchStatusPending, batch.Status)
	assert.Equal(t, 2, batch.Counts[StatusPending])

	for _, item := range capturedItems {
		require.NotNil(t, item.BatchID)
		assert.Equal(t, batch.ID, *item.BatchID)
		assert.Equal(t, StatusPending, item.Status)
	}
}

func TestServiceCreateBatchEnqueues(t *testing.T) {
	store := &fakeStore{
		createBatchFn: func(_ context.Context, batch *Batch, _ []*Notification, _ *IdempotencyRecord) (*Batch, error) {
			return batch, nil
		},
		markQueuedFn: func(_ context.Context, id uuid.UUID) (*Notification, error) {
			return &Notification{ID: id, Status: StatusQueued}, nil
		},
	}
	enqueuer := &fakeEnqueuer{}
	service := NewService(store, 5, fixedNow).WithEnqueuer(enqueuer)

	batch, replayed, err := service.CreateBatch(context.Background(), BatchInput{
		Notifications: []CreateInput{validSMS(), validSMS()},
	}, "", "req-1")
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, 2, enqueuer.calls)
	assert.Equal(t, BatchStatusProcessing, batch.Status)
	assert.Equal(t, 2, batch.Counts[StatusQueued])
}

func TestServiceCreateBatchIdempotentReplay(t *testing.T) {
	input := BatchInput{Notifications: []CreateInput{validSMS()}}
	validated, err := ValidateBatch(input, fixedNow())
	require.NoError(t, err)

	resourceID := uuid.New()
	record := &IdempotencyRecord{
		Scope:       ScopeBatch,
		Key:         "batch-key",
		RequestHash: hashValue(validated),
		State:       IdempotencyCompleted,
		ResourceID:  &resourceID,
	}

	store := &fakeStore{
		getIdempotencyFn: func(context.Context, string, string) (*IdempotencyRecord, error) {
			return record, nil
		},
		getBatchFn: func(context.Context, uuid.UUID) (*Batch, error) {
			return &Batch{ID: resourceID, Total: 1, Status: BatchStatusPending}, nil
		},
	}
	service := NewService(store, 5, fixedNow)

	batch, replayed, err := service.CreateBatch(context.Background(), input, "batch-key", "req-1")
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, resourceID, batch.ID)
}
