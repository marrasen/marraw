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

// ExportFormat selects the export encoder.
type ExportFormat string

const (
	ExportJPEG   ExportFormat = "jpeg"
	ExportTIFF16 ExportFormat = "tiff16"
)

func ExportFormatValues() []ExportFormat { return []ExportFormat{ExportJPEG, ExportTIFF16} }

// Photo is the client-facing photo record.
type Photo struct {
	ID          int64   `json:"id"`
	FileName    string  `json:"fileName"`
	CacheKey    string  `json:"cacheKey"`
	EditHash    string  `json:"editHash"`
	Rating      int     `json:"rating"`
	Flag        Flag    `json:"flag"`
	MetaLoaded  bool    `json:"metaLoaded"`
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

type FolderInfo struct {
	FolderID   int64  `json:"folderId"`
	Path       string `json:"path"`
	PhotoCount int    `json:"photoCount"`
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
