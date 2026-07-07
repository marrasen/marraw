package export

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildICC(t *testing.T) {
	for _, space := range []string{"adobergb", "prophoto"} {
		icc := ICCFor(space)
		if icc == nil {
			t.Fatalf("%s: nil profile", space)
		}
		if got := binary.BigEndian.Uint32(icc); int(got) != len(icc) {
			t.Errorf("%s: header size %d != actual %d", space, got, len(icc))
		}
		if string(icc[36:40]) != "acsp" {
			t.Errorf("%s: missing acsp signature", space)
		}
		count := binary.BigEndian.Uint32(icc[128:])
		if count != 9 {
			t.Fatalf("%s: tag count %d, want 9", space, count)
		}
		seen := map[string]bool{}
		var trcOff [3]uint32
		for i := 0; i < int(count); i++ {
			e := icc[132+12*i:]
			sig := string(e[:4])
			off := binary.BigEndian.Uint32(e[4:])
			size := binary.BigEndian.Uint32(e[8:])
			if int(off+size) > len(icc) {
				t.Errorf("%s: tag %s overruns profile (%d+%d > %d)", space, sig, off, size, len(icc))
			}
			if off%4 != 0 {
				t.Errorf("%s: tag %s misaligned offset %d", space, sig, off)
			}
			seen[sig] = true
			switch sig {
			case "rTRC":
				trcOff[0] = off
			case "gTRC":
				trcOff[1] = off
			case "bTRC":
				trcOff[2] = off
			}
		}
		for _, want := range []string{"desc", "cprt", "wtpt", "rXYZ", "gXYZ", "bXYZ", "rTRC", "gTRC", "bTRC"} {
			if !seen[want] {
				t.Errorf("%s: missing tag %s", space, want)
			}
		}
		// The three TRCs must share one curve blob (deduped offsets).
		if trcOff[0] != trcOff[1] || trcOff[1] != trcOff[2] {
			t.Errorf("%s: TRC offsets not shared: %v", space, trcOff)
		}
	}
	if ICCFor("srgb") != nil || ICCFor("") != nil {
		t.Error("sRGB must stay untagged")
	}
}

func TestEmbedICCJPEG(t *testing.T) {
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x02, 0xFF, 0xD9}
	icc := ICCFor("adobergb")
	out := embedICCJPEG(jpg, icc)
	if !bytes.HasPrefix(out, []byte{0xFF, 0xD8, 0xFF, 0xE2}) {
		t.Fatal("APP2 not spliced after SOI")
	}
	segLen := int(out[4])<<8 | int(out[5])
	if segLen != 2+14+len(icc) {
		t.Errorf("segment length %d, want %d", segLen, 2+14+len(icc))
	}
	if !bytes.Contains(out[:30], []byte("ICC_PROFILE\x00")) {
		t.Error("missing ICC_PROFILE header")
	}
	if !bytes.HasSuffix(out, jpg[2:]) {
		t.Error("original stream not preserved after the spliced segment")
	}
}
