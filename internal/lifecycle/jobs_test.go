package lifecycle_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"spotter/internal/lifecycle"
)

func TestJobsStartRunsJobOnTrackerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := lifecycle.NewJobs(ctx)

	started := make(chan context.Context, 1)
	require.True(t, jobs.Start(func(jobCtx context.Context) { started <- jobCtx }))

	assert.Equal(t, ctx, <-started, "jobs inherit the application context")
	require.NoError(t, jobs.Wait(context.Background()))
}

func TestJobsContextCancellationReachesJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := lifecycle.NewJobs(ctx)

	done := make(chan error, 1)
	require.True(t, jobs.Start(func(jobCtx context.Context) {
		<-jobCtx.Done()
		done <- jobCtx.Err()
	}))

	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("job never observed cancellation")
	}
	require.NoError(t, jobs.Wait(context.Background()))
}

func TestJobsWaitReportsDrainTimeout(t *testing.T) {
	jobs := lifecycle.NewJobs(context.Background())
	release := make(chan struct{})

	require.True(t, jobs.Start(func(context.Context) { <-release }))
	assert.EqualValues(t, 1, jobs.Running())

	jobs.Close()
	drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, jobs.Wait(drainCtx), context.DeadlineExceeded)

	close(release)
	require.Eventually(t, func() bool { return jobs.Running() == 0 }, 2*time.Second, 5*time.Millisecond)
	assert.NoError(t, jobs.Wait(context.Background()))
}

func TestJobsCloseRefusesNewJobsButStillRunsOnEnd(t *testing.T) {
	jobs := lifecycle.NewJobs(context.Background())

	var tickWG sync.WaitGroup
	tickWG.Add(1)

	jobs.Close()
	started := jobs.StartWith(tickWG.Done, func(context.Context) {
		t.Error("a job must not start after Close")
	})
	assert.False(t, started)
	assert.True(t, jobs.Closed())

	done := make(chan struct{})
	go func() {
		tickWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onEnd did not run for the refused job, so the tick would hang")
	}
	assert.EqualValues(t, 0, jobs.Running())
}

func TestJobsCloseBeforeWaitRefusesLateSpawns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := lifecycle.NewJobs(ctx)

	var spawns sync.WaitGroup
	spawns.Add(1)
	require.True(t, jobs.Start(func(context.Context) { spawns.Done() }))

	jobs.Close()
	assert.False(t, jobs.Start(func(context.Context) { t.Error("late spawn") }))

	spawns.Wait()
	assert.NoError(t, jobs.Wait(context.Background()),
		"Close before Wait keeps the WaitGroup from being added to while it is waited on")
}
