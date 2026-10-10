package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marrasen/aprot/client"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// DevWBBar is a button of the eyedropper's bar: "asShot" or "auto" sets
// that mode instead, "reset" takes the picks back, "cancel" takes them
// back and puts the eyedropper away, and "done" keeps the pick.
type DevWBBar struct{ Act string }

// wbPick is the white-balance eyedropper while it is out: the edit it
// samples and the one to go back to, the frame it samples, for the
// magnifier, and which pick is the latest, so an older answer coming late
// is let go.
type wbPick struct {
	on     bool
	base   marrawclient.Params
	before marrawclient.Params
	seq    int
	frame  *paint.Image
	pix    *image.RGBA
}

// devWBPick puts the eyedropper out, pinning the edit it samples, or away,
// keeping what it picked.
func (cu *culler) devWBPick(on bool) {
	d := &cu.dev
	if on && (!d.open || !cu.culling) {
		return
	}
	if on == cu.wb.on {
		return
	}
	if on {
		cu.disarmPick()
	}
	if !on {
		cu.wbFinish(true)
		return
	}
	cu.wb = wbPick{on: true, base: d.params, before: d.params, seq: cu.wb.seq + 1}
	cu.loadWBFrame(d.id, d.params, cu.wb.seq)
	cu.showCull()
	if d.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
}

// loadWBFrame fetches the frame the picks sample, which shows in the
// magnifier: fetched as the eyedropper comes, the first pick is as quick
// as the rest.
func (cu *culler) loadWBFrame(id int64, base marrawclient.Params, seq int) {
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 60*time.Second)
		defer cancel()
		blob, err := cu.api.Edits.WBPickFrame(ctx, id, base)
		var pix *image.RGBA
		if err == nil && blob != nil {
			var m image.Image
			if m, err = jpeg.Decode(bytes.NewReader(blob.Data)); err == nil {
				pix = image.NewRGBA(m.Bounds())
				draw.Draw(pix, pix.Bounds(), m, m.Bounds().Min, draw.Src)
			}
		}
		select {
		case cu.do <- func() {
			if err != nil || pix == nil {
				log.Printf("white balance frame: %v", err)
				return
			}
			if !cu.wb.on || cu.wb.seq != seq {
				return
			}
			cu.wb.pix, cu.wb.frame = pix, paint.NewImage(pix)
			cu.showCull()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// devWBAt picks the white balance that makes the photo neutral at x, y,
// and shows it; it is kept as the eyedropper is put away with Done.
func (cu *culler) devWBAt(x, y float64) {
	d := &cu.dev
	if !cu.wb.on || !d.open || d.id != cu.photos[cu.at].ID {
		return
	}
	cu.wb.seq++
	id, params, base, seq := d.id, d.params, cu.wb.base, cu.wb.seq
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		res, err := cu.api.Edits.PickWhiteBalance(ctx, id, params, base, x, y)
		select {
		case cu.do <- func() {
			if !cu.wb.on || cu.wb.seq != seq || d.id != id {
				return
			}
			if err != nil || res == nil {
				log.Printf("white balance pick: %v", err)
				cu.tell(whyNot(err))
				return
			}
			cu.wbShow(*res)
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// wbShow shows params, a pick or a mode from the bar, without saving it
// yet.
func (cu *culler) wbShow(params marrawclient.Params) {
	d := &cu.dev
	d.params = params
	_ = cu.c.Update("develop", cu.developState())
	d.edits++
	cu.stopTiles()
	cu.preview(true)
	cu.showCull()
}

// devWBBar takes a button of the eyedropper's bar, or Enter or Escape.
func (cu *culler) devWBBar(act string) {
	d := &cu.dev
	if !cu.wb.on {
		return
	}
	cu.wb.seq++
	switch act {
	case "asShot", "auto":
		p := d.params
		devChoices["wbMode"].set(&p, map[string]int{"asShot": 0, "auto": 1}[act])
		cu.wbShow(p)
	case "reset":
		cu.wbShow(cu.wb.before)
	case "cancel":
		cu.wbFinish(false)
	case "done":
		cu.wbFinish(true)
	}
}

// wbFinish puts the eyedropper away, keeping what it picked as a step of
// the edit's history, or going back to the edit before it.
func (cu *culler) wbFinish(keep bool) {
	d := &cu.dev
	if !cu.wb.on {
		return
	}
	before := cu.wb.before
	cu.wb = wbPick{seq: cu.wb.seq + 1}
	changed := !reflect.DeepEqual(d.params, before)
	switch {
	case keep && changed:
		cu.edited(true)
		cu.remember("White balance pick")
	case changed:
		cu.wbShow(before)
	}
	cu.showCull()
	if d.mounted {
		_ = cu.c.Update("develop", cu.developState())
	}
}

// whyNot is why a spot could not be picked, as the backend says, for a
// note.
func whyNot(err error) string {
	var ce *client.Error
	if !errors.As(err, &ce) || ce.Message == "" {
		return "That spot could not be picked"
	}
	msg := strings.TrimPrefix(ce.Message, "invalid params: ")
	msg = strings.Replace(msg, "picked area", "this spot", 1)
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}
