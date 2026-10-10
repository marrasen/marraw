package main

import (
	"context"
	"time"

	"github.com/marrasen/gunim"

	"github.com/marrasen/marraw/internal/marrawclient"
)

type (
	// ShareState is what the share dialog shows: the shoot, whether this
	// computer can be reached and how, the export presets downloads can
	// take, and the link once it is made.
	ShareState struct {
		Path, Name string
		Status     *marrawclient.ShareStatus
		Presets    []PresetChoice
		Link       *marrawclient.ShareLink
		Busy       bool
	}
	// ShareCreate makes the link.
	ShareCreate struct {
		Caps     marrawclient.GuestCaps
		Hours    int
		PresetID string
		Reach    marrawclient.ShareReach
	}
	// ShareDone closes the share dialog.
	ShareDone struct{}
)

// sharer is the share dialog's state on the controller's side.
type sharer struct {
	open bool
	st   ShareState
	stop context.CancelFunc
}

// askShare opens the dialog for sharing the shoot at path, following
// whether this computer can be reached while it is open.
func (cu *culler) askShare(path, name string) {
	s := &cu.share
	if s.open || cu.asking {
		return
	}
	s.open, cu.asking = true, true
	s.st = ShareState{Path: path, Name: name}
	if cu.ui != nil {
		for _, p := range cu.ui.ExportPresets {
			s.st.Presets = append(s.st.Presets, PresetChoice{ID: p.ID, Name: p.Name})
		}
	}
	_ = cu.c.Mount(gunim.Root, "share", "share", s.st)
	_ = cu.c.Update("share", s.st)
	ctx, cancel := context.WithCancel(cu.ctx)
	s.stop = cancel
	sub := cu.api.Share.SubscribeStatus(ctx)
	go func() {
		for st := range sub.C {
			cu.onDo(func() {
				if s.open && ctx.Err() == nil {
					s.st.Status = st
					_ = cu.c.Update("share", s.st)
				}
			})
		}
	}()
}

// shareCreate makes the link the dialog asks for.
func (cu *culler) shareCreate(in ShareCreate) {
	s := &cu.share
	if !s.open || s.st.Busy || s.st.Link != nil {
		return
	}
	s.st.Busy = true
	_ = cu.c.Unmount("share")
	path := s.st.Path
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, time.Minute)
		defer cancel()
		link, err := cu.api.Share.CreateLink(ctx, path, in.Caps, in.Hours, in.PresetID, in.Reach)
		cu.onDo(func() {
			s.st.Busy = false
			if err != nil || link == nil || !s.open {
				if err != nil {
					cu.fail("The link could not be made", err)
				}
				cu.shareDone()
				return
			}
			s.st.Link = link
			_ = cu.c.Mount(gunim.Root, "sharelink", "sharelink", s.st)
			_ = cu.c.Update("sharelink", s.st)
		})
	}()
}

// shareDone closes the share dialog.
func (cu *culler) shareDone() {
	s := &cu.share
	if !s.open {
		return
	}
	s.open, cu.asking = false, false
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	_ = cu.c.Unmount("share")
	_ = cu.c.Unmount("sharelink")
	cu.refocus()
}
