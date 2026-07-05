package decode

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/libraw"
)

// TestPoolDropsAbandonedQueuedJob verifies that a queued job whose only
// waiter cancels never runs — the fix for rapid arrow-key navigation piling
// up full renders.
func TestPoolDropsAbandonedQueuedJob(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	go p.Do(context.Background(), "running", PriorityVisible, func(ctx context.Context, _ *libraw.Processor) error {
		close(started)
		<-block
		return nil
	})
	<-started // the single worker is now busy

	ran := false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- p.Do(ctx, "queued", PriorityVisible, func(ctx context.Context, _ *libraw.Processor) error {
			ran = true
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond) // let it enqueue
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do after cancel: got %v, want context.Canceled", err)
	}

	close(block) // free the worker; the queued job must not run
	// Re-submit under the same key: a dropped job must not shadow new work.
	if err := p.Do(context.Background(), "queued", PriorityVisible, func(ctx context.Context, _ *libraw.Processor) error {
		return nil
	}); err != nil {
		t.Fatalf("fresh Do after drop: %v", err)
	}
	if ran {
		t.Fatal("abandoned queued job ran anyway")
	}
}

// TestPoolCancelsRunningJobCtx verifies that the last waiter leaving cancels
// the fn ctx of a running job, and that a new waiter does not join the
// doomed run but gets a fresh one.
func TestPoolCancelsRunningJobCtx(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	canceled := make(chan struct{})
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- p.Do(ctx, "job", PriorityVisible, func(jctx context.Context, _ *libraw.Processor) error {
			close(started)
			<-jctx.Done() // simulate a render noticing the checkpoint
			close(canceled)
			return jctx.Err()
		})
	}()
	<-started
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("running job ctx was not canceled after the last waiter left")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do: got %v, want context.Canceled", err)
	}

	// A new waiter for the same key must get a fresh, non-canceled run.
	if err := p.Do(context.Background(), "job", PriorityVisible, func(jctx context.Context, _ *libraw.Processor) error {
		return jctx.Err()
	}); err != nil {
		t.Fatalf("fresh Do after abandoned run: %v", err)
	}
}

// TestPoolSharedJobSurvivesOneWaiterLeaving verifies waiter refcounting: a
// job with two waiters keeps running when only one cancels.
func TestPoolSharedJobSurvivesOneWaiterLeaving(t *testing.T) {
	p := NewPool(1)
	defer p.Close()

	release := make(chan struct{})
	started := make(chan struct{})
	result := make(chan error, 2)
	submit := func(ctx context.Context, first bool) {
		result <- p.Do(ctx, "shared", PriorityVisible, func(jctx context.Context, _ *libraw.Processor) error {
			if first {
				close(started)
			}
			<-release
			return jctx.Err()
		})
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	go submit(ctx1, true)
	<-started
	go submit(context.Background(), false) // joins the running job
	time.Sleep(50 * time.Millisecond)

	cancel1() // first waiter leaves; second still waits
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: got %v, want context.Canceled", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("remaining waiter: got %v, want nil (job ctx must stay live)", err)
	}
}
