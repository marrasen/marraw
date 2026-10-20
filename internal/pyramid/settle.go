package pyramid

import (
	"context"
	"os"
	"time"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
)

// How long a small rendition of a photo open in the editor waits for the
// editor's 2048 settle of the same state: for it to start (a save and its
// settle leave the editor together, in either order), and in all.
const (
	settleStartWait = 500 * time.Millisecond
	settleWait      = 5 * time.Second
)

// Settling marks the editor's 2048 settle of photo under hash under way
// until the returned func is called. The small renditions of that state
// wait for it (WaitForSettle) and come from its 2048 in ~30 ms, where
// rendering them meanwhile would run a RAW decode beside the settle and
// slow it and the 1:1 render.
func (c *Cache) Settling(photo store.Photo, hash string) (done func()) {
	key := photo.CacheKey + "|" + hash
	ch := make(chan struct{})
	c.fullMu.Lock()
	if c.settles == nil {
		c.settles = map[string]chan struct{}{}
	}
	c.settles[key] = ch
	c.fullMu.Unlock()
	return func() {
		c.fullMu.Lock()
		if c.settles[key] == ch {
			delete(c.settles, key)
		}
		c.fullMu.Unlock()
		close(ch)
	}
}

// WaitForSettle waits, for a photo held open in the editor, until its 2048
// under hash is on disk, or for a settle of it that starts within
// settleStartWait to end. Some saves have none (a reset; a commit made while
// cropping settles the flattened frame), so a settle that does not start is
// not waited for long. It returns at once for a photo not held, the base
// look, or a 2048 already there.
func (c *Cache) WaitForSettle(ctx context.Context, photo store.Photo, hash string) {
	if hash == edit.BaseHash || !c.HeldInEditor(photo) {
		return
	}
	key := photo.CacheKey + "|" + hash
	path := c.PathFor(photo.CacheKey, "2048", hash)
	deadline := time.NewTimer(settleWait)
	defer deadline.Stop()
	start := time.Now()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		c.fullMu.Lock()
		ch := c.settles[key]
		c.fullMu.Unlock()
		if ch != nil {
			// It may end called off, and another settle of the state take
			// its place: look again.
			select {
			case <-ch:
				continue
			case <-ctx.Done():
			case <-deadline.C:
			}
			return
		}
		if time.Since(start) >= settleStartWait {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
