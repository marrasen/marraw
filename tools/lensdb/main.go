// Command lensdb distills the Lensfun XML lens database into the compact
// gzipped JSON blob that internal/lens embeds.
//
// Run from the repo root against a checkout of lensfun's data/db directory:
//
//	go run ./tools/lensdb -src /path/to/lensfun/data/db
//
// The Lensfun database is CC-BY-SA 3.0; the distilled blob is the same data
// in another container and carries the same licence (see
// THIRD_PARTY_NOTICES.md). Only what the renderer needs survives the trip:
// camera crop factors, and per-lens distortion / TCA / vignetting
// calibration points. Localized names, mount compatibility graphs and
// per-lens documentation are dropped.
package main

import (
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/marrasen/marraw/internal/lens"
)

// The XML shapes below mirror data/db/lensfun-database.xsd, narrowed to the
// elements we keep. Multi-language <maker>/<model> elements repeat with a
// lang attribute; the untagged one is the canonical (English) name.
type xmlDB struct {
	Cameras []xmlCamera `xml:"camera"`
	Lenses  []xmlLens   `xml:"lens"`
}

type xmlName struct {
	Lang string `xml:"lang,attr"`
	Text string `xml:",chardata"`
}

type xmlCamera struct {
	Maker      []xmlName `xml:"maker"`
	Model      []xmlName `xml:"model"`
	Mount      string    `xml:"mount"`
	CropFactor float64   `xml:"cropfactor"`
}

type xmlLens struct {
	Maker      []xmlName `xml:"maker"`
	Model      []xmlName `xml:"model"`
	Mounts     []string  `xml:"mount"`
	Type       string    `xml:"type"`
	CropFactor float64   `xml:"cropfactor"`
	Aspect     string    `xml:"aspect-ratio"`
	Focal      struct {
		Min   float64 `xml:"min,attr"`
		Max   float64 `xml:"max,attr"`
		Value float64 `xml:"value,attr"`
	} `xml:"focal"`
	Calibration struct {
		Distortion []xmlDistortion `xml:"distortion"`
		TCA        []xmlTCA        `xml:"tca"`
		Vignetting []xmlVignetting `xml:"vignetting"`
	} `xml:"calibration"`
}

type xmlDistortion struct {
	Model     string  `xml:"model,attr"`
	Focal     float64 `xml:"focal,attr"`
	RealFocal float64 `xml:"real-focal,attr"`
	K1        float64 `xml:"k1,attr"`
	K2        float64 `xml:"k2,attr"`
	A         float64 `xml:"a,attr"`
	B         float64 `xml:"b,attr"`
	C         float64 `xml:"c,attr"`
}

type xmlTCA struct {
	Model string  `xml:"model,attr"`
	Focal float64 `xml:"focal,attr"`
	VR    float64 `xml:"vr,attr"`
	VB    float64 `xml:"vb,attr"`
	CR    float64 `xml:"cr,attr"`
	CB    float64 `xml:"cb,attr"`
	BR    float64 `xml:"br,attr"`
	BB    float64 `xml:"bb,attr"`
}

type xmlVignetting struct {
	Model    string  `xml:"model,attr"`
	Focal    float64 `xml:"focal,attr"`
	Aperture float64 `xml:"aperture,attr"`
	Distance float64 `xml:"distance,attr"`
	K1       float64 `xml:"k1,attr"`
	K2       float64 `xml:"k2,attr"`
	K3       float64 `xml:"k3,attr"`
}

func main() {
	src := flag.String("src", "", "path to lensfun's data/db directory")
	out := flag.String("out", filepath.Join("internal", "lens", lens.BlobName), "output blob path")
	flag.Parse()
	if *src == "" {
		fmt.Fprintln(os.Stderr, "lensdb: -src is required")
		os.Exit(2)
	}
	db, err := build(*src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lensdb: %v\n", err)
		os.Exit(1)
	}
	if err := write(*out, db); err != nil {
		fmt.Fprintf(os.Stderr, "lensdb: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("lensdb: %d cameras, %d calibrated lenses → %s\n",
		len(db.Cameras), len(db.Lenses), *out)
}

func build(dir string) (*lens.DB, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .xml files under %s", dir)
	}
	sort.Strings(files) // deterministic blob for a given input tree
	db := &lens.DB{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var parsed xmlDB
		if err := xml.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		for _, c := range parsed.Cameras {
			if c.CropFactor <= 0 {
				continue
			}
			db.Cameras = append(db.Cameras, lens.Camera{
				Maker: canonical(c.Maker),
				Model: canonical(c.Model),
				Mount: c.Mount,
				Crop:  round(c.CropFactor, 4),
			})
		}
		for _, l := range parsed.Lenses {
			if conv := convertLens(l); conv != nil {
				db.Lenses = append(db.Lenses, *conv)
			}
		}
	}
	return db, nil
}

