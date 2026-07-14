package api

import (
	"database/sql"
	"testing"

	"github.com/marrasen/marraw/internal/store"
)

// bp builds a capture-ordered test photo. hash < 0 means unhashed.
func bp(id, takenAt int64, hash int64) store.Photo {
	p := store.Photo{ID: id, TakenAt: takenAt}
	if hash >= 0 {
		p.PHash = sql.NullInt64{Int64: hash, Valid: true}
	}
	return p
}

func TestBurstGroups(t *testing.T) {
	// Base hash and a composition 32 bits away; h^1 etc. stay within
	// Hamming 10. (Both non-negative — bp reserves negative for unhashed.)
	const h, hFar = 0x0f0f_0f0f_0f0f_0f0f, 0x00ff_00ff_00ff_00ff
	cases := []struct {
		name   string
		photos []store.Photo
		want   map[int64]int64
	}{
		{"empty", nil, map[int64]int64{}},
		{"burst chains and takes lead id",
			[]store.Photo{bp(1, 100, h), bp(2, 101, h^1), bp(3, 103, h^3), bp(9, 300, h)},
			map[int64]int64{1: 1, 2: 1, 3: 1}},
		{"time gap splits identical hashes",
			[]store.Photo{bp(1, 100, h), bp(2, 105, h)},
			map[int64]int64{}},
		{"hash distance splits adjacent frames",
			[]store.Photo{bp(1, 100, h), bp(2, 101, hFar)},
			map[int64]int64{}},
		{"two separate bursts",
			[]store.Photo{bp(1, 100, h), bp(2, 101, h), bp(3, 200, hFar), bp(4, 201, hFar)},
			map[int64]int64{1: 1, 2: 1, 3: 3, 4: 3}},
		{"untimed photos never group",
			[]store.Photo{bp(1, 0, h), bp(2, 0, h)},
			map[int64]int64{}},
		{"unhashed photo breaks the chain",
			[]store.Photo{bp(1, 100, h), bp(2, 101, -1), bp(3, 102, h)},
			map[int64]int64{}},
		{"chain drift: each link close, ends far",
			// 100→101→102 each within the gap; the middle frame bridges.
			[]store.Photo{bp(1, 100, h), bp(2, 104, h^1), bp(3, 108, h^3)},
			map[int64]int64{1: 1, 2: 1, 3: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := burstGroups(tc.photos)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for id, g := range tc.want {
				if got[id] != g {
					t.Fatalf("photo %d: got group %d, want %d (full: %v)", id, got[id], g, got)
				}
			}
		})
	}
}
