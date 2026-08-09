package pyramid

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/marrasen/marraw/internal/edit"
)

// AIMap is one decoded model-generated map for a photo, in oriented-frame
// space at a fixed modest resolution (aiMapLongEdge at generation time).
// Interpretation depends on the mask kind: a continuous 0..255 matte
// (subject), a category-ID plane (class), or normalized depth with 255 =
// nearest (depth).
type AIMap struct {
	Pix  []uint8
	W, H int
	// Key identifies the map's content (path + mtime) for the derived
	// coverage-plane cache, the brushCache precedent.
	Key string
}

// AIMapSet holds the maps one render needs, keyed by kind@version — the
// lookup an aiEval performs. Nil is valid (no AI masks, or maps unavailable:
// the affected masks contribute nothing, they never fail a render).
type AIMapSet map[string]*AIMap

func aiSetKey(kind edit.AIKind, ver string) string { return string(kind) + "@" + ver }

// AIMapStore is the on-disk home of model-generated maps: grayscale PNGs
// under its own directory (NOT the preview cache — maps cost an inference to
// regenerate, so they must survive preview Clear/Relocate), sharded like the
// pyramid cache and keyed by photo cache key + kind + model version. It keeps
// a small LRU of decoded planes so repeated renders don't re-decode PNGs.
type AIMapStore struct {
	dir string

	mu     sync.Mutex
	planes map[string]*AIMap
	order  []string // LRU, most recent last
}

const aiMapCacheCap = 8

func NewAIMapStore(dir string) *AIMapStore {
	return &AIMapStore{dir: dir, planes: map[string]*AIMap{}}
}

// verSafe strips anything filename-hostile from a model version tag. MapVer
// is server-stamped, but it round-trips through sidecars other machines wrote.
var verSafe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// Path is the on-disk location of one map.
func (s *AIMapStore) Path(photoKey string, kind edit.AIKind, ver string) string {
	return filepath.Join(s.dir, photoKey[:2],
		fmt.Sprintf("%s_ai-%s_%s.png", photoKey, kind, verSafe.ReplaceAllString(ver, "")))
}

// Save writes a generated map atomically and drops any stale cached decode.
func (s *AIMapStore) Save(photoKey string, kind edit.AIKind, ver string, m *image.Gray) error {
	path := s.Path(photoKey, kind, ver)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := png.Encode(f, m); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	delete(s.planes, path)
	s.mu.Unlock()
	return nil
}

// Has reports whether a map exists on disk without decoding it. Nil-safe.
func (s *AIMapStore) Has(photoKey string, kind edit.AIKind, ver string) bool {
	if s == nil {
		return false
	}
	_, err := os.Stat(s.Path(photoKey, kind, ver))
	return err == nil
}

