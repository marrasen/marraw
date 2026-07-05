// Package libraw is a thin cgo wrapper around the LibRaw C API.
//
// One Processor wraps one libraw_data_t handle. A handle must not be used
// from two goroutines concurrently; run N Processors in parallel instead.
package libraw

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraw/include -DLIBRAW_NODLL
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraw/lib -lraw -lstdc++ -lws2_32 -lm -static

#include <stdlib.h>
#include <libraw/libraw.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// ErrNoThumb is returned by EmbeddedThumb when the file has no usable
// embedded JPEG preview; callers fall back to a half-size RAW decode.
var ErrNoThumb = errors.New("libraw: no embedded JPEG thumbnail")

type Processor struct {
	h        *C.libraw_data_t
	unpacked bool
}

func New() (*Processor, error) {
	h := C.libraw_init(0)
	if h == nil {
		return nil, errors.New("libraw: init failed")
	}
	return &Processor{h: h}, nil
}

func lrErr(op string, code C.int) error {
	return fmt.Errorf("libraw: %s: %s", op, C.GoString(C.libraw_strerror(code)))
}

// Open reads and parses the file's metadata (no pixel decode).
func (p *Processor) Open(path string) error {
	w, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("libraw: bad path %q: %w", path, err)
	}
	if ret := C.libraw_open_wfile(p.h, (*C.wchar_t)(unsafe.Pointer(w))); ret != 0 {
		return lrErr("open "+path, ret)
	}
	p.unpacked = false
	return nil
}

type Metadata struct {
	Make, Model  string
	ISO          float64
	Shutter      float64 // seconds
	Aperture     float64 // f-number
	FocalLen     float64 // mm
	TakenAt      time.Time
	Width        int // visible processed size (pre-flip)
	Height       int
	Orientation  int // EXIF-style flip: 0 none, 3=180, 5=90ccw, 6=90cw
}

func (p *Processor) Metadata() Metadata {
	ip := C.libraw_get_iparams(p.h)
	other := C.libraw_get_imgother(p.h)
	return Metadata{
		Make:        C.GoString(&ip.make[0]),
		Model:       C.GoString(&ip.model[0]),
		ISO:         float64(other.iso_speed),
		Shutter:     float64(other.shutter),
		Aperture:    float64(other.aperture),
		FocalLen:    float64(other.focal_len),
		TakenAt:     time.Unix(int64(other.timestamp), 0),
		Width:       int(C.libraw_get_iwidth(p.h)),
		Height:      int(C.libraw_get_iheight(p.h)),
		Orientation: int(p.h.sizes.flip),
	}
}

// EmbeddedThumb extracts the largest embedded preview as raw JPEG bytes
// without decoding any RAW data.
func (p *Processor) EmbeddedThumb() ([]byte, error) {
	if ret := C.libraw_unpack_thumb(p.h); ret != 0 {
		return nil, ErrNoThumb
	}
	var errc C.int
	img := C.libraw_dcraw_make_mem_thumb(p.h, &errc)
	if img == nil {
		return nil, lrErr("make_mem_thumb", errc)
	}
	defer C.libraw_dcraw_clear_mem(img)
	if img._type != C.LIBRAW_IMAGE_JPEG {
		return nil, ErrNoThumb
	}
	data := C.GoBytes(unsafe.Pointer(&img.data[0]), C.int(img.data_size))
	return data, nil
}

// Unpack decodes the RAW sensor data. Idempotent per opened file.
func (p *Processor) Unpack() error {
	if p.unpacked {
		return nil
	}
	if ret := C.libraw_unpack(p.h); ret != 0 {
		return lrErr("unpack", ret)
	}
	p.unpacked = true
	return nil
}

// Image is an interleaved RGB bitmap produced by Process.
type Image struct {
	Width, Height int
	Channels      int // always 3 for RGB output
	Bits          int // 8 or 16 (16-bit data is host-endian uint16)
	Data          []byte
}

// Process runs the dcraw pipeline with the given params and returns the
// result. The unpacked sensor data is retained, so Process may be called
// repeatedly with different params without re-reading the file — this is
// what makes interactive editing cheap.
func (p *Processor) Process(params Params) (*Image, error) {
	if err := p.Unpack(); err != nil {
		return nil, err
	}
	params.apply(p.h)
	if ret := C.libraw_dcraw_process(p.h); ret != 0 {
		return nil, lrErr("dcraw_process", ret)
	}
	var errc C.int
	img := C.libraw_dcraw_make_mem_image(p.h, &errc)
	// Free LibRaw's intermediate image buffer but keep rawdata for reprocessing.
	defer C.libraw_free_image(p.h)
	if img == nil {
		return nil, lrErr("make_mem_image", errc)
	}
	defer C.libraw_dcraw_clear_mem(img)
	if img._type != C.LIBRAW_IMAGE_BITMAP {
		return nil, fmt.Errorf("libraw: unexpected image type %d", int(img._type))
	}
	out := &Image{
		Width:    int(img.width),
		Height:   int(img.height),
		Channels: int(img.colors),
		Bits:     int(img.bits),
		Data:     C.GoBytes(unsafe.Pointer(&img.data[0]), C.int(img.data_size)),
	}
	return out, nil
}

// Recycle resets the handle so it can Open another file.
func (p *Processor) Recycle() {
	C.libraw_recycle(p.h)
	p.unpacked = false
}

func (p *Processor) Close() {
	if p.h != nil {
		C.libraw_close(p.h)
		p.h = nil
	}
}
