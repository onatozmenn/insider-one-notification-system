package notification

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func validSMS() CreateInput {
	return CreateInput{
		Recipient: "+15555550100",
		Channel:   ChannelSMS,
		Content:   "Your order has shipped.",
	}
}

func TestValidateCreateValidSMS(t *testing.T) {
	got, err := ValidateCreate(validSMS(), testNow)
	require.NoError(t, err)
	assert.Equal(t, PriorityNormal, got.Priority)
	assert.Equal(t, ChannelSMS, got.Channel)
	assert.Equal(t, "+15555550100", got.Recipient)
}

func TestValidateCreateValidEmail(t *testing.T) {
	input := CreateInput{
		Recipient: "onat@example.com",
		Channel:   ChannelEmail,
		Subject:   "Weekly summary",
		Content:   "Here is what happened.",
		Priority:  PriorityHigh,
	}

	got, err := ValidateCreate(input, testNow)
	require.NoError(t, err)
	assert.Equal(t, PriorityHigh, got.Priority)
	assert.Equal(t, "Weekly summary", got.Subject)
}

func TestValidateCreateValidPush(t *testing.T) {
	input := CreateInput{
		Recipient: "device-token-123",
		Channel:   ChannelPush,
		Subject:   "New message",
		Content:   "Open the app to read it.",
		Priority:  PriorityLow,
	}

	got, err := ValidateCreate(input, testNow)
	require.NoError(t, err)
	assert.Equal(t, PriorityLow, got.Priority)
	assert.Equal(t, "New message", got.Subject)
}

func TestValidateCreateRejectsInvalidInput(t *testing.T) {
	past := testNow.Add(-time.Hour)
	tooFar := testNow.Add(31 * 24 * time.Hour)

	tests := []struct {
		name  string
		input CreateInput
		field string
	}{
		{
			name:  "missing recipient",
			input: CreateInput{Channel: ChannelSMS, Content: "hello"},
			field: "recipient",
		},
		{
			name:  "invalid sms recipient",
			input: CreateInput{Recipient: "05551234567", Channel: ChannelSMS, Content: "hello"},
			field: "recipient",
		},
		{
			name: "invalid email recipient",
			input: CreateInput{
				Recipient: "not-an-email",
				Channel:   ChannelEmail,
				Subject:   "Hi",
				Content:   "hello",
			},
			field: "recipient",
		},
		{
			name: "email without subject",
			input: CreateInput{
				Recipient: "onat@example.com",
				Channel:   ChannelEmail,
				Content:   "hello",
			},
			field: "subject",
		},
		{
			name: "sms with subject",
			input: CreateInput{
				Recipient: "+15555550100",
				Channel:   ChannelSMS,
				Subject:   "Hi",
				Content:   "hello",
			},
			field: "subject",
		},
		{
			name: "sms content too long",
			input: CreateInput{
				Recipient: "+15555550100",
				Channel:   ChannelSMS,
				Content:   strings.Repeat("a", maxSMSContent+1),
			},
			field: "content",
		},
		{
			name: "email content too long",
			input: CreateInput{
				Recipient: "onat@example.com",
				Channel:   ChannelEmail,
				Subject:   "Hi",
				Content:   strings.Repeat("a", maxEmailContent+1),
			},
			field: "content",
		},
		{
			name: "push content too long",
			input: CreateInput{
				Recipient: "device-token-123",
				Channel:   ChannelPush,
				Content:   strings.Repeat("a", maxPushContent+1),
			},
			field: "content",
		},
		{
			name: "invalid priority",
			input: CreateInput{
				Recipient: "+15555550100",
				Channel:   ChannelSMS,
				Content:   "hello",
				Priority:  Priority("urgent"),
			},
			field: "priority",
		},
		{
			name: "invalid channel",
			input: CreateInput{
				Recipient: "+15555550100",
				Channel:   Channel("fax"),
				Content:   "hello",
			},
			field: "channel",
		},
		{
			name: "scheduled in the past",
			input: CreateInput{
				Recipient:   "+15555550100",
				Channel:     ChannelSMS,
				Content:     "hello",
				ScheduledAt: &past,
			},
			field: "scheduled_at",
		},
		{
			name: "scheduled too far ahead",
			input: CreateInput{
				Recipient:   "+15555550100",
				Channel:     ChannelSMS,
				Content:     "hello",
				ScheduledAt: &tooFar,
			},
			field: "scheduled_at",
		},
		{
			name: "variables without template",
			input: CreateInput{
				Recipient: "+15555550100",
				Channel:   ChannelSMS,
				Content:   "hello",
				Variables: map[string]string{"name": "Onat"},
			},
			field: "variables",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateCreate(tt.input, testNow)
			require.Error(t, err)

			var validationErr *ValidationError
			require.True(t, errors.As(err, &validationErr))
			assert.Contains(t, fieldNames(validationErr), tt.field)
		})
	}
}

func TestValidateBatch(t *testing.T) {
	input := BatchInput{Notifications: []CreateInput{validSMS(), validSMS()}}

	got, err := ValidateBatch(input, testNow)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestValidateBatchRejectsEmpty(t *testing.T) {
	_, err := ValidateBatch(BatchInput{}, testNow)
	require.Error(t, err)
}

func TestValidateBatchRejectsTooManyItems(t *testing.T) {
	input := BatchInput{Notifications: make([]CreateInput, MaxBatchSize+1)}

	_, err := ValidateBatch(input, testNow)
	require.Error(t, err)

	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	assert.Contains(t, fieldNames(validationErr), "notifications")
}

func TestValidateBatchReportsIndexedErrors(t *testing.T) {
	bad := validSMS()
	bad.Recipient = "not-a-phone"

	input := BatchInput{Notifications: []CreateInput{validSMS(), bad}}

	_, err := ValidateBatch(input, testNow)
	require.Error(t, err)

	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	assert.Contains(t, fieldNames(validationErr), "notifications[1].recipient")
}

func fieldNames(err *ValidationError) []string {
	names := make([]string, 0, len(err.Details))
	for _, detail := range err.Details {
		names = append(names, detail.Field)
	}
	return names
}
