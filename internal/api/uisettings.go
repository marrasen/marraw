package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/edit"
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

// ThumbFit is how photo thumbnails are framed in the grids. crop fills a
// uniform 3:2 cell (center-crop); fit shows the whole frame in a uniform
// square cell (letterbox); natural sizes each frame to its own aspect ratio
// in justified rows.
type ThumbFit string

const (
	ThumbFitCrop    ThumbFit = "crop"
	ThumbFitFit     ThumbFit = "fit"
	ThumbFitNatural ThumbFit = "natural"
)

func ThumbFitValues() []ThumbFit { return []ThumbFit{ThumbFitCrop, ThumbFitFit, ThumbFitNatural} }

// LibrarySort is the photo ordering in the library, cull, and develop views.
// capture* is capture-time order (untimed frames last, file name breaking
// ties — the order ListPhotos serves); name* is file-name order alone.
type LibrarySort string

const (
	LibrarySortCaptureAsc  LibrarySort = "captureAsc"
	LibrarySortCaptureDesc LibrarySort = "captureDesc"
	LibrarySortNameAsc     LibrarySort = "nameAsc"
	LibrarySortNameDesc    LibrarySort = "nameDesc"
)

func LibrarySortValues() []LibrarySort {
	return []LibrarySort{LibrarySortCaptureAsc, LibrarySortCaptureDesc, LibrarySortNameAsc, LibrarySortNameDesc}
}

// ShootSort is the folder ordering in the library rail (LibrarySort is the
// photos within a folder). date* keys on Shoot.earliestTakenAt, undated
// folders last either way.
type ShootSort string

const (
	ShootSortNameAsc  ShootSort = "nameAsc"
	ShootSortNameDesc ShootSort = "nameDesc"
	ShootSortDateAsc  ShootSort = "dateAsc"
	ShootSortDateDesc ShootSort = "dateDesc"
)

func ShootSortValues() []ShootSort {
	return []ShootSort{ShootSortNameAsc, ShootSortNameDesc, ShootSortDateAsc, ShootSortDateDesc}
}

// ShootGroup is the rail's time bucketing of folders under collapsible
// year / month / day headers, keyed on Shoot.earliestTakenAt.
type ShootGroup string

const (
	ShootGroupNone  ShootGroup = "none"
	ShootGroupYear  ShootGroup = "year"
	ShootGroupMonth ShootGroup = "month"
	ShootGroupDay   ShootGroup = "day"
)

func ShootGroupValues() []ShootGroup {
	return []ShootGroup{ShootGroupNone, ShootGroupYear, ShootGroupMonth, ShootGroupDay}
}

// AutoPreset is a creative auto preset: named auto sections plus style
// offsets layered on top. The client sanitizes sections/offset keys on read,
// so unknown values from older/newer clients survive as stored.
type AutoPreset struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Sections []string           `json:"sections"`
	Offsets  map[string]float64 `json:"offsets"`
}

