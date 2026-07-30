// Command cmpcrop is a THROWAWAY viewer aid: given two exported JPEGs of the
// same frame, find the region where they differ most and write 1:1 crops of
// each, side by side. Full-frame exports hide per-pixel effects entirely.
// Delete after use.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log"
	"os"
)

func main() {
	a := flag.String("a", "", "first JPEG")
	b := flag.String("b", "", "second JPEG")
	out := flag.String("out", "compare.png", "output PNG")
	cw := flag.Int("w", 700, "crop width")
	ch := flag.Int("h", 500, "crop height")
	minLuma := flag.Float64("minluma", 45, "ignore windows darker than this (0-255)")
	maxLuma := flag.Float64("maxluma", 210, "ignore windows brighter than this")
	atX := flag.Int("x", -1, "crop origin X (-1 = auto-pick)")
	atY := flag.Int("y", -1, "crop origin Y (-1 = auto-pick)")
	detail := flag.Bool("detail", false, "rank by texture in -a instead of by difference: "+
		"shows what detail survives, rather than where the most noise was removed")
	flag.Parse()

	ia, ib := load(*a), load(*b)
	if ia.Bounds() != ib.Bounds() {
		log.Fatalf("bounds differ: %v vs %v", ia.Bounds(), ib.Bounds())
	}
	x, y := *atX, *atY
	if x < 0 || y < 0 {
		var score float64
		x, y, score = worstBlock(ia, ib, *cw, *ch, *minLuma, *maxLuma, *detail)
		fmt.Printf("auto-picked (%d,%d), score %.2f/255\n", x, y, score)
	}
	// Keep the window inside the frame so an eyeballed coordinate cannot panic.
	x = clamp(x, ia.Bounds().Min.X, ia.Bounds().Max.X-*cw)
	y = clamp(y, ia.Bounds().Min.Y, ia.Bounds().Max.Y-*ch)
	fmt.Printf("crop at (%d,%d) %dx%d\n", x, y, *cw, *ch)

	// Side by side with a 8px gutter so the seam is obvious.
	dst := image.NewRGBA(image.Rect(0, 0, *cw*2+8, *ch))
	draw.Draw(dst, image.Rect(0, 0, *cw, *ch), ia, image.Pt(x, y), draw.Src)
	draw.Draw(dst, image.Rect(*cw+8, 0, *cw*2+8, *ch), ib, image.Pt(x, y), draw.Src)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, dst); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", *out)
}

func load(p string) image.Image {
	f, err := os.Open(p)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		log.Fatalf("%s: %v", p, err)
	}
	return img
}

// worstBlock scans a coarse grid for the crop-sized window with the highest
// mean absolute luma difference -- i.e. where the model actually did something.
// Windows outside [minLuma,maxLuma] mean brightness are ignored: the raw
// maximum lands in near-black clothing, where the model just crushes shadow
// noise to black and there is no detail to judge.
func worstBlock(a, b image.Image, cw, ch int, minLuma, maxLuma float64, byDetail bool) (int, int, float64) {
	bounds := a.Bounds()
	step := 200
	bestX, bestY, best := bounds.Min.X, bounds.Min.Y, -1.0
	for y := bounds.Min.Y; y+ch <= bounds.Max.Y; y += step {
		for x := bounds.Min.X; x+cw <= bounds.Max.X; x += step {
			var sum, lum float64
			n := 0
			// Sample sparsely; we only need to rank regions.
			for dy := 0; dy < ch; dy += 7 {
				for dx := 0; dx < cw; dx += 7 {
					if byDetail {
						// Acutance in the DENOISED image, not the noisy one:
						// noise is itself high-frequency, so ranking on the
						// original just re-finds the grainiest sky. After
						// denoising, what is left of the high frequencies is
						// real structure -- hair, fabric, foliage.
						sum += absLumaDelta(b, x+dx, y+dy)
					} else {
						sum += absDiff(a, b, x+dx, y+dy)
					}
					lum += luma(a, x+dx, y+dy)
					n++
				}
			}
			if mean := lum / float64(n); mean < minLuma || mean > maxLuma {
				continue
			}
			if m := sum / float64(n); m > best {
				best, bestX, bestY = m, x, y
			}
		}
	}
	return bestX, bestY, best
}

func luma(img image.Image, x, y int) float64 {
	r, g, b, _ := img.At(x, y).RGBA()
	return (299*float64(r) + 587*float64(g) + 114*float64(b)) / 1000 / 256
}

func absDiff(a, b image.Image, x, y int) float64 {
	r1, g1, b1, _ := a.At(x, y).RGBA()
	r2, g2, b2, _ := b.At(x, y).RGBA()
	l1 := (299*float64(r1) + 587*float64(g1) + 114*float64(b1)) / 1000 / 256
	l2 := (299*float64(r2) + 587*float64(g2) + 114*float64(b2)) / 1000 / 256
	if d := l1 - l2; d < 0 {
		return -d
	}
	return l1 - l2
}

// absLumaDelta is a texture proxy: absolute horizontal luma gradient.
func absLumaDelta(img image.Image, x, y int) float64 {
	d := luma(img, x, y) - luma(img, x+1, y)
	if d < 0 {
		return -d
	}
	return d
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
