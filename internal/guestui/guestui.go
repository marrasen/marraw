// Package guestui serves the share page — the small browser app a guest opens
// from a share link. It is the one part of marraw's UI the daemon serves
// itself: the desktop client is loaded from disk by the Electron shell, but a
// visitor has only a URL.
package guestui

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// The built share page, copied in by scripts/build-server.mjs before the
// daemon is compiled. A checked-out tree carries only a placeholder, because
// CI runs `go build ./...` before the client is built — hence "all:", which
// matches the dotfile that keeps the directory embeddable, and hence notBuilt
// below.
//
// Not named dist/: the repo ignores that everywhere, and an embed directive
// whose directory is missing from a fresh clone does not compile.
//
//go:embed all:bundle
var bundle embed.FS

// entry is the bundle's HTML document. Named after its source file rather
// than renamed to index.html on the way in, so the embedded tree is exactly
// what vite emitted.
const entry = "guest.html"

// Handler serves GET /s/{token}/{path...}.
type Handler struct {
	// TokenValid reports whether a share token is live. A dead link gets a
	// page saying so — the alternative is an app that loads and then fails to
	// connect, which reads as "this software is broken" rather than "this
	// link has expired".
	TokenValid func(string) bool
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	harden(w, r)
	if h.TokenValid != nil && !h.TokenValid(r.PathValue("token")) {
		page(w, http.StatusNotFound, "Link not available",
			"This share link has expired or been withdrawn. Ask for a new one.")
		return
	}
	files, err := fs.Sub(bundle, "bundle")
	if err != nil {
		page(w, http.StatusInternalServerError, "Unavailable", "The share page is missing.")
		return
	}
	name := path.Clean("/" + r.PathValue("path"))[1:]
	if name == "" || name == "." {
		name = entry
	}
	data, err := fs.ReadFile(files, name)
	if err != nil {
		if name == entry {
			notBuilt(w)
			return
		}
		page(w, http.StatusNotFound, "Not found", "")
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	if name == entry {
		// The entry document must never be cached: it names the hashed asset
		// files, so a stale copy pins the visitor to an old bundle forever.
		w.Header().Set("Cache-Control", "no-store")
	} else {
		// Everything else is content-hashed by vite.
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	w.Write(data)
}

// tokenShape is what GeneratePairingToken emits, and the only thing that can
// name a share. Checking the shape before the token is echoed anywhere keeps
// the unauthenticated surface to a fixed-size hex string.
var tokenShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Redirect sends /s/{token} to /s/{token}/ so the page's relative asset URLs
// resolve under the token rather than at the site root.
//
// It validates the shape first. The redirect is harmless either way — the
// prefix is fixed, so it can never become protocol-relative, and Go escapes
// the URL into the body — but this endpoint is reachable by anyone who finds
// the funnel hostname, and echoing an arbitrary caller-chosen string into a
// response header for no reason is a habit worth not having.
func Redirect(w http.ResponseWriter, r *http.Request) {
	harden(w, r)
	token := r.PathValue("token")
	if !tokenShape.MatchString(token) {
		page(w, http.StatusNotFound, "Link not available",
			"This share link has expired or been withdrawn. Ask for a new one.")
		return
	}
	http.Redirect(w, r, "/s/"+token+"/", http.StatusFound)
}

// safeHost returns host when it is a plain host[:port] and "" otherwise, so a
// caller-supplied Host header can be composed into a header value. Bracketed
// IPv6 literals pass; anything carrying a quote, space, semicolon or control
// character does not.
func safeHost(host string) string {
	const maxHost = 253 + len(":65535")
	if host == "" || len(host) > maxHost {
		return ""
	}
	for i := range len(host) {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == ':', c == '[', c == ']':
		default:
			return ""
		}
	}
	return host
}

// harden sets the response headers the share page is served with. Everything
// under /s/ is reachable by anyone who has the URL — and, once the funnel is
// up, by anyone who guesses the hostname — so it gets the treatment a public
// page gets rather than the treatment the desktop app's own bundle gets.
//
// The policy is exactly what the bundle needs: its own scripts, styles, fonts
// and images, and a socket back to the daemon that served it. 'unsafe-inline'
// is there for style only, because React writes style attributes (the loupe's
// transforms) and the fallback page below carries a <style> block; no inline
// script is allowed, which is the half that matters.
func harden(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	// Named as well as 'self': Safari historically refused a WebSocket under
	// connect-src 'self', and a phone is the whole point of this page. The
	// name comes from the request, though, so it is checked before it goes
	// into a policy header — a header assembled out of request data is worth
	// not writing. A host that does not look like one costs Safari the
	// workaround and nothing else.
	connect := "connect-src 'self'"
	if host := safeHost(r.Host); host != "" {
		connect += " wss://" + host + " ws://" + host
	}
	h.Set("Content-Security-Policy", "default-src 'none'; "+
		"script-src 'self'; "+
		"style-src 'self' 'unsafe-inline'; "+
		"img-src 'self'; "+
		"font-src 'self'; "+
		connect+"; "+
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	// The URL *is* the credential, so it must not ride along in a Referer to
	// anywhere. The bundle's own <meta referrer> says the same thing; this
	// covers the responses that are not the bundle.
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
}

// contentType maps the handful of extensions vite emits. http.DetectContentType
// is no help for CSS and JS, which it reports as text/plain — and a text/plain
// module is a page that silently does nothing.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".woff2":
		return "font/woff2"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// notBuilt explains a daemon compiled without the share bundle — a developer
// running `go build` directly rather than `npm run build:server`.
func notBuilt(w http.ResponseWriter) {
	log.Print("guestui: no bundle embedded (build the client first)")
	page(w, http.StatusServiceUnavailable, "Share page not built",
		"This build of marraw has no share page. Run npm run build:client, then rebuild the daemon.")
}

// page renders a plain, self-contained message. No bundle, no assets: this has
// to work in exactly the situations where the bundle does not.
func page(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + title + `</title><style>` +
		`html{color-scheme:dark light}` +
		`body{margin:0;min-height:100dvh;display:grid;place-items:center;` +
		`font:16px/1.5 system-ui,sans-serif;background:#111;color:#eee;padding:2rem}` +
		`main{max-width:32rem;text-align:center}h1{font-size:1.25rem;font-weight:600}` +
		`p{color:#aaa}</style></head><body><main><h1>` + title + `</h1>` +
		`<p>` + body + `</p></main></body></html>`))
}
