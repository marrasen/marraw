package libraw

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fullResParams mirrors the interactive 1:1 pipeline: a full-size PPG decode,
// the slowest Process this package runs — plenty of runway to cancel into.
func fullResParams() Params {
	p := DefaultParams()
	p.UserQual = DemosaicPPG
	return p
}

func TestProcessPreCancelled(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Process(ctx, DefaultParams()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Process with pre-cancelled ctx = %v, want context.Canceled", err)
	}
}

// TestProcessCancelMidflight is the empirical gate for the checked-in static
// lib: it proves libraw.a actually invokes the progress callback and honors a
// nonzero return. If this fails on timing, the lib predates the vendored
// source — rebuild with scripts/setup-libraw.ps1 -Force.
func TestProcessCancelMidflight(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Baseline: one uncancelled full-res PPG decode, unpack paid up front so
	// both measurements cover only the dcraw pipeline.
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpack(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := p.Process(context.Background(), fullResParams()); err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(start)

	// Cancelled run: fire 30 ms in — well inside the demosaic — and require
	// the abort to land at a checkpoint, far short of a full pipeline.
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpack(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	_, err = p.Process(ctx, fullResParams())
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Process = %v, want context.Canceled", err)
	}
	if elapsed >= baseline/2 {
		t.Fatalf("cancel took %v of a %v baseline — progress callbacks not firing; rebuild libraw (scripts/setup-libraw.ps1 -Force)", elapsed, baseline)
	}
	t.Logf("baseline=%v  cancelled after=%v", baseline, elapsed)
}

// TestProcessCancelThenReuse covers the recycle-on-cancel contract: LibRaw
// recycles the handle internally when the callback aborts, so the same
// Processor must Open again cleanly, report true (not half) dimensions —
// the params-hygiene lesson of TestRecycleResetsParams — and decode.
func TestProcessCancelThenReuse(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	full := p.Metadata()
	if err := p.Unpack(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	if _, err := p.Process(ctx, fullResParams()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Process = %v, want context.Canceled", err)
	}

	if err := p.Open(path); err != nil {
		t.Fatalf("re-Open after cancel: %v", err)
	}
	got := p.Metadata()
	if got.Width != full.Width || got.Height != full.Height {
		t.Fatalf("after cancel + re-Open, Metadata = %dx%d, want %dx%d",
			got.Width, got.Height, full.Width, full.Height)
	}
	half := DefaultParams()
	half.HalfSize = true
	if _, err := p.Process(context.Background(), half); err != nil {
		t.Fatalf("Process after cancel + re-Open: %v", err)
	}
}

// TestProcessLateCancelHygiene guards the watcher join: a ctx cancelled after
// Process returned must not leak a stale cancel into the next call's freshly
// zeroed flag.
func TestProcessLateCancelHygiene(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	half := DefaultParams()
	half.HalfSize = true
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := p.Process(ctx, half); err != nil {
		t.Fatal(err)
	}
	cancel() // after return: the joined watcher must be gone already
	if _, err := p.Process(context.Background(), half); err != nil {
		t.Fatalf("Process after late cancel of previous ctx: %v", err)
	}
}

// TestProcessReportsProgress exercises the observer path: a full-res decode
// with OnProgress set must yield a non-empty, monotonically non-decreasing
// fraction sequence that gets well into the pipeline.
func TestProcessReportsProgress(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	// Appends happen on the watcher goroutine; Process joins it before
	// returning, so reading fracs afterwards is race-free.
	var fracs []float64
	p.OnProgress(func(f float64) { fracs = append(fracs, f) })
	defer p.OnProgress(nil)
	if _, err := p.Process(context.Background(), fullResParams()); err != nil {
		t.Fatal(err)
	}
	if len(fracs) == 0 {
		t.Fatal("no progress reported for a full-res decode")
	}
	for i := 1; i < len(fracs); i++ {
		if fracs[i] < fracs[i-1] {
			t.Fatalf("progress went backwards at %d: %v", i, fracs)
		}
	}
	if last := fracs[len(fracs)-1]; last < 0.5 {
		t.Errorf("final fraction %.2f, want >= 0.5 (stage weights off?)", last)
	}
	t.Logf("%d samples: first=%.3f last=%.3f", len(fracs), fracs[0], fracs[len(fracs)-1])
}
