package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spotter/internal/lifecycle"
)

func discardShutdownLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func TestDrainAndHTTPBudgets(t *testing.T) {
	tests := []struct {
		name           string
		total          time.Duration
		wantDrain      time.Duration
		wantHTTPBudget time.Duration
	}{
		{"spec budget splits 25 and 5", 30 * time.Second, 25 * time.Second, 5 * time.Second},
		{"short budget splits evenly", 6 * time.Second, 3 * time.Second, 3 * time.Second},
		{"sub-second budget still gives both phases a share", 1 * time.Second, 500 * time.Millisecond, 500 * time.Millisecond},
		{"zero budget", 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			drain, httpBudget := drainAndHTTPBudgets(tc.total)
			assert.Equal(t, tc.wantDrain, drain)
			assert.Equal(t, tc.wantHTTPBudget, httpBudget)
			assert.Equal(t, tc.total, drain+httpBudget)
		})
	}
}

func TestRunShutdownSequenceDrainsBackgroundWorkBeforeHTTPShutdown(t *testing.T) {
	log := &eventLog{}
	steps := shutdownSteps{
		Logger:       discardShutdownLogger(),
		Budget:       2 * time.Second,
		CloseJobs:    func() { log.add("close") },
		Drain:        func(context.Context) error { log.add("drain"); return nil },
		ShutdownHTTP: func(context.Context) error { log.add("http"); return nil },
		HardExit:     func() { log.add("hard-exit"); t.Error("hard exit must not fire on a clean drain") },
	}

	runShutdownSequence(steps)

	assert.Equal(t, []string{"close", "drain", "http"}, log.snapshot(),
		"SPEC graceful-shutdown REQ-TMO-003: background work drains before the HTTP server stops")
}

func TestRunShutdownSequenceReservesTheSpecBudgets(t *testing.T) {
	var drainShare, httpShare time.Duration
	steps := shutdownSteps{
		Logger:    discardShutdownLogger(),
		Budget:    30 * time.Second,
		CloseJobs: func() {},
		Drain: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "the drain must run under a budget")
			drainShare = time.Until(deadline)
			return nil
		},
		ShutdownHTTP: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "HTTP shutdown must run under a budget")
			httpShare = time.Until(deadline)
			return nil
		},
		HardExit: func() { t.Error("hard exit must not fire") },
	}

	runShutdownSequence(steps)

	assert.InDelta(t, (25 * time.Second).Seconds(), drainShare.Seconds(), 0.5)
	assert.InDelta(t, (5 * time.Second).Seconds(), httpShare.Seconds(), 0.5)
}

func TestRunShutdownSequenceShutsHTTPDownAfterDrainTimeout(t *testing.T) {
	log := &eventLog{}
	steps := shutdownSteps{
		Logger:    discardShutdownLogger(),
		Budget:    300 * time.Millisecond,
		CloseJobs: func() { log.add("close") },
		Drain: func(ctx context.Context) error {
			<-ctx.Done()
			log.add("drain-timed-out")
			return ctx.Err()
		},
		Running:      func() int64 { return 3 },
		PendingLoops: func() int64 { return 1 },
		ShutdownHTTP: func(context.Context) error { log.add("http"); return nil },
		HardExit:     func() { log.add("hard-exit") },
	}

	start := time.Now()
	runShutdownSequence(steps)

	assert.Less(t, time.Since(start), 300*time.Millisecond,
		"REQ-TMO-004: an undrained job must not hold the whole shutdown open")
	assert.Equal(t, []string{"close", "drain-timed-out", "http"}, log.snapshot())
}

func TestRunShutdownSequenceHardExitsWhenTheBudgetIsSpent(t *testing.T) {
	log := &eventLog{}
	blocked := make(chan struct{})
	hardExit := make(chan struct{})
	neverReturn := make(chan struct{})

	steps := shutdownSteps{
		Logger:    discardShutdownLogger(),
		Budget:    100 * time.Millisecond,
		CloseJobs: func() { log.add("close") },
		Drain: func(context.Context) error {
			<-blocked
			log.add("drain")
			return nil
		},
		ShutdownHTTP: func(context.Context) error { log.add("http"); return nil },
		HardExit: func() {
			log.add("hard-exit")
			close(hardExit)
			// Governing: SPEC graceful-shutdown REQ-TMO-002 - os.Exit never returns,
			// so the stand-in blocks too.
			<-neverReturn
		},
	}

	finished := make(chan struct{})
	go func() {
		runShutdownSequence(steps)
		close(finished)
	}()

	select {
	case <-hardExit:
	case <-time.After(2 * time.Second):
		t.Fatal("REQ-TMO-002: the hard exit timer never fired")
	}
	assert.Equal(t, []string{"close", "hard-exit"}, log.snapshot(),
		"the hard exit ends the run before HTTP shutdown is attempted")

	close(blocked)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the sequence never returned once the drain was released")
	}
}

