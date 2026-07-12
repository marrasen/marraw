package export

// A minimal baseline TIFF writer.
//
// golang.org/x/image/tiff can encode, but not the file marraw wants to hand to
// another editor: it has no path for 3-channel RGB (every RGB image picks up a
// fourth, fully opaque sample tagged as associated alpha) and no way to embed
// an ICC profile. Untagged Adobe RGB or ProPhoto pixels are read as sRGB by
// whatever opens them, which silently wrecks the colour. Both matter for an
// export whose entire purpose is fidelity, so we emit the IFD ourselves.
//
// The output is a single-strip, Deflate-compressed, horizontally-predicted
// 8-bit RGB TIFF — the shape Photoshop and friends write, and well inside what
// any baseline reader handles.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"time"
)

// TIFF tags, in the ascending order an IFD must list them.
const (
	tagImageWidth      = 256
	tagImageLength     = 257
	tagBitsPerSample   = 258
	tagCompression     = 259
	tagPhotometric     = 262
	tagStripOffsets    = 273
	tagSamplesPerPixel = 277
	tagRowsPerStrip    = 278
	tagStripByteCounts = 279
	tagXResolution     = 282
	tagYResolution     = 283
	tagPlanarConfig    = 284
	tagResolutionUnit  = 296
	tagPredictor       = 317
	tagICCProfile      = 34675
)

// Field types.
const (
	typShort     = 3
	typLong      = 4
	typRational  = 5
	typUndefined = 7
)

const (
	compressionDeflate  = 8 // zlib, the modern "Adobe Deflate" value
	photometricRGB      = 2
	planarChunky        = 1
	predictorHorizontal = 2
	resolutionUnitInch  = 2
)

type ifdEntry struct {
	tag, typ uint16
	count    uint32
	// value holds the field inline when it fits in four bytes, otherwise the
	// offset of the field's data. Little-endian writes put a SHORT in the low
	// two bytes, which is exactly where the spec wants it.
	value uint32
}

