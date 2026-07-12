package export

// Minimal EXIF for exports. The RAW's own maker-note-laden EXIF block never
// leaves LibRaw, so exports carry a small, clean block built from the catalog
// metadata instead: camera make/model, lens, capture time, exposure triangle,
// focal length, GPS, the rendered pixel dimensions, and the color-space flag
// — narrowed by the export's ExifMode (applyPolicy). JPEG gets it as an APP1
// segment, PNG as an eXIf chunk, and TIFF folds the same tags into its own
// IFD (see tiffwriter.go).

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"

	"github.com/marrasen/marraw/internal/store"
)

// EXIF/TIFF tags used here, beyond the baseline set in tiffwriter.go.
const (
	tagMake             = 271
	tagModel            = 272
	tagOrientation      = 274
	tagSoftware         = 305
	tagDateTime         = 306
	tagArtist           = 315
	tagCopyright        = 33432
	tagExifIFD          = 34665
	tagGPSIFD           = 34853
	tagExposureTime     = 33434
	tagFNumber          = 33437
	tagISO              = 34855
	tagExifVersion      = 36864
	tagDateTimeOriginal = 36867
	tagFocalLength      = 37386
	tagFlashpixVersion  = 40960
	tagColorSpaceExif   = 40961
	tagPixelXDimension  = 40962
	tagPixelYDimension  = 40963
	tagLensModel        = 42036
)

// GPS sub-IFD tags (tag numbering restarts inside the GPS IFD).
const (
	gpsTagVersionID    = 0
	gpsTagLatitudeRef  = 1
	gpsTagLatitude     = 2
	gpsTagLongitudeRef = 3
	gpsTagLongitude    = 4
	gpsTagAltitudeRef  = 5
	gpsTagAltitude     = 6
)

const (
	typByte  = 1
	typASCII = 2
)

const exifDateFormat = "2006:01:02 15:04:05"

// exifMeta is the catalog subset an export writes back out.
type exifMeta struct {
	Make, Model   string
	Lens          string
	TakenAt       int64   // unix seconds, 0 = unknown
	ISO           float64 // 0 = unknown, same for the rest
	Shutter       float64 // seconds
	Aperture      float64
	FocalLen      float64
	HasGPS        bool
	Lat, Lon      float64 // signed decimal degrees, S/W negative
	HasAlt        bool
	Alt           float64 // meters, negative = below sea level
	Artist        string  // user-entered credit (export dialog)
	Copyright     string
	Width, Height int // rendered output pixels
	SRGB          bool
	// Disabled means "write no EXIF at all": JPEG/PNG skip the block, TIFF
	// keeps only its structural main-IFD tags.
	Disabled bool
}

func exifFromPhoto(p store.Photo, outW, outH int, colorSpace string) exifMeta {
	m := exifMeta{
		Make: p.Make, Model: p.Model, Lens: p.Lens,
		TakenAt: p.TakenAt,
		ISO:     p.ISO, Shutter: p.Shutter, Aperture: p.Aperture, FocalLen: p.FocalLen,
		Width: outW, Height: outH,
		SRGB: colorSpace != "adobergb" && colorSpace != "prophoto",
	}
	if p.GPSLat.Valid && p.GPSLon.Valid {
		m.HasGPS = true
		m.Lat, m.Lon = p.GPSLat.Float64, p.GPSLon.Float64
		if p.GPSAlt.Valid {
			m.HasAlt, m.Alt = true, p.GPSAlt.Float64
		}
	}
	return m
}

// applyPolicy narrows the metadata to what req.ExifMode allows. The entry
// builders already treat zero values as absent, so stripping is zeroing;
// Width/Height/SRGB always survive — they describe the rendered pixels, not
// the shoot.
func (m exifMeta) applyPolicy(req Request) exifMeta {
	switch req.ExifMode {
	case "none":
		return exifMeta{Width: m.Width, Height: m.Height, SRGB: m.SRGB, Disabled: true}
	case "copyright":
		return exifMeta{
			Width: m.Width, Height: m.Height, SRGB: m.SRGB,
			Artist: req.Artist, Copyright: req.Copyright,
		}
	default: // "all", or empty from an older client
		m.Artist, m.Copyright = req.Artist, req.Copyright
		if req.RemoveLocation {
			m.HasGPS, m.HasAlt = false, false
			m.Lat, m.Lon, m.Alt = 0, 0, 0
		}
		return m
	}
}

