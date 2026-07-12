package api

// Flag is the cull state of a photo.
type Flag string

const (
	FlagNone    Flag = "none"
	FlagPick    Flag = "pick"
	FlagExclude Flag = "exclude"
)

func FlagValues() []Flag { return []Flag{FlagNone, FlagPick, FlagExclude} }

// FlagToInt maps a Flag to its photos.flag column value.
func FlagToInt(f Flag) int {
	switch f {
	case FlagPick:
		return 1
	case FlagExclude:
		return -1
	default:
		return 0
	}
}

func FlagFromInt(i int) Flag {
	switch {
	case i > 0:
		return FlagPick
	case i < 0:
		return FlagExclude
	default:
		return FlagNone
	}
}

// ExportFormat selects the export encoder. All render the same pixels; TIFF
// and PNG are the lossless options (8-bit RGB), JPEG the compact one.
type ExportFormat string

const (
	ExportJPEG  ExportFormat = "jpeg"
	ExportTIFF8 ExportFormat = "tiff8"
	ExportPNG   ExportFormat = "png"
)

func ExportFormatValues() []ExportFormat { return []ExportFormat{ExportJPEG, ExportTIFF8, ExportPNG} }

// ColorSpace selects the export output primaries.
type ColorSpace string

const (
	ColorSpaceSRGB     ColorSpace = "srgb"
	ColorSpaceAdobeRGB ColorSpace = "adobergb"
	ColorSpaceProPhoto ColorSpace = "prophoto"
)

func ColorSpaceValues() []ColorSpace {
	return []ColorSpace{ColorSpaceSRGB, ColorSpaceAdobeRGB, ColorSpaceProPhoto}
}

// SharpenTarget selects the output-sharpening medium for JPEG exports,
// applied after the final resize.
type SharpenTarget string

const (
	SharpenTargetOff    SharpenTarget = "off"
	SharpenTargetScreen SharpenTarget = "screen"
	SharpenTargetMatte  SharpenTarget = "matte"
	SharpenTargetGlossy SharpenTarget = "glossy"
)

func SharpenTargetValues() []SharpenTarget {
	return []SharpenTarget{SharpenTargetOff, SharpenTargetScreen, SharpenTargetMatte, SharpenTargetGlossy}
}

// SharpenAmount scales the output-sharpening strength.
type SharpenAmount string

const (
	SharpenAmountLow      SharpenAmount = "low"
	SharpenAmountStandard SharpenAmount = "standard"
	SharpenAmountHigh     SharpenAmount = "high"
)

func SharpenAmountValues() []SharpenAmount {
	return []SharpenAmount{SharpenAmountLow, SharpenAmountStandard, SharpenAmountHigh}
}

