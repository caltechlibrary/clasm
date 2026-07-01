package awsclient

import (
	"context"
	"errors"
	"time"

	"github.com/aws/smithy-go"
)

// throttlingCodes are the AWS API error codes callWithBackoff treats as
// retryable; everything else fails immediately.
var throttlingCodes = map[string]bool{
	"Throttling":               true,
	"ThrottlingException":      true,
	"RequestLimitExceeded":     true,
	"TooManyRequestsException": true,
}

func isThrottlingError(err error) bool {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return false
	}
	return throttlingCodes[apiErr.ErrorCode()]
}

// callWithBackoff retries fn up to maxAttempts times with exponential
// backoff, but only when fn's error is a throttling error -- matching
// DESIGN.md's Error Handling Strategy ("retry with exponential backoff,
// max 3 attempts").
func callWithBackoff[T any](ctx context.Context, maxAttempts int, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := range maxAttempts {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isThrottlingError(err) {
			return zero, err
		}
		if attempt == maxAttempts-1 {
			break
		}
		delay := (1 << attempt) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}
	return zero, lastErr
}
