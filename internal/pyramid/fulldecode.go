package pyramid

import (
	"context"
	"fmt"
	"image"
	"time"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

// fullDecodeIdle is how long the kept full-resolution decode outlives its
// last use. It is the editor's working copy, and a 42-megapixel frame holds
// 170 MB, so it goes once nobody has rendered from it for a while.
const fullDecodeIdle = 2 * time.Minute

// fullDecode is the last full-resolution LibRaw decode of the photo open in
// the editor, kept so the next 1:1 render under the same LibRaw inputs
// skips the demosaic — ~2.5 s of a 42-megapixel render. Crop,
// straighten, lens, masks, healing and every look-stage slider leave the
// decode as it is, so after an edit of any of them the tiles come back in
// the time the stages after the decode take. The pixels are exactly the
// ones a fresh decode gives: the key is every input LibRaw reads.
type fullDecode struct {
	cacheKey string
	key      string      // fullDecodeKey
	wbCompEV float64     // edit.WBCompEV for this decode's WB
	rgba     *image.RGBA // never changed once kept
	used     time.Time   // last render from it; guarded by fullMu
	timer    *time.Timer // lets the decode go fullDecodeIdle after used
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

// editorsFull reports whether a decode of photo under edits is one to keep:
// the photo is the one open in the editor (WarmFull names it) and the
// decode is an edit's. Any other 1:1 render — the pre-render pass, the
// neighbours the loupe prefetches, a share-link guest — would only push the
// editor's decode out. The base look's decode (auto-brightened) is never
// one an edit can start from.
func (c *Cache) editorsFull(photo store.Photo, edits *edit.Params) bool {
	if edits == nil {
		return false
	}
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	return photo.CacheKey == c.fullEditor
}

// keepFull makes f the kept decode, in place of any other. A closed cache
// keeps nothing: a render finishing after Close must not pin a frame.
func (c *Cache) keepFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullClosed || c.fullEntry == f {
		return
	}
	if old := c.fullEntry; old != nil {
		old.timer.Stop()
	}
	f.used = time.Now()
	f.timer = time.AfterFunc(fullDecodeIdle, func() { c.expireFull(f) })
	c.fullEntry = f
}

// expireFull lets f go when it has been idle fullDecodeIdle, or looks again
// when it has been used since.
func (c *Cache) expireFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEntry != f {
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

// keptFullDecode returns the kept decode of photo under edits, decoding and
// keeping it first when it is not kept. A decode of it under way elsewhere
// (WarmFull's, or another 1:1 render's) is waited for rather than run twice.
// The pixels are shared: copy them before changing any.
func (c *Cache) keptFullDecode(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (f *fullDecode, decoded bool, err error) {
	key := fullDecodeKey(edits)
	flight := photo.CacheKey + "|" + key
	for {
		if f := c.keptFull(photo.CacheKey, key); f != nil {
			return f, false, nil
		}
		c.fullMu.Lock()
		wait, busy := c.fullFlights[flight]
		if !busy {
			if c.fullFlights == nil {
				c.fullFlights = map[string]chan struct{}{}
			}
			done := make(chan struct{})
			c.fullFlights[flight] = done
			c.fullMu.Unlock()
			defer func() {
				c.fullMu.Lock()
				delete(c.fullFlights, flight)
				c.fullMu.Unlock()
				close(done)
			}()
			break
		}
		c.fullMu.Unlock()
		// The other decode may be called off; then this one decodes itself.
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	if f, err = c.decodeFull(ctx, proc, photo, edits, open); err != nil {
		return nil, false, err
	}
	c.keepFull(f)
	return f, true, nil
}

// fullDecodeFor returns photo's full-resolution decode under edits and the
// WB compensation it was decoded under. shared reports pixels the kept
// decode owns too: the caller copies them before changing any; decoded
// reports whether LibRaw ran. The editor's photo keeps its decode for the
// next render (see editorsFull); any other photo decodes for itself.
func (c *Cache) fullDecodeFor(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (rgba *image.RGBA, wbCompEV float64, shared, decoded bool, err error) {
	if c.editorsFull(photo, edits) {
		f, decoded, err := c.keptFullDecode(ctx, proc, photo, edits, open)
		if err != nil {
			return nil, 0, false, false, err
		}
		return f.rgba, f.wbCompEV, true, decoded, nil
	}
	f, err := c.decodeFull(ctx, proc, photo, edits, open)
	if err != nil {
		return nil, 0, false, false, err
	}
	return f.rgba, f.wbCompEV, false, true, nil
}

// WarmFull names photo the one open in the editor, whose 1:1 renders keep
// their decode, and decodes it at full resolution under edits, so the 1:1
// render of the next edit that leaves the LibRaw inputs as edits has them
// starts after the demosaic. Nothing is decoded when the decode is kept
// already; a 1:1 render asking for the same decode meanwhile waits for
// this one. Cancelling ctx stops the decode. It runs at visible priority: a
// 1:1 render may be waiting on it. edits nil only names the photo.
func (c *Cache) WarmFull(ctx context.Context, photo store.Photo, edits *edit.Params) error {
	c.fullMu.Lock()
	c.fullEditor = photo.CacheKey
	c.fullMu.Unlock()
	if c.pool == nil || edits == nil {
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
