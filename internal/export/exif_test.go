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

// richMeta is sampleMeta plus the fields added for the metadata option:
// lens, a southern/western GPS fix below sea level, and the user credit.
func richMeta() exifMeta {
	m := sampleMeta()
	m.Lens = "FE 70-200mm F2.8 GM"
	m.HasGPS = true
	m.Lat, m.Lon = -33.8688, -70.6693
	m.HasAlt = true
	m.Alt = -2.5
	m.Artist = "Jane Doe"
	m.Copyright = "© 2026 Jane Doe"
	return m
}

// readIFDAt walks one IFD at offset off inside the block at raw[base:],
// asserting ascending tag order like parseExifIn does.
func readIFDAt(t *testing.T, raw []byte, base, off uint32) map[uint16]ifdField {
	t.Helper()
	le := binary.LittleEndian
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

// asciiVal reads an ASCII field wherever it lives: strings of up to four
// bytes (like the GPS "S\x00" refs) sit inline in the value word, longer
// ones out-of-line at the offset asciiAt expects.
func asciiVal(raw []byte, base uint32, f ifdField) string {
	if f.count <= 4 {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], f.value)
		return string(bytes.TrimRight(b[:f.count], "\x00"))
	}
	return asciiAt(raw, base, f)
}

func rationalsAt(raw []byte, base uint32, f ifdField, n int) [][2]uint32 {
	le := binary.LittleEndian
	out := make([][2]uint32, n)
	for i := range n {
		out[i] = [2]uint32{
			le.Uint32(raw[base+f.value+uint32(8*i):]),
			le.Uint32(raw[base+f.value+uint32(8*i)+4:]),
		}
	}
	return out
}

func checkGPSIFD(t *testing.T, raw []byte, base uint32, ifd0 map[uint16]ifdField) {
	t.Helper()
	ptr, ok := ifd0[tagGPSIFD]
	if !ok {
		t.Fatal("no GPS IFD pointer in IFD0")
	}
	gps := readIFDAt(t, raw, base, ptr.value)
	// 4 inline BYTEs {2,3,0,0}, little-endian = 0x00000302.
	if gps[gpsTagVersionID].value != 0x302 {
		t.Errorf("GPSVersionID = %#x, want 0x302", gps[gpsTagVersionID].value)
	}
	if got := asciiVal(raw, base, gps[gpsTagLatitudeRef]); got != "S" {
		t.Errorf("GPSLatitudeRef = %q, want S", got)
	}
	if got := asciiVal(raw, base, gps[gpsTagLongitudeRef]); got != "W" {
		t.Errorf("GPSLongitudeRef = %q, want W", got)
	}
	// |-33.8688| = 33° 52′ 7.68″; |-70.6693| = 70° 40′ 9.48″.
	wantLat := [][2]uint32{{33, 1}, {52, 1}, {76800, 10000}}
	if got := rationalsAt(raw, base, gps[gpsTagLatitude], 3); got[0] != wantLat[0] || got[1] != wantLat[1] || got[2] != wantLat[2] {
		t.Errorf("GPSLatitude = %v, want %v", got, wantLat)
	}
	wantLon := [][2]uint32{{70, 1}, {40, 1}, {94800, 10000}}
	if got := rationalsAt(raw, base, gps[gpsTagLongitude], 3); got[0] != wantLon[0] || got[1] != wantLon[1] || got[2] != wantLon[2] {
		t.Errorf("GPSLongitude = %v, want %v", got, wantLon)
	}
	if gps[gpsTagAltitudeRef].value != 1 {
		t.Errorf("GPSAltitudeRef = %d, want 1 (below sea level)", gps[gpsTagAltitudeRef].value)
	}
	if num, den := rationalAt(raw, base, gps[gpsTagAltitude]); num != 250 || den != 100 {
		t.Errorf("GPSAltitude = %d/%d, want 250/100", num, den)
	}
}

func TestExifPayloadFullMeta(t *testing.T) {
	m := richMeta()
	payload := buildExifPayload(m)
	checkExifFields(t, payload, 0) // the base fields still hold with GPS added
	ifd0, exif := parseExifIn(t, payload, 0)
	if got := asciiAt(payload, 0, ifd0[tagArtist]); got != m.Artist {
		t.Errorf("Artist = %q, want %q", got, m.Artist)
	}
	if got := asciiAt(payload, 0, ifd0[tagCopyright]); got != m.Copyright {
		t.Errorf("Copyright = %q, want %q", got, m.Copyright)
	}
	if got := asciiAt(payload, 0, exif[tagLensModel]); got != m.Lens {
		t.Errorf("LensModel = %q, want %q", got, m.Lens)
	}
	checkGPSIFD(t, payload, 0, ifd0)
}

