package decode

import (
	"context"
	"log"

	"github.com/marrasen/marraw/internal/diskio"
	"github.com/marrasen/marraw/internal/libraw"
)

// ShouldBuffer reports whether a full-decode job at prio takes the staged-I/O
// path. Visible and interactive jobs never do: their latency must not queue
// behind background reads on the device gate.
func ShouldBuffer(g *diskio.Gate, prio Priority) bool {
	return g != nil && prio < PriorityVisible
}

// OpenForDecode opens path on proc for a full decode. Background and prefetch
// jobs stage the whole file through the per-device gate and decode from
// memory; higher priorities, a nil gate, and any staging failure short of
// cancellation fall back to the direct LibRaw open.
//
// A visible waiter that joins an already-queued background job via pool dedup
// still waits that job's gate turn — the same class of wait as joining its
// decode, bounded by the gate's small K and ctx-cancellable.
func OpenForDecode(ctx context.Context, g *diskio.Gate, proc *libraw.Processor, path string, prio Priority) error {
	if ShouldBuffer(g, prio) {
		fb, err := g.ReadFile(ctx, path)
		if err == nil {
			return proc.OpenBuffer(fb) // ownership transfers, even on failure
		}
		if ctx.Err() != nil {
			return ctx.Err() // never mask cancellation with a fallback open
		}
		log.Printf("diskio: staged read of %s failed, direct open: %v", path, err)
	}
	return proc.Open(path)
}
