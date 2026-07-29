// Package lens turns a photo's camera and lens EXIF strings into the
// correction coefficients the renderer needs: geometric distortion, lateral
// chromatic aberration, and vignetting.
//
// The calibration data is the Lensfun project's database (CC-BY-SA 3.0),
// distilled by tools/lensdb into the gzipped JSON blob embedded below. The
// coefficient models, their coordinate conventions and the interpolation
// scheme are Lensfun's too — see coeff.go, which cites the upstream files
// each formula comes from. Nothing here links against liblensfun; it is a
// from-scratch Go implementation of the same math over the same data.
package lens

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

// BlobName is the file name of the embedded database, shared with
// tools/lensdb so the generator and the loader cannot drift apart.
const BlobName = "lensfun.json.gz"

//go:embed lensfun.json.gz
var blobFS embed.FS

// DistModel names a distortion polynomial. The database also defines an
// "acm" model, which tools/lensdb drops: no shipped lens uses it.
type DistModel string

const (
	// DistPoly3 is Rd = Ru * (1 + k1*Ru²); Terms[0] holds k1.
	DistPoly3 DistModel = "poly3"
	// DistPoly5 is Rd = Ru * (1 + k1*Ru² + k2*Ru⁴); Terms[0..1] hold k1, k2.
	DistPoly5 DistModel = "poly5"
	// DistPTLens is Rd = Ru * (a*Ru³ + b*Ru² + c*Ru + 1); Terms[0..2] hold
	// a, b, c. The overwhelming majority of the database uses this one.
	DistPTLens DistModel = "ptlens"
)

// Camera is one body's crop factor — the only thing a body contributes to
// the correction, since the calibration lives on the lens.
type Camera struct {
	Maker string  `json:"mk"`
	Model string  `json:"md"`
	Mount string  `json:"mt,omitempty"`
	Crop  float64 `json:"c"`
}

// DistCalib is one distortion measurement, taken at Focal mm. RealFocal is
// the lens's true focal length at that setting when the calibrator measured
// it (0 = unmeasured, treat as nominal).
type DistCalib struct {
	Focal     float64    `json:"f"`
	Model     DistModel  `json:"m"`
	RealFocal float64    `json:"rf,omitempty"`
	Terms     [3]float64 `json:"t"`
}

// TCACalib is one lateral-CA measurement: per-channel radial scaling for
// red and blue against green. Terms are ordered vr, vb, cr, cb, br, bb —
// Lensfun's poly3 layout, which the linear model fills with c = b = 0.
type TCACalib struct {
	Focal float64    `json:"f"`
	Terms [6]float64 `json:"t"`
}

// VigCalib is one vignetting measurement of the "pa" model, which is a
// function of focal length, aperture AND subject distance — hence a cloud
// of points rather than a curve.
type VigCalib struct {
	Focal    float64    `json:"f"`
	Aperture float64    `json:"a"`
	Distance float64    `json:"d"`
	Terms    [3]float64 `json:"t"`
}

// Lens is one calibrated lens. Crop and Aspect describe the sensor the
// calibration was measured on; correcting a photo shot on a different
// format rescales the coefficients (see rescale in coeff.go).
type Lens struct {
	Maker    string   `json:"mk"`
	Model    string   `json:"md"`
	Mounts   []string `json:"mt,omitempty"`
	Crop     float64  `json:"c"`
	Aspect   float64  `json:"ar"`
	MinFocal float64  `json:"fmin,omitempty"`
	MaxFocal float64  `json:"fmax,omitempty"`

	Dist []DistCalib `json:"d,omitempty"`
	TCA  []TCACalib  `json:"t,omitempty"`
	Vig  []VigCalib  `json:"v,omitempty"`
}

// DB is the whole distilled database.
type DB struct {
	Cameras []Camera `json:"cameras"`
	Lenses  []Lens   `json:"lenses"`
}

var (
	loadOnce sync.Once
	loaded   *DB
	loadErr  error
)

// Load decodes the embedded database once per process and returns the shared
// value. Callers must treat it as read-only.
func Load() (*DB, error) {
	loadOnce.Do(func() {
		f, err := blobFS.Open(BlobName)
		if err != nil {
			loadErr = err
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			loadErr = fmt.Errorf("lens: opening %s: %w", BlobName, err)
			return
		}
		defer zr.Close()
		var db DB
		if err := json.NewDecoder(zr).Decode(&db); err != nil {
			loadErr = fmt.Errorf("lens: decoding %s: %w", BlobName, err)
			return
		}
		loaded = &db
	})
	return loaded, loadErr
}
