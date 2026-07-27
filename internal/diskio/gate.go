// Package diskio stages whole-file RAW reads for background decodes. A small
// per-device semaphore keeps a spinning disk streaming near-sequentially
// (~full bandwidth) instead of thrashing between N concurrent LibRaw readers
// (~10% of it); on SSDs decode is the bottleneck, so the cap costs nothing —
// one gate for every drive type, no detection.
package diskio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/sysmem"
)

// gatePerDev is how many staged reads may touch one device at once. 2 rather
// than 1 so the disk never idles between one file's last chunk and the next
// job's first; two interleaved sequential streams cost an HDD a few percent,
// nothing like the random-access collapse this package exists to avoid.
const gatePerDev = 2

// readChunk is the read granularity; a canceled job releases the disk within
// one chunk. Variable so tests can shrink it.
var readChunk = 8 << 20

const gib = 1 << 30

// ErrTooLarge marks a file the buffered path won't stage; callers fall back
// to a direct LibRaw open.
var ErrTooLarge = errors.New("diskio: file too large for buffered read")

// Gate serializes bulk RAW reads per storage device and bounds the total C
// memory held in staged buffers (invisible to GOMEMLIMIT). A nil *Gate is
// valid and simply disables staging.
type Gate struct {
	perDev int
	budget *semaphore.Weighted
	total  int64 // budget size, for logging
	max    int64 // per-file cap; larger files fall back to direct open
	mu     sync.Mutex
	devs   map[string]chan struct{}
}

// NewGate sizes the byte budget from physical RAM: an eighth of it, at least
// 1 GiB (also the fallback when sysmem fails — conservative but functional).
func NewGate() *Gate {
	budget := int64(gib)
	if st, err := sysmem.Query(); err == nil {
		budget = max(int64(st.TotalPhys)/8, int64(gib))
	}
	return newGate(gatePerDev, budget)
}

func newGate(k int, budgetBytes int64) *Gate {
	return &Gate{
		perDev: k,
		budget: semaphore.NewWeighted(budgetBytes),
		total:  budgetBytes,
		// Half the budget, so one file can never wedge Acquire forever and
		// at least two staged files always fit.
		max:  budgetBytes / 2,
		devs: make(map[string]chan struct{}),
	}
}

// Budget reports the byte budget, for the startup log line.
func (g *Gate) Budget() int64 { return g.total }

func (g *Gate) dev(path string) chan struct{} {
	key := deviceKey(path)
	if key == "" {
		key = "global"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ch, ok := g.devs[key]
	if !ok {
		ch = make(chan struct{}, g.perDev)
		g.devs[key] = ch
	}
	return ch
}

// ReadFile stages path into a C-allocated buffer under the device gate.
// The returned FileBuf's Free (usually via Processor ownership) returns the
// bytes to the budget. Errors other than ctx.Err() mean "couldn't stage" —
// callers fall back to a direct open.
func (g *Gate) ReadFile(ctx context.Context, path string) (*libraw.FileBuf, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size <= 0 || size > g.max {
		return nil, fmt.Errorf("%w: %s is %d bytes (cap %d)", ErrTooLarge, path, size, g.max)
	}

	// Budget before device token: never hold the scarce disk slot while
	// blocked on memory. Both waits honor ctx, so ordering stays deadlock-free.
	if err := g.budget.Acquire(ctx, size); err != nil {
		return nil, err
	}
	tok := g.dev(path)
	select {
	case tok <- struct{}{}:
	case <-ctx.Done():
		g.budget.Release(size)
		return nil, ctx.Err()
	}
	defer func() { <-tok }() // token covers disk time only, not the decode

	fb, err := libraw.NewFileBuf(int(size), func() { g.budget.Release(size) })
	if err != nil {
		g.budget.Release(size)
		return nil, err
	}
	if err := g.readInto(ctx, path, fb.Bytes()); err != nil {
		fb.Free()
		return nil, err
	}
	return fb, nil
}

// readInto streams the file in readChunk pieces, checking ctx before each so
// cancellation frees the disk fast.
func (g *Gate) readInto(ctx context.Context, path string, dst []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for off := 0; off < len(dst); off += readChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(off+readChunk, len(dst))
		if _, err := io.ReadFull(f, dst[off:end]); err != nil {
			// Short read: the file shrank since Stat — unusable either way.
			return fmt.Errorf("diskio: read %s: %w", path, err)
		}
	}
	return nil
}
