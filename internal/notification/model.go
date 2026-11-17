package notification

import (
	"time"

	"github.com/google/uuid"
)

// Channel is a delivery channel.
type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
	ChannelPush  Channel = "push"
)

// Valid reports whether the channel is supported.
func (c Channel) Valid() bool {
	switch c {
	case ChannelSMS, ChannelEmail, ChannelPush:
		return true
	default:
		return false
	}
}

// Priority controls queue selection.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Valid reports whether the priority is supported.
func (p Priority) Valid() bool {
	switch p {
	case PriorityHigh, PriorityNormal, PriorityLow:
		return true
	default:
		return false
	}
}

// WithDefault returns normal when the priority is empty.
func (p Priority) WithDefault() Priority {
	if p == "" {
		return PriorityNormal
	}
	return p
}

// Status is the lifecycle state of a notification.
type Status string

const (
	StatusPending    Status = "pending"
	StatusScheduled  Status = "scheduled"
	StatusQueued     Status = "queued"
	StatusProcessing Status = "processing"
	StatusAccepted   Status = "accepted"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusDeadLetter Status = "dead_letter"
	StatusDelivered  Status = "delivered"
)

// Valid reports whether the status is known.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusScheduled, StatusQueued, StatusProcessing,
		StatusAccepted, StatusFailed, StatusCancelled, StatusDeadLetter, StatusDelivered:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further delivery work is expected.
func (s Status) Terminal() bool {
	switch s {
	case StatusAccepted, StatusFailed, StatusCancelled, StatusDeadLetter, StatusDelivered:
		return true
	default:
		return false
	}
}

// Cancellable reports whether the notification can still be cancelled.
func (s Status) Cancellable() bool {
	return s == StatusPending || s == StatusScheduled
}

// DeriveBatchStatus applies the batch precedence rules.
func DeriveBatchStatus(counts map[Status]int, total int) BatchStatus {
	if total <= 0 {
		return BatchStatusPending
	}
	if counts[StatusQueued]+counts[StatusProcessing] > 0 {
		return BatchStatusProcessing
	}
	if counts[StatusPending]+counts[StatusScheduled] > 0 {
		return BatchStatusPending
	}

	accepted := counts[StatusAccepted] + counts[StatusDelivered]
	failed := counts[StatusFailed] + counts[StatusDeadLetter]
	cancelled := counts[StatusCancelled]

	switch {
	case accepted == total:
		return BatchStatusCompleted
	case failed == total:
		return BatchStatusFailed
	case cancelled == total:
		return BatchStatusCancelled
	case accepted > 0 && failed > 0:
		return BatchStatusPartialFailure
	default:
		return BatchStatusMixed
	}
}

// BatchStatus is the derived state of a batch.
type BatchStatus string

const (
	BatchStatusPending        BatchStatus = "pending"
	BatchStatusProcessing     BatchStatus = "processing"
	BatchStatusCompleted      BatchStatus = "completed"
	BatchStatusPartialFailure BatchStatus = "partial_failure"
	BatchStatusFailed         BatchStatus = "failed"
	BatchStatusCancelled      BatchStatus = "cancelled"
	BatchStatusMixed          BatchStatus = "mixed"
)

// Notification is a persisted notification request.
type Notification struct {
	ID                uuid.UUID  `json:"id"`
	BatchID           *uuid.UUID `json:"batch_id"`
	Recipient         string     `json:"recipient"`
	Channel           Channel    `json:"channel"`
	Subject           *string    `json:"subject"`
	Content           string     `json:"content"`
	Priority          Priority   `json:"priority"`
	Status            Status     `json:"status"`
	AttemptCount      int        `json:"attempt_count"`
	MaxAttempts       int        `json:"max_attempts"`
	ScheduledAt       *time.Time `json:"scheduled_at"`
	NextAttemptAt     *time.Time `json:"next_attempt_at"`
	ProviderMessageID *string    `json:"provider_message_id"`
	LastError         *string    `json:"last_error"`
	CorrelationID     string     `json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	AcceptedAt        *time.Time `json:"accepted_at"`
	CancelledAt       *time.Time `json:"cancelled_at"`
}

// Batch is a group of notifications created together.
type Batch struct {
	ID               uuid.UUID      `json:"batch_id"`
	Total            int            `json:"total"`
	Status           BatchStatus    `json:"status"`
	Counts           map[Status]int `json:"counts"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	StatusURL        string         `json:"status_url"`
	NotificationsURL string         `json:"notifications_url,omitempty"`
}

// CreateInput is the request body for one notification.
type CreateInput struct {
	Recipient   string            `json:"recipient"`
	Channel     Channel           `json:"channel"`
	Subject     string            `json:"subject,omitempty"`
	Content     string            `json:"content"`
	Priority    Priority          `json:"priority,omitempty"`
	ScheduledAt *time.Time        `json:"scheduled_at,omitempty"`
	Template    string            `json:"template,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
}

// BatchInput is the request body for a batch.
type BatchInput struct {
	Notifications []CreateInput `json:"notifications"`
}

// ListFilter is the query for the notification list.
type ListFilter struct {
	Status   *Status
	Channel  *Channel
	BatchID  *uuid.UUID
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

// Normalize applies defaults and caps.
func (f *ListFilter) Normalize() {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
}

// Offset returns the SQL offset for the page.
func (f ListFilter) Offset() int {
	return (f.Page - 1) * f.PageSize
}
