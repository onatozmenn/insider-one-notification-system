package delivery

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/hibiken/asynq"

	"github.com/onatozmenn/insider-one-notification-system/internal/provider"
)

const (
	retryBaseDelay = time.Second
	retryCapDelay  = 30 * time.Second
)

// RetryAfterError asks asynq to retry after a specific delay.
type RetryAfterError struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("retry after %s: %v", e.Delay, e.Err)
	}
	return fmt.Sprintf("retry after %s", e.Delay)
}

// Unwrap exposes the underlying error.
func (e *RetryAfterError) Unwrap() error {
	return e.Err
}

// RetryDelay computes exponential backoff with full jitter.
func RetryDelay(retryCount int, err error, _ *asynq.Task) time.Duration {
	var retryAfter *RetryAfterError
	if errors.As(err, &retryAfter) && retryAfter.Delay > 0 {
		return retryAfter.Delay
	}

	var providerErr *provider.Error
	if errors.As(err, &providerErr) && providerErr.RetryAfter > 0 {
		return providerErr.RetryAfter
	}

	if retryCount < 0 {
		retryCount = 0
	}

	base := retryBaseDelay
	for i := 0; i < retryCount; i++ {
		if base >= retryCapDelay {
			base = retryCapDelay
			break
		}
		base *= 2
	}
	if base > retryCapDelay {
		base = retryCapDelay
	}
	if base <= 0 {
		return 0
	}

	return time.Duration(rand.Int64N(int64(base)))
}
