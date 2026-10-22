package pyramid

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
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

// fullDecode is a full-resolution LibRaw decode of a photo open in the
// editor, kept so the next 1:1 render under the same LibRaw inputs skips
// the demosaic — ~2.5 s of a 42-megapixel render. Crop, straighten, lens,
// masks, healing and every look-stage slider leave the decode as it is, so
// after an edit of any of them the tiles come back in the time the stages
// after the decode take. The pixels are exactly the ones a fresh decode
// gives: the key is every input LibRaw reads.
//
// One rule decides what is kept: the decode the editor asked for last. The
// editor holds its photo open (HoldEditor); its warm-up (WarmFull) and each
// settle or save of a state (WantFull) say which decode it is on now. 1:1 renders
// do not ask — a share-link guest or the pre-render pass renders the photo
// too — but a render of the state the editor is on keeps its decode. A
// decode of anything else — a state the user has left, another photo, the
// base look — is never kept, and a warm-up of a state the editor has left
// is called off.
type fullDecode struct {
	cacheKey string
	key      string      // fullDecodeKey
	noExpKey string      // fullDecodeKeyNoExp
	noWBKey  string      // fullDecodeKeyNoWB
	baked    float64     // the exposure LibRaw baked in (edit.BakedExpEV)
	wbCompEV float64     // edit.WBCompEV for this decode's WB
	rgba     *image.RGBA // never changed once kept
	// What a window needs to fold another white balance in: the
	// multipliers this decode was balanced with (none for auto WB, which
	// dcraw works out from the pixels), the camera's as-shot balance and
	// matrices, and whether the decode lost a channel (decodeClipped).
	wbMul   [4]float64
	camMul  [4]float64
	camXYZ  [4][3]float64
	rgbCam  [3][4]float64
	wbFolds bool
	used    time.Time   // last render from it; guarded by fullMu
	timer   *time.Timer // lets the decode go fullDecodeIdle after used
}

// fullEditor is a photo held open in the editor.
type fullEditor struct {
	holds int
	want  string // fullDecodeKey of the decode it asked for last; "" none yet
	// on is the edit hash of the state it settled on or opened in last:
	// many states share a decode, and EditorWants is about this one.
	on string
}

// fullFlight is a decode under way that others wait for.
type fullFlight struct {
	done    chan struct{}
	waiters int         // guarded by fullMu
	f       *fullDecode // set before done closes, on success
	err     error       // set before done closes, on failure
}

// fullWarm is a warm-up under way (WarmFull).
type fullWarm struct {
	key    string
	cancel context.CancelFunc
}

// fullDecodeKey names the decode an edit asks LibRaw for: the LibRaw params
// themselves, and the edit's LibRaw inputs, from which the WB compensation
// comes.
func fullDecodeKey(edits *edit.Params) string {
	key := fmt.Sprintf("%+v", fullDecodeParams(edits))
	if edits != nil {
		key += "|" + edits.LibrawInputsHash()
	}
	return key
}

// fullDecodeKeyNoExp is fullDecodeKey with the exposure left out: decodes
// that differ only in exposure share it.
func fullDecodeKeyNoExp(edits *edit.Params) string {
	if edits == nil {
		return fullDecodeKey(nil)
	}
	flat := *edits
	flat.ExpEV, flat.ExpPreserve = 0, 0
	return fmt.Sprintf("%+v", fullDecodeParams(&flat)) + "|" + edits.LibrawInputsHashNoExp()
}

// fullDecodeKeyNoWB is fullDecodeKeyNoExp with the white balance left out
// too: decodes that differ only in exposure and white balance share it.
func fullDecodeKeyNoWB(edits *edit.Params) string {
	if edits == nil {
		return fullDecodeKey(nil)
	}
	flat := *edits
	flat.WBMode, flat.WBMul, flat.WBTemp, flat.WBTint, flat.WBKelvin = "", [4]float64{}, 0, 0, 0
	return fullDecodeKeyNoExp(&flat)
}

