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
	// hPose is 14 bits from h — an expressive pose shift: past the classic
	// 10-bit cutoff but under the widened default of 18. XOR distance equals
	// the mask's popcount, and 0x3fff has 14 set bits.
	const hPose = h ^ 0x3fff
	cases := []struct {
		name       string
		photos     []store.Photo
		maxHamming int
		maxGap     int
		want       map[int64]int64
	}{
		{"empty", nil, burstHammingDefault, burstGapDefault, map[int64]int64{}},
		{"burst chains and takes lead id",
			[]store.Photo{bp(1, 100, h), bp(2, 101, h^1), bp(3, 103, h^3), bp(9, 300, h)},
			burstHammingDefault, burstGapDefault, map[int64]int64{1: 1, 2: 1, 3: 1}},
		{"time gap splits identical hashes",
			[]store.Photo{bp(1, 100, h), bp(2, 105, h)},
			burstHammingDefault, burstGapDefault, map[int64]int64{}},
		{"widened window chains across the same gap",
			[]store.Photo{bp(1, 100, h), bp(2, 105, h)},
			burstHammingDefault, 10, map[int64]int64{1: 1, 2: 1}},
		{"narrowed window splits a default-window burst",
			[]store.Photo{bp(1, 100, h), bp(2, 103, h)},
			burstHammingDefault, burstGapMin, map[int64]int64{}},
		{"hash distance splits adjacent frames",
			[]store.Photo{bp(1, 100, h), bp(2, 101, hFar)},
			burstHammingDefault, burstGapDefault, map[int64]int64{}},
		{"two separate bursts",
			[]store.Photo{bp(1, 100, h), bp(2, 101, h), bp(3, 200, hFar), bp(4, 201, hFar)},
			burstHammingDefault, burstGapDefault, map[int64]int64{1: 1, 2: 1, 3: 3, 4: 3}},
		{"untimed photos never group",
			[]store.Photo{bp(1, 0, h), bp(2, 0, h)},
			burstHammingDefault, burstGapDefault, map[int64]int64{}},
		{"unhashed photo breaks the chain",
			[]store.Photo{bp(1, 100, h), bp(2, 101, -1), bp(3, 102, h)},
			burstHammingDefault, burstGapDefault, map[int64]int64{}},
		{"chain drift: each link close, ends far",
			// 100→101→102 each within the gap; the middle frame bridges.
			[]store.Photo{bp(1, 100, h), bp(2, 104, h^1), bp(3, 108, h^3)},
			burstHammingDefault, burstGapDefault, map[int64]int64{1: 1, 2: 1, 3: 1}},
		{"pose shift (14 bits) splits at the classic 10 cutoff",
			[]store.Photo{bp(1, 100, h), bp(2, 101, hPose)},
			10, burstGapDefault, map[int64]int64{}},
		{"pose shift (14 bits) groups at the widened default",
			[]store.Photo{bp(1, 100, h), bp(2, 101, hPose)},
			burstHammingDefault, burstGapDefault, map[int64]int64{1: 1, 2: 1}},
		{"recompose (32 bits) still splits at the widened default",
			[]store.Photo{bp(1, 100, h), bp(2, 101, hFar)},
			burstHammingDefault, burstGapDefault, map[int64]int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := burstGroups(tc.photos, tc.maxHamming, tc.maxGap)
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
