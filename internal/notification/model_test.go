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
	assert.True(t, StatusQueued.Cancellable())
	assert.False(t, StatusProcessing.Cancellable())
	assert.False(t, StatusAccepted.Cancellable())
}

func TestPriorityWithDefault(t *testing.T) {
	assert.Equal(t, PriorityNormal, Priority("").WithDefault())
	assert.Equal(t, PriorityHigh, PriorityHigh.WithDefault())
}

func TestDeriveBatchStatus(t *testing.T) {
	tests := []struct {
		name   string
		counts map[Status]int
		total  int
		want   BatchStatus
	}{
		{
			name:   "pending",
			counts: map[Status]int{StatusPending: 2},
			total:  2,
			want:   BatchStatusPending,
		},
		{
			name:   "processing wins over terminal rows",
			counts: map[Status]int{StatusProcessing: 1, StatusAccepted: 1},
			total:  2,
			want:   BatchStatusProcessing,
		},
		{
			name:   "completed",
			counts: map[Status]int{StatusAccepted: 2},
			total:  2,
			want:   BatchStatusCompleted,
		},
		{
			name:   "failed",
			counts: map[Status]int{StatusFailed: 1, StatusDeadLetter: 1},
			total:  2,
			want:   BatchStatusFailed,
		},
		{
			name:   "cancelled",
			counts: map[Status]int{StatusCancelled: 2},
			total:  2,
			want:   BatchStatusCancelled,
		},
		{
			name:   "partial failure",
			counts: map[Status]int{StatusAccepted: 1, StatusFailed: 1},
			total:  2,
			want:   BatchStatusPartialFailure,
		},
		{
			name:   "mixed accepted and cancelled",
			counts: map[Status]int{StatusAccepted: 1, StatusCancelled: 1},
			total:  2,
			want:   BatchStatusMixed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DeriveBatchStatus(tt.counts, tt.total))
		})
	}
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