// wbKnown reports whether the multipliers LibRaw balances a decode of
// edits with are known before it decodes (edit.Params.EffectiveWBMul): not
// for auto white balance, which dcraw works out from the pixels, nor for a
// custom balance with no multipliers, which LibRaw takes from the camera's
// daylight balance.
func wbKnown(edits *edit.Params) bool {
	return edits != nil && edits.WBMode != edit.WBAuto &&
		(edits.WBMode != edit.WBCustom || edits.WBMul != [4]float64{})
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
// Only a held photo's decodes are kept, and its kept decode does not go
// idle while held. Any other 1:1 render — the pre-render pass, the
// neighbours the loupe prefetches, a share-link guest — would only push the
// editor's decode out.
func (c *Cache) HoldEditor(photo store.Photo) (release func()) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEditors == nil {
		c.fullEditors = map[string]*fullEditor{}
	}
	ed := c.fullEditors[photo.CacheKey]
	if ed == nil {
		ed = &fullEditor{}
		c.fullEditors[photo.CacheKey] = ed
	}
	ed.holds++
	// A new session starts from what it opens the photo in: what an earlier
	// one, still letting go, asked for last says nothing about it.
	ed.want, ed.on = "", ""
	released := false
	return func() {
		c.fullMu.Lock()
		defer c.fullMu.Unlock()
		if released {
			return
		}
		released = true
		if ed.holds--; ed.holds > 0 {
			return
		}
		delete(c.fullEditors, photo.CacheKey)
		if f := c.fullEntry; f != nil && f.cacheKey == photo.CacheKey {
			f.used = time.Now() // idle from now, not from its last render
		}
		c.callOffWarmsLocked(photo.CacheKey, "")
	}
}

// HeldInEditor reports whether photo is held open in the editor.
func (c *Cache) HeldInEditor(photo store.Photo) bool {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	return c.fullEditors[photo.CacheKey] != nil
}

// WantFull records that the editor, on a held photo, is now on edits (its
// settle or save of them), so a warm-up of another state is called off and
// a decode of another state finishing later is not kept. edits is an edit,
// the neutral one included: the base look's decode is never one to keep.
func (c *Cache) WantFull(photo store.Photo, edits *edit.Params) {
	if edits == nil {
		edits = &edit.Params{}
	}
	key := fullDecodeKey(edits)
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	c.wantLocked(photo.CacheKey, key)
	if ed := c.fullEditors[photo.CacheKey]; ed != nil {
		ed.on = edits.Hash()
	}
}

// wantLocked makes key what the editor on cacheKey is on, if it is held.
func (c *Cache) wantLocked(cacheKey, key string) {
	ed := c.fullEditors[cacheKey]
	if ed == nil {
		return
	}
	if ed.want != key {
		ed.want = key
		c.callOffWarmsLocked(cacheKey, key)
	}
}

