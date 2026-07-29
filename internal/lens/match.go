package lens

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Matching EXIF strings to database entries.
//
// Lensfun matches with a fuzzy word-similarity score, taking the best
// candidate whatever its score. We are deliberately stricter: a wrong
// profile silently warps every pixel of the photo, so an unrecognised lens
// (no correction at all) is a far better failure than a confidently applied
// wrong one. Nothing the EXIF string asserts may be contradicted by the
// candidate, and an ambiguous name matches nothing.

// index is the lazily-built lookup structure over the embedded database.
type index struct {
	cameras  map[string]*Camera // normalized "maker model" → body
	lensKeys []lensKey
	byMount  map[string][]*Lens // mount name → the lenses that fit it
}

type lensKey struct {
	lens    *Lens
	words   []string // normalized non-numeric tokens of maker + model
	numbers []string // normalized numeric tokens of maker + model
}

var (
	indexOnce sync.Once
	idx       *index
	indexErr  error
)

func loadIndex() (*index, error) {
	indexOnce.Do(func() {
		db, err := Load()
		if err != nil {
			indexErr = err
			return
		}
		ix := &index{
			cameras: make(map[string]*Camera, len(db.Cameras)),
			byMount: make(map[string][]*Lens),
		}
		for i := range db.Cameras {
			c := &db.Cameras[i]
			ix.cameras[normalizeBody(c.Maker, c.Model)] = c
		}
		for i := range db.Lenses {
			l := &db.Lenses[i]
			words, numbers := splitTokens(tokenize(l.Maker + " " + l.Model))
			ix.lensKeys = append(ix.lensKeys, lensKey{lens: l, words: words, numbers: numbers})
			for _, m := range l.Mounts {
				ix.byMount[m] = append(ix.byMount[m], l)
			}
		}
		idx = ix
	})
	return idx, indexErr
}

// FindCamera resolves a body from its EXIF maker and model strings.
func FindCamera(maker, model string) *Camera {
	ix, err := loadIndex()
	if err != nil {
		return nil
	}
	return ix.cameras[normalizeBody(maker, model)]
}

// FindLens resolves a lens from its EXIF description, restricted to
// calibrations that cover the body's crop factor. bodyCrop of 0 disables
// that check (used by tests and tooling).
//
// The coverage rule is Lensfun's: a calibration measured on a smaller image
// circle than the body needs cannot be extrapolated outward, so the ratio
// bodyCrop/calibCrop must be at least 0.96. An APS-C calibration is
// therefore never applied to a full-frame frame, while the reverse is fine.
func FindLens(name string, bodyCrop float64) *Lens {
	ix, err := loadIndex()
	if err != nil || strings.TrimSpace(name) == "" {
		return nil
	}
	qWords, qNumbers := splitTokens(tokenize(name))
	if len(qWords) == 0 && len(qNumbers) == 0 {
		return nil
	}
	qLo, qHi, hasFocal := parseFocalSpec(name)

	// Three gates, all of them "everything the camera told us is also true
	// of this candidate": the focal range printed on the barrel, the
	// descriptive words, and the remaining numbers (maximum aperture, mark
	// number). The candidate is allowed to say MORE than the EXIF string
	// does — abbreviated EXIF names are the norm — but never to contradict
	// it. Ranking then prefers the candidate that says the least extra.
	var best *Lens
	bestExtra, ties := 0, 0
	for i := range ix.lensKeys {
		k := &ix.lensKeys[i]
		if bodyCrop > 0 && k.lens.Crop > 0 && bodyCrop/k.lens.Crop < 0.96 {
			continue
		}
		if hasFocal && !sameFocalRange(qLo, qHi, k.lens) {
			continue
		}
		if !subset(qWords, k.words) || !subset(qNumbers, k.numbers) {
			continue
		}
		extra := len(k.words) + len(k.numbers) - len(qWords) - len(qNumbers)
		switch {
		case best == nil || extra < bestExtra:
			best, bestExtra, ties = k.lens, extra, 1
		case extra == bestExtra:
			ties++
		}
	}
	// Two entries fitting equally well means the EXIF string genuinely does
	// not distinguish them (a lens sold under two names, or a variant whose
	// only difference is unprinted). Correcting with either is a coin flip,
	// so correct with neither.
	if ties > 1 {
		return nil
	}
	return best
}