// UserPreset is one saved develop look: an absolute Params snapshot taken
// from the current draft (geometry stripped by the client — a preset is a
// look, not a crop). Unmarshalling through edit.Params means presets stored
// by older builds gain new fields as neutral zeros on read.
type UserPreset struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Params edit.Params `json:"params"`
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
	// FileNameTemplate names the output files; empty = "{name}".
	FileNameTemplate string `json:"fileNameTemplate"`
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
	o.FileNameTemplate = strings.TrimSpace(o.FileNameTemplate)
	if len(o.FileNameTemplate) > 120 {
		o.FileNameTemplate = o.FileNameTemplate[:120]
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
	// UserPresets are saved develop looks (Presets tab → Save current look).
	UserPresets []UserPreset `json:"userPresets"`
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
	// ThumbFit is how thumbnails are framed in the grids (default fit).
	ThumbFit ThumbFit `json:"thumbFit"`
	// LibrarySort is the photo ordering in the grids and filmstrips
	// (default captureAsc).
	LibrarySort LibrarySort `json:"librarySort"`
	// ShootSort is the folder ordering in the library rail (default nameAsc,
	// the order ListShoots serves).
	ShootSort ShootSort `json:"shootSort"`
	// ShootGroup is the rail's time bucketing of folders (default none).
	ShootGroup ShootGroup `json:"shootGroup"`
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
	settingUIUserPresets   = "ui:userPresets"
	settingUIExportDir     = "ui:exportDir"
	settingUIExportOptions = "ui:exportOptions"
	settingUIDevelopPinned = "ui:developPinned"
	settingUIEditGroups    = "ui:editGroups"
	settingUIGroupAliases  = "ui:groupAliases"
	settingUIRailGroups    = "ui:railGroups"
	settingUIRailWidth     = "ui:railWidth"
	settingUIPrerenderFull = "ui:prerenderFullres"
	settingUIThumbFit      = "ui:thumbFit"
	settingUILibrarySort   = "ui:librarySort"
	settingUIShootSort     = "ui:shootSort"
	settingUIShootGroup    = "ui:shootGroup"
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

	// Whole-frame fit unless an explicit, recognized value is stored.
	thumbFit := ThumbFitFit
	if raw, _ := db.GetSetting(ctx, settingUIThumbFit); raw != "" {
		if v := ThumbFit(raw); enumValid(v, ThumbFitValues()) {
			thumbFit = v
		}
	}

	// Capture order unless an explicit, recognized value is stored.
	librarySort := LibrarySortCaptureAsc
	if raw, _ := db.GetSetting(ctx, settingUILibrarySort); raw != "" {
		if v := LibrarySort(raw); enumValid(v, LibrarySortValues()) {
			librarySort = v
		}
	}

	// Name order unless an explicit, recognized value is stored.
	shootSort := ShootSortNameAsc
	if raw, _ := db.GetSetting(ctx, settingUIShootSort); raw != "" {
		if v := ShootSort(raw); enumValid(v, ShootSortValues()) {
			shootSort = v
		}
	}

	// No time grouping unless an explicit, recognized value is stored.
	shootGroup := ShootGroupNone
	if raw, _ := db.GetSetting(ctx, settingUIShootGroup); raw != "" {
		if v := ShootGroup(raw); enumValid(v, ShootGroupValues()) {
			shootGroup = v
		}
	}

	return &UISettings{
		Theme:            theme,
		GapMinutes:       gap,
		CullDials:        jsonSetting(ctx, db, settingUICullDials, []string{}),
		QuickDials:       jsonSetting(ctx, db, settingUIQuickDials, []string{}),
		AutoPresets:      autoPresetsOrDefault(ctx, db),
		UserPresets:      jsonSetting(ctx, db, settingUIUserPresets, []UserPreset{}),
		ExportDir:        exportDir,
		ExportOptions:    normalizeExportOptions(jsonSetting(ctx, db, settingUIExportOptions, ExportOptions{})),
		DevelopPinned:    pinned,
		EditGroups:       jsonSetting(ctx, db, settingUIEditGroups, map[string]bool{}),
		GroupAliases:     jsonSetting(ctx, db, settingUIGroupAliases, map[string]string{}),
		RailGroups:       jsonSetting(ctx, db, settingUIRailGroups, map[string]bool{}),
		RailWidth:        railWidth,
		PrerenderFullres: prerenderFullRaw == "true",
		ThumbFit:         thumbFit,
		LibrarySort:      librarySort,
		ShootSort:        shootSort,
		ShootGroup:       shootGroup,
	}, nil
}

