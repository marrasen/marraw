package main

import (
	"context"
	"fmt"
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
// a few at a time.
func (cu *culler) batchPreset(in BatchPreset) {
	ids := cu.targetIDs()
	if len(ids) == 0 {
		return
	}
	users, autos := cu.presetsOf()
	if n := map[bool]int{false: len(users), true: len(autos)}[in.Auto]; in.Index < 0 || in.Index >= n {
		cu.notify(fmt.Sprintf("There is no preset %d", in.Index+1))
		return
	}
	evs := map[int64]float64{}
	for _, id := range ids {
		if i, ok := cu.index[id]; ok {
			evs[id] = cu.photos[i].BaseExpEV
		}
	}
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
		mu.Lock()
		name = n
		mu.Unlock()
		_, err = cu.api.Edits.PasteEditParams(ctx, []int64{id}, p)
		return err
	}, func(err error) {
		if err != nil {
			cu.fail("The preset could not be applied to every photo", err)
			return
		}
		note := name + " applied to " + itoa(int64(len(ids))) + " photos"
		if !in.Auto && len(users[in.Index].Params.Masks) > 0 {
			// Its masks are made one photo at a time, in Develop.
			note += ", without its smart masks: apply it to one photo in Develop for those"
		}
		cu.notify(note)
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
