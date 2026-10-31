package main

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"spotter/internal/lifecycle"
)

// defaultHTTPShutdownBudget is the share of the shutdown budget reserved for
// draining HTTP requests, per SPEC graceful-shutdown REQ-TMO-003 step 3, which
// pairs it with a 25 second goroutine drain out of the 30 second total.
const defaultHTTPShutdownBudget = 5 * time.Second

// Governing: SPEC graceful-shutdown REQ-WG-004 (ticker loops are tracked apart
// from the per-user work they spawn), ADR-0013 (goroutine ticker scheduling)
// loopSet tracks the background ticker loops themselves. The work a loop
// spawns is tracked by a lifecycle.Jobs tracker instead, so waiting for a loop
// to return from its select never waits on the jobs it launched.
type loopSet struct {
	wg   sync.WaitGroup
	live atomic.Int64
}

// Start runs a loop goroutine.
//
// Governing: SPEC graceful-shutdown REQ-CTX-001 (loops exit on ctx.Done)
func (l *loopSet) Start(fn func()) {
	l.wg.Add(1)
	l.live.Add(1)
	go func() {
		defer func() {
			l.live.Add(-1)
			l.wg.Done()
		}()
		fn()
	}()
}

// Wait blocks until every loop has returned or ctx expires.
func (l *loopSet) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pending is how many loops have not returned yet.
func (l *loopSet) Pending() int64 {
	return l.live.Load()
}

// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-041 (a tick that
// lands while the previous run is still going is skipped)
// loopGuard lets one background loop skip a tick while its previous tick is
// still running, no matter how long that tick takes.
type loopGuard struct {
	mu   sync.Mutex
	busy bool
}

// TryEnter takes the guard, reporting false when it is already held.
func (g *loopGuard) TryEnter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy {
		return false
	}
	g.busy = true
	return true
}

// Exit releases the guard.
func (g *loopGuard) Exit() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.busy = false
}

// Busy reports whether a tick is in progress.
func (g *loopGuard) Busy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.busy
}

// Governing: SPEC graceful-shutdown REQ-TMO-003 (drain budget, then HTTP budget)
// drainAndHTTPBudgets splits the total shutdown budget into the background
// drain allowance and the HTTP shutdown allowance. The HTTP share stays at
// defaultHTTPShutdownBudget unless the whole budget is too small to give both
// phases a fair share, in which case they split it evenly.
func drainAndHTTPBudgets(total time.Duration) (drain time.Duration, httpBudget time.Duration) {
	if total <= 0 {
		return 0, 0
	}
	httpBudget = defaultHTTPShutdownBudget
	if httpBudget*2 > total {
		httpBudget = total / 2
	}
	drain = total - httpBudget
	if drain <= 0 {
		drain = total
	}
	return drain, httpBudget
}

// Governing: ADR-0018 (graceful shutdown), SPEC graceful-shutdown REQ-TMO-002,
// REQ-TMO-003, REQ-TMO-004, REQ-WG-003, REQ-CTX-004
// shutdownSteps is the shutdown sequence with every side effect pulled out, so
// the ordering is testable without a listener, a database or a process signal.
type shutdownSteps struct {
	Logger *slog.Logger
	// Budget is the whole shutdown budget from the first signal (REQ-TMO-001).
	Budget time.Duration
	// CloseJobs stops background work from being accepted.
	CloseJobs func()
	// Drain waits for background work already running, and returns the drain
	// context's error when it could not finish in time.
	Drain func(ctx context.Context) error
	// Running reports how many background jobs are still going.
	Running func() int64
	// PendingLoops reports how many ticker loops have not exited.
	PendingLoops func() int64
	// ShutdownHTTP stops the HTTP listener and waits for in-flight requests.
	ShutdownHTTP func(ctx context.Context) error
	// HardExit ends the process when the whole budget is spent (REQ-TMO-002).
	HardExit func()
}

// runShutdownSequence drains background work first and only then shuts the HTTP
// server down, which is the order REQ-TMO-003 specifies. The earlier code did
// the opposite: it gave the whole budget to srv.Shutdown and only then waited
// on background jobs, so jobs raced on a context that was already expiring.
func runShutdownSequence(s shutdownSteps) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	hardExit := s.HardExit
	if hardExit == nil {
		hardExit = func() { os.Exit(1) }
	}
	drainBudget, httpBudget := drainAndHTTPBudgets(s.Budget)

	stopHardExit := time.AfterFunc(s.Budget, hardExit)
	defer stopHardExit.Stop()

	if s.CloseJobs != nil {
		s.CloseJobs()
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainBudget)
	drainErr := s.Drain(drainCtx)
	cancelDrain()

	if drainErr != nil {
		// Governing: SPEC graceful-shutdown REQ-TMO-004 (report what is still
		// running, then carry on with HTTP shutdown anyway)
		running, pending := int64(0), int64(0)
		if s.Running != nil {
			running = s.Running()
		}
		if s.PendingLoops != nil {
			pending = s.PendingLoops()
		}
		logger.Warn("background jobs did not drain in time, shutting down HTTP server anyway",
			"error", drainErr,
			"drain_budget_ms", drainBudget.Milliseconds(),
			"jobs_running", running,
			"loops_pending", pending)
	} else {
		logger.Info("all background jobs finished cleanly")
	}

	// Governing: SPEC graceful-shutdown REQ-CTX-004 (HTTP server drains via Shutdown)
	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpBudget)
	defer cancelHTTP()
	if err := s.ShutdownHTTP(httpCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}
}

// guardedTick runs one tick of a background loop, and skips it while the
// previous tick still holds the guard. It reports whether the tick ran.
//
// Governing: SPEC metadata-enrichment-pipeline REQ-ENRICH-041 (a tick whose
// previous run is still going is skipped)
func guardedTick(g *loopGuard, logger *slog.Logger, loop string, tick func()) bool {
	if !g.TryEnter() {
		if logger != nil {
			logger.Info("skipping tick, previous run still in progress", "loop", loop)
		}
		return false
	}
	defer g.Exit()
	tick()
	return true
}

// drainBackground waits for the ticker loops and then for the jobs they and the
// HTTP handlers started, and reports the drain context's error if either outlives
// the budget.
//
// Governing: SPEC graceful-shutdown REQ-WG-003, REQ-WG-004
func drainBackground(ctx context.Context, loops *loopSet, jobs *lifecycle.Jobs) error {
	if err := loops.Wait(ctx); err != nil {
		return err
	}
	return jobs.Wait(ctx)
}
