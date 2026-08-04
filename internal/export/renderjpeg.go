package export

import (
	"bytes"
	"context"
	"image/jpeg"

	"github.com/marrasen/marraw/internal/store"
)

// RenderJPEG renders one photo to JPEG bytes, carrying the same ICC profile
// and EXIF policy a file written by Run would. Used by the share page's
// download endpoint, which streams to the visitor instead of writing to disk.
//
// Deliberately not a plain jpeg.Encode: a photo downloaded from a share is the
// deliverable, so it gets the colour profile and the credit tags an export
// gets. Callers that want neither can ask for ExifMode "none".
func RenderJPEG(ctx context.Context, db *store.DB, photoID int64, req Request) ([]byte, error) {
	photo, err := db.GetPhoto(ctx, photoID)
	if err != nil {
		return nil, err
	}
	img, err := renderPhoto(ctx, photo, req)
	if err != nil {
		return nil, err
	}
	quality := req.JpegQuality
	if quality == 0 {
		// Run applies the same default; RenderOne-style entry points bypass
		// it, so spell it out rather than encoding at quality 0.
		quality = 90
	}
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if icc := ICCFor(req.ColorSpace); icc != nil {
		out = embedICCJPEG(out, icc)
	}
	meta := exifFromPhoto(photo, img.Bounds().Dx(), img.Bounds().Dy(), req.ColorSpace).applyPolicy(req)
	return embedExifJPEG(out, meta), nil
}
