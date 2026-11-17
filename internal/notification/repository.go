package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const notificationColumns = `id, batch_id, recipient, channel, subject, content, priority, status,
	attempt_count, max_attempts, scheduled_at, next_attempt_at,
	provider_message_id, last_error, correlation_id, created_at, updated_at,
	accepted_at, cancelled_at`

const insertNotificationSQL = `INSERT INTO notifications (
		id, batch_id, recipient, channel, subject, content, priority, status,
		attempt_count, max_attempts, scheduled_at, next_attempt_at,
		provider_message_id, last_error, correlation_id, created_at, updated_at,
		accepted_at, cancelled_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	RETURNING ` + notificationColumns

const insertEventSQL = `INSERT INTO notification_events (notification_id, status, detail)
	VALUES ($1, $2, $3::jsonb)`

const insertIdempotencySQL = `INSERT INTO idempotency_keys (
		scope, key, request_hash, state, resource_id, response_status
	) VALUES ($1, $2, $3, $4, $5, $6)`

// Repository is the PostgreSQL store for notifications and batches.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a repository around a connection pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create inserts one notification and its first status event.
func (r *Repository) Create(
	ctx context.Context,
	n *Notification,
	detail []byte,
	idem *IdempotencyRecord,
) (*Notification, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	created, err := scanNotification(tx.QueryRow(ctx, insertNotificationSQL, notificationArgs(n)...))
	if err != nil {
		return nil, fmt.Errorf("insert notification: %w", err)
	}

	if err := insertEvent(ctx, tx, created.ID, created.Status, detail); err != nil {
		return nil, err
	}
	if idem != nil {
		if err := insertIdempotency(ctx, tx, idem); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create: %w", err)
	}

	return created, nil
}

// CreateBatch inserts a batch, its notifications, and their events atomically.
func (r *Repository) CreateBatch(
	ctx context.Context,
	batch *Batch,
	items []*Notification,
	idem *IdempotencyRecord,
) (*Batch, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin batch: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx,
		`INSERT INTO batches (id, total_count, created_at, updated_at) VALUES ($1, $2, $3, $4)`,
		batch.ID, batch.Total, batch.CreatedAt, batch.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert batch: %w", err)
	}

	queued := &pgx.Batch{}
	for _, n := range items {
		queued.Queue(insertNotificationSQL, notificationArgs(n)...)
		queued.Queue(insertEventSQL, n.ID, string(n.Status), `{"source":"api","batch":true}`)
	}

	results := tx.SendBatch(ctx, queued)
	for i := 0; i < len(items)*2; i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return nil, fmt.Errorf("insert batch item: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return nil, fmt.Errorf("close batch results: %w", err)
	}

	if idem != nil {
		if err := insertIdempotency(ctx, tx, idem); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}

	counts := make(map[Status]int)
	for _, n := range items {
		counts[n.Status]++
	}
	batch.Counts = counts
	batch.Status = DeriveBatchStatus(counts, len(items))

	return batch, nil
}

