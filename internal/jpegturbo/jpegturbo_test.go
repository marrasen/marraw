package jpegturbo

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"testing"
)

// testImage is a photo-like picture: smooth gradients with noise.
func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			n := uint8(rng.IntN(12))
			img.SetRGBA(x, y, color.RGBA{R: uint8(x*255/w) + n, G: uint8(y*255/h) + n, B: uint8((x+y)*127/(w+h)) + n, A: 255})
		}
	}
	return img
}

// What libjpeg-turbo writes, image/jpeg reads, and the other way round,
// to the same pixels within a JPEG's error.
func TestRoundTrip(t *testing.T) {
	src := testImage(640, 427)
	var buf bytes.Buffer
	if err := EncodeRGBA(&buf, src, 92); err != nil {
		t.Fatal(err)
	}
	ours, err := DecodeRGBA(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal("image/jpeg cannot read what libjpeg-turbo wrote:", err)
	}
	if ours.Bounds() != src.Bounds() || theirs.Bounds() != src.Bounds() {
		t.Fatalf("sizes %v and %v, want %v", ours.Bounds(), theirs.Bounds(), src.Bounds())
	}
	// As close to the source as image/jpeg's own round trip at the same
	// quality comes: the test picture's noise is what any JPEG loses.
	var goBuf bytes.Buffer
	if err := encodeGo(&goBuf, src, 92); err != nil {
		t.Fatal(err)
	}
	goRound, err := decodeGo(bytes.NewReader(goBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if d, want := meanDiff(src, ours), meanDiff(src, goRound); d > want*1.2+0.5 {
		t.Errorf("decoded %0.2f levels from the source on average; image/jpeg's round trip comes to %0.2f", d, want)
	}
	if d := meanDiff(toRGBA(theirs), ours); d > 1.5 {
		t.Errorf("libjpeg-turbo and image/jpeg read the file %0.2f levels apart on average", d)
	}
	if ours.Pix[3] != 255 {
		t.Error("decoded pixels are not opaque")
	}
}

// A sub-image encodes as the part it covers.
func TestEncodeSubImage(t *testing.T) {
	sub := testImage(200, 100).SubImage(image.Rect(50, 20, 150, 80)).(*image.RGBA)
	var buf bytes.Buffer
	if err := EncodeRGBA(&buf, sub, 90); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRGBA(buf.Bytes())
	if err != nil || got.Bounds().Dx() != 100 || got.Bounds().Dy() != 60 {
		t.Fatalf("decoded %v, %v", got.Bounds(), err)
	}
}

func TestGarbage(t *testing.T) {
	if _, err := DecodeRGBA([]byte("not a jpeg at all")); err == nil {
		t.Fatal("decoded garbage")
	}
	if _, err := DecodeRGBA(nil); err == nil {
		t.Fatal("decoded nothing")
	}
}

func meanDiff(a, b *image.RGBA) float64 {
	var sum, n float64
	for i := 0; i < len(a.Pix); i += 4 {
		for c := range 3 {
			d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
			if d < 0 {
				d = -d
			}
			sum += d
			n++
		}
	}
	return sum / n
}

func encoded2048(b *testing.B) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(2048, 1366), &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	return buf.Bytes()
}

func BenchmarkDecode2048Turbo(b *testing.B) {
	data := encoded2048(b)
	for b.Loop() {
		if _, err := DecodeRGBA(data); err != nil {
			b.Fatal(err)
		}
	}
}

// What the backend did: image/jpeg, then a copy to RGBA.
func BenchmarkDecode2048Go(b *testing.B) {
	data := encoded2048(b)
	for b.Loop() {
		if _, err := decodeGo(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode2048Turbo(b *testing.B) {
	img := testImage(2048, 1366)
	for b.Loop() {
		if err := EncodeRGBA(new(bytes.Buffer), img, 90); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode2048Go(b *testing.B) {
	img := testImage(2048, 1366)
	for b.Loop() {
		if err := encodeGo(new(bytes.Buffer), img, 90); err != nil {
			b.Fatal(err)
		}
	}
}
