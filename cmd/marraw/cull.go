package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary between the window and the culler.
type (
	// Cull is what the window shows: the photo at Index of Total, the best
	// pixels of it so far, and what they are.
	Cull struct {
		Index, Total int
		Name         string
		Img          *paint.Image
		// Aspect is the photo's width over its height, for its frame before
		// any pixels arrive.
		Aspect float32
		// Note says which rendition shows and what it took.
		Note string
		// Full is the photo's full resolution, which the tiles cover, and
		// Tiles those of them fetched, by their place in the grid.
		Full  image.Point
		Tiles map[image.Point]*paint.Image
		// TileNote says how the tiles stand.
		TileNote string
		// Rating and Flag are the photo's, and Strip the photos around it,
		// for the filmstrip.
		Rating int
		Flag   string
		Strip  []Thumb
	}
	// Thumb is one photo in the filmstrip: where it is in the folder, its
	// small picture once fetched, and its rating and flag.
	Thumb struct {
		Index  int
		Img    *paint.Image
		Aspect float32
		Rating int
		Flag   string
	}
	// Rate rates the photo showing, 0 to 5 stars.
	Rate struct{ Stars int }
	// Mark flags the photo showing: "pick", "exclude" or "none".
	Mark struct{ Flag string }
	// WantTiles asks for the tiles of the photo at Index in Range of the
	// grid, as the view zooms past what the 2048 shows sharp; an empty
	// Range wants none.
	WantTiles struct {
		Index int
		Range image.Rectangle
	}
	// Step moves through the folder by By photos.
	Step struct{ By int }
	// Jump goes to the photo at To, or, negative, to the last.
	Jump struct{ To int }
	// Quit closes the window.
	Quit struct{}
)

// dwell is how long a photo has to stay before a cold one gets a real
// render, as the React loupe waits: skimming past never starts a RAW
// decode.
const dwell = 350 * time.Millisecond

// culler is the application half: the folder's photos, where the user is,
// and the pipeline that brings each photo's pixels in without ever making
// navigation wait on a RAW decode.
type culler struct {
	ctx    context.Context
	c      gunim.Client
	api    *marrawclient.Client
	im     *images
	folder int64
	photos []marrawclient.Photo
	at     int

	// cache holds decoded pixels by photo, the best of each so far.
	cache *pixelCache
	// load cancels the current photo's pipeline, and warm the neighbours'.
	load, warm context.CancelFunc
	// gen counts navigations, so a late result for a photo left behind is
	// dropped.
	gen int
	// arrived carries the pipelines' results to the culler's goroutine,
	// and do work for it from elsewhere.
	arrived chan arrival
	do      chan func()
	// stepped is when the user last moved, for the timings.
	stepped time.Time
	timings []timing

	// tiles holds the photos' full-resolution tiles, and tilesFor the state
	// of the photo showing's.
	tiles    *tileCache
	tileWant WantTiles
	tileWarm map[int64]bool
	// tileStop cancels the tile fetches under way; probing is the photo
	// whose tiles are being looked for, or rendered, and probeStop stops
	// that.
	tileStop  context.CancelFunc
	probing   int64
	probeStop context.CancelFunc
	tileNote  string

	// thumbs are the filmstrip's pictures by photo, and thumbsWanted those
	// asked for and not in yet.
	thumbs       map[int64]*paint.Image
	thumbsWanted map[int64]bool
	thumbSlots   chan struct{}
}

// stripReach is how many photos the filmstrip shows on each side of the
// one showing.
const stripReach = 7

// arrival is a rendition decoded for photo at index, in generation gen.
type arrival struct {
	gen, index int
	rank       int
	want       want
	got        got
}

// timing is how long one step took to show pixels, and to show sharp ones;
// sharpSeen says it did, as at once from the cache, where sharp is 0.
type timing struct {
	first, sharp time.Duration
	firstWhat    string
	sharpSeen    bool
}

