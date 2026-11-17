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
	createFn         func(context.Context, *Notification, []byte, *IdempotencyRecord) (*Notification, error)
	createBatchFn    func(context.Context, *Batch, []*Notification, *IdempotencyRecord) (*Batch, error)
	getFn            func(context.Context, uuid.UUID) (*Notification, error)
	listFn           func(context.Context, ListFilter) ([]Notification, int, error)
	cancelFn         func(context.Context, uuid.UUID) (*Notification, error)
	getBatchFn       func(context.Context, uuid.UUID) (*Batch, error)
	getIdempotencyFn func(context.Context, string, string) (*IdempotencyRecord, error)
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
