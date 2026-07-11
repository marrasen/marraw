package export

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"golang.org/x/image/tiff"
)

func sampleMeta() exifMeta {
	return exifMeta{
		Make: "SONY", Model: "ILCE-7RM2",
		TakenAt: time.Date(2026, 4, 18, 10, 4, 5, 0, time.Local).Unix(),
		ISO:     400, Shutter: 1 / 500.0, Aperture: 5.6, FocalLen: 200,
		Width: 1024, Height: 683,
		SRGB: true,
	}
}

// parseExifIn walks a TIFF-format block starting at raw[base:] (offsets in
// the block are relative to base) and returns IFD0 and the Exif sub-IFD.
func parseExifIn(t *testing.T, raw []byte, base uint32) (ifd0, exif map[uint16]ifdField) {
	t.Helper()
	le := binary.LittleEndian
	if string(raw[base:base+2]) != "II" || le.Uint16(raw[base+2:]) != 42 {
		t.Fatal("bad EXIF TIFF header")
	}
	read := func(off uint32) map[uint16]ifdField {
		n := le.Uint16(raw[base+off:])
		fields := make(map[uint16]ifdField, n)
		var prev uint16
		for i := range int(n) {
			e := raw[base+off+2+uint32(12*i):]
			tag := le.Uint16(e)
			if i > 0 && tag <= prev {
				t.Fatalf("IFD entries out of order: tag %d after %d", tag, prev)
			}
			prev = tag
			fields[tag] = ifdField{typ: le.Uint16(e[2:]), count: le.Uint32(e[4:]), value: le.Uint32(e[8:])}
		}
		return fields
	}
	ifd0 = read(le.Uint32(raw[base+4:]))
	ptr, ok := ifd0[tagExifIFD]
	if !ok {
		t.Fatal("no ExifIFD pointer in IFD0")
	}
	return ifd0, read(ptr.value)
}

func asciiAt(raw []byte, base uint32, f ifdField) string {
	b := raw[base+f.value : base+f.value+f.count]
	return string(bytes.TrimRight(b, "\x00"))
}

func rationalAt(raw []byte, base uint32, f ifdField) (uint32, uint32) {
	le := binary.LittleEndian
	return le.Uint32(raw[base+f.value:]), le.Uint32(raw[base+f.value+4:])
}

func checkExifFields(t *testing.T, raw []byte, base uint32) {
	t.Helper()
	m := sampleMeta()
	ifd0, exif := parseExifIn(t, raw, base)

	if got := asciiAt(raw, base, ifd0[tagMake]); got != "SONY" {
		t.Errorf("Make = %q", got)
	}
	if got := asciiAt(raw, base, ifd0[tagModel]); got != "ILCE-7RM2" {
		t.Errorf("Model = %q", got)
	}
	if ifd0[tagOrientation].value != 1 {
		t.Errorf("Orientation = %d, want 1 (pixels are pre-oriented)", ifd0[tagOrientation].value)
	}
	wantDT := time.Unix(m.TakenAt, 0).Format(exifDateFormat)
	if got := asciiAt(raw, base, ifd0[tagDateTime]); got != wantDT {
		t.Errorf("DateTime = %q, want %q", got, wantDT)
	}
	if got := asciiAt(raw, base, exif[tagDateTimeOriginal]); got != wantDT {
		t.Errorf("DateTimeOriginal = %q, want %q", got, wantDT)
	}
	if num, den := rationalAt(raw, base, exif[tagExposureTime]); num != 1 || den != 500 {
		t.Errorf("ExposureTime = %d/%d, want 1/500", num, den)
	}
	if num, den := rationalAt(raw, base, exif[tagFNumber]); num != 560 || den != 100 {
		t.Errorf("FNumber = %d/%d, want 560/100", num, den)
	}
	if exif[tagISO].value != 400 {
		t.Errorf("ISO = %d, want 400", exif[tagISO].value)
	}
	if num, den := rationalAt(raw, base, exif[tagFocalLength]); num != 20000 || den != 100 {
		t.Errorf("FocalLength = %d/%d, want 20000/100", num, den)
	}
	if exif[tagColorSpaceExif].value != 1 {
		t.Errorf("ColorSpace = %d, want 1 (sRGB)", exif[tagColorSpaceExif].value)
	}
	if exif[tagPixelXDimension].value != 1024 || exif[tagPixelYDimension].value != 683 {
		t.Errorf("PixelDimensions = %dx%d, want 1024x683",
			exif[tagPixelXDimension].value, exif[tagPixelYDimension].value)
	}
}

