package export

import (
	"bytes"
	"encoding/binary"
	"image"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/tiff"

	"github.com/marrasen/marraw/internal/store"
)

// The source image has structure the predictor and the row stride can each get
// wrong: an odd width, a non-zero origin, and channels that vary
// independently.
func TestEncodeTIFF8RoundTrip(t *testing.T) {
	const w, h = 7, 5 // odd width: catches stride and predictor off-by-ones
	src := image.NewRGBA(image.Rect(3, 5, 3+w, 5+h))
	for y := range h {
		for x := range w {
			src.Pix[src.PixOffset(3+x, 5+y)+0] = uint8(x * 31)
			src.Pix[src.PixOffset(3+x, 5+y)+1] = uint8(y * 47)
			src.Pix[src.PixOffset(3+x, 5+y)+2] = uint8(x*13 + y*7)
			src.Pix[src.PixOffset(3+x, 5+y)+3] = 0xff
		}
	}

	var buf bytes.Buffer
	if err := encodeTIFF8(&buf, src, nil, exifMeta{}); err != nil {
		t.Fatalf("encodeTIFF8: %v", err)
	}

	got, err := tiff.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := got.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("bounds = %v, want %dx%d", b, w, h)
	}
	for y := range h {
		for x := range w {
			wr, wg, wb, _ := src.At(3+x, 5+y).RGBA()
			gr, gg, gb, _ := got.At(got.Bounds().Min.X+x, got.Bounds().Min.Y+y).RGBA()
			if wr != gr || wg != gg || wb != gb {
				t.Fatalf("pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
					x, y, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wb>>8)
			}
		}
	}
}

func TestEncodeTIFF8Fields(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 3))
	icc := ICCFor("adobergb")
	if len(icc) == 0 {
		t.Fatal("ICCFor(adobergb) returned no profile")
	}

	var buf bytes.Buffer
	if err := encodeTIFF8(&buf, src, icc, exifMeta{}); err != nil {
		t.Fatalf("encodeTIFF8: %v", err)
	}
	raw := buf.Bytes()
	fields := parseIFD(t, raw)

	for _, tc := range []struct {
		tag  uint16
		want uint32
		name string
	}{
		{tagImageWidth, 4, "ImageWidth"},
		{tagImageLength, 3, "ImageLength"},
		{tagCompression, compressionDeflate, "Compression"},
		{tagPhotometric, photometricRGB, "PhotometricInterpretation"},
		{tagSamplesPerPixel, 3, "SamplesPerPixel"},
		{tagPredictor, predictorHorizontal, "Predictor"},
		{tagPlanarConfig, planarChunky, "PlanarConfiguration"},
	} {
		f, ok := fields[tc.tag]
		if !ok {
			t.Errorf("%s (tag %d) missing", tc.name, tc.tag)
			continue
		}
		if f.value != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, f.value, tc.want)
		}
	}

	// No ExtraSamples tag: three channels, so nothing to describe. Its absence
	// is the whole reason this encoder exists.
	if _, ok := fields[338]; ok {
		t.Error("ExtraSamples present: the image is not plain RGB")
	}

	bps, ok := fields[tagBitsPerSample]
	if !ok || bps.count != 3 {
		t.Fatalf("BitsPerSample count = %v, want 3", bps.count)
	}
	for i := range 3 {
		if v := binary.LittleEndian.Uint16(raw[bps.value+uint32(2*i):]); v != 8 {
			t.Errorf("BitsPerSample[%d] = %d, want 8", i, v)
		}
	}

	prof, ok := fields[tagICCProfile]
	if !ok {
		t.Fatal("ICCProfile tag missing: wide-gamut TIFFs would be read as sRGB")
	}
	if int(prof.count) != len(icc) {
		t.Fatalf("ICCProfile length = %d, want %d", prof.count, len(icc))
	}
	if !bytes.Equal(raw[prof.value:prof.value+prof.count], icc) {
		t.Error("ICCProfile bytes do not match ICCFor(adobergb)")
	}
}

// TestEncodeTIFF8SRGBUntagged: sRGB is the assumed default, so it gets no
// profile — same contract as the JPEG encoder.
func TestEncodeTIFF8SRGBUntagged(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := encodeTIFF8(&buf, src, ICCFor("srgb"), exifMeta{}); err != nil {
		t.Fatalf("encodeTIFF8: %v", err)
	}
	if _, ok := parseIFD(t, buf.Bytes())[tagICCProfile]; ok {
		t.Error("sRGB export carries an ICC profile; it should be untagged")
	}
	if _, err := tiff.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// TestExportTIFF8E2E drives a real RAW through exportOne, which is the only
// way to catch the pipeline wiring (crop/look/detail/resize) being skipped for
// TIFF the way it silently was for the old 16-bit master.
func TestExportTIFF8E2E(t *testing.T) {
	raw := sampleRAW(t)
	photo := store.Photo{FolderPath: filepath.Dir(raw), FileName: filepath.Base(raw)}
	dir := t.TempDir()

	// ProPhoto: the profile must survive into the file, or the export is a
	// wide-gamut image that every other editor reads as sRGB.
	req := Request{Format: "tiff8", LongEdge: 800, ColorSpace: "prophoto", SharpenTarget: "off"}
	out := filepath.Join(dir, "e2e.tif")
	if err := exportOne(photo, out, req); err != nil {
		t.Fatalf("exportOne: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	img, err := tiff.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode exported tiff: %v", err)
	}
	if b := img.Bounds(); max(b.Dx(), b.Dy()) != 800 {
		t.Errorf("long edge = %d, want 800 (resize not applied)", max(b.Dx(), b.Dy()))
	}
	fields := parseIFD(t, data)
	prof, ok := fields[tagICCProfile]
	if !ok {
		t.Fatal("no ICC profile on a ProPhoto export")
	}
	if int(prof.count) != len(ICCFor("prophoto")) {
		t.Errorf("ICC length = %d, want %d", prof.count, len(ICCFor("prophoto")))
	}
	if _, ok := fields[338]; ok {
		t.Error("ExtraSamples present: an opaque alpha channel leaked into the file")
	}
	t.Logf("exported %s: %v, %d bytes", filepath.Base(out), img.Bounds(), len(data))
}

type ifdField struct {
	typ   uint16
	count uint32
	value uint32
}

// parseIFD reads the first IFD. Entries whose payload fits in four bytes carry
// it inline; the rest carry an offset — parseIFD returns whichever it is, so
// callers index into raw for out-of-line data.
func parseIFD(t *testing.T, raw []byte) map[uint16]ifdField {
	t.Helper()
	le := binary.LittleEndian
	if len(raw) < 8 || string(raw[:2]) != "II" || le.Uint16(raw[2:]) != 42 {
		t.Fatal("bad TIFF header")
	}
	off := le.Uint32(raw[4:])
	if int(off)+2 > len(raw) {
		t.Fatal("IFD offset past end of file")
	}
	n := le.Uint16(raw[off:])
	fields := make(map[uint16]ifdField, n)
	var prev uint16
	for i := range int(n) {
		e := raw[int(off)+2+12*i:]
		tag := le.Uint16(e)
		if i > 0 && tag <= prev {
			t.Fatalf("IFD entries out of order: tag %d after %d", tag, prev)
		}
		prev = tag
		fields[tag] = ifdField{typ: le.Uint16(e[2:]), count: le.Uint32(e[4:]), value: le.Uint32(e[8:])}
	}
	if next := le.Uint32(raw[int(off)+2+12*int(n):]); next != 0 {
		t.Errorf("next-IFD pointer = %d, want 0", next)
	}
	return fields
}
