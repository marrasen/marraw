package pyramid

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/marrasen/marraw/internal/edit"
)

// FillPatch is one decoded ML-inpaint result for a fill-mode retouch spot: the
// spot's window (SpotFillWindow) rendered pre-look, inpainted, and stored at
// generation resolution. Renders at every size resample it through the frame
// mapping, the brush-plane philosophy — the pixels cost an inference, so they
// are computed once and sampled forever.
type FillPatch struct {
	Img *image.RGBA
	// Key identifies the patch's content (path + mtime), the AIMap precedent.
	Key string
}

// FillSet holds the patches one render needs, keyed by the spot's
// edit.SpotFillKey. Nil is valid (no fill spots, or patches not generated
// yet): the affected spots composite nothing, they never fail a render.
type FillSet map[string]*FillPatch

// FillStore is the on-disk home of ML fill patches: RGBA PNGs under their own
// directory (NOT the preview cache — patches cost an inference to regenerate,
// so they must survive preview Clear/Relocate), sharded like the pyramid
// cache and keyed by photo cache key + fill key + model version. Unlike AI
// maps the janitor never reaches this directory and RGBA patches are not
// KB-sized, so Save enforces its own size cap, oldest-first.
type FillStore struct {
	dir string
	ver string // model version tag (inpaint.FillVer), part of every file name

	mu      sync.Mutex
	patches map[string]*FillPatch
	order   []string // LRU, most recent last
}

const (
	fillCacheCap = 8         // decoded patches kept in memory
	fillDiskCap  = 256 << 20 // on-disk bytes across all patches
)

func NewFillStore(dir, ver string) *FillStore {
	return &FillStore{dir: dir, ver: ver, patches: map[string]*FillPatch{}}
}

// Path is the on-disk location of one patch.
func (s *FillStore) Path(photoKey, fillKey string) string {
	return filepath.Join(s.dir, photoKey[:2],
		fmt.Sprintf("%s_fill-%s_%s.png", photoKey, verSafe.ReplaceAllString(fillKey, ""), verSafe.ReplaceAllString(s.ver, "")))
}

