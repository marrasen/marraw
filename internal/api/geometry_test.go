package api

import (
	"database/sql"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

func TestPhotoGeometry(t *testing.T) {
	cases := []struct {
		name   string
		in     sql.NullString
		rotate int
		cropW  float64
		cropH  float64
	}{
		{name: "null", in: sql.NullString{}},
		{name: "malformed", in: sql.NullString{String: "{", Valid: true}},
		{name: "no geometry", in: sql.NullString{String: `{"exposure":0.5}`, Valid: true}},
		{
			name:   "rotate and crop",
			in:     sql.NullString{String: `{"rotate":1,"cropX":0.1,"cropY":0.2,"cropW":0.5,"cropH":0.4}`, Valid: true},
			rotate: 1, cropW: 0.5, cropH: 0.4,
		},
		{
			// Stored values outside 0..3 wrap like edit.Params.RotateTurns.
			name:   "non-canonical rotate wraps",
			in:     sql.NullString{String: `{"rotate":-3}`, Valid: true},
			rotate: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rotate, cropW, cropH := photoGeometry(tc.in)
			if rotate != tc.rotate || cropW != tc.cropW || cropH != tc.cropH {
				t.Errorf("photoGeometry(%q) = (%d, %v, %v), want (%d, %v, %v)",
					tc.in.String, rotate, cropW, cropH, tc.rotate, tc.cropW, tc.cropH)
			}
		})
	}
}

func TestEditPatch(t *testing.T) {
	p := &edit.Params{Rotate: 1, CropX: 0.1, CropY: 0.1, CropW: 0.5, CropH: 0.8}
	got := editPatch(7, "abc", p)
	if got.ID != 7 || got.EditHash == nil || *got.EditHash != "abc" {
		t.Fatalf("editPatch id/hash = %+v", got)
	}
	if got.Rotate == nil || *got.Rotate != 1 || got.CropW == nil || *got.CropW != 0.5 || got.CropH == nil || *got.CropH != 0.8 {
		t.Errorf("editPatch geometry = (%v, %v, %v), want (1, 0.5, 0.8)", got.Rotate, got.CropW, got.CropH)
	}

	// A reset (nil params) must deliver explicit zeros, not nil fields —
	// nil means "unchanged" to the client patch reducer.
	got = editPatch(7, "base", nil)
	if got.Rotate == nil || *got.Rotate != 0 || got.CropW == nil || *got.CropW != 0 || got.CropH == nil || *got.CropH != 0 {
		t.Errorf("editPatch(nil params) geometry = (%v, %v, %v), want explicit zeros", got.Rotate, got.CropW, got.CropH)
	}
}
