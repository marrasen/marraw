package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/jpeg" // watermark asset validation
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/watermark"
)

// WatermarkElementType discriminates watermark elements. Go has no unions,
// so WatermarkElement carries both field groups and the client narrows on
// this.
type WatermarkElementType string

const (
	WatermarkText  WatermarkElementType = "text"
	WatermarkImage WatermarkElementType = "image"
)

func WatermarkElementTypeValues() []WatermarkElementType {
	return []WatermarkElementType{WatermarkText, WatermarkImage}
}

// WatermarkAnchor is one of the nine placement positions.
type WatermarkAnchor string

const (
	WatermarkTopLeft     WatermarkAnchor = "topLeft"
	WatermarkTop         WatermarkAnchor = "top"
	WatermarkTopRight    WatermarkAnchor = "topRight"
	WatermarkLeft        WatermarkAnchor = "left"
	WatermarkCenter      WatermarkAnchor = "center"
	WatermarkRight       WatermarkAnchor = "right"
	WatermarkBottomLeft  WatermarkAnchor = "bottomLeft"
	WatermarkBottom      WatermarkAnchor = "bottom"
	WatermarkBottomRight WatermarkAnchor = "bottomRight"
)

func WatermarkAnchorValues() []WatermarkAnchor {
	return []WatermarkAnchor{
		WatermarkTopLeft, WatermarkTop, WatermarkTopRight,
		WatermarkLeft, WatermarkCenter, WatermarkRight,
		WatermarkBottomLeft, WatermarkBottom, WatermarkBottomRight,
	}
}

// WatermarkFontID names one of the faces bundled in internal/watermark.
type WatermarkFontID string

const (
	WatermarkFontSans   WatermarkFontID = "sans"
	WatermarkFontSerif  WatermarkFontID = "serif"
	WatermarkFontMono   WatermarkFontID = "mono"
	WatermarkFontScript WatermarkFontID = "script"
)

func WatermarkFontIDValues() []WatermarkFontID {
	return []WatermarkFontID{WatermarkFontSans, WatermarkFontSerif, WatermarkFontMono, WatermarkFontScript}
}

// WatermarkElement is one overlay in a watermark. Sizes and margins are
// percentages of the export's short edge so a watermark reads the same at
// every output resolution; the preview (client/src/lib/watermarks.ts) and
// the exporter (internal/watermark) share the formulas.
type WatermarkElement struct {
	ID   string               `json:"id"`
	Type WatermarkElementType `json:"type"`
	// Text elements.
	Text  string          `json:"text"`
	Font  WatermarkFontID `json:"font"`
	Color string          `json:"color"` // #rrggbb
	// Image elements: Asset is a file name under the watermark asset dir
	// (AddWatermarkAsset); the source dimensions let the preview reserve the
	// right box before the bitmap loads.
	Asset       string `json:"asset"`
	AssetWidth  int    `json:"assetWidth"`
	AssetHeight int    `json:"assetHeight"`
	// Shared geometry.
	Anchor    WatermarkAnchor `json:"anchor"`
	SizePct   float64         `json:"sizePct"`   // % of short edge (text em / image height)
	MarginPct float64         `json:"marginPct"` // % of short edge, anchored edges only
	Opacity   float64         `json:"opacity"`   // 0..1
}

// Watermark is a named overlay set the user applies at export.
type Watermark struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Elements []WatermarkElement `json:"elements"`
}

// Watermark geometry bounds — WATERMARK_LIMITS in
// client/src/lib/watermarks.ts mirrors these.
const (
	watermarkSizeMin, watermarkSizeMax, watermarkSizeDefault = 0.5, 50.0, 4.0
	watermarkMarginMax, watermarkMarginDefault               = 25.0, 3.0
	watermarkTextMax                                         = 200
)

// watermarkAssetName is the only shape AddWatermarkAsset produces; enforcing
// it on read/write means a stored asset name can never traverse out of the
// asset dir.
var watermarkAssetName = regexp.MustCompile(`^[0-9a-f]{16}\.(png|jpg)$`)

var watermarkHexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// normalizeWatermarkElement maps missing or invalid fields (older/partial
// blobs unmarshal as zero values) to safe defaults, on both read and write.
func normalizeWatermarkElement(e WatermarkElement) WatermarkElement {
	if !enumValid(e.Font, WatermarkFontIDValues()) {
		e.Font = WatermarkFontSans
	}
	if !enumValid(e.Anchor, WatermarkAnchorValues()) {
		e.Anchor = WatermarkBottomRight
	}
	e.Text = clampText(e.Text, watermarkTextMax)
	// Store what the exporter will actually use, so the preview never lies:
	// anything ParseHexColor would white-fall-back is stored as white.
	if c := strings.ToLower(strings.TrimSpace(e.Color)); watermarkHexColor.MatchString(c) {
		e.Color = c
	} else {
		e.Color = "#ffffff"
	}
	if !watermarkAssetName.MatchString(e.Asset) {
		e.Asset, e.AssetWidth, e.AssetHeight = "", 0, 0
	}
	if e.SizePct <= 0 {
		e.SizePct = watermarkSizeDefault
	}
	e.SizePct = clampF(e.SizePct, watermarkSizeMin, watermarkSizeMax)
	e.MarginPct = clampF(e.MarginPct, 0, watermarkMarginMax)
	if e.Opacity <= 0 || e.Opacity > 1 {
		e.Opacity = 1
	}
	return e
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// SetWatermarks replaces the watermark list (add, remove, and edit are all
// "send the new list", like the presets).
func (u *Settings) SetWatermarks(ctx context.Context, watermarks []Watermark) error {
	if watermarks == nil {
		watermarks = []Watermark{}
	}
	for i := range watermarks {
		if watermarks[i].ID == "" || watermarks[i].Name == "" {
			return aprot.ErrInvalidParams("watermarks need an id and a name")
		}
		if watermarks[i].Elements == nil {
			watermarks[i].Elements = []WatermarkElement{}
		}
		kept := watermarks[i].Elements[:0]
		for _, e := range watermarks[i].Elements {
			if e.ID == "" || !enumValid(e.Type, WatermarkElementTypeValues()) {
				continue
			}
			kept = append(kept, normalizeWatermarkElement(e))
		}
		watermarks[i].Elements = kept
	}
	return u.saveJSON(ctx, settingUIWatermarks, watermarks)
}

// WatermarkAssetInfo describes a stored watermark image.
type WatermarkAssetInfo struct {
	FileName string `json:"fileName"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// AddWatermarkAsset copies the picked image into the app-managed asset dir
// under a content-hash name (re-adding the same file is a no-op), so exports
// keep working when the user's original moves. Served back to the preview
// via GET /wm/{name}.
func (u *Settings) AddWatermarkAsset(ctx context.Context, path string) (*WatermarkAssetInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, aprot.ErrInvalidParams("cannot read image: " + err.Error())
	}
	if len(raw) > 64<<20 {
		return nil, aprot.ErrInvalidParams("image is larger than 64 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, aprot.ErrInvalidParams("not a PNG or JPEG image")
	}
	var ext string
	switch format {
	case "png":
		ext = ".png"
	case "jpeg":
		ext = ".jpg"
	default:
		return nil, aprot.ErrInvalidParams("watermark images must be PNG or JPEG")
	}
	if cfg.Width*cfg.Height > 64<<20 {
		return nil, aprot.ErrInvalidParams("image is larger than 64 megapixels")
	}
	// Fully decode to reject files whose pixel data is corrupt — better to
	// fail here than during an export batch.
	if _, _, err := image.Decode(bytes.NewReader(raw)); err != nil {
		return nil, aprot.ErrInvalidParams("broken image: " + err.Error())
	}

	sum := sha256.Sum256(raw)
	name := hex.EncodeToString(sum[:8]) + ext
	dest := filepath.Join(u.deps.WatermarkDir, name)
	if _, err := os.Stat(dest); err != nil {
		if err := os.MkdirAll(u.deps.WatermarkDir, 0o755); err != nil {
			return nil, err
		}
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, dest); err != nil {
			os.Remove(tmp)
			return nil, err
		}
	}
	return &WatermarkAssetInfo{FileName: name, Width: cfg.Width, Height: cfg.Height}, nil
}

// toWatermarkSpec resolves a stored watermark for the exporter: colors
// parsed, asset names joined onto the asset dir, content-free elements
// dropped.
func toWatermarkSpec(wm Watermark, assetDir string) *watermark.Spec {
	spec := &watermark.Spec{}
	for _, e := range wm.Elements {
		e = normalizeWatermarkElement(e)
		el := watermark.Element{
			Anchor:    watermark.Anchor(e.Anchor),
			SizePct:   e.SizePct,
			MarginPct: e.MarginPct,
			Opacity:   e.Opacity,
		}
		switch e.Type {
		case WatermarkText:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
			el.Kind = watermark.KindText
			el.Text = e.Text
			el.Font = watermark.FontID(e.Font)
			el.Color = watermark.ParseHexColor(e.Color)
		case WatermarkImage:
			if e.Asset == "" {
				continue
			}
			el.Kind = watermark.KindImage
			// normalizeWatermarkElement enforced the content-hash name shape,
			// so this join cannot escape assetDir.
			el.AssetPath = filepath.Join(assetDir, e.Asset)
		default:
			continue
		}
		spec.Elements = append(spec.Elements, el)
	}
	if len(spec.Elements) == 0 {
		return nil
	}
	return spec
}

// watermarkByID finds one stored watermark; nil when id is empty or unknown
// (a deleted watermark silently exports clean rather than failing the batch).
func watermarkByID(ctx context.Context, db *store.DB, id string) *Watermark {
	if id == "" {
		return nil
	}
	for _, wm := range jsonSetting(ctx, db, settingUIWatermarks, []Watermark{}) {
		if wm.ID == id {
			return &wm
		}
	}
	return nil
}