// Get returns one notification.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (*Notification, error) {
	row := r.db.QueryRow(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE id = $1`, id)
	n, err := scanNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get notification: %w", err)
	}
	return n, nil
}

// List returns a page of notifications and the total count.
func (r *Repository) List(ctx context.Context, filter ListFilter) ([]Notification, int, error) {
	filter.Normalize()

	where := []string{"1=1"}
	args := make([]any, 0, 6)

	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.Status != nil {
		add("status = $%d", string(*filter.Status))
	}
	if filter.Channel != nil {
		add("channel = $%d", string(*filter.Channel))
	}
	if filter.BatchID != nil {
		add("batch_id = $%d", *filter.BatchID)
	}
	if filter.From != nil {
		add("created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("created_at < $%d", *filter.To)
	}

	condition := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}

	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	args = append(args, filter.PageSize, filter.Offset())

	query := `SELECT ` + notificationColumns + ` FROM notifications WHERE ` + condition +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, limitArg, offsetArg)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	items := make([]Notification, 0, filter.PageSize)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		items = append(items, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate notifications: %w", err)
	}

	return items, total, nil
}

// Cancel moves a pending or scheduled notification to cancelled.
func (r *Repository) Cancel(ctx context.Context, id uuid.UUID) (*Notification, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	now := time.Now().UTC()
	row := tx.QueryRow(ctx,
		`UPDATE notifications
		 SET status = 'cancelled', cancelled_at = $2, updated_at = $2
		 WHERE id = $1 AND status IN ('pending', 'scheduled')
		 RETURNING `+notificationColumns,
		id, now,
	)

	n, err := scanNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM notifications WHERE id = $1`, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("read status: %w", err)
		}
		return nil, ErrNotCancellable
	}
	if err != nil {
		return nil, fmt.Errorf("cancel notification: %w", err)
	}

	if _, err := tx.Exec(ctx, insertEventSQL, n.ID, string(n.Status), `{"source":"api","action":"cancel"}`); err != nil {
		return nil, fmt.Errorf("insert cancel event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel: %w", err)
	}

	return n, nil
}