// exifEntry is one IFD field. Out-of-line values carry their bytes in data;
// values that fit in four bytes are inline (SHORTs sit in the low two bytes
// under little-endian, exactly where the spec wants them).
type exifEntry struct {
	tag, typ uint16
	count    uint32
	inline   uint32
	data     []byte
}

func asciiEntry(tag uint16, s string) exifEntry {
	b := append([]byte(s), 0)
	e := exifEntry{tag: tag, typ: typASCII, count: uint32(len(b))}
	if len(b) <= 4 {
		var v [4]byte
		copy(v[:], b)
		e.inline = binary.LittleEndian.Uint32(v[:])
	} else {
		e.data = b
	}
	return e
}

func shortEntry(tag uint16, v uint16) exifEntry {
	return exifEntry{tag: tag, typ: typShort, count: 1, inline: uint32(v)}
}

func longEntry(tag uint16, v uint32) exifEntry {
	return exifEntry{tag: tag, typ: typLong, count: 1, inline: v}
}

func rationalEntry(tag uint16, num, den uint32) exifEntry {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, num)
	binary.LittleEndian.PutUint32(b[4:], den)
	return exifEntry{tag: tag, typ: typRational, count: 1, data: b}
}

func undefinedEntry(tag uint16, v [4]byte) exifEntry {
	return exifEntry{tag: tag, typ: typUndefined, count: 4, inline: binary.LittleEndian.Uint32(v[:])}
}

// byteEntry is count BYTEs, inline (count <= 4 everywhere it's used).
func byteEntry(tag uint16, v [4]byte, count uint32) exifEntry {
	return exifEntry{tag: tag, typ: typByte, count: count, inline: binary.LittleEndian.Uint32(v[:])}
}

func rationals3Entry(tag uint16, vals [3][2]uint32) exifEntry {
	b := make([]byte, 24)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(b[i*8:], v[0])
		binary.LittleEndian.PutUint32(b[i*8+4:], v[1])
	}
	return exifEntry{tag: tag, typ: typRational, count: 3, data: b}
}

// ifdSize is the serialized size of an IFD block: entry table, next-IFD
// pointer, and the out-of-line data (each value word-aligned).
func ifdSize(entries []exifEntry) uint32 {
	size := uint32(2 + 12*len(entries) + 4)
	for _, e := range entries {
		if e.data != nil {
			size = even(size + uint32(len(e.data)))
		}
	}
	return size
}

// writeIFD serializes one IFD at absolute file offset base (offsets inside a
// TIFF are absolute), placing out-of-line data right after the table.
func writeIFD(buf *bytes.Buffer, entries []exifEntry, base uint32) {
	le := binary.LittleEndian
	dataOff := base + uint32(2+12*len(entries)+4)
	writeU16(buf, le, uint16(len(entries)))
	var data bytes.Buffer
	for _, e := range entries {
		writeU16(buf, le, e.tag)
		writeU16(buf, le, e.typ)
		writeU32(buf, le, e.count)
		if e.data == nil {
			writeU32(buf, le, e.inline)
			continue
		}
		writeU32(buf, le, dataOff)
		data.Write(e.data)
		if len(e.data)%2 == 1 {
			data.WriteByte(0)
		}
		dataOff = even(dataOff + uint32(len(e.data)))
	}
	writeU32(buf, le, 0) // no next IFD
	buf.Write(data.Bytes())
}