func newCuller(ctx context.Context, c gunim.Client, api *marrawclient.Client, im *images, folder int64, photos []marrawclient.Photo) *culler {
	sort.SliceStable(photos, func(i, j int) bool { return photos[i].TakenAt < photos[j].TakenAt })
	return &culler{ctx: ctx, c: c, api: api, im: im, folder: folder, photos: photos,
		cache: newPixelCache(16), arrived: make(chan arrival, 16), do: make(chan func(), 16),
		tiles: newTileCache(48), tileWarm: map[int64]bool{},
		thumbs: map[int64]*paint.Image{}, thumbsWanted: map[int64]bool{}, thumbSlots: make(chan struct{}, 4)}
}

func (cu *culler) serve() error {
	if err := cu.c.Mount(gunim.Root, "cull", "cull", cu.state()); err != nil {
		return err
	}
	_ = cu.c.Focus("cull")
	// Ratings, flags and edits made elsewhere, as in the Electron app on
	// the same backend, show here as they happen.
	stopPatches := cu.api.OnPhotoPatchEvent(func(ev marrawclient.PhotoPatchEvent) {
		select {
		case cu.do <- func() { cu.patched(ev.Patches) }:
		case <-cu.ctx.Done():
		}
	})
	defer stopPatches()
	cu.goTo(0)
	for {
		select {
		case <-cu.ctx.Done():
			return nil
		case a := <-cu.arrived:
			cu.take(a)
		case fn := <-cu.do:
			fn()
		case ev, ok := <-cu.c.Intents():
			if !ok {
				return cu.c.Err()
			}
			switch in := ev.Intent.(type) {
			case Step:
				cu.goTo(cu.at + in.By)
			case Jump:
				to := in.To
				if to < 0 {
					to = len(cu.photos) - 1
				}
				cu.goTo(to)
			case WantTiles:
				cu.wantTiles(in)
			case Rate:
				cu.rate(in.Stars)
			case Mark:
				cu.mark(marrawclient.Flag(in.Flag))
			case Quit:
				cu.c.Leave()
			}
		}
	}
}

// state is what the window shows now.
func (cu *culler) state() Cull {
	p := cu.photos[cu.at]
	s := size(p)
	aspect := float32(1.5)
	if s.Y > 0 {
		aspect = float32(s.X) / float32(s.Y)
	}
	st := Cull{Index: cu.at, Total: len(cu.photos), Name: p.FileName, Aspect: aspect, Full: s,
		Tiles: cu.tiles.of(p.ID), TileNote: cu.tileNote, Rating: p.Rating, Flag: string(p.Flag)}
	for i := max(0, cu.at-stripReach); i <= min(len(cu.photos)-1, cu.at+stripReach); i++ {
		q := cu.photos[i]
		qs := size(q)
		a := float32(1.5)
		if qs.Y > 0 {
			a = float32(qs.X) / float32(qs.Y)
		}
		st.Strip = append(st.Strip, Thumb{Index: i, Img: cu.thumbs[q.ID], Aspect: a, Rating: q.Rating, Flag: string(q.Flag)})
	}
	if e, ok := cu.cache.get(p.ID); ok {
		st.Img, st.Note = e.img, e.note
	}
	return st
}

