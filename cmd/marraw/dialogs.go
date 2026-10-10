package main

import (
	"fmt"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary of the dialogs the build asks with.
type (
	// ConfirmAsk asks a yes or no question, as whether to delete photos.
	ConfirmAsk struct {
		Kind, Title, Body, OK string
		Danger                bool
	}
	// Confirmed is the answer to a ConfirmAsk of Kind.
	Confirmed struct {
		Kind string
		OK   bool
	}
	// AskDelete asks to delete the photos selected, or the one showing.
	AskDelete struct{}
)

// newConfirmDialog is a dialog asking s.
func newConfirmDialog(s ConfirmAsk) *widget.Dialog {
	d := widget.NewDialog(s.Title)
	d.Danger = s.Danger
	body := widget.NewLabel(s.Body)
	body.Color, body.MaxLines = widget.PaletteHint, 4
	d.Body = body
	d.SetButtons(s.OK, "Cancel")
	d.OnAccept = widget.Sends(Confirmed{Kind: s.Kind, OK: true})
	d.OnDismiss = widget.Sends(Confirmed{Kind: s.Kind})
	return d
}

// askDelete asks whether to move the photos selected in the grid, or the
// one showing in the cull view, to the Recycle Bin.
func (cu *culler) askDelete() {
	var ids []int64
	for _, i := range cu.targets() {
		ids = append(ids, cu.photos[i].ID)
	}
	if len(ids) == 0 || cu.asking {
		return
	}
	cu.asking, cu.deleting = true, ids
	what := "this photo"
	if len(ids) > 1 {
		what = fmt.Sprintf("these %d photos", len(ids))
	}
	_ = cu.c.Mount(gunim.Root, "confirm", "confirm", ConfirmAsk{Kind: "delete", Danger: true,
		Title: "Move " + what + " to the Recycle Bin?",
		Body:  "The RAW files leave the folder and the library. You can restore them from the Recycle Bin.",
		OK:    "Move to Recycle Bin"})
}

// confirmed takes a dialog's answer.
func (cu *culler) confirmed(a Confirmed) {
	cu.asking = false
	_ = cu.c.Unmount("confirm")
	if a.Kind == "exportCreate" {
		cu.refocus()
		cu.exportCreate(a.OK)
		return
	}
	if a.Kind == "fillModel" {
		cu.refocus()
		cu.fillAnswered(a.OK)
		return
	}
	switch {
	case a.Kind == "eyesModel" && a.OK:
		cu.refocus()
		cu.checkEyes(true)
		return
	case strings.HasPrefix(a.Kind, "aiModel:") && a.OK:
		cu.refocus()
		cu.maskAI(MaskAI{Kind: strings.TrimPrefix(a.Kind, "aiModel:"), Download: true})
		return
	case strings.HasPrefix(a.Kind, "aiRestore:") && a.OK:
		cu.refocus()
		cu.aiRestore(strings.TrimPrefix(a.Kind, "aiRestore:"))
		return
	case a.Kind == "cropModel" && a.OK:
		cu.refocus()
		cu.cropAuto(true)
		return
	case a.Kind == "subjectModel" && a.OK:
		cu.refocus()
		cu.checkSubjects(true)
		return
	}
	if a.Kind != "delete" || !a.OK || len(cu.deleting) == 0 {
		cu.deleting = nil
		cu.refocus()
		return
	}
	ids := cu.deleting
	cu.deleting = nil
	cu.refocus()
	go func() {
		res, err := cu.api.Library.DeletePhotos(cu.ctx, ids)
		select {
		case cu.do <- func() {
			if err != nil {
				cu.fail("The photos could not be deleted", err)
				return
			}
			cu.removed(ids)
			n := len(ids)
			if res != nil {
				n = res.Deleted
			}
			cu.notify(fmt.Sprintf("Moved %d %s to the Recycle Bin", n, map[bool]string{false: "photos", true: "photo"}[n == 1]))
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// removed takes photos ids out of the folder: the grid shows the rest, and
// the cull view the photo after, or goes back to the grid with none left.
func (cu *culler) removed(ids []int64) {
	cu.syncAll()
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	var keep []marrawclient.Photo
	for _, p := range cu.all {
		if !gone[p.ID] {
			keep = append(keep, p)
		}
	}
	cu.setAll(keep)
	cu.applyView()
	if cu.culling {
		if len(cu.photos) == 0 {
			cu.leaveCull()
		} else {
			cu.load = nil
			cu.goTo(min(cu.at, len(cu.photos)-1))
		}
	} else {
		cu.sel, cu.cursor = nil, -1
	}
	_ = cu.c.Update("grid", cu.gridState())
}

// refocus gives the keyboard back to the view the user is in.
func (cu *culler) refocus() {
	if cu.culling {
		_ = cu.c.Focus("cull")
	} else {
		_ = cu.c.Focus("grid")
	}
}
