package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/store"
)

// Theme is the client colour-scheme preference.
type Theme string

const (
	ThemeDark   Theme = "dark"
	ThemeLight  Theme = "light"
	ThemeSystem Theme = "system"
)

func ThemeValues() []Theme { return []Theme{ThemeDark, ThemeLight, ThemeSystem} }

// AutoPreset is a creative auto preset: named auto sections plus style
// offsets layered on top. The client sanitizes sections/offset keys on read,
// so unknown values from older/newer clients survive as stored.
type AutoPreset struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Sections []string           `json:"sections"`
	Offsets  map[string]float64 `json:"offsets"`
}

// ExportOptions is the last-used state of the export dialog, persisted as
// one blob when an export starts (the dialog is "sticky", Lightroom-style).
type ExportOptions struct {
	Format      ExportFormat `json:"format"`
	JpegQuality int          `json:"jpegQuality"`
	// ResizeMode is "full" or "edge"; EdgePx is remembered even when full.
	ResizeMode    string        `json:"resizeMode"`
	EdgePx        int           `json:"edgePx"`
	ColorSpace    ColorSpace    `json:"colorSpace"`
	SharpenTarget SharpenTarget `json:"sharpenTarget"`
	SharpenAmount SharpenAmount `json:"sharpenAmount"`
}

// normalizeExportOptions maps missing or invalid fields (older/partial blobs
// unmarshal as zero values) to the dialog defaults, on both read and write.
func normalizeExportOptions(o ExportOptions) ExportOptions {
	if !enumValid(o.Format, ExportFormatValues()) {
		o.Format = ExportJPEG
	}
	if o.JpegQuality < 1 || o.JpegQuality > 100 {
		o.JpegQuality = 90
	}
	if o.ResizeMode != "edge" {
		o.ResizeMode = "full"
	}
	if o.EdgePx < 16 || o.EdgePx > 65536 {
		o.EdgePx = 2160
	}
	if !enumValid(o.ColorSpace, ColorSpaceValues()) {
		o.ColorSpace = ColorSpaceSRGB
	}
	if !enumValid(o.SharpenTarget, SharpenTargetValues()) {
		o.SharpenTarget = SharpenTargetOff
	}
	if !enumValid(o.SharpenAmount, SharpenAmountValues()) {
		o.SharpenAmount = SharpenAmountStandard
	}
	return o
}

func enumValid[T comparable](v T, values []T) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}

// UISettings is every persisted client preference. One subscription key
// serves them all: any setter re-pushes the full snapshot to every connected
// window, which is what keeps multiple windows in sync.
type UISettings struct {
	Theme Theme `json:"theme"`
	// GapMinutes is the cull time-gap grouping threshold; 0 = off.
	GapMinutes  int          `json:"gapMinutes"`
	CullDials   []string     `json:"cullDials"`
	QuickDials  []string     `json:"quickDials"`
	AutoPresets []AutoPreset `json:"autoPresets"`
	ExportDir   string       `json:"exportDir"`
	// ExportOptions is the export dialog's last-used state.
	ExportOptions ExportOptions `json:"exportOptions"`
	// DevelopPinned keeps the develop drawer expanded.
	DevelopPinned bool `json:"developPinned"`
	// EditGroups: edit-panel group id -> open. Absent means open.
	EditGroups map[string]bool `json:"editGroups"`
	// GroupAliases: lowercased library-group parent path -> display alias.
	GroupAliases map[string]string `json:"groupAliases"`
	// RailGroups: lowercased library-group parent path -> open. Absent = open.
	RailGroups map[string]bool `json:"railGroups"`
	// RailWidth is the library rail width in px (drag its right edge).
	RailWidth int `json:"railWidth"`
	// PrerenderFullres pre-renders 1:1 full-resolution tiles for a folder
	// after the calibrate and pre-render passes. Off by default — full-res
	// tiles are large, so it can push the preview cache past its cap.
	PrerenderFullres bool `json:"prerenderFullres"`
}

// Settings serves the persisted client preferences. Everything lives in the
// generic settings table, one row per setting — no schema migration needed
// for new preferences, and granular setters mean windows changing different
// settings never clobber each other.
type Settings struct {
	deps *Deps
	// mu serializes the read-modify-write of the map-valued rows.
	mu sync.Mutex
}

