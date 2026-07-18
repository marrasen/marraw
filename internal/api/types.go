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

// ExportFormat selects the export output. JPEG/TIFF/PNG render the same
// pixels (TIFF and PNG lossless 8-bit RGB, JPEG compact); rawXmp skips
// rendering entirely and instead copies the source RAW with an Adobe .xmp
// sidecar carrying the rating, label and an approximation of the edit.
type ExportFormat string

const (
	ExportJPEG   ExportFormat = "jpeg"
	ExportTIFF8  ExportFormat = "tiff8"
	ExportPNG    ExportFormat = "png"
	ExportRawXMP ExportFormat = "rawXmp"
)

func ExportFormatValues() []ExportFormat {
	return []ExportFormat{ExportJPEG, ExportTIFF8, ExportPNG, ExportRawXMP}
}

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

// ExifMode selects what metadata an export carries: everything the catalog
// has, only the artist/copyright credit, or a bare file with no EXIF at all.
type ExifMode string

const (
	ExifModeAll       ExifMode = "all"
	ExifModeCopyright ExifMode = "copyright"
	ExifModeNone      ExifMode = "none"
)

func ExifModeValues() []ExifMode {
	return []ExifMode{ExifModeAll, ExifModeCopyright, ExifModeNone}
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
	// Rotate/CropW/CropH are the aspect-affecting edit geometry: coarse
	// rotation in quarter turns CW and the crop rectangle's size as fractions
	// of the rotated frame (0 = no crop). The natural-layout grid composes
	// them with Width/Height (renderedDims) to size cells to the rendered
	// aspect without loading edit params. CropAngle/FlipH are deliberately
	// absent — they don't change the output size.
	Rotate int     `json:"rotate,omitempty"`
	CropW  float64 `json:"cropW,omitempty"`
	CropH  float64 `json:"cropH,omitempty"`
	ISO         float64 `json:"iso"`
	Shutter     float64 `json:"shutter"`
	Aperture    float64 `json:"aperture"`
	FocalLen    float64 `json:"focalLen"`
	TakenAt     int64   `json:"takenAt"` // unix seconds, 0 = unknown
	Make        string  `json:"make"`
	Model       string  `json:"model"`
	// Sharpness is the focus score (Laplacian variance of the embedded thumb
	// at a 512 px basis); the grid badges values below the soft threshold.
	// Nil = not yet measured.
	Sharpness *float64 `json:"sharpness,omitempty"`
	// SubjectSharpness is the focus score over the AI subject matte alone,
	// present only for photos with a generated subject map and a scoreable
	// subject. When set it supersedes Sharpness for the soft badge, so a
	// sharp background can't hide a soft subject.
	SubjectSharpness *float64 `json:"subjectSharpness,omitempty"`
	// SubjectAnalyzed reports that the subject matte has been measured, whether
	// or not it found a scoreable subject — so a frame with no detectable
	// subject (its score is the client-invisible "-1" sentinel) still reads as
	// done. Distinct from SubjectSharpness != null so the subject-scan
	// indicator stops flagging subjectless frames as pending: re-analyzing them
	// would never change anything.
	SubjectAnalyzed bool `json:"subjectAnalyzed"`
	// GroupID marks a near-duplicate burst: photos shot moments apart whose
	// perceptual hashes match carry the same id (the group's first photo ID).
	// Derived per list from stored hashes, never persisted. Nil = not part
	// of any burst.
	GroupID *int64 `json:"groupId,omitempty"`
	// EyesClosed is the highest closed-eye probability (0..1) across the
	// frame's detected faces — a soft cull signal (sunglasses and profiles
	// misfire). Present only for analyzed frames with a judgeable face; the
	// "no face" sentinel is client-invisible (see EyesAnalyzed).
	EyesClosed *float64 `json:"eyesClosed,omitempty"`
	// EyesAnalyzed reports that closed-eye detection has run, whether or not
	// a judgeable face was found — the eye-scan indicator's "done" state,
	// mirroring SubjectAnalyzed.
	EyesAnalyzed bool `json:"eyesAnalyzed"`
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
	// Rotate/CropW/CropH mirror Photo's aspect-affecting geometry. Every edit
	// save sets all three alongside EditHash (see editPatch), so a reset
	// delivers explicit zeros and the grid's cell aspect follows live.
	Rotate *int     `json:"rotate"`
	CropW  *float64 `json:"cropW"`
	CropH  *float64 `json:"cropH"`
	// SubjectSharpness delivers a just-measured subject focus score to the grid
	// without a full folder-list refresh. Only ever a real score — the "-1
	// unscoreable" sentinel is client-invisible (see SubjectAnalyzed).
	SubjectSharpness *float64 `json:"subjectSharpness"`
	// SubjectAnalyzed flips the photo's analyzed flag the moment its matte is
	// scored, even when there was no scoreable subject (score < 0), so the
	// subject-scan indicator stops counting it as pending live during a scan.
	SubjectAnalyzed *bool `json:"subjectAnalyzed"`
	// EyesClosed / EyesAnalyzed deliver a just-measured closed-eye result the
	// same way (the -1 "no face" sentinel stays client-invisible).
	EyesClosed   *float64 `json:"eyesClosed"`
	EyesAnalyzed *bool    `json:"eyesAnalyzed"`
}

// PhotoPatchEvent is broadcast when rating/flag/edits change so clients can
// patch their in-memory photo list without a full re-query.
type PhotoPatchEvent struct {
	Patches []PhotoPatch `json:"patches"`
}

// RenderProgressEvent is broadcast while a full-resolution (1:1 tile) render
// runs, so the loupe's decoding indicator can show a percent instead of an
// indeterminate spinner. Fraction is 0..1 across the whole render (decode,
// look, tile write-out); the final 1 always fires. Rate-limited at the
// source (pyramid.Cache.Progress) to ~10 events/s per render.
type RenderProgressEvent struct {
	PhotoID  int64   `json:"photoId"`
	EditHash string  `json:"editHash"`
	Fraction float64 `json:"fraction"`
}