// Load returns the decoded map, or nil when absent/corrupt. Decodes are
// LRU-cached; the cache key carries the file mtime so a regenerated map is
// picked up. Nil-safe.
func (s *AIMapStore) Load(photoKey string, kind edit.AIKind, ver string) *AIMap {
	if s == nil {
		return nil
	}
	path := s.Path(photoKey, kind, ver)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	key := fmt.Sprintf("%s|%d", path, info.ModTime().UnixNano())

	s.mu.Lock()
	if m, ok := s.planes[key]; ok {
		for i, k := range s.order {
			if k == key {
				s.order = append(append(s.order[:i:i], s.order[i+1:]...), key)
				break
			}
		}
		s.mu.Unlock()
		return m
	}
	s.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		return nil
	}
	gray, ok := img.(*image.Gray)
	if !ok {
		b := img.Bounds()
		gray = image.NewGray(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				gray.Set(x, y, img.At(x, y))
			}
		}
	}
	m := &AIMap{Pix: gray.Pix, W: gray.Rect.Dx(), H: gray.Rect.Dy(), Key: key}

	s.mu.Lock()
	if _, ok := s.planes[key]; !ok {
		s.planes[key] = m
		s.order = append(s.order, key)
		if len(s.order) > aiMapCacheCap {
			delete(s.planes, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.mu.Unlock()
	return m
}

// SetFor loads every map the edit's AI masks reference, transformed into the
// edit's oriented frame. Maps are stored in the base display orientation (no
// user rotate/flip) so a later quarter-rotate or mirror doesn't orphan them —
// the transform is a lossless pixel-grid permutation done at load and cached.
// Missing maps are simply absent from the set — the mask renders as a no-op
// until the map is (re)generated; rendering never fails on a missing map.
// Nil-safe on a nil store (renders that predate wiring, TS-generation Deps).
func (s *AIMapStore) SetFor(photoKey string, e *edit.Params) AIMapSet {
	if s == nil || e == nil || photoKey == "" {
		return nil
	}
	rot, flip := e.RotateTurns(), e.FlipH
	var set AIMapSet
	for i := range e.Masks {
		m := &e.Masks[i]
		if m.Type != edit.MaskAI || m.AIKind == "" {
			continue
		}
		// Background samples the subject matte, so both kinds share one entry.
		kind := m.AIKind.MapKind()
		k := aiSetKey(kind, m.MapVer)
		if _, done := set[k]; done {
			continue
		}
		if am := s.loadOriented(photoKey, kind, m.MapVer, rot, flip); am != nil {
			if set == nil {
				set = AIMapSet{}
			}
			set[k] = am
		}
	}
	return set
}

// LoadOriented returns the decoded map transformed into an edit's oriented
// frame (the crop rectangle's space), or nil when absent/corrupt. The oriented
// variant shares the decoded-plane LRU with SetFor's lookups.
func (s *AIMapStore) LoadOriented(photoKey string, kind edit.AIKind, ver string, rot int, flip bool) *AIMap {
	if s == nil {
		return nil
	}
	return s.loadOriented(photoKey, kind, ver, rot, flip)
}

// loadOriented returns the map in the edit's oriented frame: the stored
// display transform is FlipH∘RotateCW^rot (ApplyGeometry's order), applied to
// the base map. Oriented variants share the decoded-plane LRU.
func (s *AIMapStore) loadOriented(photoKey string, kind edit.AIKind, ver string, rot int, flip bool) *AIMap {
	base := s.Load(photoKey, kind, ver)
	if base == nil || (rot == 0 && !flip) {
		return base
	}
	key := fmt.Sprintf("%s|r%d|f%t", base.Key, rot, flip)

	s.mu.Lock()
	if m, ok := s.planes[key]; ok {
		s.mu.Unlock()
		return m
	}
	s.mu.Unlock()

	m := orientMap(base, rot, flip)
	m.Key = key

	s.mu.Lock()
	if _, ok := s.planes[key]; !ok {
		s.planes[key] = m
		s.order = append(s.order, key)
		if len(s.order) > aiMapCacheCap {
			delete(s.planes, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.mu.Unlock()
	return m
}

// orientMap applies FlipH∘RotateCW^rot to the plane. A 90° CW turn maps a
// source pixel (x,y) of a W×H plane to (H-1-y, x); the mirror then reflects
// about the vertical axis.
func orientMap(base *AIMap, rot int, flip bool) *AIMap {
	pix, w, h := base.Pix, base.W, base.H
	for range rot {
		nw, nh := h, w
		out := make([]uint8, len(pix))
		for y := 0; y < h; y++ {
			row := pix[y*w : (y+1)*w]
			dx := nw - 1 - y
			for x, v := range row {
				out[x*nw+dx] = v
			}
		}
		pix, w, h = out, nw, nh
	}
	if flip {
		out := make([]uint8, len(pix))
		for y := 0; y < h; y++ {
			row := pix[y*w : (y+1)*w]
			orow := out[y*w : (y+1)*w]
			for x, v := range row {
				orow[w-1-x] = v
			}
		}
		pix = out
	}
	return &AIMap{Pix: pix, W: w, H: h}
}

// --- Derived coverage planes ---

// coveragePlane turns a mask's source map into the 0..255 coverage the
// evaluator samples, applying the kind's parameters (threshold / class
// equality / depth window) and feather. Results are LRU-cached keyed by map
// content + parameters, so adjustment-slider drags re-derive nothing.
func coveragePlane(am *AIMap, m *edit.Mask) []uint8 {
	key := fmt.Sprintf("%s|%s|%d|%.4f|%.4f|%.4f|%.4f", am.Key, m.AIKind, m.ClassID,
		m.DepthLo, m.DepthHi, m.Threshold, m.Feather)
	aiPlaneCache.Lock()
	if p, ok := aiPlaneCache.planes[key]; ok {
		for i, k := range aiPlaneCache.order {
			if k == key {
				aiPlaneCache.order = append(append(aiPlaneCache.order[:i:i], aiPlaneCache.order[i+1:]...), key)
				break
			}
		}
		aiPlaneCache.Unlock()
		return p
	}
	aiPlaneCache.Unlock()

	p := deriveCoverage(am, m)

	aiPlaneCache.Lock()
	if _, ok := aiPlaneCache.planes[key]; !ok {
		aiPlaneCache.planes[key] = p
		aiPlaneCache.order = append(aiPlaneCache.order, key)
		if len(aiPlaneCache.order) > aiPlaneCacheCap {
			delete(aiPlaneCache.planes, aiPlaneCache.order[0])
			aiPlaneCache.order = aiPlaneCache.order[1:]
		}
	}
	aiPlaneCache.Unlock()
	return p
}

var aiPlaneCache = struct {
	sync.Mutex
	planes map[string][]uint8
	order  []string
}{planes: map[string][]uint8{}}

const aiPlaneCacheCap = 8

func deriveCoverage(am *AIMap, m *edit.Mask) []uint8 {
	out := make([]uint8, len(am.Pix))
	switch m.AIKind {
	case edit.AISubject, edit.AIBackground:
		// Continuous matte: remap around the threshold with a feather-wide
		// smoothstep. Threshold 0 means the 0.5 default; feather 0 keeps the
		// model's own soft edges (identity above/below the cutoff band).
		// Background derives the same coverage — newAIEval inverts it.
		t := m.Threshold
		if t == 0 {
			t = 0.5
		}
		f := math.Max(m.Feather, 0.02)
		var lut [256]uint8
		for v := range lut {
			lut[v] = uint8(math.Round(255 * smoothstep01((float64(v)/255-t+f/2)/f)))
		}
		for i, v := range am.Pix {
			out[i] = lut[v]
		}
	case edit.AIClass, edit.AIPerson:
		// Both are label planes (category ID / person instance ID); ClassID
		// picks the label either way.
		labelCoverage(out, am, uint8(m.ClassID), m.Feather)
	case edit.AIDepth:
		// Keep depths inside [lo,hi] (1 = nearest), easing in/out over the
		// feather width.
		lo, hi := m.DepthLo, m.DepthHi
		f := math.Max(m.Feather*0.25, 0.02)
		var lut [256]uint8
		for v := range lut {
			d := float64(v) / 255
			w := smoothstep01((d-(lo-f))/f) * (1 - smoothstep01((d-hi)/f))
			lut[v] = uint8(math.Round(255 * w))
		}
		for i, v := range am.Pix {
			out[i] = lut[v]
		}
	}
	return out
}

// labelCoverage fills out with 255 where the plane equals the wanted label,
// then softens the hard boundary by blurring the binary plane; radius up to
// ~3% of the long edge.
func labelCoverage(out []uint8, am *AIMap, want uint8, feather float64) {
	for i, v := range am.Pix {
		if v == want {
			out[i] = 255
		}
	}
	if feather > 0 {
		long := max(am.W, am.H)
		if r := int(math.Round(feather * float64(long) * 0.03)); r > 0 {
			boxBlurU8(out, am.W, am.H, r)
			boxBlurU8(out, am.W, am.H, r) // two passes ≈ triangular
		}
	}
}

// boxBlurU8 box-blurs a uint8 plane in place with a running-sum pass per
// axis (the boxBlurPlane precedent, on bytes).
func boxBlurU8(p []uint8, w, h, radius int) {
	if radius < 1 || w == 0 || h == 0 {
		return
	}
	tmp := make([]uint8, len(p))
	// Horizontal.
	for y := 0; y < h; y++ {
		row := p[y*w : (y+1)*w]
		out := tmp[y*w : (y+1)*w]
		var sum int
		n := 0
		for x := -radius; x <= radius; x++ {
			cx := min(max(x, 0), w-1)
			sum += int(row[cx])
			n++
		}
		for x := 0; x < w; x++ {
			out[x] = uint8(sum / n)
			addX := min(x+radius+1, w-1)
			subX := max(x-radius, 0)
			sum += int(row[addX]) - int(row[subX])
		}
	}
	// Vertical.
	for x := 0; x < w; x++ {
		var sum int
		n := 0
		for y := -radius; y <= radius; y++ {
			cy := min(max(y, 0), h-1)
			sum += int(tmp[cy*w+x])
			n++
		}
		for y := 0; y < h; y++ {
			p[y*w+x] = uint8(sum / n)
			addY := min(y+radius+1, h-1)
			subY := max(y-radius, 0)
			sum += int(tmp[addY*w+x]) - int(tmp[subY*w+x])
		}
	}
}

// SubjectMedianDepth is the weighted median of the depth map under the
// subject matte — where the subject stands, in the depth map's own normalized
// space (1 = nearest). This is what seeds the tilt-shift recipe's focus
// distance when the subject model happens to be available: the sharp band
// lands on the subject instead of on a guess. The median (not the mean) so a
// matte that spills onto a bit of background cannot drag the focus off the
// subject. False when either map is missing or the matte covers too little
// of the frame to mean anything.
func SubjectMedianDepth(depth, matte *AIMap) (float64, bool) {
	if depth == nil || matte == nil || depth.W == 0 || depth.H == 0 || matte.W == 0 || matte.H == 0 {
		return 0, false
	}
	var hist [256]uint64
	var total uint64
	for y := 0; y < depth.H; y++ {
		my := y * matte.H / depth.H
		drow := depth.Pix[y*depth.W : (y+1)*depth.W]
		mrow := matte.Pix[my*matte.W : (my+1)*matte.W]
		for x, d := range drow {
			w := uint64(mrow[x*matte.W/depth.W])
			hist[d] += w
			total += w
		}
	}
	// Coverage floor: mean matte weight under 2% of full means no subject.
	if total < uint64(depth.W*depth.H)*255/50 {
		return 0, false
	}
	var acc uint64
	for v, w := range hist {
		acc += w
		if acc*2 >= total {
			return float64(v) / 255, true
		}
	}
	return 0, false
}
