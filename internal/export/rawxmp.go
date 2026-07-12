package export

import (
	"io"
	"os"
	"time"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/xmp"
)

// exportRawXmp handles one photo of a "rawXmp" export: copy the RAW to
// outPath and write an Adobe .xmp sidecar next to it. When the destination
// is the photo's own folder (sameDir), the copy is skipped and outPath is
// the original RAW — only the sidecar is written, refreshed atomically on
// re-export. Nothing decodes here; LibRaw is never touched.
func exportRawXmp(photo store.Photo, outPath string, sameDir bool) error {
	var params *edit.Params
	if photo.EditParams.Valid {
		if p, err := edit.Parse(photo.EditParams.String); err == nil {
			params = p
		}
	}
	data := xmp.Build(xmp.Meta{
		Rating:      photo.Rating,
		Flag:        photo.Flag,
		Orientation: photo.Orientation,
	}, params)

	// Copy first, sidecar second: a failed copy must not leave an orphan
	// sidecar pointing at a RAW that never arrived.
	if !sameDir {
		if err := copyFile(photo.Path(), outPath); err != nil {
			return err
		}
	}
	return xmp.Write(outPath, data)
}

// copyFile copies src to dst via a temp file + rename (the same pattern as
// exportOne's encoder write) and keeps the source's modification time, which
// photographers sort deliveries by.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	// Best-effort: the export succeeded even if the timestamp didn't stick.
	_ = os.Chtimes(dst, time.Now(), st.ModTime())
	return nil
}
