package pyramid

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

// fullDecodeIdle is how long the kept full-resolution decode outlives its
// last use once the editor has left its photo. A 42-megapixel frame holds
// 170 MB.
const fullDecodeIdle = 2 * time.Minute

// fullDecode is the last full-resolution LibRaw decode of a photo open in
// the editor, kept so the next 1:1 render under the same LibRaw inputs
// skips the demosaic — ~2.5 s of a 42-megapixel render. Crop, straighten,
// lens, masks, healing and every look-stage slider leave the decode as it
// is, so after an edit of any of them the tiles come back in the time the
// stages after the decode take. The pixels are exactly the ones a fresh
// decode gives: the key is every input LibRaw reads.
type fullDecode struct {
	cacheKey string
	key      string      // fullDecodeKey
	seq      uint64      // order the decode was asked for in
	wbCompEV float64     // edit.WBCompEV for this decode's WB
	rgba     *image.RGBA // never changed once kept
	used     time.Time   // last render from it; guarded by fullMu
	timer    *time.Timer // lets the decode go fullDecodeIdle after used
}

// fullFlight is a decode under way that others wait for.
type fullFlight struct {
	done chan struct{}
	err  error // set before done closes
}

// fullDecodeKey names the decode an edit asks LibRaw for: the LibRaw params
// themselves, and the edit's LibRaw inputs, from which the WB compensation
// comes. The inputs hash alone normalizes them first, so an edit stored
// un-normalized (an old sidecar, say) would share a key with a different
// decode.
func fullDecodeKey(edits *edit.Params) string {
	key := fmt.Sprintf("%+v", fullDecodeParams(edits))
	if edits != nil {
		key += "|" + edits.LibrawInputsHash()
	}
	return key
}

// fullDecodeParams are the LibRaw params of a full-resolution render.
// Interactive full renders use PPG unless the edit picks a demosaic —
// roughly half AHD's cost, visually equivalent at loupe zoom; export keeps
// AHD.
func fullDecodeParams(edits *edit.Params) libraw.Params {
	params := edits.LibrawParams(false)
	if edits == nil || edits.Demosaic == "" {
		params.UserQual = libraw.DemosaicPPG
	}
	return params
}

// HoldEditor marks photo as open in the editor until release is called.
// Only a held photo's 1:1 renders keep their decode, and its kept decode
// does not go idle while held. Any other 1:1 render — the pre-render pass,
// the neighbours the loupe prefetches, a share-link guest — would only push
// the editor's decode out.
func (c *Cache) HoldEditor(photo store.Photo) (release func()) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEditors == nil {
		c.fullEditors = map[string]int{}
	}
	c.fullEditors[photo.CacheKey]++
	released := false
	return func() {
		c.fullMu.Lock()
		defer c.fullMu.Unlock()
		if released {
			return
		}
		released = true
		if c.fullEditors[photo.CacheKey]--; c.fullEditors[photo.CacheKey] <= 0 {
			delete(c.fullEditors, photo.CacheKey)
		}
	}
}

// holdsEditor reports whether a decode of photo under edits is one to keep:
// the photo is held open in the editor and the decode is an edit's. The
// base look's decode (auto-brightened) is never one an edit can start from.
func (c *Cache) holdsEditor(photo store.Photo, edits *edit.Params) bool {
	if edits == nil {
		return false
	}
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	return c.fullEditors[photo.CacheKey] > 0
}

// keptFull returns the kept decode of cacheKey under key, or nil, and
// restarts its idle time. Its pixels are shared: copy them before changing
// any.
func (c *Cache) keptFull(cacheKey, key string) *fullDecode {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	f := c.fullEntry
	if f == nil || f.cacheKey != cacheKey || f.key != key {
		return nil
	}
	f.used = time.Now()
	return f
}

// keepFull makes f the kept decode, in place of any other, when it is
// still worth keeping: its photo is still held open in the editor (the user
// may have moved on while it decoded), and no decode of the photo asked for
// later is kept already (a warm-up of the starting state finishing after
// the decode of an edit since). A closed cache keeps nothing.
func (c *Cache) keepFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullClosed || c.fullEditors[f.cacheKey] <= 0 || c.fullEntry == f {
		return
	}
	if old := c.fullEntry; old != nil {
		if old.cacheKey == f.cacheKey && old.seq > f.seq {
			return
		}
		old.timer.Stop()
	}
	f.used = time.Now()
	f.timer = time.AfterFunc(fullDecodeIdle, func() { c.expireFull(f) })
	c.fullEntry = f
}

// expireFull lets f go when it has been idle fullDecodeIdle with its photo
// no longer held open, or looks again later.
func (c *Cache) expireFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEntry != f {
		return
	}
	if c.fullEditors[f.cacheKey] > 0 {
		f.timer.Reset(fullDecodeIdle)
		return
	}
	if left := fullDecodeIdle - time.Since(f.used); left > 0 {
		f.timer.Reset(left)
		return
	}
	c.fullEntry = nil
}

// closeFull lets the kept decode go and keeps none after.
func (c *Cache) closeFull() {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	c.fullClosed = true
	if f := c.fullEntry; f != nil {
		f.timer.Stop()
		c.fullEntry = nil
	}
}

