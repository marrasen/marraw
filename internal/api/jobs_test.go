package api

import (
	"context"
	"sync"
	"testing"

	"github.com/marrasen/marraw/internal/store"
)

// TestScheduleOutwardFromFocus checks the pre-render scheduler renders work in
// non-decreasing distance from the focused photo, so the rendition nearest
// where the user is looking warms first. One worker makes the order fully
// deterministic; the invariant (distance never decreases) holds regardless of
// how ties break.
func TestScheduleOutwardFromFocus(t *testing.T) {
	const n = 12
	photos := make([]store.Photo, n)
	idPos := make(map[int64]int, n)
	var work []focusItem
	for i := range photos {
		photos[i] = store.Photo{ID: int64(i)*10 + 1} // ids distinct from positions
		idPos[photos[i].ID] = i
		work = append(work, focusItem{p: photos[i], pos: i})
	}

	run := func(focusID int64) []int {
		l := &Library{deps: &Deps{}}
		l.deps.focusPhotoID.Store(focusID)
		var mu sync.Mutex
		var order []int
		err := l.scheduleOutwardFromFocus(context.Background(), append([]focusItem(nil), work...), idPos, 1,
			func(_ context.Context, p store.Photo) {
				mu.Lock()
				order = append(order, idPos[p.ID])
				mu.Unlock()
			})
		if err != nil {
			t.Fatalf("scheduleOutwardFromFocus: %v", err)
		}
		if len(order) != n {
			t.Fatalf("rendered %d items, want %d", len(order), n)
		}
		return order
	}

	// Focus mid-folder: distance from the focus position must never decrease.
	focusPos := 7
	order := run(photos[focusPos].ID)
	if order[0] != focusPos {
		t.Errorf("first rendered pos = %d, want the focused pos %d", order[0], focusPos)
	}
	for i := 1; i < len(order); i++ {
		if focusDist(order[i], focusPos) < focusDist(order[i-1], focusPos) {
			t.Errorf("distance decreased at %d: %v", i, order)
			break
		}
	}

	// Unset focus resolves to position 0 — the front-to-back fallback.
	order = run(0)
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Errorf("unset focus should render front-to-back, got %v", order)
			break
		}
	}
}
