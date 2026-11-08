// Governing: ADR-0020 (error handling and resilience), SPEC error-handling REQ-ERR-002
package resilience

import (
	"context"
	"time"
)

// RetryConfig bounds one retry loop.
type RetryConfig struct {
	// Attempts is the total number of tries, including the first. Values
	// below 1 are treated as 1.
	Attempts int
	// Backoff decides the wait before each retry when the attempt does not
	// report its own RetryAfter.
	Backoff BackoffPolicy
	// OnRetry, when set, is called just before each wait so the caller can
	// log with its own logger and field names.
	OnRetry func(attempt int, wait time.Duration)
}

// Outcome is what one attempt reports back to Retry.
type Outcome struct {
	// Retry asks for another attempt. When the loop is out of attempts the
	// error is returned as-is, so an attempt can ask unconditionally or bound
	// itself against the last try.
	Retry bool
	// RetryAfter overrides the policy's backoff for this retry when positive.
	// A server's Retry-After header belongs here.
	RetryAfter time.Duration
}

// Retry runs attempt until it stops asking for another try, and returns the
// error from the last one (nil when the last attempt succeeded).
//
// Every wait goes through Sleep, so a cancelled context abandons the loop
// instead of sitting out the remaining backoff, and the request itself is
// built inside the callback: a caller whose request carries a body must
// rebuild it per attempt, or the retry sends a consumed reader.
func Retry(ctx context.Context, cfg RetryConfig, attempt func(ctx context.Context, attempt int) (Outcome, error)) error {
	attempts := cfg.Attempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	var retryAfter time.Duration

	for i := 0; i < attempts; i++ {
		if i > 0 {
			wait := retryAfter
			if wait <= 0 {
				wait = cfg.Backoff.Delay(i)
			}
			if cfg.OnRetry != nil {
				cfg.OnRetry(i, wait)
			}
			if err := Sleep(ctx, wait); err != nil {
				return err
			}
		}

		outcome, err := attempt(ctx, i)
		lastErr = err
		if !outcome.Retry {
			return err
		}
		retryAfter = outcome.RetryAfter
	}

	return lastErr
}
