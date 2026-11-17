package notification

import (
	"time"

	"github.com/google/uuid"
)

const (
	// ScopeSingle is the idempotency scope for single creates.
	ScopeSingle = "POST:/api/v1/notifications"
	// ScopeBatch is the idempotency scope for batch creates.
	ScopeBatch = "POST:/api/v1/notifications/batch"

	// IdempotencyCompleted means the original resource is committed.
	IdempotencyCompleted = "completed"
	// IdempotencyInProgress means the original request has not committed yet.
	IdempotencyInProgress = "in_progress"
)

// IdempotencyRecord stores the result of a create request.
type IdempotencyRecord struct {
	Scope          string
	Key            string
	RequestHash    string
	State          string
	ResourceID     *uuid.UUID
	ResponseStatus int
	CreatedAt      time.Time
	ExpiresAt      time.Time
}
