package libraw

import (
	"os"
	"reflect"
	"testing"
)

// bufFromFile copies path into a fresh FileBuf.
func bufFromFile(t *testing.T, path string, onFree func()) *FileBuf {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := NewFileBuf(len(data), onFree)
	if err != nil {
		t.Fatal(err)
	}
	copy(fb.Bytes(), data)
	return fb
}

func TestOpenBufferParity(t *testing.T) {
	path := sampleRAW(t)
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	fromFile := p.Metadata()
	p.Recycle()

	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	fromBuf := p.Metadata()
	if !reflect.DeepEqual(fromFile, fromBuf) {
		t.Fatalf("metadata differs:\n file:   %+v\n buffer: %+v", fromFile, fromBuf)
	}

	// The thumb and a decode must work off the buffer datastream too.
	if _, err := p.EmbeddedThumb(); err != nil {
		t.Fatalf("EmbeddedThumb from buffer: %v", err)
	}
	params := DefaultParams()
	params.HalfSize = true
	params.UserQual = DemosaicLinear
	img, err := p.Process(t.Context(), params)
	if err != nil {
		t.Fatalf("Process from buffer: %v", err)
	}
	if img.Width == 0 || len(img.Data) == 0 {
		t.Fatalf("empty image from buffer decode: %+v", img)
	}
}

// Every path a held buffer can take — Recycle, Open over it, OpenBuffer over
// it, a failed open, Close — must end with the C memory freed exactly once.
func TestFileBufOwnership(t *testing.T) {
	path := sampleRAW(t)
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("%d live buffers before test", n)
	}

	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// OpenBuffer → Recycle frees.
	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	p.Recycle()
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("after Recycle: %d live buffers, want 0", n)
	}

	// OpenBuffer → Open(path) frees the stale buffer.
	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	if err := p.Open(path); err != nil {
		t.Fatal(err)
	}
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("after Open over buffer: %d live buffers, want 0", n)
	}

	// OpenBuffer → OpenBuffer (no Recycle between) frees the first.
	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	if n := LiveFileBufs(); n != 1 {
		t.Fatalf("after double OpenBuffer: %d live buffers, want 1", n)
	}

	// Garbage bytes: the open fails and frees the garbage buffer; the
	// internal Recycle also frees the previously held one.
	garbage, err := NewFileBuf(64, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range garbage.Bytes() {
		garbage.Bytes()[i] = 0xAB
	}
	if err := p.OpenBuffer(garbage); err == nil {
		t.Fatal("OpenBuffer accepted garbage")
	}
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("after failed open: %d live buffers, want 0", n)
	}

	// Close frees a held buffer.
	if err := p.OpenBuffer(bufFromFile(t, path, nil)); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("after Close: %d live buffers, want 0", n)
	}
}

func TestFileBufFreeIdempotentOnFreeOnce(t *testing.T) {
	freed := 0
	fb, err := NewFileBuf(16, func() { freed++ })
	if err != nil {
		t.Fatal(err)
	}
	fb.Free()
	fb.Free()
	if freed != 1 {
		t.Fatalf("onFree ran %d times, want 1", freed)
	}
	if n := LiveFileBufs(); n != 0 {
		t.Fatalf("%d live buffers after Free, want 0", n)
	}
	if _, err := NewFileBuf(0, nil); err == nil {
		t.Fatal("NewFileBuf(0) succeeded")
	}
}
