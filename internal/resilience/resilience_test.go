package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestThrottleFirstWaitIsImmediate(t *testing.T) {
	var th Throttle

	start := time.Now()
	if err := th.Wait(context.Background(), time.Hour); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("first request waited %v, want no wait", elapsed)
	}
}

func TestThrottleSpacesRequests(t *testing.T) {
	const delay = 60 * time.Millisecond
	var th Throttle

	if err := th.Wait(context.Background(), delay); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	start := time.Now()
	if err := th.Wait(context.Background(), delay); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < delay-10*time.Millisecond {
		t.Errorf("second request waited %v, want at least %v", elapsed, delay-10*time.Millisecond)
	}
}

// TestThrottleSerializesConcurrentCallers pins the pacing itself: with one
// delay, two callers must send one interval apart rather than together.
func TestThrottleSerializesConcurrentCallers(t *testing.T) {
	const delay = 50 * time.Millisecond
	var th Throttle

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			if err := th.Wait(context.Background(), delay); err != nil {
				t.Errorf("Wait: %v", err)
			}
		}()
	}

	start := time.Now()
	wg.Wait()
	if elapsed := time.Since(start); elapsed < delay-10*time.Millisecond {
		t.Errorf("two requests took %v, want at least one %v interval", elapsed, delay-10*time.Millisecond)
	}
}

func TestThrottleAbandonsWaitOnContextCancel(t *testing.T) {
	var th Throttle
	if err := th.Wait(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := th.Wait(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Want context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancelled wait took %v, want it cut short", elapsed)
	}
}

func TestThrottleReturnsAnAlreadyDoneContext(t *testing.T) {
	var th Throttle

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := th.Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Want context.Canceled, got %v", err)
	}
}

func TestThrottleZeroDelayNeverWaits(t *testing.T) {
	var th Throttle

	if err := th.Wait(context.Background(), 0); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := th.Wait(context.Background(), 0); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
}

func TestSleepWaitsThenReturns(t *testing.T) {
	start := time.Now()
	if err := Sleep(context.Background(), 40*time.Millisecond); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("Sleep returned after %v, want ~40ms", elapsed)
	}
}

func TestSleepIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Want context.Canceled, got %v", err)
	}
}

func TestBackoffPolicyMultiplicativeJitter(t *testing.T) {
	// ADR-0020: 30s doubling to a 30m cap, jittered ±25%.
	policy := BackoffPolicy{Base: 30 * time.Second, Max: 30 * time.Minute, MultiplicativeJitter: 0.25}

	seen := make(map[time.Duration]bool)
	for attempt := 1; attempt <= 4; attempt++ {
		base := 30 * time.Second << (attempt - 1)
		for range 10 {
			delay := policy.Delay(attempt)
			if delay < time.Duration(float64(base)*0.75) || delay > time.Duration(float64(base)*1.25) {
				t.Errorf("attempt %d delay %v outside [%v, %v]", attempt, delay,
					time.Duration(float64(base)*0.75), time.Duration(float64(base)*1.25))
			}
			seen[delay] = true
		}
	}
	if len(seen) < 2 {
		t.Error("jitter should produce varying delays")
	}
}

func TestBackoffPolicyCapsAndAdditiveJitter(t *testing.T) {
	// SPEC-0017: 1m doubling to a 1h cap, plus a random pause under 1m that
	// never pushes the total past the cap.
	policy := BackoffPolicy{Base: time.Minute, Max: time.Hour, AdditiveJitter: time.Minute}

	for range 20 {
		if delay := policy.Delay(1); delay < time.Minute || delay > 2*time.Minute {
			t.Errorf("first retry delay %v outside [1m, 2m]", delay)
		}
		if delay := policy.Delay(20); delay > time.Hour {
			t.Errorf("capped delay %v exceeds 1h", delay)
		}
	}
}

func TestBackoffPolicyWithoutJitterIsExact(t *testing.T) {
	policy := BackoffPolicy{Base: 500 * time.Millisecond, Max: 2 * time.Second}

	for attempt, want := range map[int]time.Duration{
		1: 500 * time.Millisecond,
		2: time.Second,
		3: 2 * time.Second,
		4: 2 * time.Second,
	} {
		if got := policy.Delay(attempt); got != want {
			t.Errorf("Delay(%d) = %v, want %v", attempt, got, want)
		}
	}
}

func TestBackoffPolicyZeroBaseNeverWaits(t *testing.T) {
	if got := (BackoffPolicy{}).Delay(5); got != 0 {
		t.Errorf("Delay = %v, want 0", got)
	}
}

func TestRetryStopsAfterASuccessfulAttempt(t *testing.T) {
	attempts := 0
	err := Retry(context.Background(), RetryConfig{Attempts: 3}, func(context.Context, int) (Outcome, error) {
		attempts++
		if attempts < 2 {
			return Outcome{Retry: true}, errors.New("transient")
		}
		return Outcome{}, nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if attempts != 2 {
		t.Errorf("ran %d attempts, want 2", attempts)
	}
}

func TestRetryReturnsTheLastErrorWhenAttemptsRunOut(t *testing.T) {
	attempts := 0
	last := errors.New("attempt 3 still failing")
	err := Retry(context.Background(), RetryConfig{
		Attempts: 3,
		Backoff:  BackoffPolicy{Base: time.Millisecond, Max: time.Millisecond},
	}, func(context.Context, int) (Outcome, error) {
		attempts++
		if attempts == 3 {
			return Outcome{}, last
		}
		return Outcome{Retry: true}, errors.New("transient")
	})
	if !errors.Is(err, last) {
		t.Fatalf("Want the last error, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("ran %d attempts, want 3", attempts)
	}
}

func TestRetryUsesRetryAfterInsteadOfThePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("waits a second")
	}
	var waits []time.Duration
	err := Retry(context.Background(), RetryConfig{
		Attempts: 2,
		Backoff:  BackoffPolicy{Base: time.Hour},
		OnRetry:  func(_ int, wait time.Duration) { waits = append(waits, wait) },
	}, func(_ context.Context, attempt int) (Outcome, error) {
		if attempt == 0 {
			return Outcome{Retry: true, RetryAfter: 100 * time.Millisecond}, errors.New("429")
		}
		return Outcome{}, nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if len(waits) != 1 || waits[0] != 100*time.Millisecond {
		t.Errorf("waits = %v, want the 100ms Retry-After hint", waits)
	}
}

func TestRetryAbandonsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	err := Retry(ctx, RetryConfig{
		Attempts: 3,
		Backoff:  BackoffPolicy{Base: time.Hour},
	}, func(context.Context, int) (Outcome, error) {
		attempts++
		return Outcome{Retry: true}, errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Want context.Canceled, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("ran %d attempts after cancellation, want 1", attempts)
	}
}

// TestRetryCallsTheAttemptFreshEachTry documents why callers build their
// request inside the callback: a body reader is consumed by the first send.
func TestRetryCallsTheAttemptFreshEachTry(t *testing.T) {
	var built []int
	err := Retry(context.Background(), RetryConfig{
		Attempts: 3,
		Backoff:  BackoffPolicy{Base: time.Millisecond, Max: time.Millisecond},
	}, func(_ context.Context, attempt int) (Outcome, error) {
		built = append(built, attempt)
		if attempt < 2 {
			return Outcome{Retry: true}, errors.New("5xx")
		}
		return Outcome{}, nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if len(built) != 3 || built[0] != 0 || built[2] != 2 {
		t.Errorf("attempts built = %v, want a fresh request per try", built)
	}
}
