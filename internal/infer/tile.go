package infer

import (
	"context"
	"fmt"
	"image"

	ort "github.com/yalue/onnxruntime_go"
)

// Tiled image-to-image inference for restoration models (denoise, super
// resolution): the full frame won't fit one forward pass, so it runs in
// overlapping tiles whose seams are blended with a linear ramp. ctx is
// checked between tiles — that is the cancellation granularity.

// TileConfig shapes a tiled run.
type TileConfig struct {
	// Size is the tile edge fed to the model (both dims). Overlap is eaten
	// off each interior tile edge and cross-faded with the neighbor.
	Size, Overlap int
	// Scale is the model's output magnification (1 = denoise, 2 = 2x SR).
	Scale int
	// Progress, when set, receives (tilesDone, tilesTotal).
	Progress func(done, total int)
}

// RunTiled pushes img through an image-to-image session tile by tile and
// returns the assembled output at Scale times the input size. The model is
// assumed to take NCHW float32 RGB in [0,1] (input mean/std of zero/one) and
// produce the same layout — the convention of every restoration export we
// pin (SCUNet, Swin2SR).
func RunTiled(ctx context.Context, sess *Session, img *image.RGBA, cfg TileConfig) (*image.RGBA, error) {
	if cfg.Size <= 0 || cfg.Overlap < 0 || cfg.Overlap*2 >= cfg.Size {
		return nil, fmt.Errorf("infer: bad tile config %+v", cfg)
	}
	if cfg.Scale <= 0 {
		cfg.Scale = 1
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("infer: empty input")
	}

	step := cfg.Size - 2*cfg.Overlap
	nx := (w + step - 1) / step
	ny := (h + step - 1) / step
	total := nx * ny
	done := 0

	ow, oh := w*cfg.Scale, h*cfg.Scale
	// Weighted accumulation planes handle the cross-fade uniformly.
	acc := make([]float32, ow*oh*3)
	wgt := make([]float32, ow*oh)

	for ty := 0; ty < ny; ty++ {
		for tx := 0; tx < nx; tx++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Tile origin, clamped so edge tiles stay fully inside the frame
			// (models behave better on full tiles than on padded slivers).
			x0 := min(tx*step, max(0, w-cfg.Size))
			y0 := min(ty*step, max(0, h-cfg.Size))
			tw := min(cfg.Size, w)
			th := min(cfg.Size, h)

			in := tileTensorData(img, x0, y0, tw, th)
			tensor, err := ort.NewTensor(ort.NewShape(1, 3, int64(th), int64(tw)), in)
			if err != nil {
				return nil, err
			}
			outs, err := sess.Run(ctx, tensor)
			tensor.Destroy()
			if err != nil {
				return nil, err
			}
			out, ok := outs[0].(*ort.Tensor[float32])
			if !ok {
				destroyAll(outs)
				return nil, fmt.Errorf("infer: tile output is %T, want float32 tensor", outs[0])
			}
			vals := out.GetData()
			otw, oth := tw*cfg.Scale, th*cfg.Scale
			if len(vals) < otw*oth*3 {
				destroyAll(outs)
				return nil, fmt.Errorf("infer: tile output has %d values, want %d", len(vals), otw*oth*3)
			}
			blendTile(acc, wgt, ow, oh, vals, x0*cfg.Scale, y0*cfg.Scale, otw, oth, cfg.Overlap*cfg.Scale)
			destroyAll(outs)

			done++
			if cfg.Progress != nil {
				cfg.Progress(done, total)
			}
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	plane := ow * oh
	for i := 0; i < plane; i++ {
		g := wgt[i]
		if g == 0 {
			continue
		}
		di := i * 4
		dst.Pix[di] = clampU8(acc[i] / g)
		dst.Pix[di+1] = clampU8(acc[plane+i] / g)
		dst.Pix[di+2] = clampU8(acc[2*plane+i] / g)
		dst.Pix[di+3] = 255
	}
	return dst, nil
}

func destroyAll(vals []ort.Value) {
	for _, v := range vals {
		if v != nil {
			v.Destroy()
		}
	}
}

// tileTensorData extracts one tile as NCHW [0,1] floats.
func tileTensorData(img *image.RGBA, x0, y0, tw, th int) []float32 {
	out := make([]float32, 3*tw*th)
	rp, gp, bp := out[:tw*th], out[tw*th:2*tw*th], out[2*tw*th:]
	b := img.Bounds()
	for y := 0; y < th; y++ {
		row := img.Pix[img.PixOffset(b.Min.X+x0, b.Min.Y+y0+y):]
		for x := 0; x < tw; x++ {
			i := y*tw + x
			rp[i] = float32(row[x*4]) / 255
			gp[i] = float32(row[x*4+1]) / 255
			bp[i] = float32(row[x*4+2]) / 255
		}
	}
	return out
}

// blendTile accumulates one model output tile with a linear ramp over the
// overlap band, so neighboring tiles cross-fade instead of seaming.
func blendTile(acc, wgt []float32, ow, oh int, vals []float32, x0, y0, tw, th, overlap int) {
	plane := ow * oh
	tplane := tw * th
	for y := 0; y < th; y++ {
		gy := y0 + y
		if gy >= oh {
			break
		}
		wy := rampWeight(y, th, overlap)
		for x := 0; x < tw; x++ {
			gx := x0 + x
			if gx >= ow {
				break
			}
			wxy := wy * rampWeight(x, tw, overlap)
			gi := gy*ow + gx
			ti := y*tw + x
			acc[gi] += vals[ti] * wxy
			acc[plane+gi] += vals[tplane+ti] * wxy
			acc[2*plane+gi] += vals[2*tplane+ti] * wxy
			wgt[gi] += wxy
		}
	}
}

// rampWeight rises 0→1 across the leading overlap band and falls back across
// the trailing one; interior pixels weigh 1.
func rampWeight(i, n, overlap int) float32 {
	if overlap == 0 {
		return 1
	}
	w := float32(1)
	if i < overlap {
		w = (float32(i) + 0.5) / float32(overlap)
	}
	if tail := n - 1 - i; tail < overlap {
		t := (float32(tail) + 0.5) / float32(overlap)
		if t < w {
			w = t
		}
	}
	return w
}

func clampU8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}
