package watermark

import (
	"embed"
	"fmt"
	"sync"

	"golang.org/x/image/font/opentype"
)

// FontID names one of the bundled faces. The set is deliberately small and
// static (opentype cannot instance variable fonts): a clean sans, an elegant
// serif, a technical mono, and a signature script — the classic photo
// watermark looks.
type FontID string

const (
	FontSans   FontID = "sans"   // Inter
	FontSerif  FontID = "serif"  // Playfair Display
	FontMono   FontID = "mono"   // JetBrains Mono
	FontScript FontID = "script" // Great Vibes
)

// FontIDs lists the bundled faces in display order.
func FontIDs() []FontID {
	return []FontID{FontSans, FontSerif, FontMono, FontScript}
}

// All four are SIL OFL 1.1; the license texts ship next to the files.
//
//go:embed fonts/*.ttf
var fontFS embed.FS

var fontFiles = map[FontID]string{
	FontSans:   "fonts/Inter-Regular.ttf",
	FontSerif:  "fonts/PlayfairDisplay-Regular.ttf",
	FontMono:   "fonts/JetBrainsMono-Regular.ttf",
	FontScript: "fonts/GreatVibes-Regular.ttf",
}

// FontBytes returns the raw TTF for the HTTP endpoint that serves the same
// files to the client preview — byte-identical fonts on both sides is what
// keeps the preview honest.
func FontBytes(id FontID) ([]byte, bool) {
	name, ok := fontFiles[id]
	if !ok {
		return nil, false
	}
	raw, err := fontFS.ReadFile(name)
	if err != nil {
		return nil, false
	}
	return raw, true
}

var (
	fontMu     sync.Mutex
	fontParsed = map[FontID]*opentype.Font{}
)

// Font returns the parsed face, caching the parse. The returned sfnt.Font is
// safe to share; opentype.Face is NOT — callers create a Face per use
// (export runs one worker per core).
func Font(id FontID) (*opentype.Font, error) {
	if _, ok := fontFiles[id]; !ok {
		id = FontSans
	}
	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := fontParsed[id]; ok {
		return f, nil
	}
	raw, ok := FontBytes(id)
	if !ok {
		return nil, fmt.Errorf("watermark: no font %q", id)
	}
	f, err := opentype.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("watermark: parse font %q: %w", id, err)
	}
	fontParsed[id] = f
	return f, nil
}