// exifIFDEntries builds the Exif sub-IFD (the photographic fields), ascending
// by tag as the spec requires. Unknown values are simply absent.
func exifIFDEntries(m exifMeta) []exifEntry {
	var out []exifEntry
	if m.Shutter > 0 {
		if m.Shutter < 1 {
			out = append(out, rationalEntry(tagExposureTime, 1, uint32(math.Round(1/m.Shutter))))
		} else {
			out = append(out, rationalEntry(tagExposureTime, uint32(math.Round(m.Shutter*10)), 10))
		}
	}
	if m.Aperture > 0 {
		out = append(out, rationalEntry(tagFNumber, uint32(math.Round(m.Aperture*100)), 100))
	}
	if m.ISO > 0 {
		out = append(out, shortEntry(tagISO, uint16(min(math.Round(m.ISO), 65535))))
	}
	out = append(out, undefinedEntry(tagExifVersion, [4]byte{'0', '2', '3', '2'}))
	if m.TakenAt > 0 {
		out = append(out, asciiEntry(tagDateTimeOriginal, time.Unix(m.TakenAt, 0).Format(exifDateFormat)))
	}
	if m.FocalLen > 0 {
		out = append(out, rationalEntry(tagFocalLength, uint32(math.Round(m.FocalLen*100)), 100))
	}
	out = append(out, undefinedEntry(tagFlashpixVersion, [4]byte{'0', '1', '0', '0'}))
	cs := uint16(0xFFFF) // uncalibrated: the real space is in the ICC profile
	if m.SRGB {
		cs = 1
	}
	out = append(out,
		shortEntry(tagColorSpaceExif, cs),
		longEntry(tagPixelXDimension, uint32(m.Width)),
		longEntry(tagPixelYDimension, uint32(m.Height)),
	)
	if m.Lens != "" {
		out = append(out, asciiEntry(tagLensModel, m.Lens))
	}
	return out
}

// gpsIFDEntries builds the GPS sub-IFD, ascending by tag. Coordinates are
// stored as signed decimal degrees and written back as the spec's
// hemisphere-letter + deg/min/sec rational form.
func gpsIFDEntries(m exifMeta) []exifEntry {
	latRef, lat := "N", m.Lat
	if lat < 0 {
		latRef, lat = "S", -lat
	}
	lonRef, lon := "E", m.Lon
	if lon < 0 {
		lonRef, lon = "W", -lon
	}
	out := []exifEntry{
		byteEntry(gpsTagVersionID, [4]byte{2, 3, 0, 0}, 4),
		asciiEntry(gpsTagLatitudeRef, latRef),
		rationals3Entry(gpsTagLatitude, dmsRationals(lat)),
		asciiEntry(gpsTagLongitudeRef, lonRef),
		rationals3Entry(gpsTagLongitude, dmsRationals(lon)),
	}
	if m.HasAlt {
		altRef, alt := byte(0), m.Alt
		if alt < 0 {
			altRef, alt = 1, -alt // 1 = below sea level
		}
		out = append(out,
			byteEntry(gpsTagAltitudeRef, [4]byte{altRef}, 1),
			rationalEntry(gpsTagAltitude, uint32(math.Round(alt*100)), 100),
		)
	}
	return out
}

// dmsRationals splits |degrees| into deg/min/sec rationals; seconds carry
// four decimals (~3 mm at the equator). Rounding can tip seconds to exactly
// 60, which cascades up rather than emitting an out-of-range component.
func dmsRationals(v float64) [3][2]uint32 {
	deg := math.Floor(v)
	rem := (v - deg) * 60
	minutes := math.Floor(rem)
	sec := math.Round((rem - minutes) * 60 * 10000)
	if sec >= 600000 {
		sec = 0
		if minutes++; minutes >= 60 {
			minutes = 0
			deg++
		}
	}
	return [3][2]uint32{{uint32(deg), 1}, {uint32(minutes), 1}, {uint32(sec), 10000}}
}

