package xmp

import (
	"bytes"
	"encoding/xml"
	"flag"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fullParams populates every crs-mapped field with a non-zero value.
func fullParams() *edit.Params {
	p := &edit.Params{
		ExpEV: 0.5, WBMode: edit.WBKelvin, WBKelvin: 5500, WBTint: 0.2,
		NRThreshold: 250,
		Contrast:    0.25, Whites: 0.1, Blacks: -0.1, ToneShadows: 0.3, ToneHighlights: -0.4,
		Saturation: -0.15, Vibrance: 0.35,
		SplitShadowHue: 210, SplitShadowAmt: 0.3, SplitHighlightHue: 45, SplitHighlightAmt: 0.2,
		Vignette: 0.4,
		Texture:  0.1, Clarity: 0.2, Dehaze: -0.1, Sharpen: 0.5,
		Rotate: 1, FlipH: true,
		CropX: 0.1, CropY: 0.2, CropW: 0.5, CropH: 0.4, CropAngle: 1.5,
	}
	for i := range p.HSLHue {
		p.HSLHue[i] = float64(i-4) / 10
		p.HSLSat[i] = float64(i-3) / 10
		p.HSLLum[i] = float64(4-i) / 10
	}
	return p
}

func TestBuildFullGolden(t *testing.T) {
	got := Build(Meta{Rating: 4, Flag: 1, Orientation: 6}, fullParams())
	golden := filepath.Join("testdata", "full.xmp")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden missing (run with -update to create): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch (re-run with -update after intentional changes)\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildIsWellFormedXML(t *testing.T) {
	dec := xml.NewDecoder(bytes.NewReader(Build(Meta{Rating: 4, Flag: 1, Orientation: 6}, fullParams())))
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				return
			}
			t.Fatalf("output is not well-formed XML: %v", err)
		}
	}
}

func TestBuildNeutralOmitsCrs(t *testing.T) {
	for _, e := range []*edit.Params{nil, {}} {
		got := string(Build(Meta{Rating: 3, Orientation: 6}, e))
		if !strings.Contains(got, `xmp:Rating="3"`) {
			t.Fatalf("rating missing: %s", got)
		}
		if strings.Contains(got, "crs:") || strings.Contains(got, "tiff:") {
			t.Fatalf("neutral edit must not emit crs/tiff blocks: %s", got)
		}
		if strings.Contains(got, "xmp:Label") {
			t.Fatalf("flag none must omit the label: %s", got)
		}
	}
}

func TestPathFor(t *testing.T) {
	if got := PathFor(`D:\shoot\IMG1234.ARW`); got != `D:\shoot\IMG1234.xmp` {
		t.Fatalf("PathFor: %s", got)
	}
}

