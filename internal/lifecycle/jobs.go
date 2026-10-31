// Package lifecycle carries work that outlives the code that started it into
// the application's shutdown sequence.
//
// Governing: ADR-0018 (graceful shutdown and context cancellation),
// ADR-0013 (goroutine ticker background scheduling),
// SPEC graceful-shutdown REQ-CTX-002, REQ-CTX-003, REQ-WG-001, REQ-WG-002, REQ-WG-003.
package lifecycle

import (
	"context"
	"sync"
)

// Jobs tracks background goroutines that have to finish before the process
// exits. Every job inherits the application context, so cancelling it asks the
// jobs to stop; Close refuses further jobs; Wait joins the ones already
// running within a drain budget.
type Jobs struct {
	ctx context.Context

	mu      sync.Mutex
	closing bool
	running int64
	wg      sync.WaitGroup
}

// NewJobs returns a tracker whose jobs are cancelled when ctx is cancelled.
//
// Governing: SPEC graceful-shutdown REQ-CTX-003 (background work uses the root context)
func NewJobs(ctx context.Context) *Jobs {
	return &Jobs{ctx: ctx}
}

// Context is the context every started job receives.
//
// Governing: SPEC graceful-shutdown REQ-CTX-002 (cancelled context passed to service methods)
func (j *Jobs) Context() context.Context {
	return j.ctx
}

// Start runs fn in a tracked goroutine and returns whether it was started.
func (j *Jobs) Start(fn func(context.Context)) bool {
	return j.StartWith(nil, fn)
}

// StartWith runs fn in a tracked goroutine and returns whether it started.
// onEnd runs exactly once either way, which lets a caller keep per-tick
// bookkeeping (a tick WaitGroup, for example) balanced when a job is refused
// because shutdown has begun.
//
// Governing: SPEC graceful-shutdown REQ-WG-002 (Add before spawn, Done deferred first)
func (j *Jobs) StartWith(onEnd func(), fn func(context.Context)) bool {
	j.mu.Lock()
	if j.closing {
		j.mu.Unlock()
		if onEnd != nil {
			onEnd()
		}
		return false
	}
	j.wg.Add(1)
	j.running++
	j.mu.Unlock()

	go func() {
		defer func() {
			j.mu.Lock()
			j.running--
			j.mu.Unlock()
			j.wg.Done()
			if onEnd != nil {
				onEnd()
			}
		}()
		fn(j.ctx)
	}()
	return true
}

// Close refuses further jobs. It is the barrier that keeps a WaitGroup from
// being added to while it is being waited on: call it before Wait.
func (j *Jobs) Close() {
	j.mu.Lock()
	j.closing = true
	j.mu.Unlock()
}

// Closed reports whether shutdown has begun refusing jobs.
func (j *Jobs) Closed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closing
}

// Running is how many jobs have started but not returned.
//
// Governing: SPEC graceful-shutdown REQ-TMO-004 (report still-running work when the drain budget is spent)
func (j *Jobs) Running() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running
}

// Wait blocks until every started job has returned, drainCtx expires, or the
// job context is cancelled. It returns drainCtx's error when it gave up early.
//
// Governing: SPEC graceful-shutdown REQ-WG-003 (drain per-user goroutines before exiting)
func (j *Jobs) Wait(drainCtx context.Context) error {
	done := make(chan struct{})
	go func() {
		j.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-drainCtx.Done():
		return drainCtx.Err()
	}
}
