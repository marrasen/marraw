package main

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The settings dialog, as marraw's: what the app does, kept by the
// backend.
type (
	// AskSettings opens the settings dialog, at Section if given.
	AskSettings struct{ Section string }
	// SettingsDone closes it.
	SettingsDone struct{}
	// SettingSet sets the setting Key: a switch to On, a number to N, or a
	// choice to Value.
	SettingSet struct {
		Key   string
		On    bool
		N     int
		Value string
	}
	// CacheClear clears the cache of previews, and CacheCap sets its
	// limit in GB.
	CacheClear struct{}
	CacheCap   struct{ GB int }
	// CacheDirAsk asks for the cache's folder, and CacheDir moves it to
	// Path, "" for the default.
	CacheDirAsk struct{}
	CacheDir    struct{ Path string }
	// ModelDelete deletes the downloaded model File.
	ModelDelete struct{ File string }
	// DefaultPreset makes the preset ID, "" none, the default look of new
	// photos from the camera Key, "*" any.
	DefaultPreset struct{ Key, ID string }
	// LinkRevoke withdraws the shared link ID.
	LinkRevoke struct{ ID string }
)

// SettingsState is what the settings dialog shows.
type SettingsState struct {
	Section string
	// ThumbFit is how the grid frames its pictures: "crop" or "fit".
	ThumbFit string
	// Features are the culling aids on or off, by their ids, and the
	// bursts' sensitivity and time window.
	Features                map[string]bool
	BurstHamming, BurstGap  int
	Prerender               bool
	Sidecars, SidecarsKnown bool
	Cache                   *marrawclient.CacheInfo
	Models                  *marrawclient.ModelsInfo
	Cameras                 []marrawclient.CameraInfo
	Defaults                map[string]string
	Presets                 []PresetChoice
	Links                   []marrawclient.ShareLink
	LinksKnown              bool
	Busy                    string
}

// PresetChoice is a preset that can be a camera's default: its id and
// name.
type PresetChoice struct{ ID, Name string }

// settingsSections are the dialog's sections, in order.
var settingsSections = []string{"General", "Features", "Default presets", "Cache", "Models", "Sidecars", "Shared albums"}

// features are the culling aids that can be turned off, as marraw's, with
// whether each is on by default.
var features = []struct {
	id, title, help string
	on              bool
}{
	{"bursts", "Burst grouping", "Group near-duplicate frames into bursts: the Bursts and Judge buttons, burst badges, and the best-of-burst keys.", true},
	{"softFilter", "Soft-focus filter", "Flag soft frames: the Soft filter button and the softness badge.", true},
	{"eyes", "Closed-eye detection", "Scan portraits for closed eyes: the eyes scan, the Blinks filter button, and blink badges.", true},
	{"subjects", "Subject-aware focus", "Re-score sharpness on the detected subject instead of the whole frame. Scores already computed keep informing burst ranking.", true},
}

// feature reports whether the culling aid id is on.
func (cu *culler) feature(id string) bool {
	if cu.ui != nil {
		if on, ok := cu.ui.Features[id]; ok {
			return on
		}
	}
	for _, f := range features {
		if f.id == id {
			return f.on
		}
	}
	return true
}

// featuresOff are the culling aids turned off, by their ids.
func (cu *culler) featuresOff() map[string]bool {
	off := map[string]bool{}
	for _, f := range features {
		if !cu.feature(f.id) {
			off[f.id] = true
		}
	}
	return off
}

// settingsState is what the settings dialog shows now.
func (cu *culler) settingsState() SettingsState {
	s := cu.settings
	s.Features = map[string]bool{}
	for _, f := range features {
		s.Features[f.id] = cu.feature(f.id)
	}
	s.ThumbFit, s.BurstHamming, s.BurstGap = "fit", 18, 4
	if ui := cu.ui; ui != nil {
		if ui.ThumbFit != "" {
			s.ThumbFit = string(ui.ThumbFit)
		}
		if ui.BurstHamming > 0 {
			s.BurstHamming = ui.BurstHamming
		}
		if ui.BurstGapSeconds > 0 {
			s.BurstGap = ui.BurstGapSeconds
		}
		s.Prerender = ui.PrerenderFullres
		s.Defaults = maps.Clone(ui.DefaultPresets)
		for _, p := range ui.UserPresets {
			if len(p.AutoSections) == 0 {
				s.Presets = append(s.Presets, PresetChoice{ID: p.ID, Name: p.Name})
			}
		}
	}
	return s
}

