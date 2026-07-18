package edit

import "testing"

// OutputDims is mirrored by client/src/lib/crop.ts renderedDims — both sides
// must agree so the grid, loupe box and dimension healing size identically.
func TestOutputDims(t *testing.T) {
	cases := []struct {
		name   string
		params *Params
		w, h   int
	}{
		{name: "nil params", params: nil, w: 6000, h: 4000},
		{name: "neutral", params: &Params{}, w: 6000, h: 4000},
		{name: "crop only", params: &Params{CropW: 0.5, CropH: 1}, w: 3000, h: 4000},
		{name: "odd rotate swaps axes", params: &Params{Rotate: 1}, w: 4000, h: 6000},
		{name: "half rotate keeps axes", params: &Params{Rotate: 2}, w: 6000, h: 4000},
		{
			// Crop fractions apply to the rotated frame.
			name:   "rotate then crop",
			params: &Params{Rotate: 1, CropW: 0.5, CropH: 0.25},
			w:      2000, h: 1500,
		},
		{name: "straighten alone keeps size", params: &Params{CropAngle: 3}, w: 6000, h: 4000},
		{name: "tiny crop clamps to 1px", params: &Params{CropW: 0.00001, CropH: 0.00001}, w: 1, h: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := tc.params.OutputDims(6000, 4000)
			if w != tc.w || h != tc.h {
				t.Errorf("OutputDims(6000, 4000) = (%d, %d), want (%d, %d)", w, h, tc.w, tc.h)
			}
		})
	}
}
