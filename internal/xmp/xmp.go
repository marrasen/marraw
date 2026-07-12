// Package xmp writes Adobe Camera Raw-compatible .xmp sidecars so RAW files
// exported from marraw carry their rating, label and an approximation of the
// develop settings into Lightroom/Bridge. The mapping is deliberately lossy:
// marraw's look pipeline is not ACR, so sliders translate by intent
// (Contrast → crs:Contrast2012 and so on), not by identical rendering.
//
// Fields with no ACR analog are dropped: ExpPreserve, Bright, Gamma, Shadow
// (LibRaw brightness/gamma-curve params), Highlight (a recovery mode, not a
// slider), FBDDNoiseRd, MedPasses, Demosaic, CARed/CABlue, raw WBMul
// multipliers, and the relative WBTemp/WBTint shifts outside kelvin mode
// (they offset multipliers, so no absolute Kelvin base exists to express
// them against).
package xmp

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/marrasen/marraw/internal/edit"
)

// Ext replaces the RAW extension to form the sidecar name (Adobe convention:
// "IMG1234.ARW" -> "IMG1234.xmp", not "IMG1234.ARW.xmp").
const Ext = ".xmp"

// PathFor returns the sidecar path for a RAW file path.
func PathFor(rawPath string) string {
	return strings.TrimSuffix(rawPath, filepath.Ext(rawPath)) + Ext
}

// Meta is the portable non-develop metadata of one photo.
type Meta struct {
	Rating      int // 0..5
	Flag        int // photos.flag: 1 pick, -1 exclude, 0 none
	Orientation int // EXIF base orientation 1..8 (0 = treat as upright)
}

// Build renders the sidecar bytes. A nil or neutral edit yields a
// metadata-only packet with no crs block, so Lightroom keeps its own raw
// defaults. Pure string assembly — it cannot fail.
func Build(m Meta, e *edit.Params) []byte {
	a := &attrs{}
	a.ns("xmp", "http://ns.adobe.com/xap/1.0/")
	if m.Rating > 0 {
		a.add("xmp:Rating", fmt.Sprintf("%d", m.Rating))
	}
	// Lightroom pick/reject flags are catalog-local and not XMP-portable, so
	// the cull flag travels as a color label instead.
	switch {
	case m.Flag > 0:
		a.add("xmp:Label", "Green")
	case m.Flag < 0:
		a.add("xmp:Label", "Red")
	}

	neutral := e.IsNeutral()
	var n edit.Params
	if !neutral {
		n = *e
		n.Normalize()
	}

	// Total orientation = the RAW's own EXIF orientation composed with the
	// user's rotate/flip. Emitted only when the user actually rotated or
	// mirrored — otherwise the reader keeps using the RAW's embedded value.
	total := composeOrient(orientFromEXIF(m.Orientation), orient{r: n.RotateTurns(), f: n.FlipH})
	if !neutral && (n.RotateTurns() != 0 || n.FlipH) {
		a.ns("tiff", "http://ns.adobe.com/tiff/1.0/")
		a.add("tiff:Orientation", fmt.Sprintf("%d", orientToEXIF(total)))
	}

	if !neutral {
		a.ns("crs", "http://ns.adobe.com/camera-raw-settings/1.0/")
		crsAttrs(a, &n, total)
	}
	return render(a)
}

