// Governing: ADR-0020 (exponential backoff with jitter), SPEC-0017 REQ "Backoff Strategy"
package resilience

import (
	"math"
	"math/rand/v2"
	"time"
)

// BackoffPolicy is the exponential part of a backoff plus one of the two
// jitter shapes this codebase's design records name. It exists so a call site
// declares its numbers instead of re-deriving the formula: every retry loop
// and queue in Spotter otherwise grew its own copy, and those copies drifted
// (the Lidarr submitter's duplicate capped at 1h while the sync loops capped
// at 30m, and one of them matched error prose to decide whether to retry at
// all).
//
// The exponential part is always min(Base * 2^(attempt-1), Max). Jitter is
// then applied per the fields the caller sets:
//
//   - MultiplicativeJitter spreads the capped delay into
//     [Base*2^n*(1-f), Base*2^n*(1+f)] — ADR-0020's ±25%, which can push the
//     total past Max by design so a fleet of providers does not wake together.
//   - AdditiveJitter adds a random pause in [0, AdditiveJitter) and keeps the
//     total at or under Max — SPEC-0017's queue retry, where the cap is a
//     promise about when the queue is next drained.
type BackoffPolicy struct {
	// Base is the delay before the first retry.
	Base time.Duration
	// Max caps the exponential part (and the total when AdditiveJitter is set).
	Max time.Duration

	// MultiplicativeJitter is the fractional band around the delay, 0 for none.
	MultiplicativeJitter float64
	// AdditiveJitter is the upper bound of the extra random pause, 0 for none.
	AdditiveJitter time.Duration
}

// Delay returns how long to wait before retry number attempt, where attempt 1
// is the first retry after the first failure. Counts below 1 are clamped to
// the base delay, matching the sync loops, which may ask for a delay before
// any failure has been recorded.
func (p BackoffPolicy) Delay(attempt int) time.Duration {
	if p.Base <= 0 {
		return 0
	}

	exponent := attempt - 1
	if exponent < 0 {
		exponent = 0
	}
	// math.Pow overflows to +Inf long before a counter reaches it; clamping
	// the exponent keeps the result finite so the cap below still applies.
	if exponent > 62 {
		exponent = 62
	}

	delay := float64(p.Base) * math.Pow(2, float64(exponent))
	if delay > float64(p.Max) || math.IsInf(delay, 1) {
		delay = float64(p.Max)
	}

	if p.MultiplicativeJitter > 0 {
		delay *= 1 - p.MultiplicativeJitter + rand.Float64()*2*p.MultiplicativeJitter
	}
	if p.AdditiveJitter > 0 {
		delay += float64(rand.Int64N(int64(p.AdditiveJitter)))
		if delay > float64(p.Max) {
			delay = float64(p.Max)
		}
	}
	return time.Duration(delay)
}