const (
	uiSettingsKey = "uiSettings"

	settingUITheme         = "ui:theme"
	settingUIGapMinutes    = "ui:gapMinutes"
	settingUICullDials     = "ui:cullDials"
	settingUIQuickDials    = "ui:quickDials"
	settingUIAutoPresets   = "ui:autoPresets"
	settingUIExportDir     = "ui:exportDir"
	settingUIExportOptions = "ui:exportOptions"
	settingUIDevelopPinned = "ui:developPinned"
	settingUIEditGroups    = "ui:editGroups"
	settingUIGroupAliases  = "ui:groupAliases"
	settingUIRailGroups    = "ui:railGroups"
	settingUIRailWidth     = "ui:railWidth"
	settingUIPrerenderFull = "ui:prerenderFullres"
)

// Library rail width bounds; the default matches the design handoff.
const (
	railWidthMin     = 180
	railWidthDefault = 214
	railWidthMax     = 440
)

// GetUISettings returns all persisted client preferences with defaults
// applied. Subscription query: every setter below pushes an update.
func (u *Settings) GetUISettings(ctx context.Context) (*UISettings, error) {
	aprot.RegisterRefreshTrigger(ctx, uiSettingsKey)
	db := u.deps.DB

	theme := ThemeDark
	if raw, _ := db.GetSetting(ctx, settingUITheme); raw != "" {
		for _, t := range ThemeValues() {
			if raw == string(t) {
				theme = t
			}
		}
	}

	gap := 6
	if raw, _ := db.GetSetting(ctx, settingUIGapMinutes); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			gap = n
		}
	}

	exportDir, _ := db.GetSetting(ctx, settingUIExportDir)

	railWidth := railWidthDefault
	if raw, _ := db.GetSetting(ctx, settingUIRailWidth); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= railWidthMin && n <= railWidthMax {
			railWidth = n
		}
	}

	// Off unless explicitly enabled.
	prerenderFullRaw, _ := db.GetSetting(ctx, settingUIPrerenderFull)

	// Pinned by default; only an explicit "false" unpins (SidecarWrites style).
	pinnedRaw, _ := db.GetSetting(ctx, settingUIDevelopPinned)
	pinned := pinnedRaw != "false"

	return &UISettings{
		Theme:            theme,
		GapMinutes:       gap,
		CullDials:        jsonSetting(ctx, db, settingUICullDials, []string{}),
		QuickDials:       jsonSetting(ctx, db, settingUIQuickDials, []string{}),
		AutoPresets:      jsonSetting(ctx, db, settingUIAutoPresets, []AutoPreset{}),
		ExportDir:        exportDir,
		ExportOptions:    normalizeExportOptions(jsonSetting(ctx, db, settingUIExportOptions, ExportOptions{})),
		DevelopPinned:    pinned,
		EditGroups:       jsonSetting(ctx, db, settingUIEditGroups, map[string]bool{}),
		GroupAliases:     jsonSetting(ctx, db, settingUIGroupAliases, map[string]string{}),
		RailGroups:       jsonSetting(ctx, db, settingUIRailGroups, map[string]bool{}),
		RailWidth:        railWidth,
		PrerenderFullres: prerenderFullRaw == "true",
	}, nil
}

// SetTheme persists the colour scheme.
func (u *Settings) SetTheme(ctx context.Context, theme Theme) error {
	valid := false
	for _, t := range ThemeValues() {
		if theme == t {
			valid = true
		}
	}
	if !valid {
		return aprot.ErrInvalidParams(fmt.Sprintf("unknown theme %q", theme))
	}
	return u.save(ctx, settingUITheme, string(theme))
}

// SetGapMinutes persists the cull time-gap threshold (0 = off).
func (u *Settings) SetGapMinutes(ctx context.Context, minutes int) error {
	if minutes < 0 || minutes > 1440 {
		return aprot.ErrInvalidParams("gap minutes must be 0..1440")
	}
	return u.save(ctx, settingUIGapMinutes, strconv.Itoa(minutes))
}

// SetCullDials replaces the Cull toolbar dial selection.
func (u *Settings) SetCullDials(ctx context.Context, dials []string) error {
	return u.saveJSON(ctx, settingUICullDials, emptyIfNil(dials))
}

