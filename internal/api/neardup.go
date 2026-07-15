package api

import (
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

// Near-duplicate burst grouping. Groups are derived, never stored: the
// calibrate pass persists one perceptual hash per photo (store.Photo.PHash)
// and ListPhotos re-clusters on every list, so photos arriving or leaving a
// folder can never strand a stale group id.
const (
	// burstMaxGapSeconds joins consecutive frames shot at most this far
	// apart. taken_at is whole-second (LibRaw carries no sub-second), so a
	// tight window still spans several rounded seconds of continuous
	// shooting; the hash gate below does the discriminating.
	burstMaxGapSeconds = 4
	// burstHammingDefault is the dHash distance (of 64 bits) up to which two
	// adjacent frames count as the same composition, unless the user tunes
	// it. 10 is the classic near-duplicate cutoff, but an expressive pose
	// change in a locked-off frame reads to dHash like a recompose, so the
	// default runs looser to keep posed-portrait bursts together; the
	// "Burst grouping" setting overrides it (see burstHammingSetting).
	burstHammingDefault = 18
	// burstHammingMin / burstHammingMax bound the tunable cutoff. Below the
	// floor almost nothing groups; above the ceiling (~half of 64 bits, where
	// dHash distance is essentially random) genuinely different frames merge.
	burstHammingMin = 4
	burstHammingMax = 30
)

// burstGroups clusters a capture-ordered photo list into near-duplicate
// groups: consecutive frames chain while they are close in time AND
// perceptually similar (within maxHamming dHash bits). Only groups of two or
// more get an id — the first member's photo ID, so ids are stable across
// refreshes as long as the group's lead frame stays. Untimed (taken_at = 0)
// and unhashed photos never group.
func burstGroups(photos []store.Photo, maxHamming int) map[int64]int64 {
	groups := make(map[int64]int64)
	start := 0 // index of the current chain's first member
	for i := 1; i <= len(photos); i++ {
		if i < len(photos) && linked(photos[i-1], photos[i], maxHamming) {
			continue
		}
		if i-start >= 2 {
			id := photos[start].ID
			for _, p := range photos[start:i] {
				groups[p.ID] = id
			}
		}
		start = i
	}
	return groups
}

func linked(a, b store.Photo, maxHamming int) bool {
	if a.TakenAt == 0 || b.TakenAt == 0 || b.TakenAt-a.TakenAt > burstMaxGapSeconds {
		return false
	}
	if !a.PHash.Valid || !b.PHash.Valid {
		return false
	}
	return pyramid.HammingDist(uint64(a.PHash.Int64), uint64(b.PHash.Int64)) <= maxHamming
}