// GetBatch returns a batch summary with counts.
func (r *Repository) GetBatch(ctx context.Context, id uuid.UUID) (*Batch, error) {
	var batch Batch
	err := r.db.QueryRow(ctx,
		`SELECT id, total_count, created_at, updated_at FROM batches WHERE id = $1`,
		id,
	).Scan(&batch.ID, &batch.Total, &batch.CreatedAt, &batch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}

	counts := emptyStatusCounts()
	rows, err := r.db.Query(ctx,
		`SELECT status, count(*) FROM notifications WHERE batch_id = $1 GROUP BY status`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("count batch items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan batch count: %w", err)
		}
		counts[Status(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batch counts: %w", err)
	}

	batch.Counts = counts
	batch.Status = DeriveBatchStatus(counts, batch.Total)

	return &batch, nil
}

// GetIdempotency returns a non-expired idempotency record.
func (r *Repository) GetIdempotency(ctx context.Context, scope, key string) (*IdempotencyRecord, error) {
	var (
		record         IdempotencyRecord
		resourceID     uuid.NullUUID
		responseStatus sql.NullInt32
	)

	err := r.db.QueryRow(ctx,
		`SELECT scope, key, request_hash, state, resource_id, response_status, created_at, expires_at
		 FROM idempotency_keys
		 WHERE scope = $1 AND key = $2 AND expires_at > now()`,
		scope, key,
	).Scan(
		&record.Scope, &record.Key, &record.RequestHash, &record.State,
		&resourceID, &responseStatus, &record.CreatedAt, &record.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get idempotency record: %w", err)
	}

	if resourceID.Valid {
		id := resourceID.UUID
		record.ResourceID = &id
	}
	if responseStatus.Valid {
		record.ResponseStatus = int(responseStatus.Int32)
	}

	return &record, nil
}

// MarkQueued moves a pending row to queued after a successful enqueue.
func (r *Repository) MarkQueued(ctx context.Context, id uuid.UUID) (*Notification, error) {
	n, err := r.updateWithEvent(ctx, id,
		`UPDATE notifications SET status = 'queued', updated_at = now()
		 WHERE id = $1 AND status = 'pending'
		 RETURNING `+notificationColumns,
		[]any{id},
		func(*Notification) map[string]any {
			return map[string]any{"source": "api", "action": "enqueue"}
		},
	)
	if errors.Is(err, ErrNotClaimable) {
		return r.Get(ctx, id)
	}
	return n, err
}

// ListPending returns pending rows older than the cutoff.
func (r *Repository) ListPending(ctx context.Context, olderThan time.Time, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.Query(ctx,
		`SELECT `+notificationColumns+`
		 FROM notifications
		 WHERE status = 'pending' AND created_at < $1
		 ORDER BY created_at
		 LIMIT $2`,
		olderThan, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	defer rows.Close()

	items := make([]Notification, 0, limit)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pending notification: %w", err)
		}
		items = append(items, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending notifications: %w", err)
	}

	return items, nil
}

// Claim moves a claimable row to processing and increments the attempt count.
func (r *Repository) Claim(ctx context.Context, id uuid.UUID, now time.Time, lease time.Duration) (*Notification, error) {
	now = now.UTC()
	staleBefore := now.Add(-lease)

	return r.updateWithEvent(ctx, id,
		`UPDATE notifications
		 SET status = 'processing', attempt_count = attempt_count + 1, updated_at = $2
		 WHERE id = $1
		   AND (
		     status IN ('pending', 'queued')
		     OR (status = 'scheduled' AND scheduled_at <= $2)
		     OR (status = 'processing' AND updated_at < $3)
		   )
		 RETURNING `+notificationColumns,
		[]any{id, now, staleBefore},
		func(n *Notification) map[string]any {
			return map[string]any{
				"source":  "worker",
				"action":  "claim",
				"attempt": n.AttemptCount,
			}
		},
	)
}

// MarkAccepted records a provider acceptance.
func (r *Repository) MarkAccepted(ctx context.Context, id uuid.UUID, providerMessageID string, now time.Time) (*Notification, error) {
	var messageID any
	if providerMessageID != "" {
		messageID = providerMessageID
	}

	return r.updateWithEvent(ctx, id,
		`UPDATE notifications
		 SET status = 'accepted', accepted_at = $2, provider_message_id = $3,
		     last_error = NULL, updated_at = $2
		 WHERE id = $1 AND status = 'processing'
		 RETURNING `+notificationColumns,
		[]any{id, now.UTC(), messageID},
		func(*Notification) map[string]any {
			return map[string]any{
				"source":              "worker",
				"action":              "accept",
				"provider_message_id": providerMessageID,
			}
		},
	)
}

// MarkFailed records a permanent failure.
func (r *Repository) MarkFailed(ctx context.Context, id uuid.UUID, message string, now time.Time) (*Notification, error) {
	return r.updateWithEvent(ctx, id,
		`UPDATE notifications
		 SET status = 'failed', last_error = $2, updated_at = $3
		 WHERE id = $1 AND status = 'processing'
		 RETURNING `+notificationColumns,
		[]any{id, message, now.UTC()},
		func(*Notification) map[string]any {
			return map[string]any{"source": "worker", "action": "fail", "error": message}
		},
	)
}

// MarkDeadLetter records exhausted retries.
func (r *Repository) MarkDeadLetter(ctx context.Context, id uuid.UUID, message string, now time.Time) (*Notification, error) {
	return r.updateWithEvent(ctx, id,
		`UPDATE notifications
		 SET status = 'dead_letter', last_error = $2, updated_at = $3
		 WHERE id = $1 AND status = 'processing'
		 RETURNING `+notificationColumns,
		[]any{id, message, now.UTC()},
		func(*Notification) map[string]any {
			return map[string]any{"source": "worker", "action": "dead_letter", "error": message}
		},
	)
}

// MarkQueuedForRetry returns a row to queued with the next attempt time.
func (r *Repository) MarkQueuedForRetry(
	ctx context.Context,
	id uuid.UUID,
	message string,
	nextAttempt time.Time,
	now time.Time,
) (*Notification, error) {
	return r.updateWithEvent(ctx, id,
		`UPDATE notifications
		 SET status = 'queued', last_error = $2, next_attempt_at = $3, updated_at = $4
		 WHERE id = $1 AND status = 'processing'
		 RETURNING `+notificationColumns,
		[]any{id, message, nextAttempt.UTC(), now.UTC()},
		func(*Notification) map[string]any {
			return map[string]any{
				"source":          "worker",
				"action":          "retry",
				"error":           message,
				"next_attempt_at": nextAttempt.UTC(),
			}
		},
	)
}

func (r *Repository) updateWithEvent(
	ctx context.Context,
	id uuid.UUID,
	query string,
	args []any,
	detail func(*Notification) map[string]any,
) (*Notification, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transition: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	n, err := scanNotification(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotClaimable
	}
	if err != nil {
		return nil, fmt.Errorf("transition notification: %w", err)
	}

	payload := map[string]any{"source": "worker"}
	if detail != nil {
		payload = detail(n)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"source":"worker"}`)
	}

	if err := insertEvent(ctx, tx, n.ID, n.Status, encoded); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transition: %w", err)
	}

	return n, nil
}

// IsIdempotencyConflict reports whether the error is the idempotency primary key.
func IsIdempotencyConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "idempotency_keys_pkey"
}

func insertEvent(ctx context.Context, tx pgx.Tx, notificationID uuid.UUID, status Status, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte(`{"source":"api"}`)
	}
	if _, err := tx.Exec(ctx, insertEventSQL, notificationID, string(status), string(detail)); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func insertIdempotency(ctx context.Context, tx pgx.Tx, record *IdempotencyRecord) error {
	state := record.State
	if state == "" {
		state = IdempotencyCompleted
	}

	var resourceID any
	if record.ResourceID != nil {
		resourceID = *record.ResourceID
	}

	if _, err := tx.Exec(ctx, insertIdempotencySQL,
		record.Scope, record.Key, record.RequestHash, state, resourceID, record.ResponseStatus,
	); err != nil {
		return fmt.Errorf("insert idempotency record: %w", err)
	}
	return nil
}

func notificationArgs(n *Notification) []any {
	return []any{
		n.ID,
		optionalUUID(n.BatchID),
		n.Recipient,
		string(n.Channel),
		optionalString(n.Subject),
		n.Content,
		string(n.Priority),
		string(n.Status),
		n.AttemptCount,
		n.MaxAttempts,
		optionalTime(n.ScheduledAt),
		optionalTime(n.NextAttemptAt),
		optionalString(n.ProviderMessageID),
		optionalString(n.LastError),
		n.CorrelationID,
		n.CreatedAt,
		n.UpdatedAt,
		optionalTime(n.AcceptedAt),
		optionalTime(n.CancelledAt),
	}
}

func optionalUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row rowScanner) (*Notification, error) {
	var (
		n                            Notification
		batchID                      uuid.NullUUID
		channel, priority, status    string
		subject                      sql.NullString
		scheduledAt, nextAttemptAt   sql.NullTime
		providerMessageID, lastError sql.NullString
		acceptedAt, cancelledAt      sql.NullTime
	)

	err := row.Scan(
		&n.ID, &batchID, &n.Recipient, &channel, &subject, &n.Content, &priority, &status,
		&n.AttemptCount, &n.MaxAttempts, &scheduledAt, &nextAttemptAt,
		&providerMessageID, &lastError, &n.CorrelationID, &n.CreatedAt, &n.UpdatedAt,
		&acceptedAt, &cancelledAt,
	)
	if err != nil {
		return nil, err
	}

	n.Channel = Channel(channel)
	n.Priority = Priority(priority)
	n.Status = Status(status)

	if batchID.Valid {
		id := batchID.UUID
		n.BatchID = &id
	}
	n.Subject = nullStringPtr(subject)
	n.ScheduledAt = nullTimePtr(scheduledAt)
	n.NextAttemptAt = nullTimePtr(nextAttemptAt)
	n.ProviderMessageID = nullStringPtr(providerMessageID)
	n.LastError = nullStringPtr(lastError)
	n.AcceptedAt = nullTimePtr(acceptedAt)
	n.CancelledAt = nullTimePtr(cancelledAt)

	return &n, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time
	return &timestamp
}

func emptyStatusCounts() map[Status]int {
	return map[Status]int{
		StatusPending:    0,
		StatusScheduled:  0,
		StatusQueued:     0,
		StatusProcessing: 0,
		StatusAccepted:   0,
		StatusFailed:     0,
		StatusCancelled:  0,
		StatusDeadLetter: 0,
		StatusDelivered:  0,
	}
}