// TestBuildConversions pins the field-level value mappings, including the
// sign flips (vignette, crop angle) that are easy to regress silently.
func TestBuildConversions(t *testing.T) {
	cases := []struct {
		name    string
		meta    Meta
		params  edit.Params
		want    []string
		notWant []string
	}{
		{
			name:   "vignette darkens with negated sign",
			params: edit.Params{Vignette: 0.5},
			want:   []string{`crs:PostCropVignetteAmount="-50"`, `crs:PostCropVignetteMidpoint="50"`},
		},
		{
			name:    "no vignette block when zero",
			params:  edit.Params{Contrast: -0.25},
			want:    []string{`crs:Contrast2012="-25"`},
			notWant: []string{"PostCropVignette"},
		},
		{
			name:   "nr threshold scales to 0..100",
			params: edit.Params{NRThreshold: 250},
			want:   []string{`crs:LuminanceSmoothing="25"`},
		},
		{
			name:   "kelvin white balance",
			params: edit.Params{WBMode: edit.WBKelvin, WBKelvin: 5500, WBTint: 0.5},
			want:   []string{`crs:WhiteBalance="Custom"`, `crs:Temperature="5500"`, `crs:Tint="+75"`},
		},
		{
			name:    "auto white balance",
			params:  edit.Params{WBMode: edit.WBAuto, Contrast: 0.1},
			want:    []string{`crs:WhiteBalance="Auto"`},
			notWant: []string{"crs:Temperature"},
		},
		{
			name:    "custom multipliers fall back to as shot",
			params:  edit.Params{WBMode: edit.WBCustom, WBMul: [4]float64{2, 1, 1.5, 1}},
			want:    []string{`crs:WhiteBalance="As Shot"`},
			notWant: []string{"crs:Temperature"},
		},
		{
			name:   "exposure keeps two decimals",
			params: edit.Params{ExpEV: 0.5},
			want:   []string{`crs:Exposure2012="+0.50"`},
		},
		{
			name:   "sharpen scales to 150 with adobe companions",
			params: edit.Params{Sharpen: 0.5},
			want:   []string{`crs:Sharpness="75"`, `crs:SharpenRadius="+1.0"`, `crs:SharpenDetail="25"`},
		},
		{
			name:   "hsl band naming",
			params: edit.Params{HSLHue: [8]float64{0, 0.3, 0, 0, 0, -0.5, 0, 0}},
			want:   []string{`crs:HueAdjustmentOrange="+30"`, `crs:HueAdjustmentBlue="-50"`, `crs:HueAdjustmentRed="0"`},
		},
		{
			name:   "rating and pick flag",
			meta:   Meta{Rating: 5, Flag: 1},
			params: edit.Params{ExpEV: 0.1},
			want:   []string{`xmp:Rating="5"`, `xmp:Label="Green"`},
		},
		{
			name:   "exclude flag",
			meta:   Meta{Flag: -1},
			params: edit.Params{ExpEV: 0.1},
			want:   []string{`xmp:Label="Red"`},
		},
		{
			name: "crop maps through inverse orientation 6",
			meta: Meta{Orientation: 6},
			// Display space (after the base 90° CW): x 0.1..0.6, y 0.2..0.6.
			params: edit.Params{CropX: 0.1, CropY: 0.2, CropW: 0.5, CropH: 0.4, CropAngle: 2},
			want: []string{
				`crs:HasCrop="True"`,
				`crs:CropLeft="0.200000"`,
				`crs:CropTop="0.400000"`,
				`crs:CropRight="0.600000"`,
				`crs:CropBottom="0.900000"`,
				`crs:CropAngle="-2.000000"`,
			},
			// The user did not rotate, so the RAW's own orientation stands.
			notWant: []string{"tiff:Orientation"},
		},
		{
			name:   "angle-only straighten uses the full frame",
			params: edit.Params{CropAngle: 1.5},
			want:   []string{`crs:HasCrop="True"`, `crs:CropLeft="0.000000"`, `crs:CropRight="1.000000"`, `crs:CropAngle="-1.500000"`},
		},
		{
			name:   "user rotate composes with the exif base",
			meta:   Meta{Orientation: 1},
			params: edit.Params{Rotate: 1},
			want:   []string{`tiff:Orientation="6"`},
		},
		{
			name:   "mirror negates the straighten angle",
			params: edit.Params{FlipH: true, CropAngle: 2},
			want:   []string{`crs:CropAngle="2.000000"`, `tiff:Orientation="2"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(Build(tc.meta, &tc.params))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %s in:\n%s", w, got)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("unexpected %s in:\n%s", nw, got)
				}
			}
		})
	}
}

// TestOrientAlgebra checks the orientation group exhaustively: EXIF code
// round-trips, compose matches point-map composition, and inverse undoes.
func TestOrientAlgebra(t *testing.T) {
	all := make([]orient, 0, 8)
	for code := 1; code <= 8; code++ {
		o := orientFromEXIF(code)
		if got := orientToEXIF(o); got != code {
			t.Fatalf("EXIF %d round-trips to %d", code, got)
		}
		all = append(all, o)
	}
	points := [][2]float64{{0, 0}, {1, 0}, {0.25, 0.75}}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-12 }
	for _, a := range all {
		inv := a.inverse()
		for _, p := range points {
			u, v := a.apply(p[0], p[1])
			bu, bv := inv.apply(u, v)
			if !near(bu, p[0]) || !near(bv, p[1]) {
				t.Fatalf("inverse of %+v failed: (%v,%v) -> (%v,%v) -> (%v,%v)", a, p[0], p[1], u, v, bu, bv)
			}
		}
		for _, b := range all {
			c := composeOrient(a, b)
			for _, p := range points {
				au, av := a.apply(p[0], p[1])
				wu, wv := b.apply(au, av)
				gu, gv := c.apply(p[0], p[1])
				if !near(gu, wu) || !near(gv, wv) {
					t.Fatalf("compose(%+v,%+v)=%+v: point (%v,%v) got (%v,%v) want (%v,%v)",
						a, b, c, p[0], p[1], gu, gv, wu, wv)
				}
			}
		}
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "IMG1.ARW")
	if err := Write(raw, Build(Meta{Rating: 1}, nil)); err != nil {
		t.Fatal(err)
	}
	if err := Write(raw, Build(Meta{Rating: 5}, nil)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(PathFor(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `xmp:Rating="5"`) {
		t.Fatalf("overwrite lost: %s", b)
	}
	// No stray temp files left in the directory.
	ents, _ := os.ReadDir(filepath.Dir(raw))
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
	if len(ents) == 0 {
		t.Fatal("expected the sidecar on disk")
	}
}
