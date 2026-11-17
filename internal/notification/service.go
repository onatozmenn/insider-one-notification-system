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
	DeleteExpiredIdempotency(ctx context.Context) (int64, error)
	MarkQueued(ctx context.Context, id uuid.UUID) (*Notification, error)
	Retry(ctx context.Context, id uuid.UUID) (*Notification, error)
	Stats(ctx context.Context, since time.Time) (Stats, error)
	CreateTemplate(ctx context.Context, template *Template) (*Template, error)
	GetTemplate(ctx context.Context, id uuid.UUID) (*Template, error)
	GetTemplateByName(ctx context.Context, name string) (*Template, error)
	ListTemplates(ctx context.Context) ([]Template, error)
}

// Enqueuer submits a persisted notification to the delivery queue.
type Enqueuer interface {
	Enqueue(ctx context.Context, id uuid.UUID, priority Priority, correlationID string, scheduledAt *time.Time) error
	EnqueueRetry(ctx context.Context, id uuid.UUID, priority Priority, correlationID string) error
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

// resolveTemplate renders a template reference into subject and content.
func (s *Service) resolveTemplate(ctx context.Context, input CreateInput) (CreateInput, error) {
	if input.Template == "" {
		return input, nil
	}

	template, err := s.store.GetTemplateByName(ctx, input.Template)
	if errors.Is(err, ErrTemplateNotFound) {
		return CreateInput{}, ErrTemplateNotFound
	}
	if err != nil {
		return CreateInput{}, err
	}

	channel := Channel(strings.ToLower(strings.TrimSpace(string(input.Channel))))
	if channel != template.Channel {
		return CreateInput{}, &ValidationError{Details: []FieldError{{
			Field:  "channel",
			Reason: "must match the template channel",
		}}}
	}

	rendered, err := RenderTemplate(*template, input.Variables)
	if err != nil {
		return CreateInput{}, err
	}

	input.Subject = rendered.Subject
	input.Content = rendered.Content
	input.Template = ""
	input.Variables = nil
	return input, nil
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

	input, err := s.resolveTemplate(ctx, input)
	if err != nil {
		return nil, false, err
	}

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

	if err := validateBatchSize(len(input.Notifications)); err != nil {
		return nil, false, err
	}

	// Render every template before validation so a template-only item passes
	// channel validation and the batch is still all-or-nothing.
	resolved := make([]CreateInput, len(input.Notifications))
	var errs []FieldError
	for i, item := range input.Notifications {
		value, err := s.resolveTemplate(ctx, item)
		if err != nil {
			var validationErr *ValidationError
			switch {
			case errors.Is(err, ErrTemplateNotFound):
				errs = append(errs, FieldError{
					Field:  fmt.Sprintf("notifications[%d].template", i),
					Reason: "does not exist",
				})
			case errors.As(err, &validationErr):
				for _, detail := range validationErr.Details {
					errs = append(errs, FieldError{
						Field:  fmt.Sprintf("notifications[%d].%s", i, detail.Field),
						Reason: detail.Reason,
					})
				}
			default:
				return nil, false, err
			}
			continue
		}
		resolved[i] = value
	}
	if len(errs) > 0 {
		return nil, false, &ValidationError{Details: errs}
	}

	validated, err := ValidateBatch(BatchInput{Notifications: resolved}, now)
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

// Retry resets a failed or dead-letter notification and enqueues a fresh task.
func (s *Service) Retry(ctx context.Context, id uuid.UUID) (*Notification, error) {
	n, err := s.store.Retry(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.enqueuer == nil {
		return n, nil
	}

	if err := s.enqueuer.EnqueueRetry(ctx, n.ID, n.Priority, n.CorrelationID); err != nil {
		s.logger.Warn("retry enqueue failed; reconciler will retry",
			"notification_id", n.ID,
			"error", err,
		)
		return n, nil
	}

	queued, err := s.store.MarkQueued(ctx, n.ID)
	if err != nil {
		s.logger.Warn("failed to mark retried notification queued",
			"notification_id", n.ID,
			"error", err,
		)
		return n, nil
	}
	return queued, nil
}

// GetBatch returns a batch summary.
func (s *Service) GetBatch(ctx context.Context, id uuid.UUID) (*Batch, error) {
	return s.store.GetBatch(ctx, id)
}

// Stats returns delivery counts and latency since the cutoff.
func (s *Service) Stats(ctx context.Context, since time.Time) (Stats, error) {
	return s.store.Stats(ctx, since)
}

// CreateTemplate validates and persists a reusable template.
func (s *Service) CreateTemplate(ctx context.Context, input TemplateInput) (*Template, error) {
	validated, err := ValidateTemplate(input)
	if err != nil {
		return nil, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate template id: %w", err)
	}

	now := s.now().UTC()
	template := &Template{
		ID:        id,
		Name:      validated.Name,
		Channel:   validated.Channel,
		Subject:   stringPointer(validated.Subject),
		Content:   validated.Content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return s.store.CreateTemplate(ctx, template)
}

// GetTemplate returns one template.
func (s *Service) GetTemplate(ctx context.Context, id uuid.UUID) (*Template, error) {
	return s.store.GetTemplate(ctx, id)
}

// ListTemplates returns all templates.
func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	return s.store.ListTemplates(ctx)
}

// RunIdempotencyCleanup deletes expired idempotency keys until the context is
// canceled. Reads also delete their expired key, so this loop keeps the table
// from growing without bound.
func (s *Service) RunIdempotencyCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}

	cleanup := func() {
		deleted, err := s.store.DeleteExpiredIdempotency(ctx)
		if err != nil {
			s.logger.Warn("idempotency cleanup failed", "error", err)
			return
		}
		if deleted > 0 {
			s.logger.Info("expired idempotency keys deleted", "count", deleted)
		}
	}

	cleanup()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
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
