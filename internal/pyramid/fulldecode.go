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

// fullDecode is the last full-resolution LibRaw decode the user waited on,
// kept so the next 1:1 render of the same photo under the same LibRaw
// inputs skips the demosaic — ~2.5 s of a 42-megapixel render. Crop,
// straighten, lens, masks, healing and every look-stage slider leave the
// decode as it is, so after an edit of any of them the tiles come back in
// the time the stages after the decode take. The pixels are exactly the
// ones a fresh decode gives: the key is every input LibRaw reads.
type fullDecode struct {
	cacheKey string
	key      string      // fullDecodeKey
	wbCompEV float64     // edit.WBCompEV for this decode's WB
	rgba     *image.RGBA // never changed once kept
	used     time.Time   // last render someone waited on; guarded by fullMu
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

// keptFull returns the kept decode of cacheKey under key, or nil. Its
// pixels are shared: copy them before changing any. touch, for a render
// someone waits on, restarts its idle time; the background pass using it
// does not keep it alive.
func (c *Cache) keptFull(cacheKey, key string, touch bool) *fullDecode {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	f := c.fullEntry
	if f == nil || f.cacheKey != cacheKey || f.key != key {
		return nil
	}
	if touch {
		f.used = time.Now()
	}
	return f
}

// keepFull makes f the kept decode, in place of any other. A closed cache
// keeps nothing: a render finishing after Close must not pin a frame.
func (c *Cache) keepFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullClosed {
		return
	}
	f.used = time.Now()
	f.timer = time.AfterFunc(fullDecodeIdle, func() { c.expireFull(f) })
	if old := c.fullEntry; old != nil {
		old.timer.Stop()
	}
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
func (c *Cache) keptFullDecode(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (*fullDecode, error) {
	key := fullDecodeKey(edits)
	flight := photo.CacheKey + "|" + key
	for {
		if f := c.keptFull(photo.CacheKey, key, true); f != nil {
			return f, nil
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
			return nil, ctx.Err()
		}
	}
	f, err := c.decodeFull(ctx, proc, photo, edits, open)
	if err != nil {
		return nil, err
	}
	c.keepFull(f)
	return f, nil
}

// fullDecodeFor returns photo's full-resolution decode under edits and the
// WB compensation it was decoded under. shared reports pixels the kept
// decode owns too: the caller copies them before changing any. A render
// someone waits on (its pool job at visible priority or up, joined since
// or not) keeps its decode for the next one; the background pass uses the
// kept decode when it is the one but never replaces it, or the 1:1
// pre-render pass would push the photo being edited out within seconds.
func (c *Cache) fullDecodeFor(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error, prio decode.Priority) (rgba *image.RGBA, wbCompEV float64, shared bool, err error) {
	waited := func() bool {
		if now, ok := decode.JobPriority(ctx); ok {
			prio = now
		}
		return prio >= decode.PriorityVisible
	}
	if waited() {
		f, err := c.keptFullDecode(ctx, proc, photo, edits, open)
		if err != nil {
			return nil, 0, false, err
		}
		return f.rgba, f.wbCompEV, true, nil
	}
	if f := c.keptFull(photo.CacheKey, fullDecodeKey(edits), false); f != nil {
		return f.rgba, f.wbCompEV, true, nil
	}
	f, err := c.decodeFull(ctx, proc, photo, edits, open)
	if err != nil {
		return nil, 0, false, err
	}
	if waited() {
		// Someone came to wait on it during the decode.
		c.keepFull(f)
		return f.rgba, f.wbCompEV, true, nil
	}
	return f.rgba, f.wbCompEV, false, nil
}

// WarmFull decodes photo at full resolution under edits and keeps the
// decode, so the 1:1 render of the next edit that leaves the LibRaw inputs
// as edits has them starts after the demosaic. Nothing is decoded when the
// decode is kept already; a 1:1 render asking for the same decode meanwhile
// waits for this one. Cancelling ctx stops the decode. It runs at visible
// priority: a 1:1 render may be waiting on it.
func (c *Cache) WarmFull(ctx context.Context, photo store.Photo, edits *edit.Params) error {
	if c.pool == nil {
		return nil
	}
	const prio = decode.PriorityVisible
	key := photo.CacheKey + "|warmfull|" + fullDecodeKey(edits)
	return c.pool.Do(ctx, key, prio, func(jctx context.Context, proc *libraw.Processor) error {
		_, err := c.keptFullDecode(jctx, proc, photo, edits, func() error {
			return decode.OpenForDecode(jctx, c.IOGate, proc, photo.Path(), prio)
		})
		return err
	})
}