// Photo is the client-facing photo record.
type Photo struct {
	ID         int64  `json:"id"`
	FileName   string `json:"fileName"`
	CacheKey   string `json:"cacheKey"`
	EditHash   string `json:"editHash"`
	Rating     int    `json:"rating"`
	Flag       Flag   `json:"flag"`
	MetaLoaded bool   `json:"metaLoaded"`
	FileSize   int64  `json:"fileSize"` // bytes on disk, 0 = unknown
	// BaseExpEV is the measured camera-mimic exposure compensation that seeds
	// the exposure dial (see Edits.seededParams). It is the slider's neutral:
	// the exposure "reset" returns here, not to 0. 0 = unmeasured / no lift.
	BaseExpEV   float64 `json:"baseExpEV"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Orientation int     `json:"orientation"`
	ISO         float64 `json:"iso"`
	Shutter     float64 `json:"shutter"`
	Aperture    float64 `json:"aperture"`
	FocalLen    float64 `json:"focalLen"`
	TakenAt     int64   `json:"takenAt"` // unix seconds, 0 = unknown
	Make        string  `json:"make"`
	Model       string  `json:"model"`
}

type DriveInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type DirEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	HasSubdirs bool   `json:"hasSubdirs"`
}

// FolderPrefs is the user's persistent folder-panel state: pinned favourite
// folders plus the most recently opened ones (newest first).
type FolderPrefs struct {
	Favorites []string `json:"favorites"`
	Recents   []string `json:"recents"`
}

// LibraryRoot is one shoot folder added to the curated library. Roots are
// grouped in the rail by their parent directory; order in the stored list is
// the display order. Alias is a display-only rename — the folder on disk
// keeps its name.
type LibraryRoot struct {
	Path              string `json:"path"`
	Alias             string `json:"alias"`
	IncludeSubfolders bool   `json:"includeSubfolders"`
	// PhotoCount is the last known RAW count, refreshed by the client after
	// each open/rescan — display only, never authoritative.
	PhotoCount int `json:"photoCount"`
	// IsParent marks a managed "library folder": its child shoots are
	// discovered from disk by ListShoots and deliberately never stored here.
	// SetLibraryRoots is a whole-list replace driven by the client, so a
	// synthesized child returned from GetLibraryRoots would be written straight
	// back as a real root on the next reorder — putting children on a separate
	// RPC makes that impossible rather than merely unlikely.
	IsParent bool `json:"isParent"`
	// ExcludedChildren are discovered children the user hid, as lowercased
	// absolute paths. Only meaningful on a parent.
	ExcludedChildren []string `json:"excludedChildren,omitempty"`
}

// RootStatus is the live reachability of one stored root. It is served apart
// from LibraryRoot on purpose: that struct round-trips through SetLibraryRoots,
// so a derived field would be written back into the stored config.
type RootStatus struct {
	Path string `json:"path"`
	// Online is false when the folder's storage is disconnected — an external
	// drive that is unplugged, or a network share that is unreachable.
	Online bool `json:"online"`
}

// Shoot is one folder discovered beneath a managed parent, or the parent's own
// row for RAWs sitting loose in it. Shoots are re-derived from disk on every
// listing and never persisted.
type Shoot struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// PhotoCount is exact once the folder has been scanned; before that it is
	// the direct RAW count, which undercounts a shoot with nested subfolders.
	// Display only, like LibraryRoot.PhotoCount.
	PhotoCount int `json:"photoCount"`
	// IsSelf marks the parent's own row.
	IsSelf bool `json:"isSelf"`
	// EarliestTakenAt is the earliest capture time (unix seconds) among the
	// folder's catalogued photos, 0 until the metadata pass has read one.
	// Display only — feeds the rail's date sort and time grouping.
	EarliestTakenAt int64 `json:"earliestTakenAt"`
}

// PickEntry is one folder row in the Add-folder picker: a subdirectory with
// its direct (non-recursive) RAW file count.
type PickEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	HasSubdirs bool   `json:"hasSubdirs"`
	RawCount   int    `json:"rawCount"`
}

// RawTotal sums RAW files under the requested paths (picker footer).
type RawTotal struct {
	Files int `json:"files"`
}

// RenameResult carries the folder's path after an on-disk rename.
type RenameResult struct {
	Path string `json:"path"`
}

type FolderInfo struct {
	FolderID   int64  `json:"folderId"`
	Path       string `json:"path"`
	PhotoCount int    `json:"photoCount"`
}

// AppSettings is the client-facing application preferences record.
type AppSettings struct {
	// SidecarWrites mirrors edits/rating/flag to portable .marraw.json files
	// next to each RAW, so copying a folder carries the work to another
	// machine. Importing existing sidecars happens regardless of this flag.
	SidecarWrites bool `json:"sidecarWrites"`
}

// DeleteResult reports how many photos DeletePhotos moved to the trash.
type DeleteResult struct {
	Deleted int `json:"deleted"`
}

// PhotoPatch is a partial update to one photo; nil fields are unchanged.
type PhotoPatch struct {
	ID       int64   `json:"id"`
	Rating   *int    `json:"rating"`
	Flag     *Flag   `json:"flag"`
	EditHash *string `json:"editHash"`
}

// PhotoPatchEvent is broadcast when rating/flag/edits change so clients can
// patch their in-memory photo list without a full re-query.
type PhotoPatchEvent struct {
	Patches []PhotoPatch `json:"patches"`
}
