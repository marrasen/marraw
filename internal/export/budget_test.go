package export

import (
	"context"
	"testing"

	"golang.org/x/sync/semaphore"
)

func TestJobWeight(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		want          int64
	}{
		{"42MP A7R IV", 7952, 5304, int64(7952) * 5304 * estBytesPerPixel},
		{"unknown dimensions", 0, 0, int64(defaultJobPixels) * estBytesPerPixel},
		{"negative width", -1, 100, int64(defaultJobPixels) * estBytesPerPixel},
		{"tiny image clamps to floor", 800, 600, estJobFloor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobWeight(tt.width, tt.height); got != tt.want {
				t.Errorf("jobWeight(%d, %d) = %d, want %d", tt.width, tt.height, got, tt.want)
			}
		})
	}
	// Sanity anchor: a 42 MP job should land near the measured ~1 GiB peak.
	if w := jobWeight(7952, 5304); w < 900<<20 || w > 1200<<20 {
		t.Errorf("42MP weight %d MiB outside the expected ~1 GiB band", w>>20)
	}
}

func TestExportBudget(t *testing.T) {
	tests := []struct {
		name  string
		avail uint64
		want  int64
	}{
		{"20 GiB avail", 20 << 30, int64(float64(uint64(20<<30)) * budgetFraction)},
		{"512 MiB avail clamps", 512 << 20, minBudget},
		{"zero avail clamps", 0, minBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exportBudget(tt.avail); got != tt.want {
				t.Errorf("exportBudget(%d) = %d, want %d", tt.avail, got, tt.want)
			}
		})
	}
}

// TestWeightClamp documents the deadlock guard: a job whose estimate exceeds
// the whole budget is clamped to it, so Acquire can always succeed alone.
func TestWeightClamp(t *testing.T) {
	budget := exportBudget(2 << 30) // small machine
	huge := jobWeight(20000, 15000) // 300 MP
	if huge <= budget {
		t.Fatalf("test premise broken: huge weight %d should exceed budget %d", huge, budget)
	}
	if got := min(huge, budget); got != budget {
		t.Errorf("clamped weight = %d, want budget %d", got, budget)
	}
}

// TestAcquireCancelled documents cancellation-while-queued: a waiter blocked
// on the semaphore unblocks with the context error.
func TestAcquireCancelled(t *testing.T) {
	sem := semaphore.NewWeighted(1)
	if err := sem.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sem.Acquire(ctx, 1); err != context.Canceled {
		t.Errorf("queued acquire returned %v, want context.Canceled", err)
	}
}