// Write atomically writes the sidecar next to the RAW (temp file + rename),
// mirroring internal/sidecar.Write.
func Write(rawPath string, data []byte) error {
	dst := PathFor(rawPath)
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".marraw-xmp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// os.Rename replaces an existing destination on both POSIX and Windows.
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// crsAttrs emits the complete canonical slider set for a normalized,
// non-neutral edit — zeros included, so an ACR reader takes these values
// instead of substituting its raw defaults (e.g. Sharpness 40).
func crsAttrs(a *attrs, n *edit.Params, total orient) {
	// ProcessVersion 11.0 is Adobe's Process Version 4 (the *2012 sliders),
	// readable by Lightroom Classic 8+ — chosen over newer PVs for reach.
	a.add("crs:Version", "15.4")
	a.add("crs:ProcessVersion", "11.0")

	switch n.WBMode {
	case edit.WBAuto:
		a.add("crs:WhiteBalance", "Auto")
	case edit.WBKelvin:
		a.add("crs:WhiteBalance", "Custom")
		a.add("crs:Temperature", fmt.Sprintf("%d", clampInt(int(math.Round(n.WBKelvin)), 2000, 50000)))
		// LibRaw tint > 0 shifts magenta (G÷2^t), same polarity as ACR.
		a.add("crs:Tint", signedInt(clampInt(int(math.Round(150*n.WBTint)), -150, 150)))
	default:
		// Camera ("" after Normalize) and custom multipliers both fall back
		// to As Shot — see the unmapped list in the package comment.
		a.add("crs:WhiteBalance", "As Shot")
	}

	a.add("crs:Exposure2012", fmt.Sprintf("%+.2f", n.ExpEV))
	a.add("crs:Contrast2012", pct(n.Contrast))
	a.add("crs:Highlights2012", pct(n.ToneHighlights))
	a.add("crs:Shadows2012", pct(n.ToneShadows))
	a.add("crs:Whites2012", pct(n.Whites))
	a.add("crs:Blacks2012", pct(n.Blacks))
	a.add("crs:Texture", pct(n.Texture))
	a.add("crs:Clarity2012", pct(n.Clarity))
	a.add("crs:Dehaze", pct(n.Dehaze))
	a.add("crs:Vibrance", pct(n.Vibrance))
	a.add("crs:Saturation", pct(n.Saturation))

	// marraw's single Sharpen slider maps onto ACR's amount; the radius/
	// detail/masking companions are Adobe's raw defaults.
	a.add("crs:Sharpness", fmt.Sprintf("%d", int(math.Round(150*n.Sharpen))))
	a.add("crs:SharpenRadius", "+1.0")
	a.add("crs:SharpenDetail", "25")
	a.add("crs:SharpenEdgeMasking", "0")

	a.add("crs:LuminanceSmoothing", fmt.Sprintf("%d", clampInt(int(math.Round(n.NRThreshold/10)), 0, 100)))
	a.add("crs:ColorNoiseReduction", "25")

	bands := []string{"Red", "Orange", "Yellow", "Green", "Aqua", "Blue", "Purple", "Magenta"}
	for i, b := range bands {
		a.add("crs:HueAdjustment"+b, pct(n.HSLHue[i]))
	}
	for i, b := range bands {
		a.add("crs:SaturationAdjustment"+b, pct(n.HSLSat[i]))
	}
	for i, b := range bands {
		a.add("crs:LuminanceAdjustment"+b, pct(n.HSLLum[i]))
	}

	// Split-toning hues and saturations are unsigned in ACR (0..359 / 0..100).
	a.add("crs:SplitToningShadowHue", fmt.Sprintf("%d", int(math.Round(n.SplitShadowHue))%360))
	a.add("crs:SplitToningShadowSaturation", fmt.Sprintf("%d", int(math.Round(100*n.SplitShadowAmt))))
	a.add("crs:SplitToningHighlightHue", fmt.Sprintf("%d", int(math.Round(n.SplitHighlightHue))%360))
	a.add("crs:SplitToningHighlightSaturation", fmt.Sprintf("%d", int(math.Round(100*n.SplitHighlightAmt))))
	a.add("crs:SplitToningBalance", "0")

	// ACR's post-crop vignette darkens with negative amounts; marraw's
	// darkens with positive, so the sign flips. Companions are ACR defaults.
	if n.Vignette != 0 {
		a.add("crs:PostCropVignetteAmount", signedInt(int(math.Round(-100*n.Vignette))))
		a.add("crs:PostCropVignetteMidpoint", "50")
		a.add("crs:PostCropVignetteFeather", "50")
		a.add("crs:PostCropVignetteRoundness", "0")
		a.add("crs:PostCropVignetteStyle", "1")
		a.add("crs:PostCropVignetteHighlightContrast", "0")
	}

	if n.HasCrop() || n.CropAngle != 0 {
		left, top, right, bottom, angle := cropToNative(n, total)
		a.add("crs:HasCrop", "True")
		a.add("crs:CropLeft", fmt.Sprintf("%.6f", left))
		a.add("crs:CropTop", fmt.Sprintf("%.6f", top))
		a.add("crs:CropRight", fmt.Sprintf("%.6f", right))
		a.add("crs:CropBottom", fmt.Sprintf("%.6f", bottom))
		a.add("crs:CropAngle", fmt.Sprintf("%.6f", angle))
		a.add("crs:CropConstrainToWarp", "0")
	}

	a.add("crs:HasSettings", "True")
}

// orient is "rotate r quarter turns clockwise, then mirror about the
// vertical axis when f" — exactly marraw's user-geometry model.
type orient struct {
	r int
	f bool
}

// orientFromEXIF maps an EXIF orientation code onto the op that turns the
// as-stored image into the upright display image.
func orientFromEXIF(code int) orient {
	switch code {
	case 2:
		return orient{0, true}
	case 3:
		return orient{2, false}
	case 4:
		return orient{2, true}
	case 5:
		return orient{1, true}
	case 6:
		return orient{1, false}
	case 7:
		return orient{3, true}
	case 8:
		return orient{3, false}
	default: // 0/1/invalid: upright
		return orient{0, false}
	}
}

// orientToEXIF is the inverse table of orientFromEXIF.
func orientToEXIF(o orient) int {
	switch o {
	case orient{0, true}:
		return 2
	case orient{2, false}:
		return 3
	case orient{2, true}:
		return 4
	case orient{1, true}:
		return 5
	case orient{1, false}:
		return 6
	case orient{3, true}:
		return 7
	case orient{3, false}:
		return 8
	default:
		return 1
	}
}

// composeOrient returns "a then b" as one op, from the group identity
// Flip∘Rotate(r) == Rotate(-r)∘Flip.
func composeOrient(a, b orient) orient {
	rb := b.r % 4
	if a.f {
		rb = (4 - rb) % 4
	}
	return orient{r: (a.r + rb) % 4, f: a.f != b.f}
}

// inverse returns the op undoing o.
func (o orient) inverse() orient {
	if o.f {
		return o
	}
	return orient{r: (4 - o.r%4) % 4, f: false}
}

// apply maps a point of the unit square through the op: r quarter turns
// clockwise ((u,v) -> (1-v,u)), then the mirror (u -> 1-u).
func (o orient) apply(u, v float64) (float64, float64) {
	for range o.r % 4 {
		u, v = 1-v, u
	}
	if o.f {
		u = 1 - u
	}
	return u, v
}

// cropToNative maps marraw's display-space crop onto crs crop coordinates,
// which live in the RAW's as-stored (unoriented) space: the rectangle
// corners go through the inverse of the total orientation, and the
// straighten angle negates under a mirror (quarter turns preserve it).
// crs:CropAngle then stores the negation of the content-clockwise angle
// (ACR keeps the crop-rect rotation, the inverse of the content rotation).
// Both conventions are assumptions NOT yet round-tripped through a real
// Lightroom — if one proves wrong, each is a single flip in this function.
func cropToNative(n *edit.Params, total orient) (left, top, right, bottom, angle float64) {
	x0, y0, x1, y1 := 0.0, 0.0, 1.0, 1.0
	if n.HasCrop() {
		x0, y0 = n.CropX, n.CropY
		x1, y1 = n.CropX+n.CropW, n.CropY+n.CropH
	}
	inv := total.inverse()
	u0, v0 := inv.apply(x0, y0)
	u1, v1 := inv.apply(x1, y1)
	left, right = math.Min(u0, u1), math.Max(u0, u1)
	top, bottom = math.Min(v0, v1), math.Max(v0, v1)
	angle = n.CropAngle
	if total.f {
		angle = -angle
	}
	return left, top, right, bottom, -angle
}

// attrs is the ordered attribute list of the single rdf:Description element;
// construction order is emission order, keeping golden files byte-stable.
type attrs struct {
	nsList []attr // xmlns declarations, emitted first
	list   []attr
}

type attr struct{ name, value string }

func (a *attrs) ns(prefix, uri string) { a.nsList = append(a.nsList, attr{"xmlns:" + prefix, uri}) }
func (a *attrs) add(name, value string) {
	a.list = append(a.list, attr{name, value})
}

// render assembles the xpacket. Hand-built rather than encoding/xml:
// Marshal cannot emit the xpacket processing instructions and rewrites
// namespace prefixes, and attribute-per-line layout keeps diffs readable.
func render(a *attrs) []byte {
	var b strings.Builder
	// The begin attribute is the zero-width byte-order mark U+FEFF, per the
	// XMP spec; the file itself starts with plain ASCII "<?xpacket".
	b.WriteString("<?xpacket begin=\"\uFEFF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n")
	b.WriteString("<x:xmpmeta xmlns:x=\"adobe:ns:meta/\" x:xmptk=\"marraw\">\n")
	b.WriteString(" <rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\">\n")
	b.WriteString("  <rdf:Description rdf:about=\"\"")
	for _, at := range append(append([]attr{}, a.nsList...), a.list...) {
		b.WriteString("\n    ")
		b.WriteString(at.name)
		b.WriteString("=\"")
		b.WriteString(escape(at.value))
		b.WriteString("\"")
	}
	b.WriteString("/>\n")
	b.WriteString(" </rdf:RDF>\n")
	b.WriteString("</x:xmpmeta>\n")
	b.WriteString("<?xpacket end=\"w\"?>\n")
	return []byte(b.String())
}

// escape XML-escapes an attribute value. Values are mostly self-formatted
// numbers, but escaping unconditionally keeps the writer safe by construction.
func escape(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// pct renders a ±1 slider as ACR's ±100 signed integer.
func pct(v float64) string { return signedInt(int(math.Round(100 * v))) }

// signedInt renders 0 bare and everything else with an explicit sign,
// matching Adobe's slider serialization ("+25", "-100", "0").
func signedInt(v int) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", v)
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
