package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// batchDelta adds a change to one field of every photo selected, each
// on its own value, as the batch card's sliders are let go.
func (cu *culler) batchDelta(in BatchDelta) {
	ids := cu.targetIDs()
	if len(ids) == 0 {
		return
	}
	cu.batchGen++
	by := in.By
	var d marrawclient.Delta
	switch in.Field {
	case "expEV":
		d.ExpEV = &by
	case "contrast":
		d.Contrast = &by
	case "saturation":
		d.Saturation = &by
	default:
		return
	}
	cu.call("The change could not be made", func(ctx context.Context) error {
		return cu.api.Edits.ApplyBatchEdit(ctx, ids, d)
	}, nil)
}

// batchPreset lays a preset over each photo selected, on its own edit,
// a few at a time. A user preset's smart masks go on with its look, added
// to each photo's own, as they do on one photo: the AI masks' maps are
// made for the first photo first, which asks for a model not downloaded
// yet and tells the maps' version, and for the rest behind, as one task
// once their edits are saved.
func (cu *culler) batchPreset(in BatchPreset) {
	ids := cu.targetIDs()
	if len(ids) == 0 {
		return
	}
	users, autos := cu.presetsOf()
	if in.Key != "" {
		in.Index = -1
		for _, c := range cu.presetCards() {
			if c.Key == in.Key {
				in.Index = c.Index
			}
		}
		if in.Index < 0 {
			cu.notify("That preset is gone")
			return
		}
	}
	if n := map[bool]int{false: len(users), true: len(autos)}[in.Auto]; in.Index < 0 || in.Index >= n {
		cu.notify(fmt.Sprintf("There is no preset %d", in.Index+1))
		return
	}
	// Each photo's exposure rest, as the photos are now: the AI masks'
	// maps can take minutes, and another shoot may show by then.
	evs := map[int64]float64{}
	for _, id := range ids {
		if i, ok := cu.index[id]; ok {
			evs[id] = cu.photos[i].BaseExpEV
		} else if i, ok := cu.allIndex[id]; ok {
			evs[id] = cu.all[i].BaseExpEV
		}
	}
	cu.batchGen++
	gen := cu.batchGen
	var ai, ranges []marrawclient.Mask
	if !in.Auto {
		ai, ranges = presetRecipes(users[in.Index])
	}
	if in.SkipAI && len(ai) > 0 {
		ai = nil
		if isMasksOnlyPreset(users[in.Index]) && len(ranges) == 0 {
			cu.notify(users[in.Index].Name + " carries only AI masks, and their model is not downloaded: nothing changed")
			return
		}
	}
	if len(ai) == 0 {
		cu.batchPresetRun(ids, in, users, autos, ranges, nil, evs, "")
		return
	}
	key := users[in.Index].ID
	kinds := aiMapKinds(ai)
	cu.notify("Making the preset's AI masks…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 10*time.Minute)
		defer cancel()
		vers := map[marrawclient.AIKind]string{}
		var mapErr error
		for _, k := range kinds {
			if !in.Download {
				if st, err := cu.api.Edits.AIModelStatus(ctx, k); err == nil && st != nil && !st.Downloaded {
					cu.askModelOr("batchPresetMasks:"+key, aiModelWhat[string(k)],
						fmt.Sprintf("the smart masks of %d photos", len(ids)), st.Bytes, func() {
							cu.notify("Answer the open question first, then apply the preset again")
						})
					return
				}
			}
			res, err := cu.api.Edits.GenerateAIMap(ctx, ids[0], k, in.Download)
			if err != nil || res == nil {
				mapErr = orNoAnswer(err)
				break
			}
			vers[k] = res.MapVer
		}
		cu.onDo(func() {
			// Something else was chosen or done to the photos meanwhile:
			// this would land on top of it.
			if cu.batchGen != gen || !slices.Equal(cu.targetIDs(), ids) {
				cu.notify("The photos changed meanwhile, so the preset was not applied")
				return
			}
			if mapErr != nil {
				// The look goes on all the same, as when the model is
				// declined.
				cu.batchPresetRun(ids, in, users, autos, ranges, nil, evs, "without its AI masks: "+mapErr.Error())
				return
			}
			var masks []marrawclient.Mask
			for _, m := range ai {
				m.MapVer = vers[aiMapKind(m.AIKind)]
				masks = append(masks, m)
			}
			cu.batchPresetRun(ids, in, users, autos, append(masks, ranges...), kinds, evs, "")
		})
	}()
}

// batchPresetAnswer takes the answer to the download a batch preset's AI
// masks asked for: with it the preset goes on with them, and without it
// with its look and range masks alone, as on one photo. what is the
// preset's id.
func (cu *culler) batchPresetAnswer(what string, ok bool) {
	cu.batchPreset(BatchPreset{Key: "u:" + what, Download: ok, SkipAI: !ok})
}