func TestApplyPolicyCopyright(t *testing.T) {
	m := richMeta().applyPolicy(Request{
		ExifMode: "copyright", Artist: "Jane Doe", Copyright: "© 2026 Jane Doe",
	})
	payload := buildExifPayload(m)
	ifd0, exif := parseExifIn(t, payload, 0)
	for tag, name := range map[uint16]string{
		tagMake: "Make", tagModel: "Model", tagDateTime: "DateTime", tagGPSIFD: "GPS IFD",
	} {
		if _, ok := ifd0[tag]; ok {
			t.Errorf("copyright mode must strip %s", name)
		}
	}
	for tag, name := range map[uint16]string{
		tagExposureTime: "ExposureTime", tagISO: "ISO", tagDateTimeOriginal: "DateTimeOriginal",
		tagFocalLength: "FocalLength", tagLensModel: "LensModel",
	} {
		if _, ok := exif[tag]; ok {
			t.Errorf("copyright mode must strip %s", name)
		}
	}
	if got := asciiAt(payload, 0, ifd0[tagArtist]); got != "Jane Doe" {
		t.Errorf("Artist = %q", got)
	}
	if got := asciiAt(payload, 0, ifd0[tagCopyright]); got != "© 2026 Jane Doe" {
		t.Errorf("Copyright = %q", got)
	}
	if exif[tagPixelXDimension].value != 1024 || exif[tagColorSpaceExif].value != 1 {
		t.Error("structural tags must survive copyright mode")
	}
}

func TestApplyPolicyRemoveLocation(t *testing.T) {
	m := richMeta().applyPolicy(Request{ExifMode: "all", RemoveLocation: true})
	payload := buildExifPayload(m)
	ifd0, exif := parseExifIn(t, payload, 0)
	if _, ok := ifd0[tagGPSIFD]; ok {
		t.Error("removeLocation must strip the GPS IFD")
	}
	if got := asciiAt(payload, 0, ifd0[tagMake]); got != "SONY" {
		t.Error("removeLocation must keep the camera fields")
	}
	if got := asciiAt(payload, 0, exif[tagLensModel]); got != m.Lens {
		t.Error("removeLocation must keep the lens")
	}
}

func TestApplyPolicyNone(t *testing.T) {
	m := richMeta().applyPolicy(Request{ExifMode: "none"})
	if !m.Disabled {
		t.Fatal("none mode must set Disabled")
	}

	jbuf := &bytes.Buffer{}
	if err := jpeg.Encode(jbuf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	if out := embedExifJPEG(jbuf.Bytes(), m); !bytes.Equal(out, jbuf.Bytes()) {
		t.Error("none mode must leave the JPEG byte-identical (no APP1)")
	}

	pbuf := &bytes.Buffer{}
	if err := png.Encode(pbuf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if out := embedExifPNG(pbuf.Bytes(), m); !bytes.Equal(out, pbuf.Bytes()) {
		t.Error("none mode must leave the PNG byte-identical (no eXIf)")
	}
}

func TestDMSRationalsCascade(t *testing.T) {
	// A value whose seconds round to exactly 60.0000 must cascade into the
	// minutes (and degrees) instead of emitting 600000/10000.
	got := dmsRationals(29.99999999999)
	want := [3][2]uint32{{30, 1}, {0, 1}, {0, 10000}}
	if got != want {
		t.Errorf("dmsRationals(29.99999999999) = %v, want %v", got, want)
	}
	got = dmsRationals(33.8688)
	want = [3][2]uint32{{33, 1}, {52, 1}, {76800, 10000}}
	if got != want {
		t.Errorf("dmsRationals(33.8688) = %v, want %v", got, want)
	}
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

func TestEncodeTIFF8FullMeta(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	m := richMeta()
	if err := encodeTIFF8(&buf, src, ICCFor("adobergb"), m); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// parseIFD asserts ascending tag order, which is the trap here: the GPS
	// pointer (34853) must land after the ICC profile (34675).
	fields := parseIFD(t, raw)
	if got := asciiAt(raw, 0, fields[tagArtist]); got != m.Artist {
		t.Errorf("TIFF Artist = %q, want %q", got, m.Artist)
	}
	if got := asciiAt(raw, 0, fields[tagCopyright]); got != m.Copyright {
		t.Errorf("TIFF Copyright = %q, want %q", got, m.Copyright)
	}
	if _, ok := fields[tagICCProfile]; !ok {
		t.Fatal("TIFF has no ICC profile")
	}
	checkGPSIFD(t, raw, 0, fields)
	exif := readIFDAt(t, raw, 0, fields[tagExifIFD].value)
	if got := asciiAt(raw, 0, exif[tagLensModel]); got != m.Lens {
		t.Errorf("TIFF LensModel = %q, want %q", got, m.Lens)
	}
	if _, err := tiff.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("decode with full metadata: %v", err)
	}
}

func TestEncodeTIFF8Disabled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	m := richMeta().applyPolicy(Request{ExifMode: "none"})
	if err := encodeTIFF8(&buf, src, ICCFor("adobergb"), m); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	fields := parseIFD(t, raw)
	for tag, name := range map[uint16]string{
		tagMake: "Make", tagModel: "Model", tagSoftware: "Software", tagDateTime: "DateTime",
		tagArtist: "Artist", tagCopyright: "Copyright", tagExifIFD: "Exif IFD", tagGPSIFD: "GPS IFD",
	} {
		if _, ok := fields[tag]; ok {
			t.Errorf("disabled metadata must drop %s from the TIFF", name)
		}
	}
	// The structural tags and the ICC profile describe the pixels and stay.
	if fields[tagImageWidth].value != 4 || fields[tagImageLength].value != 3 {
		t.Error("structural dimensions missing")
	}
	if _, ok := fields[tagICCProfile]; !ok {
		t.Error("ICC profile must survive disabled metadata")
	}
	if _, err := tiff.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("decode without metadata: %v", err)
	}
}
