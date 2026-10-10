package main

import (
	"context"
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
	evs := map[int64]float64{}
	for _, id := range ids {
		if i, ok := cu.index[id]; ok {
			evs[id] = cu.photos[i].BaseExpEV
		}
	}
	cu.notify("Applying the preset to " + itoa(int64(len(ids))) + " photos…")
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 10*time.Minute)
		defer cancel()
		var (
			mu    sync.Mutex
			first error
			name  string
			wg    sync.WaitGroup
		)
		work := make(chan int64)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range work {
					err := func() error {
						base, err := cu.api.Edits.GetEditParams(ctx, id)
						if err != nil {
							return err
						}
						j := presetJob{id: id, base: *base, ev: evs[id], users: users, autos: autos}
						p, n, err := cu.presetParams(ctx, j, in.Auto, in.Index)
						if err != nil {
							return err
						}
						mu.Lock()
						name = n
						mu.Unlock()
						return cu.api.Edits.PasteEditParams(ctx, []int64{id}, p)
					}()
					if err != nil {
						mu.Lock()
						if first == nil {
							first = err
						}
						mu.Unlock()
					}
				}
			}()
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
		case cu.do <- func() {
			if first != nil {
				cu.fail("The preset could not be applied to every photo", first)
				return
			}
			cu.notify(name + " applied to " + itoa(int64(len(ids))) + " photos")
		}:
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
