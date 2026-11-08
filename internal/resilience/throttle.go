// Governing: ADR-0020 (error handling and resilience), ADR-0029 (Lidarr submission rate limiting)
package resilience

import (
	"context"
	"sync"
	"time"
)

// Throttle spaces requests to at most one per delay. It replaces the
// per-client mutex-plus-sleep loops, which slept while holding the mutex that
// guarded the throttle state (so concurrent callers queued behind a sleep they
// could not cancel) and ignored context cancellation entirely.
//
// The zero value is ready to use and imposes no delay, so a client that
// constructs its request struct as a literal needs no extra wiring: the rate
// is declared at the call site.
type Throttle struct {
	mu sync.Mutex
	// next is the reserved send time of the most recently admitted request.
	next time.Time
}

// Wait blocks until it is this caller's turn to send, at least delay after the
// previous admitted request. The mutex is held only long enough to reserve the
// slot, so waiting never blocks another caller from reserving its own, and the
// wait is abandoned as soon as ctx is done.
func (t *Throttle) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		// Nothing is being paced; do not take or move the shared slot.
		return nil
	}

	t.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if !t.next.IsZero() {
		if remaining := delay - now.Sub(t.next); remaining > 0 {
			wait = remaining
		}
	}
	t.next = now.Add(wait)
	t.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	return Sleep(ctx, wait)
}

// Sleep waits for d, returning ctx.Err() early when ctx is done first. Retry
// paths must use it instead of time.Sleep so a cancelled sync or shutdown is
// not held up by a pending backoff.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
