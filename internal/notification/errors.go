package notification

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound means the notification does not exist.
	ErrNotFound = errors.New("notification not found")
	// ErrBatchNotFound means the batch does not exist.
	ErrBatchNotFound = errors.New("batch not found")
	// ErrNotCancellable means the notification is past the cancellable states.
	ErrNotCancellable = errors.New("notification is not cancellable")
	// ErrNotClaimable means another worker or a terminal state won the race.
	ErrNotClaimable = errors.New("notification is not claimable")
	// ErrNotRetryable means the notification is not failed or dead-lettered.
	ErrNotRetryable = errors.New("notification is not retryable")
	// ErrTemplateNotFound means the template does not exist.
	ErrTemplateNotFound = errors.New("template not found")
	// ErrTemplateExists means the template name is already in use.
	ErrTemplateExists = errors.New("template name already exists")
	// ErrIdempotencyConflict means the key was reused with a different body.
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request body")
	// ErrIdempotencyInProgress means the original request is still running.
	ErrIdempotencyInProgress = errors.New("idempotency request is still in progress")
)

// FieldError describes one invalid field.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// ValidationError is returned when a request fails domain validation.
type ValidationError struct {
	Details []FieldError
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %d field(s)", len(e.Details))
}
