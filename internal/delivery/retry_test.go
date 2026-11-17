package delivery

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/provider"
)

func TestQueueForPriority(t *testing.T) {
	tests := []struct {
		priority notification.Priority
		want     string
	}{
		{notification.PriorityHigh, QueueHigh},
		{notification.PriorityNormal, QueueNormal},
		{notification.PriorityLow, QueueLow},
		{notification.Priority("unknown"), QueueNormal},
		{notification.Priority(""), QueueNormal},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, queueForPriority(tt.priority), string(tt.priority))
	}
}

func TestRetryDelayBounds(t *testing.T) {
	delay := RetryDelay(0, nil, nil)
	assert.GreaterOrEqual(t, delay, time.Duration(0))
	assert.Less(t, delay, time.Second)

	delay = RetryDelay(10, nil, nil)
	assert.GreaterOrEqual(t, delay, time.Duration(0))
	assert.LessOrEqual(t, delay, 30*time.Second)
}

func TestRetryDelayPrefersRetryAfter(t *testing.T) {
	delay := RetryDelay(0, &provider.Error{RetryAfter: 2 * time.Second}, nil)
	assert.Equal(t, 2*time.Second, delay)

	delay = RetryDelay(0, &RetryAfterError{Delay: 5 * time.Second}, nil)
	assert.Equal(t, 5*time.Second, delay)
}

func TestRetryAfterErrorUnwraps(t *testing.T) {
	cause := errors.New("cause")
	err := &RetryAfterError{Delay: time.Second, Err: cause}
	require.ErrorIs(t, err, cause)
}