// batchPresetRun lays the preset of in over each of ids, its look and
// then masks after the photo's own, and makes the maps of kinds for the
// photos behind once all are saved. evs are the photos' exposure rests,
// and without, where not "", what the note says was left out.
func (cu *culler) batchPresetRun(ids []int64, in BatchPreset, users []marrawclient.UserPreset,
	autos []marrawclient.AutoPreset, masks []marrawclient.Mask, kinds []marrawclient.AIKind,
	evs map[int64]float64, without string) {
	cu.notify("Applying the preset to " + itoa(int64(len(ids))) + " photos…")
	var (
		mu   sync.Mutex
		name string
	)
	cu.eachPhoto(ids, func(ctx context.Context, id int64) error {
		base, err := cu.api.Edits.GetEditParams(ctx, id)
		if err != nil {
			return err
		}
		if base == nil {
			// No edit stored: the preset goes on a neutral one.
			base = &marrawclient.Params{}
		}
		j := presetJob{id: id, base: *base, ev: evs[id], users: users, autos: autos}
		p, n, err := cu.presetParams(ctx, j, in.Auto, in.Index)
		if err != nil {
			return err
		}
		if len(masks) > 0 {
			// Added to the photo's own: a second apply adds them again,
			// there to see and delete.
			p.Masks = append(slices.Clone(p.Masks), masks...)
		}
		mu.Lock()
		name = n
		mu.Unlock()
		_, err = cu.api.Edits.PasteEditParams(ctx, []int64{id}, p)
		return err
	}, func(err error) {
		if err != nil {
			cu.fail("The preset could not be applied to every photo", err)
		} else {
			note := name + " applied to " + itoa(int64(len(ids))) + " photos"
			switch {
			case without != "":
				note += ", " + without
			case in.SkipAI:
				note += ", without its AI masks: their model is not downloaded"
			}
			cu.notify(note)
		}
		// The maps of the photos saved with the masks, even where some
		// were not: a map of one not saved is made for nothing.
		if len(kinds) > 0 && len(ids) > 1 {
			// Made after the save, so the render each map drops is that of
			// the edit with the masks.
			rest := ids[1:]
			go func() {
				if _, err := cu.api.Edits.GenerateAIMaps(cu.ctx, rest, kinds, false); err != nil && cu.ctx.Err() == nil {
					cu.onDo(func() { cu.fail("The AI masks' maps could not be made", err) })
				}
			}()
		}
	})
}

// BatchAuto runs the autos of Sections on every photo selected, each on
// its own edit, as Ctrl and U do on the one in hand.
type BatchAuto struct{ Sections []string }

// batchAuto runs in.Sections' autos on each photo selected, a few at a
// time.
func (cu *culler) batchAuto(in BatchAuto) {
	ids := cu.targetIDs()
	if len(ids) == 0 {
		return
	}
	label := "Auto tone"
	switch {
	case len(in.Sections) > 0 && in.Sections[0] == "all":
		label = "Auto"
	case len(in.Sections) > 0 && in.Sections[0] != "tone":
		label = "Auto color"
	}
	cu.batchGen++
	cu.notify(label + " on " + itoa(int64(len(ids))) + " photos…")
	cu.eachPhoto(ids, func(ctx context.Context, id int64) error {
		base, err := cu.api.Edits.GetEditParams(ctx, id)
		if err != nil {
			return err
		}
		if base == nil {
			base = &marrawclient.Params{}
		}
		p, err := cu.api.Edits.AutoAdjust(ctx, id, *base, in.Sections)
		if err != nil || p == nil {
			return orNoAnswer(err)
		}
		_, err = cu.api.Edits.PasteEditParams(ctx, []int64{id}, *p)
		return err
	}, func(err error) {
		if err != nil {
			cu.fail(label+" did not work on every photo", err)
			return
		}
		cu.notify(label + " on " + itoa(int64(len(ids))) + " photos")
	})
}

// eachPhoto runs fn on each of ids, four at a time, away from the
// controller, then done on it with the first error.
func (cu *culler) eachPhoto(ids []int64, fn func(ctx context.Context, id int64) error, done func(error)) {
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 10*time.Minute)
		defer cancel()
		var (
			mu    sync.Mutex
			first error
			wg    sync.WaitGroup
		)
		work := make(chan int64)
		for range 4 {
			wg.Go(func() {
				for id := range work {
					if err := fn(ctx, id); err != nil {
						mu.Lock()
						if first == nil {
							first = err
						}
						mu.Unlock()
					}
				}
			})
		}
		for _, id := range ids {
			select {
			case work <- id:
			case <-ctx.Done():
			}
		}
		close(work)
		wg.Wait()
		select {
		case cu.do <- func() { done(first) }:
		case <-cu.ctx.Done():
		}
	}()
}

// targetIDs are the ids of the photos selected, or of the one in hand.
func (cu *culler) targetIDs() []int64 {
	var ids []int64
	for _, i := range cu.targets() {
		ids = append(ids, cu.photos[i].ID)
	}
	return ids
}