// askSettings opens the settings dialog, and fetches what it shows.
func (cu *culler) askSettings(section string) {
	if section == "" {
		section = "General"
	}
	cu.settings = SettingsState{Section: section}
	cu.settingsOpen = true
	_ = cu.c.Mount(gunim.Root, "settings", "settings", cu.settingsState())
	cu.fetchSettings()
}

// fetchSettings fetches what the settings dialog shows from the backend,
// each part as it comes.
func (cu *culler) fetchSettings() {
	get := func(fn func(ctx context.Context) func()) {
		go func() {
			ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
			defer cancel()
			apply := fn(ctx)
			select {
			case cu.do <- func() {
				apply()
				cu.settingsChanged()
			}:
			case <-cu.ctx.Done():
			}
		}()
	}
	get(func(ctx context.Context) func() {
		info, err := cu.api.System.GetCacheInfo(ctx)
		return func() {
			if err == nil {
				cu.settings.Cache = info
			}
		}
	})
	get(func(ctx context.Context) func() {
		m, err := cu.api.System.GetModelsInfo(ctx)
		return func() {
			if err == nil {
				cu.settings.Models = m
			}
		}
	})
	get(func(ctx context.Context) func() {
		a, err := cu.api.Library.GetAppSettings(ctx)
		return func() {
			if err == nil && a != nil {
				cu.settings.Sidecars, cu.settings.SidecarsKnown = a.SidecarWrites, true
			}
		}
	})
	get(func(ctx context.Context) func() {
		cams, err := cu.api.Library.ListCameras(ctx)
		return func() {
			if err == nil {
				cu.settings.Cameras = cams
			}
		}
	})
	get(func(ctx context.Context) func() {
		links, err := cu.api.Share.ListLinks(ctx)
		return func() {
			if err == nil {
				cu.settings.Links, cu.settings.LinksKnown = links, true
			}
		}
	})
}

// settingsChanged shows the settings anew, while the dialog is open.
func (cu *culler) settingsChanged() {
	if cu.settingsOpen {
		_ = cu.c.Update("settings", cu.settingsState())
	}
}

// settingsDone closes the settings dialog.
func (cu *culler) settingsDone() {
	cu.settingsOpen = false
	_ = cu.c.Unmount("settings")
	cu.refocus()
}

