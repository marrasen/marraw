package decode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/diskio"
)

func TestShouldBuffer(t *testing.T) {
	g := diskio.NewGate()
	cases := []struct {
		gate *diskio.Gate
		prio Priority
		want bool
	}{
		{nil, PriorityBackground, false},
		{nil, PriorityInteractive, false},
		{g, PriorityBackground, true},
		{g, PriorityPrefetch, true},
		{g, PriorityVisible, false},
		{g, PriorityInteractive, false},
	}
	for _, c := range cases {
		if got := ShouldBuffer(c.gate, c.prio); got != c.want {
			t.Errorf("ShouldBuffer(gate=%v, prio=%d) = %v, want %v", c.gate != nil, c.prio, got, c.want)
		}
	}
}

// A canceled staged read must return ctx.Err(), not fall back to a direct
// open. The nil Processor makes any fallback attempt fail loudly.
func TestOpenForDecodeCanceledNoFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.arw")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := OpenForDecode(ctx, diskio.NewGate(), nil, path, PriorityBackground)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}