func TestBuildExifPayload(t *testing.T) {
	checkExifFields(t, buildExifPayload(sampleMeta()), 0)
}

func TestExifPayloadSparseMeta(t *testing.T) {
	// A photo with no scanned metadata still yields a valid block with just
	// the always-present fields.
	payload := buildExifPayload(exifMeta{Width: 10, Height: 20, SRGB: false})
	ifd0, exif := parseExifIn(t, payload, 0)
	if _, ok := ifd0[tagMake]; ok {
		t.Error("empty Make must be absent")
	}
	if _, ok := exif[tagExposureTime]; ok {
		t.Error("zero shutter must be absent")
	}
	if exif[tagColorSpaceExif].value != 0xFFFF {
		t.Errorf("wide-gamut ColorSpace = %d, want 65535 (uncalibrated)", exif[tagColorSpaceExif].value)
	}
	if exif[tagPixelXDimension].value != 10 || exif[tagPixelYDimension].value != 20 {
		t.Error("pixel dimensions wrong on sparse meta")
	}
}

func TestEmbedExifJPEG(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	out := embedExifJPEG(buf.Bytes(), sampleMeta())
	// APP1 sits directly after SOI.
	if out[2] != 0xFF || out[3] != 0xE1 {
		t.Fatalf("no APP1 after SOI: % x", out[2:4])
	}
	if string(out[6:12]) != "Exif\x00\x00" {
		t.Fatalf("APP1 is not Exif: %q", out[6:12])
	}
	checkExifFields(t, out, 12)
	// The file still decodes.
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode with EXIF: %v", err)
	}
	// Composes with the ICC splice (ICC first, then EXIF → APP1 leads).
	withICC := embedExifJPEG(embedICCJPEG(buf.Bytes(), ICCFor("adobergb")), sampleMeta())
	if withICC[2] != 0xFF || withICC[3] != 0xE1 {
		t.Fatal("APP1 must lead even with an ICC segment present")
	}
	if _, err := jpeg.Decode(bytes.NewReader(withICC)); err != nil {
		t.Fatalf("decode with EXIF+ICC: %v", err)
	}
}

func TestEmbedExifPNG(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := png.Encode(buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	out := embedExifPNG(buf.Bytes(), sampleMeta())
	idx := bytes.Index(out, []byte("eXIf"))
	if idx < 0 {
		t.Fatal("no eXIf chunk")
	}
	length := binary.BigEndian.Uint32(out[idx-4:])
	payload := out[idx+4 : idx+4+int(length)]
	// Chunk CRC covers type + data.
	wantCRC := crc32PNG(out[idx : idx+4+int(length)])
	if got := binary.BigEndian.Uint32(out[idx+4+int(length):]); got != wantCRC {
		t.Fatalf("eXIf CRC = %08x, want %08x", got, wantCRC)
	}
	checkExifFields(t, payload, 0)
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("decode with eXIf: %v", err)
	}
}

func TestEncodeTIFF8Metadata(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	m := sampleMeta()
	if err := encodeTIFF8(&buf, src, ICCFor("adobergb"), m); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	fields := parseIFD(t, raw)
	if got := asciiAt(raw, 0, fields[tagMake]); got != "SONY" {
		t.Errorf("TIFF Make = %q", got)
	}
	if got := asciiAt(raw, 0, fields[tagSoftware]); got != "marraw" {
		t.Errorf("TIFF Software = %q", got)
	}
	exifPtr, ok := fields[tagExifIFD]
	if !ok {
		t.Fatal("TIFF has no ExifIFD pointer")
	}
	// Walk the sub-IFD (absolute offsets, base 0).
	le := binary.LittleEndian
	n := le.Uint16(raw[exifPtr.value:])
	exif := map[uint16]ifdField{}
	for i := range int(n) {
		e := raw[exifPtr.value+2+uint32(12*i):]
		exif[le.Uint16(e)] = ifdField{typ: le.Uint16(e[2:]), count: le.Uint32(e[4:]), value: le.Uint32(e[8:])}
	}
	if exif[tagISO].value != 400 {
		t.Errorf("TIFF ISO = %d, want 400", exif[tagISO].value)
	}
	if num, den := rationalAt(raw, 0, exif[tagExposureTime]); num != 1 || den != 500 {
		t.Errorf("TIFF ExposureTime = %d/%d, want 1/500", num, den)
	}
	// Still a decodable baseline TIFF for readers that ignore the extras.
	if _, err := tiff.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("decode with metadata: %v", err)
	}
}