// SetTheme persists the colour scheme.
func (u *Settings) SetTheme(ctx context.Context, theme Theme) error {
	if !enumValid(theme, ThemeValues()) {
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

// SetUserPresets replaces the saved develop looks (add, remove, and rename
// are all "send the new list", like the auto presets).
func (u *Settings) SetUserPresets(ctx context.Context, presets []UserPreset) error {
	if presets == nil {
		presets = []UserPreset{}
	}
	for i := range presets {
		if presets[i].ID == "" || presets[i].Name == "" {
			return aprot.ErrInvalidParams("user presets need an id and a name")
		}
		presets[i].Params.Normalize()
	}
	return u.saveJSON(ctx, settingUIUserPresets, presets)
}

// autoPresetsOrDefault reads the stored creative-auto presets, seeding the
// shipped defaults on a fresh install (the row was never written). A user who
// deletes every preset stores an explicit "[]" — not empty — so the defaults do
// not creep back; "Restore defaults" in Settings is the deliberate way back.
func autoPresetsOrDefault(ctx context.Context, db *store.DB) []AutoPreset {
	raw, err := db.GetSetting(ctx, settingUIAutoPresets)
	if err != nil || raw == "" {
		return defaultAutoPresets()
	}
	var v []AutoPreset
	if json.Unmarshal([]byte(raw), &v) != nil {
		return []AutoPreset{}
	}
	return v
}

// defaultAutoPresets is the six presets marraw ships with — kept in lockstep
// with DEFAULT_PRESETS in client/src/lib/autoPresets.ts (same IDs, sections, and
// native-unit offset values). The client sanitizes on read, so this only needs
// to stay a valid superset. Boldly different from one another: cinematic
// teal/orange, matte faded film, monochrome noir, punchy daylight, dark moody,
// and warm golden hour.
func defaultAutoPresets() []AutoPreset {
	return []AutoPreset{
		{
			ID:       "default-cinematic",
			Name:     "Cinematic",
			Sections: []string{"tone", "wb", "color"},
			Offsets: map[string]float64{
				"contrast": 0.15, "blacks": -0.1, "toneShadows": 0.1, "toneHighlights": -0.08,
				"vibrance": 0.1, "saturation": -0.05,
				"splitShadowHue": 195, "splitShadowAmt": 0.25, "splitHighlightHue": 40, "splitHighlightAmt": 0.22,
				"clarity": 0.08, "vignette": 0.12,
			},
		},
		{
			ID:       "default-faded",
			Name:     "Faded film",
			Sections: []string{"tone", "color"},
			Offsets: map[string]float64{
				"contrast": -0.2, "whites": -0.08, "blacks": 0.15, "toneHighlights": -0.1,
				"vibrance": -0.2, "saturation": -0.25,
				"splitShadowHue": 210, "splitShadowAmt": 0.1, "splitHighlightHue": 50, "splitHighlightAmt": 0.12,
				"texture": -0.12, "clarity": -0.1, "dehaze": -0.08,
			},
		},
		{
			ID:       "default-noir",
			Name:     "Noir B&W",
			Sections: []string{"tone"},
			Offsets: map[string]float64{
				"contrast": 0.3, "whites": 0.12, "blacks": -0.2, "toneShadows": -0.08,
				"saturation": -1, "vibrance": -1,
				"clarity": 0.25, "texture": 0.15, "dehaze": 0.1, "vignette": 0.28,
			},
		},
		{
			ID:       "default-punchy",
			Name:     "Punchy",
			Sections: []string{"tone", "color"},
			Offsets: map[string]float64{
				"contrast": 0.28, "whites": 0.15, "blacks": -0.15,
				"vibrance": 0.35, "saturation": 0.2,
				"clarity": 0.22, "texture": 0.1, "dehaze": 0.15, "vignette": 0.08,
			},
		},
		{
			ID:       "default-moody",
			Name:     "Moody",
			Sections: []string{"tone", "color"},
			Offsets: map[string]float64{
				"expEV": -0.35, "contrast": 0.18, "whites": -0.1, "blacks": -0.22, "toneShadows": 0.1,
				"vibrance": -0.15, "saturation": -0.2,
				"splitShadowHue": 220, "splitShadowAmt": 0.18,
				"dehaze": 0.15, "clarity": 0.1, "vignette": 0.3,
			},
		},
		{
			ID:       "default-golden",
			Name:     "Golden hour",
			Sections: []string{"tone"},
			Offsets: map[string]float64{
				"expEV": 0.1, "contrast": -0.05, "toneShadows": 0.12, "toneHighlights": -0.05,
				"vibrance": 0.2, "saturation": 0.08,
				"splitShadowHue": 35, "splitShadowAmt": 0.12, "splitHighlightHue": 45, "splitHighlightAmt": 0.28,
				"clarity": -0.05, "texture": 0.05, "vignette": 0.1,
			},
		},
	}
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

// SetThumbFit persists how thumbnails are framed in the grids.
func (u *Settings) SetThumbFit(ctx context.Context, fit ThumbFit) error {
	if !enumValid(fit, ThumbFitValues()) {
		return aprot.ErrInvalidParams(fmt.Sprintf("unknown thumbFit %q", fit))
	}
	return u.save(ctx, settingUIThumbFit, string(fit))
}

// SetLibrarySort persists the photo ordering in the grids and filmstrips.
func (u *Settings) SetLibrarySort(ctx context.Context, sort LibrarySort) error {
	if !enumValid(sort, LibrarySortValues()) {
		return aprot.ErrInvalidParams(fmt.Sprintf("unknown librarySort %q", sort))
	}
	return u.save(ctx, settingUILibrarySort, string(sort))
}

// SetShootSort persists the folder ordering in the library rail.
func (u *Settings) SetShootSort(ctx context.Context, sort ShootSort) error {
	if !enumValid(sort, ShootSortValues()) {
		return aprot.ErrInvalidParams(fmt.Sprintf("unknown shootSort %q", sort))
	}
	return u.save(ctx, settingUIShootSort, string(sort))
}

// SetShootGroup persists the rail's time bucketing of folders.
func (u *Settings) SetShootGroup(ctx context.Context, group ShootGroup) error {
	if !enumValid(group, ShootGroupValues()) {
		return aprot.ErrInvalidParams(fmt.Sprintf("unknown shootGroup %q", group))
	}
	return u.save(ctx, settingUIShootGroup, string(group))
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