// callOffWarmsLocked calls off the warm-ups of cacheKey that decode
// another state than key ("" calls off all of them).
func (c *Cache) callOffWarmsLocked(cacheKey, key string) {
	for _, w := range c.fullWarms[cacheKey] {
		if w.key != key {
			w.cancel()
		}
	}
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

// KeptForWindow returns photo's kept full-resolution decode when a window
// of edits can render from it (RenderWindow): the decode of edits' LibRaw
// inputs, or of the same ones but for exposure and white balance. fold is
// what the window folds in after the decode — the exposure the decode
// lacks, its WB compensation, and the change to edits' white balance.
// exact reports the decode is edits' own, so the window matches the 1:1
// tiles; otherwise the fold lands near them, as the drafts' fold does. A
// change of white balance needs both balances known (wbKnown) and a decode
// that kept every channel (decodeClipped). The pixels are shared: only
// read them.
func (c *Cache) KeptForWindow(photo store.Photo, edits *edit.Params) (src *image.RGBA, fold Fold, exact, ok bool) {
	if edits == nil {
		return nil, Fold{}, false, false // the base look is never kept
	}
	key, noExpKey, noWBKey := fullDecodeKey(edits), fullDecodeKeyNoExp(edits), fullDecodeKeyNoWB(edits)
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	f := c.fullEntry
	if f == nil || f.cacheKey != photo.CacheKey {
		return nil, Fold{}, false, false
	}
	fold.ExpEV = edits.ExpEV - f.baked + f.wbCompEV
	if f.noExpKey != noExpKey {
		if f.noWBKey != noWBKey || !f.wbFolds || !wbKnown(edits) {
			return nil, Fold{}, false, false
		}
		wb, ok := newWBFold(f.wbMul, edits.EffectiveWBMul(f.camMul, f.camXYZ), f.rgbCam, edits.Highlight)
		if !ok {
			return nil, Fold{}, false, false
		}
		fold.WB = &wb
	}
	f.used = time.Now()
	return f.rgba, fold, f.key == key, true
}

// EditorWants reports whether the editor holds photo and is on edits: the
// state it settled on, saved or opened in last (WantFull, WarmFull), and
// no other — a drag frame of a look slider shares its decode, but is not
// one to decode for. WarmFull of edits then gets the decode kept, joining
// a 1:1 render's decode under way.
func (c *Cache) EditorWants(photo store.Photo, edits *edit.Params) bool {
	if edits == nil {
		return false
	}
	key, hash := fullDecodeKey(edits), edits.Hash()
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	ed := c.fullEditors[photo.CacheKey]
	return ed != nil && ed.want == key && ed.on == hash
}

// keepFull makes f the kept decode, in place of any other, when it is the
// one the editor is on — its photo still held, and the decode the editor
// asked for last — and reports whether it is kept. A closed cache keeps
// nothing.
func (c *Cache) keepFull(f *fullDecode) bool {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEntry == f {
		return true
	}
	if ed := c.fullEditors[f.cacheKey]; c.fullClosed || ed == nil || ed.want != f.key {
		return false
	}
	if old := c.fullEntry; old != nil {
		old.timer.Stop()
	}
	f.used = time.Now()
	f.timer = time.AfterFunc(fullDecodeIdle, func() { c.expireFull(f) })
	c.fullEntry = f
	return true
}

// expireFull lets f go when it has been idle fullDecodeIdle with its photo
// no longer held open, or looks again later.
func (c *Cache) expireFull(f *fullDecode) {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	if c.fullEntry != f {
		return
	}
	if c.fullEditors[f.cacheKey] != nil {
		f.timer.Reset(fullDecodeIdle)
		return
	}
	if left := fullDecodeIdle - time.Since(f.used); left > 0 {
		f.timer.Reset(left)
		return
	}
	c.fullEntry = nil
	forgetWindowStats()
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
	forgetWindowStats()
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
	camMul, camXYZ := proc.CamMul(), proc.CamXYZ()
	wbCompEV := edits.WBCompEV(camMul, camXYZ)
	img, err := proc.Process(ctx, fullDecodeParams(edits))
	if err != nil {
		return nil, err
	}
	rgba, err := FromLibraw(img)
	if err != nil {
		return nil, err
	}
	c.fullDecodes.Add(1)
	return &fullDecode{
		cacheKey: photo.CacheKey, key: fullDecodeKey(edits), noExpKey: fullDecodeKeyNoExp(edits),
		noWBKey: fullDecodeKeyNoWB(edits), baked: edits.BakedExpEV(), wbCompEV: wbCompEV, rgba: rgba,
		wbMul: edits.EffectiveWBMul(camMul, camXYZ), camMul: camMul, camXYZ: camXYZ, rgbCam: proc.RgbCam(),
		wbFolds: wbKnown(edits) && !decodeClipped(rgba),
	}, nil
}

// How a full render got its decode, for the render log.
const (
	fullDecoded = "RAW render"
	fullKept    = "render from the kept decode"
	fullWaited  = "render after waiting on a decode under way"
)

// sharedFullDecode returns photo's decode under edits through the kept
// decode and the decodes under way: the kept one when it is the one; else
// the one under way, waited for rather than run twice — if that one is
// called off this one decodes itself, and if it fails this one fails the
// same; else a decode of its own, kept when it is what the editor is on.
// shared reports pixels others hold too (kept, or handed to waiters): copy
// them before changing any. kept reports a decode the cache keeps.
func (c *Cache) sharedFullDecode(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (f *fullDecode, shared, kept bool, how string, err error) {
	key := fullDecodeKey(edits)
	flight := photo.CacheKey + "|" + key
	how = fullKept
	var mine *fullFlight
	for {
		c.fullMu.Lock()
		if e := c.fullEntry; e != nil && e.cacheKey == photo.CacheKey && e.key == key {
			e.used = time.Now()
			c.fullMu.Unlock()
			return e, true, true, how, nil
		}
		other, busy := c.fullFlights[flight]
		if !busy {
			if c.fullFlights == nil {
				c.fullFlights = map[string]*fullFlight{}
			}
			mine = &fullFlight{done: make(chan struct{})}
			c.fullFlights[flight] = mine
			c.fullMu.Unlock()
			break
		}
		other.waiters++
		c.fullMu.Unlock()
		how = fullWaited
		select {
		case <-other.done:
			if other.f != nil {
				return other.f, true, c.keepFull(other.f), how, nil
			}
			if !canceled(other.err) {
				return nil, false, false, how, other.err
			}
		case <-ctx.Done():
			return nil, false, false, how, ctx.Err()
		}
	}
	defer func() {
		c.fullMu.Lock()
		delete(c.fullFlights, flight)
		shared = kept || mine.waiters > 0
		c.fullMu.Unlock()
		mine.f, mine.err = f, err
		close(mine.done)
	}()
	if f, err = c.decodeFull(ctx, proc, photo, edits, open); err != nil {
		return nil, false, false, fullDecoded, err
	}
	return f, false, c.keepFull(f), fullDecoded, nil
}

func canceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// fullDecodeFor returns photo's full-resolution decode under edits and the
// WB compensation it was decoded under. shared reports pixels others hold
// too: the caller copies them before changing any. kept reports a decode
// the cache keeps, which a render called off after it does not lose. how
// says where the decode came from (fullDecoded, fullKept, fullWaited).
func (c *Cache) fullDecodeFor(ctx context.Context, proc *libraw.Processor, photo store.Photo, edits *edit.Params, open func() error) (rgba *image.RGBA, wbCompEV float64, shared, kept bool, how string, err error) {
	f, shared, kept, how, err := c.sharedFullDecode(ctx, proc, photo, edits, open)
	if err != nil {
		return nil, 0, false, false, how, err
	}
	return f.rgba, f.wbCompEV, shared, kept, how, nil
}

// WarmFull decodes photo, held open in the editor (HoldEditor), at full
// resolution under edits — the state the editor opens it in — and keeps the
// decode, so the 1:1 render of the next edit that leaves those LibRaw
// inputs alone starts after the demosaic. It does nothing when the editor
// has asked for another decode already, and the editor asking for another
// one calls it off. A 1:1 render asking for the same decode meanwhile waits
// for this one. Cancelling ctx stops the decode. It runs at visible
// priority: a 1:1 render may be waiting on it.
func (c *Cache) WarmFull(ctx context.Context, photo store.Photo, edits *edit.Params) error {
	if c.pool == nil || edits == nil {
		return nil
	}
	key := fullDecodeKey(edits)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &fullWarm{key: key, cancel: cancel}
	c.fullMu.Lock()
	ed := c.fullEditors[photo.CacheKey]
	if ed == nil || (ed.want != "" && ed.want != key) {
		c.fullMu.Unlock()
		return nil
	}
	ed.want = key
	if ed.on == "" {
		ed.on = edits.Hash() // the state it opens in
	}
	if c.fullWarms == nil {
		c.fullWarms = map[string][]*fullWarm{}
	}
	c.fullWarms[photo.CacheKey] = append(c.fullWarms[photo.CacheKey], w)
	c.fullMu.Unlock()
	defer func() {
		c.fullMu.Lock()
		ws := slices.DeleteFunc(c.fullWarms[photo.CacheKey], func(x *fullWarm) bool { return x == w })
		if len(ws) == 0 {
			delete(c.fullWarms, photo.CacheKey)
		} else {
			c.fullWarms[photo.CacheKey] = ws
		}
		c.fullMu.Unlock()
	}()
	const prio = decode.PriorityVisible
	return c.pool.Do(ctx, photo.CacheKey+"|warmfull|"+key, prio, func(jctx context.Context, proc *libraw.Processor) error {
		_, _, _, _, err := c.sharedFullDecode(jctx, proc, photo, edits, func() error {
			return decode.OpenForDecode(jctx, c.IOGate, proc, photo.Path(), prio)
		})
		return err
	})
}