// call runs fn against the backend, telling a failure as what failed;
// then, back on the culler, after.
func (cu *culler) call(what string, fn func(ctx context.Context) error, after func()) {
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 2*time.Minute)
		defer cancel()
		err := fn(ctx)
		select {
		case cu.do <- func() {
			if err != nil {
				cu.fail(what, err)
			}
			if after != nil {
				after()
			}
			cu.settingsChanged()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// settingSet takes a setting changed in the dialog: the copy here first,
// so the app follows at once, then the backend's.
func (cu *culler) settingSet(in SettingSet) {
	if cu.ui == nil {
		cu.ui = &marrawclient.UISettings{}
	}
	ui := cu.ui
	switch in.Key {
	case "thumbFit":
		ui.ThumbFit = marrawclient.ThumbFit(in.Value)
		cu.call("The thumbnails could not be set", func(ctx context.Context) error {
			return cu.api.Settings.SetThumbFit(ctx, ui.ThumbFit)
		}, nil)
		_ = cu.c.Update("grid", cu.gridState())
	case "burstHamming":
		ui.BurstHamming = in.N
		cu.call("The burst grouping could not be set", func(ctx context.Context) error {
			return cu.api.Settings.SetBurstHamming(ctx, in.N)
		}, nil)
	case "burstGap":
		ui.BurstGapSeconds = in.N
		cu.call("The burst time window could not be set", func(ctx context.Context) error {
			return cu.api.Settings.SetBurstGapSeconds(ctx, in.N)
		}, nil)
	case "prerender":
		ui.PrerenderFullres = in.On
		cu.call("Pre-rendering could not be set", func(ctx context.Context) error {
			return cu.api.Settings.SetPrerenderFullres(ctx, in.On)
		}, nil)
	case "sidecars":
		cu.settings.Sidecars = in.On
		cu.call("Sidecar writing could not be set", func(ctx context.Context) error {
			return cu.api.Library.SetSidecarWrites(ctx, in.On)
		}, nil)
	default:
		if !slices.ContainsFunc(features, func(f struct {
			id, title, help string
			on              bool
		}) bool {
			return f.id == in.Key
		}) {
			return
		}
		if ui.Features == nil {
			ui.Features = map[string]bool{}
		}
		ui.Features[in.Key] = in.On
		cu.featureChanged(in.Key, in.On)
		cu.call("The feature could not be set", func(ctx context.Context) error {
			return cu.api.Settings.SetFeature(ctx, in.Key, in.On)
		}, nil)
	}
	cu.settingsChanged()
}

// featureChanged follows a culling aid turned on or off: its filter let
// go with it, and the grid and the photo showing what it has.
func (cu *culler) featureChanged(id string, on bool) {
	if !on {
		v := cu.libView
		switch id {
		case "bursts":
			v.Collapse = false
		case "softFilter":
			v.Soft = false
		case "eyes":
			v.Blinks = false
		}
		if v != cu.libView {
			cu.setView(v)
		}
	}
	cu.aidsChanged()
}

// cacheClear clears the cache of previews.
func (cu *culler) cacheClear() {
	cu.settings.Busy = "cache"
	cu.settingsChanged()
	cu.call("The cache could not be cleared", func(ctx context.Context) error {
		info, err := cu.api.System.ClearCache(ctx)
		if err == nil {
			cu.do <- func() { cu.settings.Cache = info }
		}
		return err
	}, func() {
		cu.settings.Busy = ""
		cu.notify("Cache cleared")
	})
}

// cacheCap sets the cache's limit.
func (cu *culler) cacheCap(gb int) {
	if gb < 1 || gb > 2048 {
		return
	}
	cu.call("The cache limit could not be set", func(ctx context.Context) error {
		info, err := cu.api.System.SetCacheCap(ctx, gb)
		if err == nil {
			cu.do <- func() { cu.settings.Cache = info }
		}
		return err
	}, nil)
}

// cacheDirAsk asks for a folder for the cache, on this computer.
func (cu *culler) cacheDirAsk() {
	go func() {
		paths, err := cu.c.ChooseFiles(cu.ctx, chooseFolder("Choose the cache's folder"))
		if err != nil || len(paths) == 0 {
			return
		}
		select {
		case cu.do <- func() { cu.cacheDir(paths[0]) }:
		case <-cu.ctx.Done():
		}
	}()
}

// cacheDir moves the cache to path, "" its default; the backend clears
// the old one.
func (cu *culler) cacheDir(path string) {
	cu.settings.Busy = "cache"
	cu.settingsChanged()
	cu.call("The cache could not be moved", func(ctx context.Context) error {
		info, err := cu.api.System.SetCacheDir(ctx, path)
		if err == nil {
			cu.do <- func() { cu.settings.Cache = info }
		}
		return err
	}, func() { cu.settings.Busy = "" })
}

// modelDelete deletes a downloaded model.
func (cu *culler) modelDelete(file string) {
	cu.call("The model could not be deleted", func(ctx context.Context) error {
		m, err := cu.api.System.DeleteModel(ctx, file)
		if err == nil {
			cu.do <- func() { cu.settings.Models = m }
		}
		return err
	}, func() { cu.notify("Model deleted") })
}

// defaultPreset sets a camera's default look.
func (cu *culler) defaultPreset(in DefaultPreset) {
	if cu.ui == nil {
		return
	}
	m := maps.Clone(cu.ui.DefaultPresets)
	if m == nil {
		m = map[string]string{}
	}
	if in.ID == "" {
		delete(m, in.Key)
	} else {
		m[in.Key] = in.ID
	}
	cu.ui.DefaultPresets = m
	cu.call("The default preset could not be set", func(ctx context.Context) error {
		return cu.api.Settings.SetDefaultPresets(ctx, m)
	}, nil)
	cu.settingsChanged()
}

// linkRevoke withdraws a shared link.
func (cu *culler) linkRevoke(id string) {
	name := ""
	for _, l := range cu.settings.Links {
		if l.ID == id {
			name = l.Name
		}
	}
	cu.call("The link could not be withdrawn", func(ctx context.Context) error {
		if err := cu.api.Share.RevokeLink(ctx, id); err != nil {
			return err
		}
		links, err := cu.api.Share.ListLinks(ctx)
		if err == nil {
			cu.do <- func() { cu.settings.Links = links }
		}
		return nil
	}, func() { cu.notify("“" + name + "” is no longer shared") })
}

// chooseFolder is the system's dialog asking for a folder, titled title.
func chooseFolder(title string) driver.ChooseOptions {
	return driver.ChooseOptions{Title: title, Folders: true}
}
