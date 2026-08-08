//go:build unix

// The stall this file pins needs an open that genuinely blocks, which a FIFO
// gives us for free — LibRaw's Open reads the header and a reader with no
// writer waits. Windows has no cheap equivalent, so the check is unix-only;
// the code it covers is platform-independent.
package decode

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A RAW living on a spun-down drive or a stalled network share takes seconds
// to open. That is unavoidable — but it used to happen while holding the lock
// every other photo's Acquire needs, so one unreachable file froze culling
// through photos that were already open.
func TestAcquireDoesNotStallOtherPhotosWhileOpening(t *testing.T) {
	dir := t.TempDir()
	stalled := filepath.Join(dir, "stalled.arw")
	if err := syscall.Mkfifo(stalled, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	hc := NewHandleCache(4)

	// Hand the blocked opener an EOF at the end so it unwinds instead of
	// sitting in LibRaw for the rest of the run.
	slowDone := make(chan struct{})
	t.Cleanup(func() {
		if w, err := os.OpenFile(stalled, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
		<-slowDone
		hc.Close()
	})

	go func() {
		defer close(slowDone)
		proc, release, err := hc.Acquire(1, stalled)
		if err == nil {
			_ = proc
			release()
		}
	}()

	// Let it get all the way into the open.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hc.mu.Lock()
		e, ok := hc.entries[1]
		inOpen := ok && e.proc == nil
		hc.mu.Unlock()
		if inOpen {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Now a second, unrelated photo. Its own open fails immediately — there is
	// no such file — and that is the whole point: it has to reach its open at
	// all rather than queue behind the first photo's.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := hc.Acquire(2, filepath.Join(dir, "missing.arw")); err == nil {
			t.Error("Acquire of a missing file unexpectedly succeeded")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire for a second photo blocked behind the first photo's open")
	}

	// A failed open must not leave its reservation behind, or the photo would
	// be permanently unopenable.
	hc.mu.Lock()
	_, stillThere := hc.entries[2]
	hc.mu.Unlock()
	if stillThere {
		t.Error("a failed open left an entry in the cache")
	}
}
