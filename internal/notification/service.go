package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence boundary used by the application service.
type Store interface {
	Create(ctx context.Context, n *Notification, detail []byte, idem *IdempotencyRecord) (*Notification, error)
	CreateBatch(ctx context.Context, batch *Batch, items []*Notification, idem *IdempotencyRecord) (*Batch, error)
	Get(ctx context.Context, id uuid.UUID) (*Notification, error)
	List(ctx context.Context, filter ListFilter) ([]Notification, int, error)
	Cancel(ctx context.Context, id uuid.UUID) (*Notification, error)
	GetBatch(ctx context.Context, id uuid.UUID) (*Batch, error)
	GetIdempotency(ctx context.Context, scope, key string) (*IdempotencyRecord, error)
	MarkQueued(ctx context.Context, id uuid.UUID) (*Notification, error)
}

// Enqueuer submits a persisted notification to the delivery queue.
type Enqueuer interface {
	Enqueue(ctx context.Context, id uuid.UUID, priority Priority, correlationID string, scheduledAt *time.Time) error
}

var _ Store = (*Repository)(nil)

// Service applies validation, id generation, idempotency, and enqueue rules.
type Service struct {
	store       Store
	enqueuer    Enqueuer
	logger      *slog.Logger
	maxAttempts int
	now         func() time.Time
}

// NewService creates the notification application service.
func NewService(store Store, maxAttempts int, now func() time.Time) *Service {
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:       store,
		logger:      slog.Default(),
		maxAttempts: maxAttempts,
		now:         now,
	}
}

// WithEnqueuer enables queue submission after persistence.
func (s *Service) WithEnqueuer(enqueuer Enqueuer) *Service {
	s.enqueuer = enqueuer
	return s
}

// WithLogger sets the service logger.
func (s *Service) WithLogger(logger *slog.Logger) *Service {
	if logger != nil {
		s.logger = logger
	}
	return s
}

