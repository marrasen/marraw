package pyramid

import (
	"context"
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

// fullDecode is the last full-resolution LibRaw decode, kept so the next 1:1
// render of the same photo under the same LibRaw inputs skips the demosaic —
// ~6.5 s of a 42-megapixel render. Crop, straighten, lens, masks, healing
// and every look-stage slider leave the decode as it is, so after an edit of
// any of them the tiles come back in the time the stages after the decode
// take. The pixels are exactly the ones a fresh decode gives: the key is
// every input LibRaw reads.
type fullDecode struct {
	cacheKey string
	key      string      // fullDecodeKey
	wbCompEV float64     // edit.WBCompEV for this decode's WB
	rgba     *image.RGBA // never mutated once kept
	timer    *time.Timer // lets the decode go after fullDecodeIdle
}

// fullDecodeKey names the decode an edit asks LibRaw for: the base look's
// own (auto-brightened) or the edit's LibRaw inputs.
func fullDecodeKey(edits *edit.Params) string {
	if edits == nil {
		return edit.BaseHash
	}
	return edits.LibrawInputsHash()
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

// keptFull returns a copy of the kept decode of cacheKey under key, for the
// caller to change, with the WB compensation it was decoded under.
func (c *Cache) keptFull(cacheKey, key string) (*image.RGBA, float64, bool) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	f := c.fullEntry
	if f == nil || f.cacheKey != cacheKey || f.key != key {
		return nil, 0, false
	}
	f.timer.Reset(fullDecodeIdle)
	return cloneRGBA(f.rgba), f.wbCompEV, true
}

// keepFull makes rgba the kept decode, in place of any other. rgba must not
// be changed after.
func (c *Cache) keepFull(cacheKey, key string, wbCompEV float64, rgba *image.RGBA) {
	f := &fullDecode{cacheKey: cacheKey, key: key, wbCompEV: wbCompEV, rgba: rgba}
	f.timer = time.AfterFunc(fullDecodeIdle, func() {
		c.fullMu.Lock()
		defer c.fullMu.Unlock()
		if c.fullEntry == f {
			c.fullEntry = nil
		}
	})
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if old := c.fullEntry; old != nil {
		old.timer.Stop()
	}
	c.fullEntry = f
}

// dropFull lets the kept decode go now.
func (c *Cache) dropFull() {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if f := c.fullEntry; f != nil {
		f.timer.Stop()
		c.fullEntry = nil
	}
}

// decodeFull returns the full-resolution decode of photo under edits and the
// WB compensation it was decoded under. The kept decode serves it when it
// is the same one; a decode of it under way elsewhere (WarmFull's) is waited
// for rather than run twice; otherwise open readies proc and proc decodes.
// With keep false the result is only kept, and nil is returned; otherwise
// the caller owns the pixels it gets.
func (c *Cache) decodeFull(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error, keep bool) (*image.RGBA, float64, error) {
	key := fullDecodeKey(edits)
	flight := photo.CacheKey + "|" + key
	for {
		if rgba, comp, ok := c.keptFull(photo.CacheKey, key); ok {
			if !keep {
				return nil, comp, nil
			}
			return rgba, comp, nil
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
			return nil, 0, ctx.Err()
		}
	}
	if err := open(); err != nil {
		return nil, 0, err
	}
	c.fullDecodes.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	// Read the as-shot balance BEFORE Process resolves the chosen WB into
	// pre_mul in place: the compensation is measured against it.
	wbCompEV := edits.WBCompEV(proc.CamMul(), proc.CamXYZ())
	img, err := proc.Process(ctx, fullDecodeParams(edits))
	if err != nil {
		return nil, 0, err
	}
	rgba, err := FromLibraw(img)
	if err != nil {
		return nil, 0, err
	}
	if !keep {
		c.keepFull(photo.CacheKey, key, wbCompEV, rgba)
		return nil, wbCompEV, nil
	}
	c.keepFull(photo.CacheKey, key, wbCompEV, cloneRGBA(rgba))
	return rgba, wbCompEV, nil
}

// WarmFull decodes photo at full resolution under its saved edit and keeps
// the decode, so the 1:1 render of the next edit — most edits leave the
// LibRaw inputs as they are — starts after the demosaic. Nothing is decoded
// when the decode is kept already; a 1:1 render asking for the same decode
// meanwhile waits for this one. Cancelling ctx stops the decode.
func (c *Cache) WarmFull(ctx context.Context, photo store.Photo, prio decode.Priority) error {
	if c.pool == nil {
		return nil
	}
	edits, err := editsForHash(photo, photo.EditHash)
	if err != nil {
		return err
	}
	key := photo.CacheKey + "|warmfull|" + fullDecodeKey(edits)
	return c.pool.Do(ctx, key, prio, func(jctx context.Context, proc *libraw.Processor) error {
		_, _, err := c.decodeFull(jctx, proc, photo, edits, func() error {
			return decode.OpenForDecode(jctx, c.IOGate, proc, photo.Path(), prio)
		}, false)
		return err
	})
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := &image.RGBA{Pix: make([]uint8, len(src.Pix)), Stride: src.Stride, Rect: src.Rect}
	copy(dst.Pix, src.Pix)
	return dst
}