// goTo moves to the photo at i: what is cached shows at once, the pipeline
// for the rest starts, and the old one stops.
func (cu *culler) goTo(i int) {
	i = max(0, min(i, len(cu.photos)-1))
	if i == cu.at && cu.load != nil {
		return
	}
	cu.at, cu.gen, cu.stepped = i, cu.gen+1, time.Now()
	if cu.load != nil {
		cu.load()
	}
	if cu.warm != nil {
		cu.warm()
	}
	if cu.tileStop != nil {
		cu.tileStop()
		cu.tileStop = nil
	}
	if cu.probeStop != nil {
		cu.probeStop()
		cu.probing, cu.probeStop = 0, nil
	}
	cu.tileNote = ""
	_ = cu.c.Update("cull", cu.state())
	cu.loadStrip()
	p := cu.photos[i]
	// Where the user is, so the backend's pre-render works outward from
	// here.
	go func(folder, id int64) { _ = cu.api.Library.SetFocus(cu.ctx, folder, id) }(cu.folder, p.ID)
	if e, ok := cu.cache.get(p.ID); ok && e.rank >= rankSharp {
		cu.record(e.note, true)
		cu.warmNeighbours()
		return
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.load = cancel
	go cu.pipeline(ctx, cu.gen, i, p)
}

// How good a rendition is: a stand-in at 512, a provisional at the fit
// level (the camera's embedded JPEG or a downscale), or the real pixels.
const (
	rankBase = iota + 1
	rankProvisional
	rankSharp
)

// target is the level the window is shown at.
const target = "2048"

// pipeline brings photo p's pixels in, best last, never asking for a RAW
// decode until the user has stayed on it for dwell.
func (cu *culler) pipeline(ctx context.Context, gen, i int, p marrawclient.Photo) {
	have := 0
	if e, ok := cu.cache.get(p.ID); ok {
		have = e.rank
	}
	try := func(w want, rank int) bool {
		if rank <= have {
			return false
		}
		g, err := cu.im.get(ctx, p, w)
		if err != nil {
			return false
		}
		if g.provisional && rank == rankSharp {
			rank = rankProvisional
		}
		cu.send(arrival{gen: gen, index: i, rank: rank, want: w, got: g})
		have = max(have, rank)
		return rank == rankSharp
	}
	// Something at once: whatever 512 there is, at any edit state.
	if have == 0 {
		try(want{level: "512", stale: true, fast: true}, rankBase)
	}
	// The real pixels, if they are rendered already.
	if try(want{level: target, cacheOnly: true}, rankSharp) {
		cu.warmNeighbours()
		return
	}
	// The camera's embedded JPEG, or a downscale: tens of milliseconds.
	try(want{level: target, fast: true}, rankSharp)
	if have >= rankSharp {
		cu.warmNeighbours()
		return
	}
	cu.warmNeighbours()
	// Still here: one real render.
	select {
	case <-ctx.Done():
		return
	case <-time.After(dwell):
	}
	try(want{level: target}, rankSharp)
}

// warmNeighbours fetches the next, the previous and the one after next,
// from what is rendered already, so stepping to them is instant. It may be
// called from a pipeline's goroutine.
func (cu *culler) warmNeighbours() {
	select {
	case cu.do <- cu.startWarming:
	case <-cu.ctx.Done():
	}
}

// send hands a to the culler's goroutine.
func (cu *culler) send(a arrival) {
	select {
	case cu.arrived <- a:
	case <-cu.ctx.Done():
	}
}

// take keeps an arrival, and shows it if it is for the photo showing.
func (cu *culler) take(a arrival) {
	p := cu.photos[a.index]
	note := fmt.Sprintf("%s · %dx%d · fetch %d ms, decode %d ms", a.want, a.got.w, a.got.h,
		a.got.fetch.Milliseconds(), a.got.decode.Milliseconds())
	if a.got.provisional {
		note += " · provisional"
	}
	cu.cache.put(p.ID, cacheEntry{img: a.got.img, rank: a.rank, note: note})
	if a.index != cu.at {
		return
	}
	_ = cu.c.Update("cull", cu.state())
	if a.gen == cu.gen {
		cu.record(note, a.rank >= rankSharp)
	}
}

// startWarming warms the neighbours of the photo showing, once.
func (cu *culler) startWarming() {
	if cu.warm != nil {
		cu.warm()
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	cu.warm = cancel
	gen := cu.gen
	var idx []int
	for _, d := range []int{1, -1, 2} {
		if j := cu.at + d; j >= 0 && j < len(cu.photos) {
			if e, ok := cu.cache.get(cu.photos[j].ID); !ok || e.rank < rankSharp {
				idx = append(idx, j)
			}
		}
	}
	go func() {
		for _, j := range idx {
			p := cu.photos[j]
			for _, w := range []want{{level: target, cacheOnly: true}, {level: target, fast: true}} {
				g, err := cu.im.get(ctx, p, w)
				if err != nil {
					continue
				}
				rank := rankSharp
				if g.provisional {
					rank = rankProvisional
				}
				cu.send(arrival{gen: gen, index: j, rank: rank, want: w, got: g})
				break
			}
		}
	}()
}

// record notes how long the step showing took, the first pixels and the
// sharp ones.
func (cu *culler) record(what string, sharp bool) {
	for len(cu.timings) < cu.gen {
		cu.timings = append(cu.timings, timing{})
	}
	t := &cu.timings[cu.gen-1]
	d := time.Since(cu.stepped)
	if t.firstWhat == "" {
		t.first, t.firstWhat = d, what
	}
	if sharp && !t.sharpSeen {
		t.sharp, t.sharpSeen = d, true
	}
}

// script steps right n times, every apart, then reports the timings and
// writes a picture of the window to shot, for measuring without hands.
func (cu *culler) script(n int, every time.Duration, shot string, zoom bool, keys string) {
	time.Sleep(time.Second)
	for range n {
		cu.c.Input(cu.ctx, keyRight())
		time.Sleep(every)
	}
	time.Sleep(time.Second)
	for _, k := range strings.Split(keys, ",") {
		if key, ok := namedKeys[strings.TrimSpace(strings.ToLower(k))]; ok {
			cu.c.Input(cu.ctx, input.KeyPress{Key: key})
			time.Sleep(300 * time.Millisecond)
		}
	}
	if zoom {
		start := time.Now()
		cu.c.Input(cu.ctx, keyZ())
		// Until the tiles in view are in, or a while.
		for time.Since(start) < 20*time.Second {
			time.Sleep(200 * time.Millisecond)
			done := make(chan bool, 1)
			cu.do <- func() {
				r := cu.tileWant.Range
				done <- !r.Empty() && len(cu.tiles.of(cu.photos[cu.at].ID)) >= r.Dx()*r.Dy()
			}
			if <-done {
				break
			}
		}
		took := time.Since(start)
		done := make(chan string, 1)
		cu.do <- func() { done <- cu.tileNote }
		fmt.Fprintf(os.Stderr, "zoom: tiles in view after %d ms (%s)\n", took.Milliseconds(), <-done)
		time.Sleep(500 * time.Millisecond)
	}
	if shot != "" {
		if err := writeShot(cu.ctx, cu.c, shot); err != nil {
			log.Print(err)
		}
	}
	if n > 0 || zoom || keys != "" {
		cu.do <- func() {
			cu.report()
			cu.c.Leave()
		}
	}
}

// report prints the steps' timings.
func (cu *culler) report() {
	var firsts, sharps []time.Duration
	var lines []string
	for i, t := range cu.timings {
		if t.firstWhat == "" {
			lines = append(lines, fmt.Sprintf("step %2d: nothing shown before the next step", i))
			continue
		}
		firsts = append(firsts, t.first)
		if t.sharpSeen {
			sharps = append(sharps, t.sharp)
		}
		lines = append(lines, fmt.Sprintf("step %2d: first %4d ms (%s), sharp %4d ms", i, t.first.Milliseconds(), t.firstWhat, t.sharp.Milliseconds()))
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
	fmt.Fprintf(os.Stderr, "steps %d; first pixels median %d ms, max %d ms; sharp in %d of them, median %d ms\n",
		len(cu.timings), median(firsts).Milliseconds(), slices.Max(append(firsts, 0)).Milliseconds(), len(sharps), median(sharps).Milliseconds())
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

// pixelCache keeps the decoded pixels of the photos seen and warmed last.
type pixelCache struct {
	mu    sync.Mutex
	limit int
	order []int64
	m     map[int64]cacheEntry
}

type cacheEntry struct {
	img  *paint.Image
	rank int
	note string
}

func newPixelCache(limit int) *pixelCache {
	return &pixelCache{limit: limit, m: map[int64]cacheEntry{}}
}

// drop forgets id's pixels, as after an edit.
func (pc *pixelCache) drop(id int64) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	delete(pc.m, id)
	pc.order = slices.DeleteFunc(pc.order, func(x int64) bool { return x == id })
}

func (pc *pixelCache) get(id int64) (cacheEntry, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	e, ok := pc.m[id]
	return e, ok
}

// put keeps e for id unless a better one is kept, and lets the oldest go.
func (pc *pixelCache) put(id int64, e cacheEntry) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if old, ok := pc.m[id]; ok && old.rank > e.rank {
		return
	}
	pc.m[id] = e
	pc.order = slices.DeleteFunc(pc.order, func(x int64) bool { return x == id })
	pc.order = append(pc.order, id)
	for len(pc.order) > pc.limit {
		delete(pc.m, pc.order[0])
		pc.order = pc.order[1:]
	}
}

// loadStrip fetches the filmstrip's pictures not fetched yet: the 256 at any
// edit state, never decoding a RAW, a few at a time, nearest first.
func (cu *culler) loadStrip() {
	for d := 0; d <= stripReach; d++ {
		for _, i := range []int{cu.at + d, cu.at - d} {
			if i < 0 || i >= len(cu.photos) {
				continue
			}
			p := cu.photos[i]
			if cu.thumbs[p.ID] != nil || cu.thumbsWanted[p.ID] {
				continue
			}
			cu.thumbsWanted[p.ID] = true
			go func() {
				select {
				case cu.thumbSlots <- struct{}{}:
				case <-cu.ctx.Done():
					return
				}
				g, err := cu.im.get(cu.ctx, p, want{level: "256", stale: true, fast: true})
				<-cu.thumbSlots
				select {
				case cu.do <- func() {
					delete(cu.thumbsWanted, p.ID)
					if err != nil {
						return
					}
					cu.thumbs[p.ID] = g.img
					if i >= cu.at-stripReach && i <= cu.at+stripReach {
						_ = cu.c.Update("cull", cu.state())
					}
				}:
				case <-cu.ctx.Done():
				}
			}()
		}
	}
}

// rate gives the photo showing stars, here at once and on the backend.
func (cu *culler) rate(stars int) {
	p := &cu.photos[cu.at]
	p.Rating = max(0, min(stars, 5))
	_ = cu.c.Update("cull", cu.state())
	id, r := p.ID, p.Rating
	go func() {
		if err := cu.api.Library.SetRating(cu.ctx, []int64{id}, r); err != nil {
			log.Printf("rate: %v", err)
		}
	}()
}

// mark flags the photo showing, here at once and on the backend.
func (cu *culler) mark(f marrawclient.Flag) {
	p := &cu.photos[cu.at]
	p.Flag = f
	_ = cu.c.Update("cull", cu.state())
	id := p.ID
	go func() {
		if err := cu.api.Library.SetFlag(cu.ctx, []int64{id}, f); err != nil {
			log.Printf("flag: %v", err)
		}
	}()
}

// patched takes changes to photos made anywhere: their ratings and flags,
// and an edit, whose new pixels are fetched again.
func (cu *culler) patched(ps []marrawclient.PhotoPatch) {
	changed := false
	for _, pp := range ps {
		for i := range cu.photos {
			p := &cu.photos[i]
			if p.ID != pp.ID {
				continue
			}
			if pp.Rating != nil {
				p.Rating = *pp.Rating
			}
			if pp.Flag != nil {
				p.Flag = *pp.Flag
			}
			if pp.EditHash != nil && *pp.EditHash != p.EditHash {
				p.EditHash = *pp.EditHash
				cu.cache.drop(p.ID)
				delete(cu.thumbs, p.ID)
				if i == cu.at {
					// Again, with the new edit's pixels.
					cu.load = nil
					cu.goTo(i)
				}
			}
			if i >= cu.at-stripReach && i <= cu.at+stripReach {
				changed = true
			}
		}
	}
	if changed {
		_ = cu.c.Update("cull", cu.state())
		cu.loadStrip()
	}
}
