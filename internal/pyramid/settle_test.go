package pyramid

import (
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/store"
)

// TestWaitForSettle: a small rendition of a state the editor settles on
// waits for that settle while it runs, only briefly for one that never
// starts (a reset renders none), and not at all for a photo not held open
// in the editor.
func TestWaitForSettle(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := store.Photo{CacheKey: "abcdef"}

	start := time.Now()
	c.WaitForSettle(t.Context(), p, "h")
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("a photo not held waited %v", took)
	}

	defer c.HoldEditor(p)()
	start = time.Now()
	c.WaitForSettle(t.Context(), p, "h")
	if took := time.Since(start); took < settleStartWait || took > settleStartWait+time.Second {
		t.Errorf("with no settle, waited %v; want about %v", took, settleStartWait)
	}

	done := c.Settling(p, "h")
	go func() {
		time.Sleep(settleStartWait + 300*time.Millisecond)
		done()
	}()
	start = time.Now()
	c.WaitForSettle(t.Context(), p, "h")
	if took := time.Since(start); took < settleStartWait+300*time.Millisecond {
		t.Errorf("left after %v, before the settle under way ended", took)
	}
}