// SetQuickDials replaces the Develop quick-dock dial selection.
func (u *Settings) SetQuickDials(ctx context.Context, dials []string) error {
	return u.saveJSON(ctx, settingUIQuickDials, emptyIfNil(dials))
}

// SetAutoPresets replaces the creative auto presets (add, remove, and edit
// are all "send the new list").
func (u *Settings) SetAutoPresets(ctx context.Context, presets []AutoPreset) error {
	if presets == nil {
		presets = []AutoPreset{}
	}
	return u.saveJSON(ctx, settingUIAutoPresets, presets)
}

// SetExportDir persists the last export destination.
func (u *Settings) SetExportDir(ctx context.Context, dir string) error {
	return u.save(ctx, settingUIExportDir, dir)
}

// SetExportOptions persists the export dialog's last-used state.
func (u *Settings) SetExportOptions(ctx context.Context, opts ExportOptions) error {
	return u.saveJSON(ctx, settingUIExportOptions, normalizeExportOptions(opts))
}

// SetDevelopPinned persists the develop-drawer pin.
func (u *Settings) SetDevelopPinned(ctx context.Context, pinned bool) error {
	v := "true"
	if !pinned {
		v = "false"
	}
	return u.save(ctx, settingUIDevelopPinned, v)
}

// SetEditGroupOpen persists one edit-panel group's collapse state. Open is
// the default, so opening deletes the entry and the row stays small.
func (u *Settings) SetEditGroupOpen(ctx context.Context, id string, open bool) error {
	return updateMapSetting(ctx, u, settingUIEditGroups, id, !open, func(m map[string]bool) {
		m[id] = false
	})
}

// SetGroupAlias persists a library-group display alias; empty alias clears it.
func (u *Settings) SetGroupAlias(ctx context.Context, parentPath, alias string) error {
	return updateMapSetting(ctx, u, settingUIGroupAliases, parentPath, alias != "", func(m map[string]string) {
		m[parentPath] = alias
	})
}

// SetRailGroupOpen persists a library-rail group's collapse state (absent =
// open, same convention as edit groups).
func (u *Settings) SetRailGroupOpen(ctx context.Context, parentPath string, open bool) error {
	return updateMapSetting(ctx, u, settingUIRailGroups, parentPath, !open, func(m map[string]bool) {
		m[parentPath] = false
	})
}

// SetRailWidth persists the library rail width in pixels.
func (u *Settings) SetRailWidth(ctx context.Context, px int) error {
	if px < railWidthMin || px > railWidthMax {
		return aprot.ErrInvalidParams(fmt.Sprintf("rail width must be %d..%d", railWidthMin, railWidthMax))
	}
	return u.save(ctx, settingUIRailWidth, strconv.Itoa(px))
}

// SetPrerenderFullres persists whether opened folders auto-render 1:1
// full-resolution tiles after the pre-render pass.
func (u *Settings) SetPrerenderFullres(ctx context.Context, enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return u.save(ctx, settingUIPrerenderFull, v)
}

// save writes one row and pushes the fresh snapshot to every window.
func (u *Settings) save(ctx context.Context, key, value string) error {
	if err := u.deps.DB.SetSetting(ctx, key, value); err != nil {
		return err
	}
	aprot.TriggerRefresh(ctx, uiSettingsKey)
	return nil
}

func (u *Settings) saveJSON(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return u.save(ctx, key, string(raw))
}

// updateMapSetting does the locked read-modify-write for a map-valued row:
// set applies the mutation when keep is true, otherwise the entry is removed.
func updateMapSetting[V any](ctx context.Context, u *Settings, rowKey, entryKey string, keep bool, set func(map[string]V)) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	m := jsonSetting(ctx, u.deps.DB, rowKey, map[string]V{})
	if keep {
		set(m)
	} else {
		delete(m, entryKey)
	}
	return u.saveJSON(ctx, rowKey, m)
}

// jsonSetting reads a JSON-encoded row, falling back to empty on any problem
// (settings must never fail a read).
func jsonSetting[T any](ctx context.Context, db *store.DB, key string, empty T) T {
	raw, err := db.GetSetting(ctx, key)
	if err != nil || raw == "" {
		return empty
	}
	var v T
	if json.Unmarshal([]byte(raw), &v) != nil {
		return empty
	}
	return v
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
