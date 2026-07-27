package aimask

import (
	"image"
	"sort"
)

// Person-instance planes: pixel = instance ID, 0 = background, 1..N ordered
// left-to-right (centroid.x asc, tie centroid.y asc). Centroid ordering is
// what keeps IDs stable across regenerations — positions don't jitter the
// way areas do — and makes "Person 1..N" chips read left-to-right.

const (
	// personScoreMin is the per-query person confidence cutoff.
	personScoreMin = 0.5
	// personProbMin binarizes per-pixel mask probability.
	personProbMin = 0.5
	// instanceMinFraction drops slivers below ~0.2% of the frame. People can
	// be small — deliberately much looser than the 1.5% category threshold.
	instanceMinFraction = 0.002
	// maxInstances caps a plane; beyond this a crowd is one adjustment away
	// from the People category mask anyway.
	maxInstances = 32
)

// InstanceMask is one detected person candidate at mask resolution.
type InstanceMask struct {
	Prob  []float32 // w*h row-major per-pixel probability
	Score float32
}

// ComposeInstancePlane flattens overlapping per-instance probability masks
// into one ID plane: each pixel joins the highest-probability instance above
// personProbMin (argmax resolves overlapping people cleanly), sub-area
// instances drop, and survivors relabel 1..N left-to-right.
func ComposeInstancePlane(insts []InstanceMask, w, h int) *image.Gray {
	dst := image.NewGray(image.Rect(0, 0, w, h))
	if len(insts) == 0 || w <= 0 || h <= 0 {
		return dst
	}
	idx := make([]int32, w*h)
	for i := range idx {
		idx[i] = -1
	}
	best := make([]float32, w*h)
	for k, m := range insts {
		if m.Score < personScoreMin || len(m.Prob) < w*h {
			continue
		}
		for i, p := range m.Prob[:w*h] {
			if p >= personProbMin && p > best[i] {
				best[i], idx[i] = p, int32(k)
			}
		}
	}
	area := make([]int, len(insts))
	sumX := make([]float64, len(insts))
	sumY := make([]float64, len(insts))
	for y := 0; y < h; y++ {
		row := idx[y*w : (y+1)*w]
		for x, k := range row {
			if k >= 0 {
				area[k]++
				sumX[k] += float64(x)
				sumY[k] += float64(y)
			}
		}
	}
	type cand struct {
		src    int
		area   int
		cx, cy float64
	}
	minArea := max(1, int(instanceMinFraction*float64(w*h)))
	var cands []cand
	for k, a := range area {
		if a >= minArea {
			cands = append(cands, cand{k, a, sumX[k] / float64(a), sumY[k] / float64(a)})
		}
	}
	if len(cands) > maxInstances {
		sort.Slice(cands, func(i, j int) bool { return cands[i].area > cands[j].area })
		cands = cands[:maxInstances]
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].cx != cands[j].cx {
			return cands[i].cx < cands[j].cx
		}
		return cands[i].cy < cands[j].cy
	})
	label := make([]uint8, len(insts)) // 0 = dropped
	for i, c := range cands {
		label[c.src] = uint8(i + 1)
	}
	for i, k := range idx {
		if k >= 0 {
			dst.Pix[i] = label[k]
		}
	}
	return dst
}

// Instance reports one person present in a stored instance plane: area
// fraction and centroid as fractions of the oriented map.
type Instance struct {
	ID       int     `json:"id"`
	Fraction float64 `json:"fraction"`
	CX       float64 `json:"cx"`
	CY       float64 `json:"cy"`
}

// DetectInstances lists the instances present in a plane in ID order (which
// is left-to-right by construction) — the DetectCategories twin, used to
// rebuild chips from a map already on disk.
func DetectInstances(pix []uint8, w, h int) []Instance {
	if w <= 0 || h <= 0 || len(pix) < w*h {
		return nil
	}
	var area [256]int
	var sumX, sumY [256]float64
	for y := 0; y < h; y++ {
		for x, v := range pix[y*w : (y+1)*w] {
			if v != 0 {
				area[v]++
				sumX[v] += float64(x)
				sumY[v] += float64(y)
			}
		}
	}
	var out []Instance
	total := float64(w * h)
	for id := 1; id < 256; id++ {
		if area[id] > 0 {
			out = append(out, Instance{
				ID:       id,
				Fraction: float64(area[id]) / total,
				CX:       (sumX[id]/float64(area[id]) + 0.5) / float64(w),
				CY:       (sumY[id]/float64(area[id]) + 0.5) / float64(h),
			})
		}
	}
	return out
}
