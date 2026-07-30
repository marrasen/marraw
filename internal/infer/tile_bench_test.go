package infer

import (
	"image"
	"testing"
)

// Benchmarks for the CPU-side work that surrounds each inference pass, so the
// value of overlapping or parallelising it can be judged against the measured
// per-tile inference cost (design/ml-denoise.md: ~83 ms/tile CUDA, ~754 ms/tile
// CPU for SCUNet at 256^2). If these are microseconds, there is nothing to win
// here and the GPU-utilisation figure was measuring something else.
//
// Run with: go test ./internal/infer -run xxx -bench 'Tile|Blend|Compose' -benchmem

func BenchmarkTileTensorData(b *testing.B) {
	for _, edge := range []int{128, 256} {
		img := noisyBench(edge * 4)
		b.Run(sizeName(edge), func(b *testing.B) {
			b.SetBytes(int64(edge * edge * 3 * 4))
			for i := 0; i < b.N; i++ {
				_ = tileTensorData(img, 0, 0, edge, edge)
			}
		})
	}
}

func BenchmarkBlendTile(b *testing.B) {
	for _, edge := range []int{128, 256} {
		// Accumulate into a plane the size of the 100-tile soak frame, so the
		// scattered writes hit realistic cache behaviour rather than staying
		// resident in L2.
		ow, oh := 2240, 2240
		acc := make([]float32, ow*oh*3)
		wgt := make([]float32, ow*oh)
		vals := make([]float32, edge*edge*3)
		for i := range vals {
			vals[i] = 0.5
		}
		b.Run(sizeName(edge), func(b *testing.B) {
			b.SetBytes(int64(edge * edge * 3 * 4))
			for i := 0; i < b.N; i++ {
				// Vary the origin so we are not repeatedly warming one region.
				x0 := (i * 224) % (ow - edge)
				y0 := (i * 224) % (oh - edge)
				blendTile(acc, wgt, ow, oh, vals, x0, y0, edge, edge, 16)
			}
		})
	}
}

// BenchmarkComposeOutput covers the final acc/wgt -> RGBA pass, which runs once
// per image rather than per tile.
func BenchmarkComposeOutput(b *testing.B) {
	ow, oh := 2240, 2240
	acc := make([]float32, ow*oh*3)
	wgt := make([]float32, ow*oh)
	for i := range wgt {
		wgt[i] = 1
	}
	for i := range acc {
		acc[i] = 0.5
	}
	plane := ow * oh
	b.SetBytes(int64(plane * 4))
	for i := 0; i < b.N; i++ {
		dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
		for j := 0; j < plane; j++ {
			g := wgt[j]
			if g == 0 {
				continue
			}
			dj := j * 4
			dst.Pix[dj] = clampU8(acc[j] / g)
			dst.Pix[dj+1] = clampU8(acc[plane+j] / g)
			dst.Pix[dj+2] = clampU8(acc[2*plane+j] / g)
			dst.Pix[dj+3] = 255
		}
	}
}

func sizeName(edge int) string {
	if edge == 128 {
		return "128x128"
	}
	return "256x256"
}

func noisyBench(edge int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7 % 251)
	}
	return img
}