func TestRunShutdownSequenceReportsHTTPError(t *testing.T) {
	steps := shutdownSteps{
		Logger:       discardShutdownLogger(),
		Budget:       time.Second,
		CloseJobs:    func() {},
		Drain:        func(context.Context) error { return nil },
		ShutdownHTTP: func(context.Context) error { return context.Canceled },
		HardExit:     func() { t.Error("hard exit must not fire") },
	}
	runShutdownSequence(steps)
}

func TestGuardedTickSkipsTickWhileRunIsInFlight(t *testing.T) {
	var g loopGuard
	var ran atomic.Int64
	secondResult := make(chan bool, 1)

	ranFirst := guardedTick(&g, discardShutdownLogger(), "metadata", func() {
		ran.Add(1)
		// A tick landing while this run is still going must be skipped, not stacked.
		secondResult <- guardedTick(&g, discardShutdownLogger(), "metadata", func() { ran.Add(1) })
	})

	assert.True(t, ranFirst)
	assert.False(t, <-secondResult, "REQ-ENRICH-041: the overlapping tick must be skipped")
	assert.EqualValues(t, 1, ran.Load())

	assert.False(t, g.Busy(), "the guard is released when the run returns")
	assert.True(t, guardedTick(&g, discardShutdownLogger(), "metadata", func() { ran.Add(1) }),
		"the next tick runs once the long one is done")
	assert.EqualValues(t, 2, ran.Load())
}

func TestGuardedTickReleasesGuardWhenTickPanics(t *testing.T) {
	var g loopGuard
	func() {
		defer func() { _ = recover() }()
		guardedTick(&g, discardShutdownLogger(), "metadata", func() { panic("boom") })
	}()
	assert.False(t, g.Busy(), "a panicking tick must not wedge the loop")
}

func TestLoopSetJoinDoesNotWaitForTheJobsItSpawned(t *testing.T) {
	jobs := lifecycle.NewJobs(context.Background())
	var loops loopSet

	release := make(chan struct{})
	loopReturned := make(chan struct{})
	loops.Start(func() {
		defer close(loopReturned)
		jobs.StartWith(func() {}, func(context.Context) { <-release })
	})
	<-loopReturned

	require.NoError(t, loops.Wait(context.Background()),
		"REQ-WG-004: joining a ticker loop must not wait for the work it spawned")
	assert.EqualValues(t, 0, loops.Pending())
	assert.EqualValues(t, 1, jobs.Running(), "the spawned job is still draining")

	close(release)
	jobs.Close()
	assert.NoError(t, jobs.Wait(context.Background()))
}

func TestLoopSetWaitReportsTimeout(t *testing.T) {
	var loops loopSet
	release := make(chan struct{})
	loops.Start(func() { <-release })
	assert.EqualValues(t, 1, loops.Pending())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, loops.Wait(ctx), context.DeadlineExceeded)

	close(release)
	assert.NoError(t, loops.Wait(context.Background()))
	assert.EqualValues(t, 0, loops.Pending())
}

func TestDrainBackgroundWaitsForLoopsAndTheirJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := lifecycle.NewJobs(ctx)
	var loops loopSet
	order := &eventLog{}

	loops.Start(func() { <-ctx.Done() })
	require.True(t, jobs.Start(func(context.Context) {
		time.Sleep(60 * time.Millisecond)
		order.add("job")
	}))
	jobs.Close()

	cancel()
	require.NoError(t, drainBackground(context.Background(), &loops, jobs))
	assert.Equal(t, []string{"job"}, order.snapshot())
	assert.EqualValues(t, 0, loops.Pending())
}

func TestDrainBackgroundReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := lifecycle.NewJobs(ctx)
	var loops loopSet
	release := make(chan struct{})
	loops.Start(func() {
		<-ctx.Done()
		<-release
	})
	jobs.Close()
	cancel()

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer drainCancel()
	assert.ErrorIs(t, drainBackground(drainCtx, &loops, jobs), context.DeadlineExceeded)

	close(release)
}

func TestRunShutdownSequenceWithRealLoopsAndJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := lifecycle.NewJobs(ctx)
	var loops loopSet
	order := &eventLog{}

	loops.Start(func() { <-ctx.Done() })
	require.True(t, jobs.Start(func(jobCtx context.Context) {
		select {
		case <-time.After(60 * time.Millisecond):
		case <-jobCtx.Done():
		}
		order.add("job")
	}))

	steps := shutdownSteps{
		Logger:    discardShutdownLogger(),
		Budget:    2 * time.Second,
		CloseJobs: jobs.Close,
		Drain: func(drainCtx context.Context) error {
			return drainBackground(drainCtx, &loops, jobs)
		},
		Running:      jobs.Running,
		PendingLoops: loops.Pending,
		ShutdownHTTP: func(context.Context) error { order.add("http"); return nil },
		HardExit:     func() { t.Error("hard exit must not fire on a clean drain") },
	}

	cancel()
	runShutdownSequence(steps)

	assert.Equal(t, []string{"job", "http"}, order.snapshot(),
		"the in-flight job finishes before the listener stops, so SIGTERM under load drains inside the budget")
	assert.EqualValues(t, 0, jobs.Running())
}