// Save writes a generated patch atomically, drops any stale cached decode and
// prunes the store to its disk cap.
func (s *FillStore) Save(photoKey, fillKey string, img *image.RGBA) error {
	path := s.Path(photoKey, fillKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
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
	delete(s.patches, path)
	s.mu.Unlock()
	s.prune()
	return nil
}

// prune deletes oldest-modified patches until the store fits its disk cap —
// cheap insurance against unbounded growth, since nothing else evicts here.
// Best-effort: an undeletable file is skipped, never an error.
func (s *FillStore) prune() {
	type entry struct {
		path string
		mod  int64
		size int64
	}
	var files []entry
	var total int64
	_ = filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			files = append(files, entry{path, info.ModTime().UnixNano(), info.Size()})
			total += info.Size()
		}
		return nil
	})
	if total <= fillDiskCap {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if total <= fillDiskCap {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// Has reports whether a patch exists on disk without decoding it. Nil-safe.
func (s *FillStore) Has(photoKey, fillKey string) bool {
	if s == nil {
		return false
	}
	_, err := os.Stat(s.Path(photoKey, fillKey))
	return err == nil
}

// Load returns the decoded patch, or nil when absent/corrupt. Decodes are
// LRU-cached; the cache key carries the file mtime so a regenerated patch is
// picked up. Nil-safe.
func (s *FillStore) Load(photoKey, fillKey string) *FillPatch {
	if s == nil {
		return nil
	}
	path := s.Path(photoKey, fillKey)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	key := fmt.Sprintf("%s|%d", path, info.ModTime().UnixNano())

	s.mu.Lock()
	if p, ok := s.patches[key]; ok {
		for i, k := range s.order {
			if k == key {
				s.order = append(append(s.order[:i:i], s.order[i+1:]...), key)
				break
			}
		}
		s.mu.Unlock()
		return p
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
	rgba, ok := img.(*image.RGBA)
	if !ok {
		b := img.Bounds()
		rgba = image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
	}
	p := &FillPatch{Img: rgba, Key: key}

	s.mu.Lock()
	if _, ok := s.patches[key]; !ok {
		s.patches[key] = p
		s.order = append(s.order, key)
		if len(s.order) > fillCacheCap {
			delete(s.patches, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.mu.Unlock()
	return p
}

// SetFor loads the patch for every enabled fill spot and Remove mask in the
// edit. Missing patches are simply absent from the set — the spot or mask
// composites nothing until the patch is generated; rendering never fails on a
// missing patch. Nil-safe on a nil store (renders that predate wiring,
// TS-generation Deps).
func (s *FillStore) SetFor(photoKey string, e *edit.Params) FillSet {
	if s == nil || e == nil || photoKey == "" {
		return nil
	}
	var set FillSet
	load := func(k string) {
		if _, done := set[k]; done {
			return
		}
		if p := s.Load(photoKey, k); p != nil {
			if set == nil {
				set = FillSet{}
			}
			set[k] = p
		}
	}
	for i := range e.Spots {
		sp := &e.Spots[i]
		if sp.Mode != edit.SpotFill || sp.Disabled {
			continue
		}
		load(e.SpotFillKey(sp))
	}
	for i := range e.Masks {
		m := &e.Masks[i]
		if !m.Remove || m.Disabled {
			continue
		}
		load(e.MaskFillKey(m))
	}
	return set
}

// SpotFillWindow is the deterministic context window an ML fill is generated
// in and composited from, as fractions of the oriented frame: the spot
// region's bounding box padded by the larger of its own span and 5% of the
// frame long edge (context for the model), clamped to the frame. Generation
// and composite MUST agree on this rect — it is recomputed from the
// normalized spot on both sides rather than stored, so there is nothing to
// drift. aspect is frameW/frameH.
func SpotFillWindow(aspect float64, s *edit.Spot) (x0, y0, x1, y1 float64) {
	// Work in long-edge units so both axes share the radius' scale, then
	// convert back to per-axis fractions at the end.
	fw, fh := frameLongUnits(aspect)
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	grow := func(x, y, r float64) {
		minX = math.Min(minX, x-r)
		maxX = math.Max(maxX, x+r)
		minY = math.Min(minY, y-r)
		maxY = math.Max(maxY, y+r)
	}
	if s.Kind == "stroke" {
		for i := range s.Strokes {
			st := &s.Strokes[i]
			if st.Erase {
				continue
			}
			for p := 0; p+1 < len(st.Pts); p += 2 {
				grow(st.Pts[p]*fw, st.Pts[p+1]*fh, st.Radius)
			}
		}
	} else {
		grow(s.CX*fw, s.CY*fh, s.Radius)
	}
	if minX > maxX {
		return 0, 0, 0, 0
	}
	return fillWindowFromBBox(fw, fh, minX, minY, maxX, maxY, math.Inf(1))
}

// frameLongUnits gives the frame's extents in long-edge units (the long axis
// is 1), the space region bounding boxes and radii share. aspect is
// frameW/frameH.
func frameLongUnits(aspect float64) (fw, fh float64) {
	if aspect < 1 {
		return aspect, 1.0
	}
	return 1.0, 1.0 / aspect
}

// fillWindowFromBBox pads a region's bounding box into the context window an
// ML fill is generated in and composited from. Inputs are in long-edge units
// (fw×fh is the frame there); the result is per-axis frame fractions, clamped
// to the frame. The pad is the region's own span — a small region wants
// context wider than itself — floored at 5% of the long edge so a dust spot
// still gets something to inpaint from, and capped at maxPad so a large
// region does not drag half the frame into a window the model resolves at a
// fixed 512px.
func fillWindowFromBBox(fw, fh, minX, minY, maxX, maxY, maxPad float64) (x0, y0, x1, y1 float64) {
	if minX > maxX || minY > maxY {
		return 0, 0, 0, 0
	}
	span := math.Max(maxX-minX, maxY-minY)
	pad := math.Min(math.Max(span, 0.05), maxPad)
	x0 = clampF((minX-pad)/fw, 0, 1)
	x1 = clampF((maxX+pad)/fw, 0, 1)
	y0 = clampF((minY-pad)/fh, 0, 1)
	y1 = clampF((maxY+pad)/fh, 0, 1)
	return x0, y0, x1, y1
}

// --- Mask removal regions ---

const (
	// maskFillDilate grows the binary region before it goes to the model, as a
	// fraction of the region plane's long edge. The composite edge is feathered
	// and AI mattes are edge-snapped against the render's own pixels, so the
	// blend can reach slightly outside the plane's own boundary; synthesizing a
	// margin means it never blends back toward the thing being removed.
	maskFillDilate = 0.008
	// maskFillMaxPad caps the context margin around a removal window, in
	// long-edge units — see fillWindowFromBBox.
	maskFillMaxPad = 0.25
	// MaskFillMaxArea is the largest share of the frame a removal may cover.
	// Past this there is not enough surround left to synthesize from and the
	// model's fixed internal resolution shows badly, so the RPC refuses rather
	// than spending an inference on a result the photographer will discard.
	MaskFillMaxArea = 0.40
)

// MaskRegion is the binary region a mask removal inpaints: a 0/255 plane in
// oriented-frame space plus the bounds and coverage derived from it. It is a
// pure function of the mask's parameters and its stored AI map — never of the
// rendered pixels — so generation and composite agree without storing it, the
// SpotFillWindow contract.
type MaskRegion struct {
	Plane          []uint8 // 0 or 255, W*H, row-major
	W, H           int
	X0, Y0, X1, Y1 float64 // bounding box, frame fractions
	Area           float64 // covered share of the frame, 0..1
}

// MaskFillRegion derives the removal region for a Remove mask, or ok=false
// when the mask is not eligible, its AI map is not generated yet, or the
// region is empty. frameW/frameH are the oriented frame's pixel dimensions
// (only their ratio matters).
//
// Feather is deliberately ignored here: it softens the composite edge, and
// baking it into the region would make every feather tweak cost an inference
// (see edit.MaskFillKey). The model gets a hard, dilated region instead.
func MaskFillRegion(m *edit.Mask, frameW, frameH float64, ai AIMapSet) (*MaskRegion, bool) {
	if m == nil || frameW <= 0 || frameH <= 0 || !m.MaskRemoveAllowed() {
		return nil, false
	}
	var src []uint8
	var pw, ph int
	switch m.Type {
	case edit.MaskBrush:
		pw, ph = brushPlaneDims(frameW, frameH)
		src = brushPlaneFor(m.Strokes, pw, ph)
	case edit.MaskAI:
		am := ai[aiSetKey(m.AIKind.MapKind(), m.MapVer)]
		if am == nil || am.W == 0 || am.H == 0 {
			return nil, false
		}
		hard := *m
		hard.Feather = 0
		src = deriveCoverage(am, &hard)
		pw, ph = am.W, am.H
	default:
		return nil, false
	}
	if pw <= 0 || ph <= 0 || len(src) < pw*ph {
		return nil, false
	}
	// Binarize into a fresh plane: brushPlaneFor and deriveCoverage hand back
	// LRU-cached slices other renders share, so neither may be written to.
	plane := make([]uint8, pw*ph)
	for i, v := range src[:pw*ph] {
		if v >= 128 {
			plane[i] = 255
		}
	}
	dilateU8(plane, pw, ph, max(4, int(math.Round(float64(max(pw, ph))*maskFillDilate))))

	minX, minY, maxX, maxY := pw, ph, -1, -1
	covered := 0
	for y := 0; y < ph; y++ {
		row := plane[y*pw : (y+1)*pw]
		for x, v := range row {
			if v == 0 {
				continue
			}
			covered++
			if maxY < 0 {
				minY = y
			}
			maxY = y
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
		}
	}
	if maxX < 0 {
		return nil, false
	}
	return &MaskRegion{
		Plane: plane, W: pw, H: ph,
		X0: float64(minX) / float64(pw), X1: float64(maxX+1) / float64(pw),
		Y0: float64(minY) / float64(ph), Y1: float64(maxY+1) / float64(ph),
		Area: float64(covered) / float64(pw*ph),
	}, true
}

// MaskFillWindow is the context window a mask removal is generated in and
// composited from, as fractions of the oriented frame — SpotFillWindow's
// counterpart, recomputed from the region on both sides rather than stored.
// aspect is frameW/frameH.
func MaskFillWindow(aspect float64, r *MaskRegion) (x0, y0, x1, y1 float64) {
	if r == nil {
		return 0, 0, 0, 0
	}
	fw, fh := frameLongUnits(aspect)
	return fillWindowFromBBox(fw, fh, r.X0*fw, r.Y0*fh, r.X1*fw, r.Y1*fh, maskFillMaxPad)
}

// MaskFillMask rasterizes a removal region into the model's mask over the
// window rect: 255 = keep, 0 = inpaint (the MI-GAN convention, as
// SpotFillMask). rect is the window in pixels of an uncropped oriented buffer
// whose frame is frameW×frameH px; the mask is mw×mh mapped linearly onto
// rect. The region plane is already binary and dilated, so nearest sampling
// is exact.
func MaskFillMask(rect image.Rectangle, mw, mh int, frameW, frameH float64, r *MaskRegion) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, mw, mh))
	for i := range m.Pix {
		m.Pix[i] = 255
	}
	if rect.Dx() <= 0 || rect.Dy() <= 0 || r == nil || r.W == 0 || r.H == 0 {
		return m
	}
	for y := 0; y < mh; y++ {
		fy := float64(rect.Min.Y) + (float64(y)+0.5)*float64(rect.Dy())/float64(mh)
		py := int(fy / frameH * float64(r.H))
		if py < 0 || py >= r.H {
			continue
		}
		prow := r.Plane[py*r.W : (py+1)*r.W]
		row := m.Pix[y*m.Stride:]
		for x := 0; x < mw; x++ {
			fx := float64(rect.Min.X) + (float64(x)+0.5)*float64(rect.Dx())/float64(mw)
			px := int(fx / frameW * float64(r.W))
			if px < 0 || px >= r.W {
				continue
			}
			if prow[px] != 0 {
				row[x] = 0
			}
		}
	}
	return m
}

// dilateU8 grows the non-zero areas of a binary plane by radius pixels, in
// place, with a separable running-max pass per axis (the boxBlurU8 shape).
func dilateU8(p []uint8, w, h, radius int) {
	if radius < 1 || w == 0 || h == 0 {
		return
	}
	tmp := make([]uint8, len(p))
	// Horizontal: a pixel survives if any neighbour within radius is set.
	for y := 0; y < h; y++ {
		row := p[y*w : (y+1)*w]
		out := tmp[y*w : (y+1)*w]
		for x := 0; x < w; x++ {
			lo, hi := max(0, x-radius), min(w-1, x+radius)
			var v uint8
			for i := lo; i <= hi; i++ {
				if row[i] != 0 {
					v = 255
					break
				}
			}
			out[x] = v
		}
	}
	// Vertical.
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			lo, hi := max(0, y-radius), min(h-1, y+radius)
			var v uint8
			for i := lo; i <= hi; i++ {
				if tmp[i*w+x] != 0 {
					v = 255
					break
				}
			}
			p[y*w+x] = v
		}
	}
}

// SpotFillMask rasterizes the spot's region into a mask over the window rect:
// 255 = keep, 0 = inpaint (the MI-GAN pipeline convention). rect is the
// window in pixels of an uncropped oriented buffer whose frame is
// frameW×frameH px; the mask is mw×mh, mapped linearly onto rect (the same
// mapping the window render uses). Strokes stamp discs along their polylines
// at half-radius spacing, the brush-plane stamping precedent.
func SpotFillMask(rect image.Rectangle, mw, mh int, frameW, frameH float64, s *edit.Spot) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, mw, mh))
	for i := range m.Pix {
		m.Pix[i] = 255
	}
	if rect.Dx() <= 0 || rect.Dy() <= 0 {
		return m
	}
	long := math.Max(frameW, frameH)
	sx := float64(mw) / float64(rect.Dx())
	sy := float64(mh) / float64(rect.Dy())
	stamp := func(fx, fy, r float64) {
		// Frame px → mask px; the window render preserves aspect, so one
		// radius serves both axes.
		cx := (fx - float64(rect.Min.X)) * sx
		cy := (fy - float64(rect.Min.Y)) * sy
		rp := math.Max(1, r*sx)
		x0 := max(0, int(math.Floor(cx-rp)))
		x1 := min(mw-1, int(math.Ceil(cx+rp)))
		y0 := max(0, int(math.Floor(cy-rp)))
		y1 := min(mh-1, int(math.Ceil(cy+rp)))
		r2 := rp * rp
		for y := y0; y <= y1; y++ {
			dy := float64(y) + 0.5 - cy
			row := m.Pix[y*m.Stride:]
			for x := x0; x <= x1; x++ {
				dx := float64(x) + 0.5 - cx
				if dx*dx+dy*dy <= r2 {
					row[x] = 0
				}
			}
		}
	}
	if s.Kind == "stroke" {
		for i := range s.Strokes {
			st := &s.Strokes[i]
			if st.Erase {
				continue
			}
			r := st.Radius * long
			px, py := 0.0, 0.0
			for p := 0; p+1 < len(st.Pts); p += 2 {
				x := st.Pts[p] * frameW
				y := st.Pts[p+1] * frameH
				if p == 0 {
					stamp(x, y, r)
				} else {
					// Fill the segment with stamps at half-radius spacing so
					// fast gestures leave no gaps.
					d := math.Hypot(x-px, y-py)
					steps := max(1, int(math.Ceil(d/math.Max(1, r/2))))
					for k := 1; k <= steps; k++ {
						t := float64(k) / float64(steps)
						stamp(px+(x-px)*t, py+(y-py)*t, r)
					}
				}
				px, py = x, y
			}
		}
	} else {
		stamp(s.CX*frameW, s.CY*frameH, s.Radius*long)
	}
	return m
}
