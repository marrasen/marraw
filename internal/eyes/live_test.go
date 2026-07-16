package eyes

import (
	"context"
	"image"
	"image/color"
	_ "image/jpeg"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/infer"
)

// TestScoreLive runs the real models over a real portrait. Opt-in: it needs
// the ONNX runtime and both weights on disk, so it only runs when
// MARRAW_EYES_TESTIMG points at a JPEG with one frontal face with open eyes,
// and MARRAW_EYES_MODELS_DIR at a dir holding yunet-2023mar.onnx and
// openclosedeye-0001.onnx.
func TestScoreLive(t *testing.T) {
	imgPath := os.Getenv("MARRAW_EYES_TESTIMG")
	modelsDir := os.Getenv("MARRAW_EYES_MODELS_DIR")
	if imgPath == "" || modelsDir == "" {
		t.Skip("set MARRAW_EYES_TESTIMG and MARRAW_EYES_MODELS_DIR to run")
	}
	mgr := infer.NewManager(modelsDir)
	if !ModelsInstalled(mgr) {
		t.Fatalf("models not present in %s", modelsDir)
	}
	f, err := os.Open(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}

	faces, err := detectFaces(context.Background(), mgr, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("faces: %d", len(faces))
	for _, fc := range faces {
		t.Logf("  score=%.3f box=%.0f eyeR=%.0f eyeL=%.0f", fc.score, fc.box, fc.eyeR, fc.eyeL)
	}
	if len(faces) != 1 {
		t.Fatalf("want exactly 1 face in the portrait, got %d", len(faces))
	}

	score, ok, err := Score(context.Background(), mgr, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("closed-eye score: %.3f scored=%v", score, ok)
	if !ok {
		t.Fatal("expected a judgeable eye")
	}
	if score >= 0.5 {
		t.Fatalf("open-eye portrait scored closed (%.3f)", score)
	}

	// A featureless gradient must detect nothing and return the sentinel path.
	flat := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			flat.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	_, ok, err = Score(context.Background(), mgr, flat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("gradient image should have no judgeable face")
	}
}