// focalSpecRe reads the focal length printed in a lens name: "24-70mm" for a
// zoom, "50mm" for a prime. Makers vary the spacing but not the shape.
var focalSpecRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:-\s*(\d+(?:\.\d+)?)\s*)?mm`)

// parseFocalSpec extracts the focal range from a lens name, or ok=false
// when the name doesn't print one (some bodies write only "70-200").
func parseFocalSpec(name string) (lo, hi float64, ok bool) {
	m := focalSpecRe.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	lo, err := strconv.ParseFloat(m[1], 64)
	if err != nil || lo <= 0 {
		return 0, 0, false
	}
	hi = lo
	if m[2] != "" {
		if v, err := strconv.ParseFloat(m[2], 64); err == nil && v > 0 {
			hi = v
		}
	}
	return lo, hi, true
}

// sameFocalRange compares a name's printed focal range against the lens's
// declared one. The tolerance absorbs the rounding makers apply to the
// nominal figures (an 18-55 whose true range is 18.1-55.4). A lens whose
// database entry declares no range can't be gated, so it passes.
func sameFocalRange(lo, hi float64, l *Lens) bool {
	if l.MinFocal <= 0 || l.MaxFocal <= 0 {
		return true
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.05*math.Max(a, b) }
	return near(lo, l.MinFocal) && near(hi, l.MaxFocal)
}

// subset reports whether every token in a appears in b.
func subset(a, b []string) bool {
	bs := toSet(b)
	for _, w := range a {
		if !bs[w] {
			return false
		}
	}
	return true
}

// FixedLensFor returns the built-in lens of a fixed-lens body, identified by
// the pseudo-mount the database gives such cameras. Compacts and bridge
// cameras usually record no lens string at all — there is only one lens and
// it never comes off — so the mount is the only thing tying the body to its
// calibration.
//
// The uniqueness check is what keeps this safe: a real interchangeable mount
// like "Sony E" has hundreds of entries and never resolves here, while a
// fixed-lens pseudo-mount has exactly one.
func FixedLensFor(mount string) *Lens {
	ix, err := loadIndex()
	if err != nil || mount == "" {
		return nil
	}
	if ls := ix.byMount[mount]; len(ls) == 1 {
		return ls[0]
	}
	return nil
}

// Resolve is the one call the renderer makes: EXIF in, coefficients out.
// It returns nil whenever anything is unknown — an unmatched body, an
// unmatched lens, or a lens with no usable calibration.
func Resolve(maker, model, lensName string, focal, aperture float64) *Correction {
	cam := FindCamera(maker, model)
	if cam == nil {
		return nil
	}
	l := FindLens(lensName, cam.Crop)
	if l == nil && strings.TrimSpace(lensName) == "" {
		// No lens recorded: the only lens this can be is a built-in one.
		l = FixedLensFor(cam.Mount)
	}
	if l == nil {
		return nil
	}
	// Subject distance is not in the EXIF LibRaw hands us, so vignetting
	// resolves at infinity — the right default for the landscape, sports
	// and event frames this app is pointed at, and within a few percent of
	// the near-field curves for anything past a couple of meters.
	return l.Resolve(cam.Crop, focal, aperture, 1000)
}

// normalizeBody builds the camera lookup key. EXIF makers are shouty and
// sometimes carry a company suffix ("NIKON CORPORATION"), and some bodies
// repeat the maker inside the model ("NIKON D850"), so the key is the
// maker's first word plus the model with any leading maker word removed.
func normalizeBody(maker, model string) string {
	mk := firstWord(maker)
	words := tokenize(model)
	if len(words) > 0 && words[0] == mk {
		words = words[1:]
	}
	return mk + " " + strings.Join(words, " ")
}

func firstWord(s string) string {
	w := tokenize(s)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}

// tokenize lowercases and cuts the string into runs of letters and runs of
// digits, so that however a maker glues its name together the pieces come
// out the same on both sides of the comparison: "EF24-105mm f/4L",
// "EF 24-105mm f/4 L" and "M.12-40mm" all yield the same tokens the
// database's spelling does. A "." survives only between two digits, where
// it is a decimal point ("2.8") rather than an abbreviation ("M.Zuiko").
func tokenize(s string) []string {
	s = strings.ToLower(s)
	runes := []rune(s)

	var out []string
	var cur strings.Builder
	curDigit := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		w := strings.Trim(cur.String(), ".")
		cur.Reset()
		if w != "" && !noiseWords[w] {
			out = append(out, w)
		}
	}
	for i, r := range runes {
		switch {
		case r >= 'a' && r <= 'z':
			if curDigit {
				flush()
			}
			curDigit = false
			cur.WriteRune(r)
		case r >= '0' && r <= '9':
			if !curDigit {
				flush()
			}
			curDigit = true
			cur.WriteRune(r)
		case r == '.' && curDigit && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9':
			cur.WriteRune(r)
		default:
			flush()
			curDigit = false
		}
	}
	flush()
	return out
}

// noiseWords are tokens that carry no evidence: either they appear on one
// side of the comparison only, or — like the "mm" and "f" that every lens
// name contains — they appear on both sides of every comparison and would
// let an unrelated lens score above zero.
var noiseWords = map[string]bool{
	"mm": true, "f": true,
	"lens": true, "camera": true, "corporation": true, "corp": true,
	"co": true, "ltd": true, "inc": true, "imaging": true, "optical": true,
	"the": true, "and": true, "for": true, "or": true,
}

// splitTokens separates the numeric part of a name (focal lengths,
// apertures, mark numbers — the part that must match exactly) from the
// descriptive words (which are matched loosely).
func splitTokens(words []string) (text, numbers []string) {
	for _, w := range words {
		if strings.ContainsAny(w, "0123456789") {
			numbers = append(numbers, w)
		} else {
			text = append(text, w)
		}
	}
	return text, numbers
}

func toSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}
