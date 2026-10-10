package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

type (
	// ToggleViewer opens the pop-out viewer, or closes the one open, as
	// Ctrl+N does.
	ToggleViewer struct{}
	// ViewerState is what the pop-out viewer shows: the photo the main
	// window has in hand, its best pixels so far, and whether it floats
	// over other windows.
	ViewerState struct {
		ID     int64
		Name   string
		Img    *paint.Image
		Pinned bool
	}
	// ViewerClose closes the viewer, from its own Ctrl+N or its close
	// button; ViewerPin keeps it over other windows, or not.
	ViewerClose struct{}
	ViewerPin   struct{ On bool }
)

// popViewer is the pop-out viewer's state on the controller's side.
type popViewer struct {
	app  *gunim.App
	win  *gunim.Window
	c    gunim.Client
	st   ViewerState
	key  string
	stop context.CancelFunc
}

// viewerPrefs are what the viewer keeps between runs: where it was, and
// whether it floated.
type viewerPrefs struct {
	Place  *driver.Placement `json:"place,omitempty"`
	Pinned *bool             `json:"pinned,omitempty"`
}

// viewerPrefsPath is where the viewer's prefs are kept.
func viewerPrefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "marraw-gunim", "viewer.json")
}

func loadViewerPrefs() viewerPrefs {
	var p viewerPrefs
	if path := viewerPrefsPath(); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &p)
		}
	}
	return p
}

func saveViewerPrefs(p viewerPrefs) {
	path := viewerPrefsPath()
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		log.Printf("viewer: %v", err)
	}
}

// toggleViewer opens the pop-out viewer, or closes it.
func (cu *culler) toggleViewer() {
	if cu.viewer.win != nil {
		cu.closeViewer()
		return
	}
	cu.openViewer()
}

// openViewer opens the pop-out viewer where it last was, floating over
// other windows unless the user said not to, on the photo in hand.
func (cu *culler) openViewer() {
	pv := &cu.viewer
	if pv.app == nil {
		return
	}
	prefs := loadViewerPrefs()
	pinned := prefs.Pinned == nil || *prefs.Pinned
	o := gunim.WindowOptions{Title: "marraw viewer", Size: geom.Sz(1000, 680), UnderTitleBar: true, AskToClose: ViewerClose{},
		Root: newFullSurface()}
	if prefs.Place != nil {
		o.Place = prefs.Place
	}
	w, err := pv.app.NewWindow(o)
	if err != nil {
		cu.fail("The viewer could not open", err)
		return
	}
	w.RegisterTheme(marrawTheme())
	gunim.RegisterView(w, "viewer", newViewerView, (*viewerView).show)
	pv.win, pv.c, pv.key = w, w.Client(), ""
	pv.st = ViewerState{Pinned: pinned}
	_ = pv.c.SetTheme("marraw")
	_ = pv.c.Mount(gunim.Root, "viewer", "viewer", pv.st)
	_ = pv.c.Focus("viewer")
	ctx, cancel := context.WithCancel(cu.ctx)
	pv.stop = cancel
	c := pv.c
	go func() {
		for {
			select {
			case ev, ok := <-c.Intents():
				if !ok {
					cu.onDo(func() {
						if pv.c == c {
							cu.viewerGone()
						}
					})
					return
				}
				cu.onDo(func() {
					switch in := ev.Intent.(type) {
					case ViewerClose:
						cu.closeViewer()
					case ViewerPin:
						pv.st.Pinned = in.On
						p := loadViewerPrefs()
						p.Pinned = &in.On
						saveViewerPrefs(p)
					case ToggleViewer:
						cu.closeViewer()
					}
				})
			case <-ctx.Done():
				return
			}
		}
	}()
	cu.viewerTick()
}

// closeViewer keeps where the viewer is, for next time, and closes it.
func (cu *culler) closeViewer() {
	pv := &cu.viewer
	if pv.win == nil {
		return
	}
	w := pv.win
	go func() {
		if p, ok := w.Placement(); ok {
			prefs := loadViewerPrefs()
			prefs.Place = &p
			saveViewerPrefs(prefs)
		}
		w.Client().Leave()
	}()
	cu.viewerGone()
}

// viewerGone forgets the viewer.
func (cu *culler) viewerGone() {
	pv := &cu.viewer
	if pv.stop != nil {
		pv.stop()
	}
	pv.win, pv.c, pv.stop, pv.key = nil, gunim.Client{}, nil, ""
}

// viewerTarget is the photo the viewer follows: the one showing in the
// cull view, or the grid's cursor.
func (cu *culler) viewerTarget() int {
	switch {
	case len(cu.photos) == 0:
		return -1
	case cu.culling:
		return cu.at
	case cu.cursor >= 0 && cu.cursor < len(cu.photos):
		return cu.cursor
	}
	return -1
}

// viewerTick shows the viewer the photo in hand, as it changes or its
// edit does: what is cached at once, then its large rendition.
func (cu *culler) viewerTick() {
	pv := &cu.viewer
	if pv.win == nil {
		return
	}
	i := cu.viewerTarget()
	if i < 0 {
		return
	}
	p := cu.photos[i]
	key := p.CacheKey + "|" + p.EditHash
	if key == pv.key {
		return
	}
	pv.key = key
	pv.st.ID, pv.st.Name = p.ID, p.FileName
	if e, ok := cu.cache.get(p.ID); ok && e.img != nil {
		pv.st.Img = e.img
	} else if t := cu.thumbs[p.ID]; t != nil {
		pv.st.Img = t
	}
	_ = pv.c.Update("viewer", pv.st)
	c := pv.c
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, time.Minute)
		defer cancel()
		g, err := cu.im.get(ctx, p, want{level: "2048", stale: true})
		if err != nil {
			return
		}
		cu.onDo(func() {
			if pv.c == c && pv.key == key {
				pv.st.Img = g.img
				_ = pv.c.Update("viewer", pv.st)
			}
		})
	}()
}
