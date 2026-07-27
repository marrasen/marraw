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

// SetFor loads the patch for every enabled fill spot in the edit. Missing
// patches are simply absent from the set — the spot composites nothing until
// the patch is generated; rendering never fails on a missing patch. Nil-safe
// on a nil store (renders that predate wiring, TS-generation Deps).
func (s *FillStore) SetFor(photoKey string, e *edit.Params) FillSet {
	if s == nil || e == nil || photoKey == "" {
		return nil
	}
	var set FillSet
	for i := range e.Spots {
		sp := &e.Spots[i]
		if sp.Mode != edit.SpotFill || sp.Disabled {
			continue
		}
		k := e.SpotFillKey(sp)
		if _, done := set[k]; done {
			continue
		}
		if p := s.Load(photoKey, k); p != nil {
			if set == nil {
				set = FillSet{}
			}
			set[k] = p
		}
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
	fw, fh := 1.0, 1.0/aspect // frame extents in long-edge units
	if aspect < 1 {
		fw, fh = aspect, 1.0
	}
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
	span := math.Max(maxX-minX, maxY-minY)
	pad := math.Max(span, 0.05)
	x0 = clampF((minX-pad)/fw, 0, 1)
	x1 = clampF((maxX+pad)/fw, 0, 1)
	y0 = clampF((minY-pad)/fh, 0, 1)
	y1 = clampF((maxY+pad)/fh, 0, 1)
	return x0, y0, x1, y1
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
