package export

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/marrasen/marraw/internal/libraw"
)

// Wide-gamut export: LibRaw converts to the requested primaries
// (output_color) and the JPEG gets a matching ICC profile embedded so
// color-managed viewers interpret the file correctly. The tone curve stays
// the pipeline's BT.709-style encoding (the same curve previews use), so
// the profile's TRC is that curve — not the space's nominal gamma.

// ColorSpaceOutput maps a request color space to LibRaw's output_color.
func ColorSpaceOutput(space string) int {
	switch space {
	case "adobergb":
		return libraw.ColorAdobe
	case "prophoto":
		return libraw.ColorProPhoto
	default:
		return libraw.ColorSRGB
	}
}

// ICCFor returns the profile to embed for a color space; nil for sRGB
// (untagged JPEG is interpreted as sRGB everywhere).
func ICCFor(space string) []byte {
	switch space {
	case "adobergb":
		return buildICC("marraw Adobe RGB (1998) compatible", adobeRGB)
	case "prophoto":
		return buildICC("marraw ProPhoto RGB compatible", proPhoto)
	default:
		return nil
	}
}

type xyz struct{ x, y, z float64 }

type primaries struct{ r, g, b xyz }

// D50-adapted colorant values from the published profile specifications.
var (
	adobeRGB = primaries{
		r: xyz{0.60974, 0.31111, 0.01947},
		g: xyz{0.20528, 0.62567, 0.06087},
		b: xyz{0.14919, 0.06322, 0.74457},
	}
	proPhoto = primaries{
		r: xyz{0.79767, 0.28804, 0.00000},
		g: xyz{0.13519, 0.71188, 0.00000},
		b: xyz{0.03134, 0.00009, 0.82491},
	}
	d50 = xyz{0.96420, 1.00000, 0.82491}
)

func s15(v float64) uint32 { return uint32(int32(math.Round(v * 65536))) }

func xyzTag(v xyz) []byte {
	b := make([]byte, 20)
	copy(b, "XYZ ")
	binary.BigEndian.PutUint32(b[8:], s15(v.x))
	binary.BigEndian.PutUint32(b[12:], s15(v.y))
	binary.BigEndian.PutUint32(b[16:], s15(v.z))
	return b
}

// trcTag is the shared tone curve: a 1024-entry table of the inverse
// BT.709-style transfer LibRaw encodes with (power 1/0.45, toe slope 4.5).
func trcTag() []byte {
	const n = 1024
	b := make([]byte, 12+2*n)
	copy(b, "curv")
	binary.BigEndian.PutUint32(b[8:], n)
	for i := 0; i < n; i++ {
		v := float64(i) / (n - 1)
		var lin float64
		if v < 0.081 {
			lin = v / 4.5
		} else {
			lin = math.Pow((v+0.099)/1.099, 1/0.45)
		}
		binary.BigEndian.PutUint16(b[12+2*i:], uint16(math.Round(lin*65535)))
	}
	return b
}

func descTag(text string) []byte {
	// textDescriptionType: sig+reserved, ASCII count (incl NUL) + string,
	// empty Unicode block (4+4), ScriptCode block (2+1+67).
	n := len(text) + 1
	b := make([]byte, 12+n+8+70)
	copy(b, "desc")
	binary.BigEndian.PutUint32(b[8:], uint32(n))
	copy(b[12:], text)
	return b
}

func textTag(text string) []byte {
	b := make([]byte, 8+len(text)+1)
	copy(b, "text")
	copy(b[8:], text)
	return b
}

// buildICC assembles a minimal v2 matrix/TRC display profile.
func buildICC(name string, p primaries) []byte {
	type tag struct {
		sig  string
		data []byte
	}
	trc := trcTag()
	tags := []tag{
		{"desc", descTag(name)},
		{"cprt", textTag("no copyright, use freely")},
		{"wtpt", xyzTag(d50)},
		{"rXYZ", xyzTag(p.r)},
		{"gXYZ", xyzTag(p.g)},
		{"bXYZ", xyzTag(p.b)},
		{"rTRC", trc},
		{"gTRC", trc}, // identical bytes → deduped to one offset below
		{"bTRC", trc},
	}

	// Layout: 128-byte header, tag count, tag table, tag data (4-aligned).
	tableSize := 4 + 12*len(tags)
	offset := 128 + tableSize
	offsets := make([]int, len(tags))
	sizes := make([]int, len(tags))
	dataBuf := &bytes.Buffer{}
	seen := map[*byte]int{} // dedupe identical slices (the shared TRC)
	for i, t := range tags {
		if len(t.data) > 0 {
			if off, ok := seen[&t.data[0]]; ok {
				offsets[i] = off
				sizes[i] = len(t.data)
				continue
			}
		}
		// 4-byte alignment for each element.
		for dataBuf.Len()%4 != 0 {
			dataBuf.WriteByte(0)
		}
		offsets[i] = offset + dataBuf.Len()
		sizes[i] = len(t.data)
		if len(t.data) > 0 {
			seen[&t.data[0]] = offsets[i]
		}
		dataBuf.Write(t.data)
	}

	total := offset + dataBuf.Len()
	out := make([]byte, 128, total)
	binary.BigEndian.PutUint32(out[0:], uint32(total))
	binary.BigEndian.PutUint32(out[8:], 0x02400000) // version 2.4
	copy(out[12:], "mntr")
	copy(out[16:], "RGB ")
	copy(out[20:], "XYZ ")
	// Header date left zero (deterministic output beats a timestamp here).
	copy(out[36:], "acsp")
	// PCS illuminant: D50.
	binary.BigEndian.PutUint32(out[68:], 0x0000F6D6)
	binary.BigEndian.PutUint32(out[72:], 0x00010000)
	binary.BigEndian.PutUint32(out[76:], 0x0000D32D)

	table := make([]byte, tableSize)
	binary.BigEndian.PutUint32(table, uint32(len(tags)))
	for i, t := range tags {
		e := table[4+12*i:]
		copy(e, t.sig)
		binary.BigEndian.PutUint32(e[4:], uint32(offsets[i]))
		binary.BigEndian.PutUint32(e[8:], uint32(sizes[i]))
	}
	out = append(out, table...)
	out = append(out, dataBuf.Bytes()...)
	return out
}

// embedICCJPEG splices an APP2 ICC_PROFILE segment right after the SOI
// marker of an encoded JPEG. Profiles here are far below the 64KB segment
// cap, so a single chunk (1 of 1) always suffices.
func embedICCJPEG(jpg, icc []byte) []byte {
	if icc == nil || len(jpg) < 2 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return jpg
	}
	const header = "ICC_PROFILE\x00"
	segLen := 2 + len(header) + 2 + len(icc)
	out := make([]byte, 0, len(jpg)+4+segLen)
	out = append(out, 0xFF, 0xD8, 0xFF, 0xE2)
	out = append(out, byte(segLen>>8), byte(segLen))
	out = append(out, header...)
	out = append(out, 1, 1) // chunk 1 of 1
	out = append(out, icc...)
	out = append(out, jpg[2:]...)
	return out
}
