package notification

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// MaxBatchSize is the largest batch accepted by the API.
	MaxBatchSize = 1000

	maxSMSContent     = 160
	maxEmailSubject   = 200
	maxEmailContent   = 50_000
	maxPushSubject    = 100
	maxPushContent    = 500
	maxRecipient      = 512
	maxScheduledAhead = 30 * 24 * time.Hour
)

var (
	e164Pattern  = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// ValidateCreate normalizes and validates one notification request.
func ValidateCreate(input CreateInput, now time.Time) (CreateInput, error) {
	var errs []FieldError

	input.Channel = Channel(strings.ToLower(strings.TrimSpace(string(input.Channel))))
	input.Priority = input.Priority.WithDefault()
	input.Recipient = strings.TrimSpace(input.Recipient)
	input.Subject = strings.TrimSpace(input.Subject)

	if !input.Channel.Valid() {
		errs = append(errs, FieldError{Field: "channel", Reason: "must be one of sms, email, push"})
	}
	if !input.Priority.Valid() {
		errs = append(errs, FieldError{Field: "priority", Reason: "must be one of high, normal, low"})
	}

	if input.Recipient == "" {
		errs = append(errs, FieldError{Field: "recipient", Reason: "is required"})
	} else {
		if len([]rune(input.Recipient)) > maxRecipient {
			errs = append(errs, FieldError{Field: "recipient", Reason: fmt.Sprintf("must be at most %d characters", maxRecipient)})
		}
		switch input.Channel {
		case ChannelSMS:
			if !e164Pattern.MatchString(input.Recipient) {
				errs = append(errs, FieldError{Field: "recipient", Reason: "must be a valid E.164 phone number"})
			}
		case ChannelEmail:
			if !emailPattern.MatchString(input.Recipient) {
				errs = append(errs, FieldError{Field: "recipient", Reason: "must be a valid email address"})
			}
		case ChannelPush:
			// Any non-empty token up to the recipient limit is accepted.
		}
	}

	if strings.TrimSpace(input.Content) == "" {
		errs = append(errs, FieldError{Field: "content", Reason: "is required"})
	} else {
		limit := maxPushContent
		switch input.Channel {
		case ChannelSMS:
			limit = maxSMSContent
		case ChannelEmail:
			limit = maxEmailContent
		case ChannelPush:
			limit = maxPushContent
		}
		if len([]rune(input.Content)) > limit {
			errs = append(errs, FieldError{Field: "content", Reason: fmt.Sprintf("must be at most %d characters for %s", limit, input.Channel)})
		}
	}

	switch input.Channel {
	case ChannelSMS:
		if input.Subject != "" {
			errs = append(errs, FieldError{Field: "subject", Reason: "is not allowed for sms"})
		}
	case ChannelEmail:
		if input.Subject == "" {
			errs = append(errs, FieldError{Field: "subject", Reason: "is required for email"})
		} else if len([]rune(input.Subject)) > maxEmailSubject {
			errs = append(errs, FieldError{Field: "subject", Reason: fmt.Sprintf("must be at most %d characters for email", maxEmailSubject)})
		}
	case ChannelPush:
		if len([]rune(input.Subject)) > maxPushSubject {
			errs = append(errs, FieldError{Field: "subject", Reason: fmt.Sprintf("must be at most %d characters for push", maxPushSubject)})
		}
	}

	if input.ScheduledAt != nil {
		if !input.ScheduledAt.After(now) {
			errs = append(errs, FieldError{Field: "scheduled_at", Reason: "must be in the future"})
		} else if input.ScheduledAt.After(now.Add(maxScheduledAhead)) {
			errs = append(errs, FieldError{Field: "scheduled_at", Reason: "must be at most 30 days ahead"})
		}
	}

	if input.Template == "" && len(input.Variables) > 0 {
		errs = append(errs, FieldError{Field: "variables", Reason: "require a template"})
	}

	if len(errs) > 0 {
		return CreateInput{}, &ValidationError{Details: errs}
	}

	return input, nil
}

// ValidateBatch validates every item and reports indexed field errors.
func ValidateBatch(input BatchInput, now time.Time) ([]CreateInput, error) {
	if err := validateBatchSize(len(input.Notifications)); err != nil {
		return nil, err
	}

	validated := make([]CreateInput, 0, len(input.Notifications))
	var errs []FieldError

	for i, item := range input.Notifications {
		value, err := ValidateCreate(item, now)
		if err != nil {
			var validationErr *ValidationError
			if errors.As(err, &validationErr) {
				for _, detail := range validationErr.Details {
					errs = append(errs, FieldError{
						Field:  fmt.Sprintf("notifications[%d].%s", i, detail.Field),
						Reason: detail.Reason,
					})
				}
				continue
			}
			return nil, err
		}
		validated = append(validated, value)
	}

	if len(errs) > 0 {
		return nil, &ValidationError{Details: errs}
	}

	return validated, nil
}

func validateBatchSize(count int) error {
	if count == 0 {
		return &ValidationError{Details: []FieldError{{Field: "notifications", Reason: "must contain at least one item"}}}
	}
	if count > MaxBatchSize {
		return &ValidationError{Details: []FieldError{{Field: "notifications", Reason: fmt.Sprintf("must contain at most %d items", MaxBatchSize)}}}
	}
	return nil
}