// decodeFull decodes photo at full resolution under edits with proc, which
// open readies first.
func (c *Cache) decodeFull(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (*fullDecode, error) {
	if err := open(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Read the as-shot balance BEFORE Process resolves the chosen WB into
	// pre_mul in place: the compensation is measured against it.
	wbCompEV := edits.WBCompEV(proc.CamMul(), proc.CamXYZ())
	img, err := proc.Process(ctx, fullDecodeParams(edits))
	if err != nil {
		return nil, err
	}
	rgba, err := FromLibraw(img)
	if err != nil {
		return nil, err
	}
	c.fullDecodes.Add(1)
	return &fullDecode{cacheKey: photo.CacheKey, key: fullDecodeKey(edits), wbCompEV: wbCompEV, rgba: rgba}, nil
}

// How a full render got its decode, for the render log.
const (
	fullDecoded = "RAW render"
	fullKept    = "render from the kept decode"
	fullWaited  = "render after waiting on a decode under way"
)

// keptFullDecode returns the kept decode of photo under edits, decoding and
// keeping it first when it is not kept. A decode of it under way elsewhere
// (a warm-up's, or another 1:1 render's) is waited for rather than run
// twice; if that one is called off, this one decodes itself, and if it
// fails, this one fails the same. The pixels are shared: copy them before
// changing any.
func (c *Cache) keptFullDecode(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (f *fullDecode, how string, err error) {
	key := fullDecodeKey(edits)
	flight := photo.CacheKey + "|" + key
	how = fullKept
	var mine *fullFlight
	var seq uint64
	for {
		if f := c.keptFull(photo.CacheKey, key); f != nil {
			return f, how, nil
		}
		c.fullMu.Lock()
		other, busy := c.fullFlights[flight]
		if !busy {
			if c.fullFlights == nil {
				c.fullFlights = map[string]*fullFlight{}
			}
			mine = &fullFlight{done: make(chan struct{})}
			c.fullFlights[flight] = mine
			c.fullSeq++
			seq = c.fullSeq
			c.fullMu.Unlock()
			break
		}
		c.fullMu.Unlock()
		how = fullWaited
		select {
		case <-other.done:
			if other.err != nil && !canceled(other.err) {
				return nil, how, other.err
			}
		case <-ctx.Done():
			return nil, how, ctx.Err()
		}
	}
	defer func() {
		c.fullMu.Lock()
		delete(c.fullFlights, flight)
		c.fullMu.Unlock()
		mine.err = err
		close(mine.done)
	}()
	if f, err = c.decodeFull(ctx, proc, photo, edits, open); err != nil {
		return nil, fullDecoded, err
	}
	f.seq = seq
	c.keepFull(f)
	return f, fullDecoded, nil
}

func canceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// fullDecodeFor returns photo's full-resolution decode under edits and the
// WB compensation it was decoded under. shared reports pixels the kept
// decode owns too: the caller copies them before changing any. how says
// where the decode came from (fullDecoded, fullKept, fullWaited). A photo
// held open in the editor keeps its decode for the next render (see
// holdsEditor); any other uses the kept decode when it is the one, and
// otherwise decodes for itself.
func (c *Cache) fullDecodeFor(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (rgba *image.RGBA, wbCompEV float64, shared bool, how string, err error) {
	if c.holdsEditor(photo, edits) {
		f, how, err := c.keptFullDecode(ctx, proc, photo, edits, open)
		if err != nil {
			return nil, 0, false, how, err
		}
		return f.rgba, f.wbCompEV, true, how, nil
	}
	if f := c.keptFull(photo.CacheKey, fullDecodeKey(edits)); f != nil {
		return f.rgba, f.wbCompEV, true, fullKept, nil
	}
	f, err := c.decodeFull(ctx, proc, photo, edits, open)
	if err != nil {
		return nil, 0, false, fullDecoded, err
	}
	return f.rgba, f.wbCompEV, false, fullDecoded, nil
}

// WarmFull decodes photo at full resolution under edits and keeps the
// decode, so the 1:1 render of the next edit that leaves the LibRaw inputs
// as edits has them starts after the demosaic. The caller holds the photo
// open in the editor (HoldEditor), or nothing is kept. Nothing is decoded
// when the decode is kept already; a 1:1 render asking for the same decode
// meanwhile waits for this one. Cancelling ctx stops the decode. It runs at
// visible priority: a 1:1 render may be waiting on it.
func (c *Cache) WarmFull(ctx context.Context, photo store.Photo, edits *edit.Params) error {
	if c.pool == nil {
		return nil
	}
	const prio = decode.PriorityVisible
	key := photo.CacheKey + "|warmfull|" + fullDecodeKey(edits)
	return c.pool.Do(ctx, key, prio, func(jctx context.Context, proc *libraw.Processor) error {
		_, _, err := c.keptFullDecode(jctx, proc, photo, edits, func() error {
			return decode.OpenForDecode(jctx, c.IOGate, proc, photo.Path(), prio)
		})
		return err
	})
}