// Create validates and persists one notification.
// The boolean result reports an idempotent replay.
func (s *Service) Create(
	ctx context.Context,
	input CreateInput,
	idempotencyKey string,
	correlationID string,
) (*Notification, bool, error) {
	now := s.now().UTC()

	validated, err := ValidateCreate(input, now)
	if err != nil {
		return nil, false, err
	}

	key := strings.TrimSpace(idempotencyKey)
	hash := hashValue(validated)

	if key != "" {
		record, err := s.store.GetIdempotency(ctx, ScopeSingle, key)
		if err == nil {
			return s.replaySingle(ctx, record, hash)
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, false, fmt.Errorf("generate notification id: %w", err)
	}

	status := StatusPending
	if validated.ScheduledAt != nil {
		status = StatusScheduled
	}

	n := &Notification{
		ID:            id,
		Recipient:     validated.Recipient,
		Channel:       validated.Channel,
		Subject:       stringPointer(validated.Subject),
		Content:       validated.Content,
		Priority:      validated.Priority,
		Status:        status,
		AttemptCount:  0,
		MaxAttempts:   s.maxAttempts,
		ScheduledAt:   validated.ScheduledAt,
		CorrelationID: correlationID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var idem *IdempotencyRecord
	if key != "" {
		idem = &IdempotencyRecord{
			Scope:          ScopeSingle,
			Key:            key,
			RequestHash:    hash,
			State:          IdempotencyCompleted,
			ResourceID:     &n.ID,
			ResponseStatus: 202,
		}
	}

	created, err := s.store.Create(ctx, n, []byte(`{"source":"api"}`), idem)
	if err != nil {
		if key != "" && IsIdempotencyConflict(err) {
			record, getErr := s.store.GetIdempotency(ctx, ScopeSingle, key)
			if getErr != nil {
				return nil, false, getErr
			}
			return s.replaySingle(ctx, record, hash)
		}
		return nil, false, err
	}

	return s.enqueueSingle(ctx, created)
}

// CreateBatch validates and persists a batch of notifications.
// The boolean result reports an idempotent replay.
func (s *Service) CreateBatch(
	ctx context.Context,
	input BatchInput,
	idempotencyKey string,
	correlationID string,
) (*Batch, bool, error) {
	now := s.now().UTC()

	validated, err := ValidateBatch(input, now)
	if err != nil {
		return nil, false, err
	}

	key := strings.TrimSpace(idempotencyKey)
	hash := hashValue(validated)

	if key != "" {
		record, err := s.store.GetIdempotency(ctx, ScopeBatch, key)
		if err == nil {
			return s.replayBatch(ctx, record, hash)
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
	}

	batchID, err := uuid.NewV7()
	if err != nil {
		return nil, false, fmt.Errorf("generate batch id: %w", err)
	}

	batch := &Batch{
		ID:        batchID,
		Total:     len(validated),
		Status:    BatchStatusPending,
		Counts:    map[Status]int{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	items := make([]*Notification, 0, len(validated))
	for _, input := range validated {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, false, fmt.Errorf("generate notification id: %w", err)
		}

		status := StatusPending
		if input.ScheduledAt != nil {
			status = StatusScheduled
		}
		batch.Counts[status]++

		items = append(items, &Notification{
			ID:            id,
			BatchID:       &batch.ID,
			Recipient:     input.Recipient,
			Channel:       input.Channel,
			Subject:       stringPointer(input.Subject),
			Content:       input.Content,
			Priority:      input.Priority,
			Status:        status,
			AttemptCount:  0,
			MaxAttempts:   s.maxAttempts,
			ScheduledAt:   input.ScheduledAt,
			CorrelationID: correlationID,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}

	var idem *IdempotencyRecord
	if key != "" {
		idem = &IdempotencyRecord{
			Scope:          ScopeBatch,
			Key:            key,
			RequestHash:    hash,
			State:          IdempotencyCompleted,
			ResourceID:     &batch.ID,
			ResponseStatus: 202,
		}
	}

	created, err := s.store.CreateBatch(ctx, batch, items, idem)
	if err != nil {
		if key != "" && IsIdempotencyConflict(err) {
			record, getErr := s.store.GetIdempotency(ctx, ScopeBatch, key)
			if getErr != nil {
				return nil, false, getErr
			}
			return s.replayBatch(ctx, record, hash)
		}
		return nil, false, err
	}

	return s.enqueueBatch(ctx, created, items)
}

// Get returns one notification.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Notification, error) {
	return s.store.Get(ctx, id)
}

// List returns a page of notifications and the total count.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Notification, int, error) {
	filter.Normalize()
	return s.store.List(ctx, filter)
}

// Cancel cancels a pending or scheduled notification.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (*Notification, error) {
	return s.store.Cancel(ctx, id)
}

// GetBatch returns a batch summary.
func (s *Service) GetBatch(ctx context.Context, id uuid.UUID) (*Batch, error) {
	return s.store.GetBatch(ctx, id)
}

func (s *Service) enqueueSingle(ctx context.Context, n *Notification) (*Notification, bool, error) {
	if s.enqueuer == nil {
		return n, false, nil
	}

	if err := s.enqueuer.Enqueue(ctx, n.ID, n.Priority, n.CorrelationID, n.ScheduledAt); err != nil {
		s.logger.Warn("enqueue failed; reconciler will retry",
			"notification_id", n.ID,
			"error", err,
		)
		return n, false, nil
	}

	if n.ScheduledAt != nil {
		return n, false, nil
	}

	queued, err := s.store.MarkQueued(ctx, n.ID)
	if err != nil {
		s.logger.Warn("failed to mark notification queued",
			"notification_id", n.ID,
			"error", err,
		)
		return n, false, nil
	}

	return queued, false, nil
}

func (s *Service) enqueueBatch(
	ctx context.Context,
	batch *Batch,
	items []*Notification,
) (*Batch, bool, error) {
	if s.enqueuer == nil {
		return batch, false, nil
	}

	counts := make(map[Status]int)
	for _, item := range items {
		if err := s.enqueuer.Enqueue(ctx, item.ID, item.Priority, item.CorrelationID, item.ScheduledAt); err != nil {
			s.logger.Warn("batch enqueue failed; reconciler will retry",
				"notification_id", item.ID,
				"batch_id", batch.ID,
				"error", err,
			)
			counts[StatusPending]++
			continue
		}

		if item.ScheduledAt != nil {
			counts[StatusScheduled]++
			continue
		}

		queued, err := s.store.MarkQueued(ctx, item.ID)
		if err != nil {
			s.logger.Warn("failed to mark batch item queued",
				"notification_id", item.ID,
				"batch_id", batch.ID,
				"error", err,
			)
			counts[StatusPending]++
			continue
		}
		counts[queued.Status]++
	}

	batch.Counts = counts
	batch.Status = DeriveBatchStatus(counts, batch.Total)
	return batch, false, nil
}

func (s *Service) replaySingle(
	ctx context.Context,
	record *IdempotencyRecord,
	hash string,
) (*Notification, bool, error) {
	if record.RequestHash != hash {
		return nil, false, ErrIdempotencyConflict
	}
	if record.State == IdempotencyCompleted && record.ResourceID != nil {
		n, err := s.store.Get(ctx, *record.ResourceID)
		return n, true, err
	}
	return nil, false, ErrIdempotencyInProgress
}

func (s *Service) replayBatch(
	ctx context.Context,
	record *IdempotencyRecord,
	hash string,
) (*Batch, bool, error) {
	if record.RequestHash != hash {
		return nil, false, ErrIdempotencyConflict
	}
	if record.State == IdempotencyCompleted && record.ResourceID != nil {
		batch, err := s.store.GetBatch(ctx, *record.ResourceID)
		return batch, true, err
	}
	return nil, false, ErrIdempotencyInProgress
}

func hashValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