// ifd0Entries builds the top-level camera/file fields, ascending by tag.
// exifPtr and gpsPtr are the absolute offsets of the sub-IFDs; gpsPtr 0 means
// no GPS IFD.
func ifd0Entries(m exifMeta, exifPtr, gpsPtr uint32) []exifEntry {
	var out []exifEntry
	if m.Make != "" {
		out = append(out, asciiEntry(tagMake, m.Make))
	}
	if m.Model != "" {
		out = append(out, asciiEntry(tagModel, m.Model))
	}
	// Pixels are fully oriented (and rotated/mirrored) by the render.
	out = append(out, shortEntry(tagOrientation, 1))
	out = append(out, asciiEntry(tagSoftware, "marraw"))
	if m.TakenAt > 0 {
		out = append(out, asciiEntry(tagDateTime, time.Unix(m.TakenAt, 0).Format(exifDateFormat)))
	}
	if m.Artist != "" {
		out = append(out, asciiEntry(tagArtist, m.Artist))
	}
	if m.Copyright != "" {
		out = append(out, asciiEntry(tagCopyright, m.Copyright))
	}
	out = append(out, longEntry(tagExifIFD, exifPtr))
	if gpsPtr != 0 {
		out = append(out, longEntry(tagGPSIFD, gpsPtr))
	}
	return out
}

// buildExifPayload assembles a standalone TIFF-format EXIF block ("II"
// header, IFD0, Exif IFD, GPS IFD when there's a fix) — the payload of a JPEG
// APP1 segment (after the "Exif\0\0" identifier) or a PNG eXIf chunk (as is).
func buildExifPayload(m exifMeta) []byte {
	const headerLen = 8
	exifEntriesList := exifIFDEntries(m)
	var gpsList []exifEntry
	if m.HasGPS {
		gpsList = gpsIFDEntries(m)
	}
	// IFD0's size depends only on entry count and data lengths, so build it
	// once with placeholder pointers to learn where the sub-IFDs land. The GPS
	// placeholder must be nonzero when a GPS IFD exists, or the sizing pass
	// misses its pointer entry and every offset lands 12 bytes short.
	gpsPlaceholder := uint32(0)
	if m.HasGPS {
		gpsPlaceholder = 1
	}
	ifd0 := ifd0Entries(m, 0, gpsPlaceholder)
	exifPtr := headerLen + ifdSize(ifd0)
	gpsPtr := uint32(0)
	if m.HasGPS {
		gpsPtr = exifPtr + ifdSize(exifEntriesList)
	}
	ifd0 = ifd0Entries(m, exifPtr, gpsPtr)

	buf := &bytes.Buffer{}
	le := binary.LittleEndian
	buf.WriteString("II")
	writeU16(buf, le, 42)
	writeU32(buf, le, headerLen)
	writeIFD(buf, ifd0, headerLen)
	padTo(buf, exifPtr)
	writeIFD(buf, exifEntriesList, exifPtr)
	if m.HasGPS {
		padTo(buf, gpsPtr)
		writeIFD(buf, gpsList, gpsPtr)
	}
	return buf.Bytes()
}

// embedExifJPEG splices an APP1 Exif segment right after the SOI marker.
// Call it after the ICC splice so APP1 ends up first, the order EXIF-aware
// readers expect.
func embedExifJPEG(jpg []byte, m exifMeta) []byte {
	if m.Disabled || len(jpg) < 2 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return jpg
	}
	payload := buildExifPayload(m)
	const header = "Exif\x00\x00"
	segLen := 2 + len(header) + len(payload)
	out := make([]byte, 0, len(jpg)+2+segLen)
	out = append(out, 0xFF, 0xD8, 0xFF, 0xE1)
	out = append(out, byte(segLen>>8), byte(segLen))
	out = append(out, header...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

// embedExifPNG splices an eXIf chunk right after IHDR. The chunk carries the
// TIFF-format block directly — no JPEG-style identifier, per the PNG spec.
func embedExifPNG(pngData []byte, m exifMeta) []byte {
	const ihdrEnd = 8 + 25
	if m.Disabled || len(pngData) < ihdrEnd {
		return pngData
	}
	payload := buildExifPayload(m)
	chunkLen := make([]byte, 4)
	binary.BigEndian.PutUint32(chunkLen, uint32(len(payload)))
	crcData := append([]byte("eXIf"), payload...)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc32PNG(crcData))

	out := make([]byte, 0, len(pngData)+4+len(crcData)+4)
	out = append(out, pngData[:ihdrEnd]...)
	out = append(out, chunkLen...)
	out = append(out, crcData...)
	out = append(out, crcBytes...)
	return append(out, pngData[ihdrEnd:]...)
}
