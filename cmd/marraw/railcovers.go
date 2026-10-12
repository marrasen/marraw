package main

import (
	"fmt"
	"image"
	"sort"
	"strings"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// The sidebar's covers: each shoot's first pick, or its first photo, as
// a small picture beside its name. They come from the same small
// renditions as the grid's tiles, asked for fast, so a cover never waits
// on a RAW, and are cut down to the cover's own size, so hundreds of
// shoots cost little.

// coverW and coverH are a cover's size in the sidebar, and coverScale
// how many pixels it keeps for each, for sharp edges on a scaled screen.
const (
	coverW, coverH = 46, 32
	coverScale     = 2
)

// CoverIn is a cover's picture, arrived, by its key.
type CoverIn struct {
	Key string
	Img *paint.Image
}

// coverKey names a cover's picture: its photo as it is edited now.
func coverKey(c *marrawclient.ShootCover) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s:%s", c.PhotoID, c.CacheKey, c.EditHash)
}

// liveSum is a shoot's cull counts and cover as this program knows them,
// newer than the backend's listing while the user culls it.
type liveSum struct {
	picked, rejected int
	cover            *marrawclient.ShootCover
}

// liveSummary is the open folder's cull counts and cover from its photos,
// chosen as the backend chooses: the first pick, else the first photo,
// by capture time, untimed last, then by file name.
func (cu *culler) liveSummary() liveSum {
	cu.syncAll()
	var s liveSum
	if len(cu.all) == 0 {
		return s
	}
	order := make([]int, len(cu.all))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		p, q := cu.all[order[a]], cu.all[order[b]]
		if pp, qp := p.Flag == "pick", q.Flag == "pick"; pp != qp {
			return pp
		}
		if pu, qu := p.TakenAt <= 0, q.TakenAt <= 0; pu != qu {
			return qu
		}
		if p.TakenAt != q.TakenAt {
			return p.TakenAt < q.TakenAt
		}
		return p.FileName < q.FileName
	})
	for _, p := range cu.all {
		switch p.Flag {
		case "pick":
			s.picked++
		case "exclude":
			s.rejected++
		}
	}
	c := cu.all[order[0]]
	s.cover = &marrawclient.ShootCover{PhotoID: c.ID, CacheKey: c.CacheKey, EditHash: c.EditHash}
	return s
}

// wantCover fetches c's picture, unless it is held or on its way, and
// shows it in the sidebar as it comes.
func (cu *culler) wantCover(c *marrawclient.ShootCover) {
	key := coverKey(c)
	if key == "" || cu.lib.covers[key] != nil || cu.lib.coverWant[key] {
		return
	}
	cu.lib.coverWant[key] = true
	p := marrawclient.Photo{ID: c.PhotoID, CacheKey: c.CacheKey, EditHash: c.EditHash}
	go func() {
		var img *paint.Image
		select {
		case cu.thumbSlots <- struct{}{}:
			m, err := cu.im.rgba(cu.ctx, p, want{level: "256", stale: true, fast: true})
			<-cu.thumbSlots
			if err == nil {
				img = paint.NewImage(coverCut(m, coverW*coverScale, coverH*coverScale))
			}
		case <-cu.ctx.Done():
			return
		}
		cu.onDo(func() {
			delete(cu.lib.coverWant, key)
			if img == nil {
				return
			}
			cu.lib.covers[key] = img
			_ = cu.c.Patch("grid", CoverIn{Key: key, Img: img})
		})
	}()
}

// previewKey names the cover of the folder at path before it is
// scanned: its first RAW's embedded preview.
func previewKey(path string) string { return "preview:" + strings.ToLower(path) }

// wantPreview fetches the cover of the folder at path, not scanned yet:
// the backend reads the preview its camera put in its first RAW, without
// cataloguing the folder. A folder with none, or one that fails, is not
// asked again while this program runs.
func (cu *culler) wantPreview(path string) {
	key := previewKey(path)
	if cu.lib.covers[key] != nil || cu.lib.coverWant[key] {
		return
	}
	cu.lib.coverWant[key] = true
	go func() {
		var img *paint.Image
		select {
		case cu.lib.previewSlots <- struct{}{}:
			res, err := cu.api.Library.ShootPreview(cu.ctx, path)
			<-cu.lib.previewSlots
			if err == nil && res != nil && len(res.JPEG) > 0 {
				if m, err := jpegturbo.DecodeRGBA(res.JPEG); err == nil {
					img = paint.NewImage(coverCut(m, coverW*coverScale, coverH*coverScale))
				}
			}
		case <-cu.ctx.Done():
			return
		}
		if img == nil {
			return
		}
		cu.onDo(func() {
			cu.lib.covers[key] = img
			_ = cu.c.Patch("grid", CoverIn{Key: key, Img: img})
		})
	}()
}

// coverCut is the middle of src at w by h's shape, scaled down to w by h,
// each pixel the mean of those it covers.
func coverCut(src *image.RGBA, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	// The largest part of src at w by h's shape, in its middle.
	cw, ch := sw, sw*h/w
	if ch > sh {
		cw, ch = sh*w/h, sh
	}
	x0, y0 := b.Min.X+(sw-cw)/2, b.Min.Y+(sh-ch)/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if cw <= 0 || ch <= 0 {
		return out
	}
	for y := range h {
		ya, yb := y0+y*ch/h, y0+max((y+1)*ch/h, y*ch/h+1)
		for x := range w {
			xa, xb := x0+x*cw/w, x0+max((x+1)*cw/w, x*cw/w+1)
			var r, g, bl, a, n int
			for sy := ya; sy < yb; sy++ {
				row := src.Pix[src.PixOffset(xa, sy):]
				for sx := xa; sx < xb; sx++ {
					i := (sx - xa) * 4
					r, g, bl, a = r+int(row[i]), g+int(row[i+1]), bl+int(row[i+2]), a+int(row[i+3])
					n++
				}
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return out
}

// dropLive lets go of the counts and covers known here for the shoots
// under parent, but the one open, as a newer listing of them comes.
func (cu *culler) dropLive(under string) {
	for path := range cu.lib.live {
		if !strings.EqualFold(path, cu.folderPath) && (strings.EqualFold(path, under) || hasPrefixFold(path, under)) {
			delete(cu.lib.live, path)
		}
	}
}

// hasPrefixFold reports whether path lies under dir, in any case.
func hasPrefixFold(path, dir string) bool {
	p, d := strings.ToLower(path), strings.ToLower(strings.TrimRight(dir, `/\`))
	return len(p) > len(d)+1 && strings.HasPrefix(p, d) && (p[len(d)] == '/' || p[len(d)] == '\\')
}
