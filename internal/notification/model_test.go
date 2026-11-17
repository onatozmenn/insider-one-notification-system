package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusTerminal(t *testing.T) {
	terminal := []Status{StatusAccepted, StatusFailed, StatusCancelled, StatusDeadLetter, StatusDelivered}
	active := []Status{StatusPending, StatusScheduled, StatusQueued, StatusProcessing}

	for _, status := range terminal {
		assert.True(t, status.Terminal(), status)
	}
	for _, status := range active {
		assert.False(t, status.Terminal(), status)
	}
}

func TestStatusCancellable(t *testing.T) {
	assert.True(t, StatusPending.Cancellable())
	assert.True(t, StatusScheduled.Cancellable())
	assert.False(t, StatusQueued.Cancellable())
	assert.False(t, StatusProcessing.Cancellable())
	assert.False(t, StatusAccepted.Cancellable())
}

func TestPriorityWithDefault(t *testing.T) {
	assert.Equal(t, PriorityNormal, Priority("").WithDefault())
	assert.Equal(t, PriorityHigh, PriorityHigh.WithDefault())
}

func TestListFilterNormalize(t *testing.T) {
	filter := ListFilter{}
	filter.Normalize()
	assert.Equal(t, 1, filter.Page)
	assert.Equal(t, 20, filter.PageSize)
	assert.Equal(t, 0, filter.Offset())

	filter = ListFilter{Page: 3, PageSize: 500}
	filter.Normalize()
	assert.Equal(t, 3, filter.Page)
	assert.Equal(t, 100, filter.PageSize)
	assert.Equal(t, 200, filter.Offset())
}
