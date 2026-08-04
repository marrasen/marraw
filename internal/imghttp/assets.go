package imghttp

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// assetName is the only shape AddWatermarkAsset produces (content-hash +
// extension); rejecting anything else makes path traversal impossible.
var assetName = regexp.MustCompile(`^[0-9a-f]{16}\.(png|jpg)$`)

// Assets serves watermark asset files for the editor preview:
// GET /wm/{name}?t=token.
type Assets struct {
	Dir string
	// Authorize checks the ?t=/X-Marraw-Token credential; nil disables the
	// check (dev mode). Watermark assets belong to the library rather than to
	// any one folder, so a confined credential (a share link) is refused
	// outright — the shared page has no editor to preview them in.
	Authorize Authorizer
}

func (h *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !authorized(h.Authorize, r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.PathValue("name")
	if !assetName.MatchString(name) {
		http.Error(w, "bad asset name", http.StatusBadRequest)
		return
	}
	f, err := os.Open(filepath.Join(h.Dir, name))
	if err != nil {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	defer f.Close()
	ctype := "image/png"
	if strings.HasSuffix(name, ".jpg") {
		ctype = "image/jpeg"
	}
	// Same trust model as the photo endpoints: wide-open CORS (the renderer
	// runs on file://), access control is the token in the URL. Names are
	// content-hashed, so immutable is exact.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}
