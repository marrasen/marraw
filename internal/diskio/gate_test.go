package diskio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/libraw"
)

func tempRAW(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.arw")
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGateLimitsPerDevice(t *testing.T) {
	g := newGate(2, 1<<30)
	path := tempRAW(t, 1024)

	// Fill both device slots by hand; ReadFile must then block until one
	// frees, and honor cancellation while blocked.
	tok := g.dev(path)
	tok <- struct{}{}
	tok <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.ReadFile(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadFile with full gate: err=%v, want deadline exceeded", err)
	}
	if !g.budget.TryAcquire(1 << 30) {
		t.Fatal("budget leaked by blocked ReadFile")
	}
	g.budget.Release(1 << 30)

	<-tok // free one slot
	fb, err := g.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadFile with one free slot: %v", err)
	}
	fb.Free()
	<-tok
	if n := libraw.LiveFileBufs(); n != 0 {
		t.Fatalf("%d live buffers, want 0", n)
	}
}

// errAfter reports ctx.Err() = Canceled after limit calls — a deterministic
// stand-in for a cancellation landing between read chunks.
type errAfter struct {
	context.Context
	calls atomic.Int32
	limit int32
}

func (c *errAfter) Err() error {
	if c.calls.Add(1) > c.limit {
		return context.Canceled
	}
	return nil
}

func TestReadFileCancelMidRead(t *testing.T) {
	old := readChunk
	readChunk = 4 << 10
	defer func() { readChunk = old }()

	g := newGate(2, 1<<30)
	path := tempRAW(t, 64<<10) // 16 chunks

	ctx := &errAfter{Context: context.Background(), limit: 4}
	if _, err := g.ReadFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if n := libraw.LiveFileBufs(); n != 0 {
		t.Fatalf("%d live buffers after cancel, want 0", n)
	}
	if !g.budget.TryAcquire(1 << 30) {
		t.Fatal("budget not fully released after cancel")
	}
}

func TestPreCanceledCtx(t *testing.T) {
	g := newGate(2, 1<<30)
	path := tempRAW(t, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.ReadFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if !g.budget.TryAcquire(1 << 30) {
		t.Fatal("budget not fully released")
	}
}

func TestTooLargeAndBudgetRelease(t *testing.T) {
	g := newGate(2, 1024) // per-file cap 512
	if _, err := g.ReadFile(context.Background(), tempRAW(t, 600)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("600-byte file under 512 cap: err=%v, want ErrTooLarge", err)
	}

	fb, err := g.ReadFile(context.Background(), tempRAW(t, 400))
	if err != nil {
		t.Fatal(err)
	}
	if g.budget.TryAcquire(1024) {
		t.Fatal("budget not held while buffer live")
	}
	fb.Free()
	if !g.budget.TryAcquire(1024) {
		t.Fatal("budget not returned by Free")
	}
}

func TestDeviceKeyFallback(t *testing.T) {
	g := newGate(2, 1<<30)
	a := g.dev("/nonexistent/one")
	b := g.dev("/nonexistent/two")
	if a != b {
		t.Fatal("unknown devices should share the global gate")
	}
}

func TestReadFileMissing(t *testing.T) {
	g := newGate(2, 1<<30)
	if _, err := g.ReadFile(context.Background(), filepath.Join(t.TempDir(), "gone.arw")); err == nil {
		t.Fatal("ReadFile of missing file succeeded")
	}
	if !g.budget.TryAcquire(1 << 30) {
		t.Fatal("budget leaked on stat failure")
	}
}
