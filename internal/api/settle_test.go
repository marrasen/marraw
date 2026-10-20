package api

import (
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// TestWaitForSettle: the post-save warm waits for the editor's settle of
// the same state while it runs, and only briefly for one that never starts
// (a reset renders none).
func TestWaitForSettle(t *testing.T) {
	cache, err := pyramid.New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	d := &Deps{Cache: cache}
	p := store.Photo{CacheKey: "abcdef"}

	start := time.Now()
	d.waitForSettle(t.Context(), p, "h")
	if took := time.Since(start); took < settleStartWait || took > settleStartWait+time.Second {
		t.Errorf("with no settle, waited %v; want about %v", took, settleStartWait)
	}

	done := d.settling("abcdef|h")
	go func() {
		time.Sleep(settleStartWait + 300*time.Millisecond)
		done()
	}()
	start = time.Now()
	d.waitForSettle(t.Context(), p, "h")
	if took := time.Since(start); took < settleStartWait+300*time.Millisecond {
		t.Errorf("left after %v, before the settle under way ended", took)
	}
}