// encodeTIFF8 writes img as an 8-bit RGB TIFF. The alpha channel is dropped
// (export renders are opaque). A nil icc leaves the file untagged, which is
// the right thing for sRGB. The catalog metadata rides along as the same
// EXIF tags the JPEG/PNG exports carry: camera strings and DateTime in the
// main IFD, the photographic fields in an Exif sub-IFD.
func encodeTIFF8(w io.Writer, img *image.RGBA, icc []byte, meta exifMeta) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= 0 || height <= 0 {
		return errors.New("export: tiff: zero-size image")
	}

	strip, err := deflateRGB(img)
	if err != nil {
		return err
	}

	// The out-of-line metadata strings (NUL-terminated, may be absent), split
	// by where their tags sort in the main IFD: Make/Model (271/272) before
	// StripOffsets (273); Software/DateTime/Artist (305/306/315) between
	// ResolutionUnit (296) and Predictor (317); Copyright (33432) between
	// Predictor and the Exif IFD pointer (34665). Disabled mode drops them
	// all — the main IFD keeps only what describes the pixels.
	type asciiField struct {
		tag uint16
		val string
		off uint32
	}
	var preStrip, postRes []*asciiField
	if !meta.Disabled {
		preStrip = []*asciiField{
			{tag: tagMake, val: meta.Make},
			{tag: tagModel, val: meta.Model},
		}
		postRes = []*asciiField{{tag: tagSoftware, val: "marraw"}}
		if meta.TakenAt > 0 {
			postRes = append(postRes, &asciiField{tag: tagDateTime, val: time.Unix(meta.TakenAt, 0).Format(exifDateFormat)})
		}
		if meta.Artist != "" {
			postRes = append(postRes, &asciiField{tag: tagArtist, val: meta.Artist})
		}
		if meta.Copyright != "" {
			postRes = append(postRes, &asciiField{tag: tagCopyright, val: meta.Copyright})
		}
	}
	strs := append(append([]*asciiField{}, preStrip...), postRes...)

	// Everything but the IFD is laid out first, so entry offsets are known by
	// the time the entries are written. Field data must start on a word
	// boundary.
	const headerLen = 8
	pos := uint32(headerLen)
	stripOff := pos
	pos = even(pos + uint32(len(strip)))
	bpsOff := pos
	pos += 6 // three SHORTs
	xresOff := pos
	pos += 8 // one RATIONAL
	yresOff := pos
	pos += 8
	for _, s := range strs {
		if s.val == "" {
			continue
		}
		s.off = pos
		pos = even(pos + uint32(len(s.val)) + 1)
	}
	var exifEntriesList, gpsList []exifEntry
	var exifOff, gpsOff uint32
	if !meta.Disabled {
		exifEntriesList = exifIFDEntries(meta)
		exifOff = pos
		pos = even(pos + ifdSize(exifEntriesList))
		if meta.HasGPS {
			gpsList = gpsIFDEntries(meta)
			gpsOff = pos
			pos = even(pos + ifdSize(gpsList))
		}
	}
	var iccOff uint32
	if len(icc) > 0 {
		iccOff = pos
		pos = even(pos + uint32(len(icc)))
	}
	ifdOff := pos

	strEntry := func(s *asciiField) ifdEntry {
		return ifdEntry{s.tag, typASCII, uint32(len(s.val)) + 1, s.off}
	}
	entries := []ifdEntry{
		{tagImageWidth, typLong, 1, uint32(width)},
		{tagImageLength, typLong, 1, uint32(height)},
		{tagBitsPerSample, typShort, 3, bpsOff},
		{tagCompression, typShort, 1, compressionDeflate},
		{tagPhotometric, typShort, 1, photometricRGB},
	}
	for _, s := range preStrip {
		if s.val != "" {
			entries = append(entries, strEntry(s))
		}
	}
	entries = append(entries,
		ifdEntry{tagStripOffsets, typLong, 1, stripOff},
		ifdEntry{tagOrientation, typShort, 1, 1},
		ifdEntry{tagSamplesPerPixel, typShort, 1, 3},
		ifdEntry{tagRowsPerStrip, typLong, 1, uint32(height)},
		ifdEntry{tagStripByteCounts, typLong, 1, uint32(len(strip))},
		ifdEntry{tagXResolution, typRational, 1, xresOff},
		ifdEntry{tagYResolution, typRational, 1, yresOff},
		ifdEntry{tagPlanarConfig, typShort, 1, planarChunky},
		ifdEntry{tagResolutionUnit, typShort, 1, resolutionUnitInch},
	)
	for _, s := range postRes {
		if s.val != "" && s.tag != tagCopyright {
			entries = append(entries, strEntry(s))
		}
	}
	entries = append(entries, ifdEntry{tagPredictor, typShort, 1, predictorHorizontal})
	for _, s := range postRes {
		if s.val != "" && s.tag == tagCopyright {
			entries = append(entries, strEntry(s))
		}
	}
	if !meta.Disabled {
		entries = append(entries, ifdEntry{tagExifIFD, typLong, 1, exifOff})
	}
	if len(icc) > 0 {
		entries = append(entries, ifdEntry{tagICCProfile, typUndefined, uint32(len(icc)), iccOff})
	}
	// The GPS pointer (34853) sorts after the ICC profile (34675), so it is
	// the last entry — counterintuitive but required for ascending tag order.
	if gpsOff != 0 {
		entries = append(entries, ifdEntry{tagGPSIFD, typLong, 1, gpsOff})
	}

	le := binary.LittleEndian
	buf := bytes.NewBuffer(make([]byte, 0, int(ifdOff)+2+12*len(entries)+4))

	buf.WriteString("II")     // little-endian byte order
	writeU16(buf, le, 42)     // magic: "this is a TIFF"
	writeU32(buf, le, ifdOff) // where the tags live

	buf.Write(strip)
	padTo(buf, bpsOff)
	for range 3 {
		writeU16(buf, le, 8) // BitsPerSample, one per channel
	}
	padTo(buf, xresOff)
	writeU32(buf, le, 72) // XResolution = 72/1 dpi
	writeU32(buf, le, 1)
	padTo(buf, yresOff)
	writeU32(buf, le, 72)
	writeU32(buf, le, 1)
	for _, s := range strs {
		if s.val == "" {
			continue
		}
		padTo(buf, s.off)
		buf.WriteString(s.val)
		buf.WriteByte(0)
	}
	if !meta.Disabled {
		padTo(buf, exifOff)
		writeIFD(buf, exifEntriesList, exifOff)
		if gpsOff != 0 {
			padTo(buf, gpsOff)
			writeIFD(buf, gpsList, gpsOff)
		}
	}
	if len(icc) > 0 {
		padTo(buf, iccOff)
		buf.Write(icc)
	}
	padTo(buf, ifdOff)

	writeU16(buf, le, uint16(len(entries)))
	for _, e := range entries {
		writeU16(buf, le, e.tag)
		writeU16(buf, le, e.typ)
		writeU32(buf, le, e.count)
		writeU32(buf, le, e.value)
	}
	writeU32(buf, le, 0) // no next IFD

	_, err = w.Write(buf.Bytes())
	return err
}

// deflateRGB drops alpha, applies horizontal differencing, and zlib-compresses
// the whole image as one strip. Differencing before Deflate is what makes a
// photographic TIFF compress at all: neighbouring pixels are close, so the
// deltas cluster near zero.
func deflateRGB(img *image.RGBA) ([]byte, error) {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()

	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	row := make([]byte, 3*width)
	for y := range height {
		src := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := range width {
			row[3*x+0] = src[4*x+0]
			row[3*x+1] = src[4*x+1]
			row[3*x+2] = src[4*x+2]
		}
		// Right to left: each sample becomes its delta from the same channel
		// of the pixel to its left. Wrapping subtraction is intended — the
		// decoder's wrapping addition undoes it exactly.
		for x := width - 1; x >= 1; x-- {
			row[3*x+0] -= row[3*(x-1)+0]
			row[3*x+1] -= row[3*(x-1)+1]
			row[3*x+2] -= row[3*(x-1)+2]
		}
		if _, err := zw.Write(row); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func even(n uint32) uint32 {
	if n%2 == 1 {
		return n + 1
	}
	return n
}

func padTo(buf *bytes.Buffer, off uint32) {
	for uint32(buf.Len()) < off {
		buf.WriteByte(0)
	}
}

func writeU16(buf *bytes.Buffer, le binary.ByteOrder, v uint16) {
	var b [2]byte
	le.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, le binary.ByteOrder, v uint32) {
	var b [4]byte
	le.PutUint32(b[:], v)
	buf.Write(b[:])
}
