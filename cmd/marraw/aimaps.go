package main

import (
	"context"
	"slices"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The maps an AI mask reads are made by a model, per photo, and kept in
// the data folder, outside the edit: a sidecar from another computer, an
// edit from marraw's own data folder, or a paste carries the mask but not
// its map, and without it the mask does nothing. As marraw's
// esEnsureAIMaps does, the photo stopped on has its missing maps made,
// and its pixels drawn again once one is. A model not downloaded yet is
// asked for once.

// aiEnsureIdle is how long the cull view rests on a photo before its
// maps are seen to, so stepping through a folder starts no inference.
const aiEnsureIdle = 400 * time.Millisecond

// aiModelWhat names each map kind's model, for the download's question.
var aiModelWhat = map[string]string{
	"subject": "the subject detection model", "depth": "the depth estimation model",
	"class": "the scene detection model", "person": "the people detection model",
}

// aiMaps is what has been seen to: the photo and kind pairs asked for,
// and the kinds whose download was asked about.
type aiMaps struct {
	fired map[string]bool
	asked map[string]bool
	timer *time.Timer
}

// ensureAIMapsSoon sees to the maps of the photo at i once the cull view
// has rested on it a moment.
func (cu *culler) ensureAIMapsSoon(i int) {
	a := &cu.aimaps
	if a.timer != nil {
		a.timer.Stop()
	}
	if i < 0 || i >= len(cu.photos) {
		return
	}
	id := cu.photos[i].ID
	a.timer = time.AfterFunc(aiEnsureIdle, func() {
		cu.onDo(func() {
			if cu.culling && cu.at < len(cu.photos) && cu.photos[cu.at].ID == id {
				cu.ensureAIMaps(id)
			}
		})
	})
}

// ensureAIMaps makes the maps photo id's AI masks read and does not have
// yet, never downloading a model without asking.
func (cu *culler) ensureAIMaps(id int64) {
	a := &cu.aimaps
	if a.fired == nil {
		a.fired, a.asked = map[string]bool{}, map[string]bool{}
	}
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 5*time.Minute)
		defer cancel()
		p, err := cu.api.Edits.GetEditParams(ctx, id)
		if err != nil || p == nil {
			return
		}
		var kinds []string
		for _, m := range p.Masks {
			if m.Type != "ai" || m.AIKind == "" {
				continue
			}
			k := string(m.AIKind)
			if k == "background" {
				k = "subject"
			}
			if !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
		for _, k := range kinds {
			key := itoa(id) + "|" + k
			fresh := make(chan bool, 1)
			cu.onDo(func() {
				fresh <- !a.fired[key]
				a.fired[key] = true
			})
			if !<-fresh {
				continue
			}
			if st, err := cu.api.Edits.AIModelStatus(ctx, marrawclient.AIKind(k)); err == nil && st != nil && !st.Downloaded {
				cu.onDo(func() {
					if !a.asked[k] {
						a.asked[k] = true
						cu.askModel("aiRestore:"+k, aiModelWhat[k], st.Bytes)
					}
					// Asked: once the model is here, the map is made again.
					delete(a.fired, key)
				})
				continue
			}
			res, err := cu.api.Edits.GenerateAIMap(ctx, id, marrawclient.AIKind(k), false)
			if err != nil {
				cu.onDo(func() { delete(a.fired, key) })
				continue
			}
			if res != nil && res.Generated {
				cu.onDo(func() { cu.pixelsRenewed(id) })
			}
		}
	}()
}

// aiRestore makes the maps of kind for the photo showing, the model
// downloaded with the user's leave.
func (cu *culler) aiRestore(kind string) {
	if !cu.culling || cu.at >= len(cu.photos) {
		return
	}
	id := cu.photos[cu.at].ID
	cu.tell("Making the " + map[string]string{"subject": "subject", "depth": "depth", "class": "scene", "person": "people"}[kind] + " map…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 10*time.Minute)
		defer cancel()
		res, err := cu.api.Edits.GenerateAIMap(ctx, id, marrawclient.AIKind(kind), true)
		cu.onDo(func() {
			if err != nil {
				cu.fail("The AI mask's map could not be made", err)
				return
			}
			cu.aimaps.fired[itoa(id)+"|"+kind] = true
			if res != nil && res.Generated {
				cu.pixelsRenewed(id)
			}
		})
	}()
}

// pixelsRenewed shows photo id's pixels again: what is rendered of it
// changed without its edit changing, as a map made does.
func (cu *culler) pixelsRenewed(id int64) {
	i, ok := cu.index[id]
	if !ok {
		return
	}
	cu.tiles.drop(id)
	cu.cache.drop(id)
	cu.upgradeThumb(cu.photos[i])
	if d := &cu.dev; d.open && d.id == id {
		cu.preview(true)
	}
	if cu.culling && i == cu.at {
		cu.load = nil
		cu.goTo(i)
	}
}

// upgradeThumb renders photo p's small picture as its edit is now and
// shows it in place of the one showing: the quick one the grid starts
// with may be the camera's own JPEG or a render of an older edit. Two at
// a time, behind what is on screen.
func (cu *culler) upgradeThumb(p marrawclient.Photo) {
	go func() {
		select {
		case cu.editThumbSlots <- struct{}{}:
		case <-cu.ctx.Done():
			return
		}
		defer func() { <-cu.editThumbSlots }()
		ctx, cancel := context.WithTimeout(cu.ctx, 2*time.Minute)
		defer cancel()
		g, err := cu.im.get(ctx, p, want{level: "256"})
		if err != nil {
			return
		}
		cu.onDo(func() {
			i, ok := cu.index[p.ID]
			if !ok || cu.photos[i].EditHash != p.EditHash {
				return
			}
			cu.keepThumb(p.ID, g.img)
			_ = cu.c.Patch("grid", ThumbIn{Folder: cu.folder, Index: i, Img: g.img})
			if cu.culling && i >= cu.at-stripReach && i <= cu.at+stripReach {
				cu.showCull()
			}
		})
	}()
}