// convertLens returns nil for lenses that carry no calibration we can use —
// the database lists many bodies-of-record with names only, and an entry
// without coefficients would only add matching noise.
func convertLens(l xmlLens) *lens.Lens {
	out := lens.Lens{
		Maker:  canonical(l.Maker),
		Model:  canonical(l.Model),
		Mounts: l.Mounts,
		Crop:   round(l.CropFactor, 4),
		Aspect: parseAspect(l.Aspect),
	}
	out.MinFocal, out.MaxFocal = l.Focal.Min, l.Focal.Max
	if l.Focal.Value > 0 {
		out.MinFocal, out.MaxFocal = l.Focal.Value, l.Focal.Value
	}
	if out.Crop <= 0 {
		out.Crop = 1 // lensfun's own default for an omitted <cropfactor>
	}
	for _, d := range l.Calibration.Distortion {
		model, terms, ok := distTerms(d)
		if !ok {
			continue
		}
		out.Dist = append(out.Dist, lens.DistCalib{
			Focal: d.Focal, Model: model, RealFocal: d.RealFocal, Terms: terms,
		})
	}
	for _, t := range l.Calibration.TCA {
		switch t.Model {
		case "linear":
			// Stored in the poly3 slot with the higher-order terms zeroed:
			// linear is poly3 with c = b = 0, so the renderer needs one path.
			out.TCA = append(out.TCA, lens.TCACalib{
				Focal: t.Focal, Terms: [6]float64{t.VR, t.VB, 0, 0, 0, 0},
			})
		case "poly3":
			out.TCA = append(out.TCA, lens.TCACalib{
				Focal: t.Focal, Terms: [6]float64{t.VR, t.VB, t.CR, t.CB, t.BR, t.BB},
			})
		}
	}
	for _, v := range l.Calibration.Vignetting {
		if v.Model != "pa" || v.Aperture <= 0 || v.Distance <= 0 {
			continue
		}
		out.Vig = append(out.Vig, lens.VigCalib{
			Focal: v.Focal, Aperture: v.Aperture, Distance: v.Distance,
			Terms: [3]float64{v.K1, v.K2, v.K3},
		})
	}
	if len(out.Dist) == 0 && len(out.TCA) == 0 && len(out.Vig) == 0 {
		return nil
	}
	// The focal range gates the vignetting distance metric; a missing range
	// on a calibrated lens falls back to the calibration points themselves.
	if out.MinFocal == 0 && out.MaxFocal == 0 {
		out.MinFocal, out.MaxFocal = focalSpan(out)
	}
	sort.Slice(out.Dist, func(i, j int) bool { return out.Dist[i].Focal < out.Dist[j].Focal })
	sort.Slice(out.TCA, func(i, j int) bool { return out.TCA[i].Focal < out.TCA[j].Focal })
	return &out
}

func distTerms(d xmlDistortion) (lens.DistModel, [3]float64, bool) {
	switch d.Model {
	case "poly3":
		return lens.DistPoly3, [3]float64{d.K1, 0, 0}, true
	case "poly5":
		return lens.DistPoly5, [3]float64{d.K1, d.K2, 0}, true
	case "ptlens":
		return lens.DistPTLens, [3]float64{d.A, d.B, d.C}, true
	}
	return "", [3]float64{}, false // "acm" and friends: not implemented
}

func focalSpan(l lens.Lens) (lo, hi float64) {
	first := true
	visit := func(f float64) {
		if f <= 0 {
			return
		}
		if first {
			lo, hi, first = f, f, false
			return
		}
		lo, hi = min(lo, f), max(hi, f)
	}
	for _, d := range l.Dist {
		visit(d.Focal)
	}
	for _, t := range l.TCA {
		visit(t.Focal)
	}
	for _, v := range l.Vig {
		visit(v.Focal)
	}
	return lo, hi
}

// canonical picks the untagged name element, which lensfun uses for the
// manufacturer's own spelling; localized variants are dropped.
func canonical(names []xmlName) string {
	for _, n := range names {
		if n.Lang == "" {
			return strings.TrimSpace(n.Text)
		}
	}
	if len(names) > 0 {
		return strings.TrimSpace(names[0].Text)
	}
	return ""
}

// parseAspect reads lensfun's "W:H" aspect notation (or a bare float),
// defaulting to 3:2 — the ratio lensfun assumes for an omitted element.
func parseAspect(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1.5
	}
	if w, h, ok := strings.Cut(s, ":"); ok {
		wf, err1 := strconv.ParseFloat(strings.TrimSpace(w), 64)
		hf, err2 := strconv.ParseFloat(strings.TrimSpace(h), 64)
		if err1 == nil && err2 == nil && hf > 0 {
			return round(wf/hf, 6)
		}
		return 1.5
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return round(f, 6)
	}
	return 1.5
}

// round trims float noise so the committed blob is stable across runs and
// diffs stay readable when the upstream database is refreshed.
func round(v float64, places int) float64 {
	f, err := strconv.ParseFloat(strconv.FormatFloat(v, 'f', places, 64), 64)
	if err != nil {
		return v
	}
	return f
}

func write(path string, db *lens.DB) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// BestCompression: the blob is written once and read on every cold
	// start, so a smaller binary is worth the one-off encode cost.
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(zw).Encode(db); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}
