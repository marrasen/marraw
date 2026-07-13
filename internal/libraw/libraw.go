// Package libraw is a thin cgo wrapper around the LibRaw C API.
//
// One Processor wraps one libraw_data_t handle. A handle must not be used
// from two goroutines concurrently; run N Processors in parallel instead.
package libraw

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libraw/include -DLIBRAW_NODLL
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libraw/lib -lraw -lm
#cgo windows LDFLAGS: -lstdc++ -lws2_32 -static
#cgo linux LDFLAGS: -lstdc++ -lz -static-libstdc++ -static-libgcc
#cgo darwin LDFLAGS: -lc++ -lz

#include <stdlib.h>
#include <libraw/libraw.h>

// Per-handle state shared with LibRaw's progress callback. cancel is written
// by Go and read here; stage/iter/expected are written here and read by Go.
// All access is 4-byte-aligned int stores/loads (atomic on every target we
// build for); volatile keeps them out of registers across LibRaw's
// checkpoint loop. The values are monotonic hints, not synchronized data.
typedef struct {
	volatile int cancel;
	volatile int stage;
	volatile int iter;
	volatile int expected;
} marraw_cb_state;

// marraw_progress_cb runs inside libraw_dcraw_process at its pipeline
// checkpoints. Pure C on the hot path — no cgo crosscall. A nonzero return
// makes LibRaw abort with LIBRAW_CANCELLED_BY_CALLBACK.
static int marraw_progress_cb(void *d, enum LibRaw_progress stage, int iteration, int expected) {
	marraw_cb_state *s = (marraw_cb_state *)d;
	s->stage = (int)stage;
	s->iter = iteration;
	s->expected = expected;
	return s->cancel;
}

static void marraw_register_cb(libraw_data_t *h, marraw_cb_state *s) {
	libraw_set_progress_handler(h, marraw_progress_cb, s);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"
	"unsafe"
)

// The watcher goroutine accesses the C callback state via int32 atomics, so
// C.int must be exactly 4 bytes; either array is unrepresentable if not.
var (
	_ [unsafe.Sizeof(C.int(0)) - 4]byte
	_ [4 - unsafe.Sizeof(C.int(0))]byte
)

// ErrNoThumb is returned by EmbeddedThumb when the file has no usable
// embedded JPEG preview; callers fall back to a half-size RAW decode.
var ErrNoThumb = errors.New("libraw: no embedded JPEG thumbnail")

type Processor struct {
	h        *C.libraw_data_t
	unpacked bool
	// cb is read/written by LibRaw's progress callback during Process. It is
	// C-allocated so the pointer handed to C never moves; registered once —
	// libraw_recycle preserves callback registrations (only the constructor
	// zeroes them).
	cb *C.marraw_cb_state
	// onProgress, when set, receives a coarse 0..1 fraction of the running
	// dcraw pipeline. Must not be changed while Process runs.
	onProgress func(frac float64)
}

func New() (*Processor, error) {
	h := C.libraw_init(0)
	if h == nil {
		return nil, errors.New("libraw: init failed")
	}
	cb := (*C.marraw_cb_state)(C.calloc(1, C.sizeof_marraw_cb_state))
	if cb == nil {
		C.libraw_close(h)
		return nil, errors.New("libraw: callback state alloc failed")
	}
	C.marraw_register_cb(h, cb)
	return &Processor{h: h, cb: cb}, nil
}

// OnProgress sets (or, with nil, clears) the decode progress observer.
// Callers that share a Processor across jobs must clear it when the job ends.
func (p *Processor) OnProgress(fn func(frac float64)) { p.onProgress = fn }

func lrErr(op string, code C.int) error {
	return fmt.Errorf("libraw: %s: %s", op, C.GoString(C.libraw_strerror(code)))
}

// Open reads and parses the file's metadata (no pixel decode).
func (p *Processor) Open(path string) error {
	// libraw_recycle deliberately preserves params, and a pool worker reuses one
	// handle across jobs. Opening a file sizes it according to the params in
	// effect at that moment, so a half-size decode (the calibration pass) would
	// leave half_size set and the next job's Metadata() — which applies no
	// params of its own — would report half the file's real dimensions straight
	// into the catalog. Reset before opening, not after: it is the open that
	// computes the dimensions. Process() applies its own params afterwards, so
	// nothing else is affected.
	DefaultParams().apply(p.h)
	if err := p.openFile(path); err != nil { // per-OS: open_windows.go / open_unix.go
		return err
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
	Orientation  int    // EXIF-style flip: 0 none, 3=180, 5=90ccw, 6=90cw
	Lens         string // lens model, "" = unknown
	GPSValid     bool   // Latitude/Longitude hold a real fix
	Latitude     float64
	Longitude    float64 // signed decimal degrees, S/W negative
	AltValid     bool
	Altitude     float64 // meters, negative = below sea level
}

func (p *Processor) Metadata() Metadata {
	ip := C.libraw_get_iparams(p.h)
	other := C.libraw_get_imgother(p.h)
	md := Metadata{
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
		Lens:        lensOf(p.h),
	}
	md.readGPS(other.parsed_gps)
	return md
}

// lensOf reads the lens model, preferring the normalized EXIF string and
// falling back to the maker-note one (some mounts fill only the latter).
func lensOf(h *C.libraw_data_t) string {
	li := C.libraw_get_lensinfo(h)
	if s := C.GoString(&li.Lens[0]); s != "" {
		return s
	}
	return C.GoString(&li.makernotes.Lens[0])
}

// readGPS converts LibRaw's parsed GPS block to signed decimal degrees.
// gpsparsed alone is not proof of a fix: some bodies write an all-zero GPS
// block, which parses "successfully" — real hemisphere letters are required
// too. Altitude has no validity flag at all, so an exact 0 m reading is
// indistinguishable from "no altitude tag" and is dropped.
func (md *Metadata) readGPS(g C.libraw_gps_info_t) {
	if g.gpsparsed == 0 ||
		(g.latref != 'N' && g.latref != 'S') ||
		(g.longref != 'E' && g.longref != 'W') {
		return
	}
	lat := float64(g.latitude[0]) + float64(g.latitude[1])/60 + float64(g.latitude[2])/3600
	lon := float64(g.longitude[0]) + float64(g.longitude[1])/60 + float64(g.longitude[2])/3600
	if g.latref == 'S' {
		lat = -lat
	}
	if g.longref == 'W' {
		lon = -lon
	}
	if math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return
	}
	md.GPSValid = true
	md.Latitude, md.Longitude = lat, lon
	if alt := float64(g.altitude); alt != 0 {
		if g.altref != 0 {
			alt = -math.Abs(alt)
		}
		md.AltValid = true
		md.Altitude = alt
	}
}

// CamMul returns the file's as-shot white balance multipliers (falling back
// to daylight pre_mul, then unity, for files without them). Valid after Open.
func (p *Processor) CamMul() [4]float64 {
	return camMulOf(p.h)
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
//
// Cancelling ctx aborts the pipeline at LibRaw's next progress checkpoint
// (PPG demosaic checks at ⅓ intervals; AHD and X-Trans only between stages)
// and Process returns ctx.Err(). A cancelled call leaves the handle recycled
// inside LibRaw — datastream closed, unpacked data freed — so the caller must
// Open() again before further use. Callers whose handle must survive (the
// interactive HandleCache) pass context.Background(), which also skips the
// watcher goroutine entirely.
func (p *Processor) Process(ctx context.Context, params Params) (*Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.Unpack(); err != nil {
		return nil, err
	}
	// Unpack fires no usable callbacks, so a cancellation that landed during
	// it would otherwise only be noticed after the whole dcraw pipeline ran.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params.apply(p.h)
	stop := p.watch(ctx)
	ret := C.libraw_dcraw_process(p.h)
	stop()
	if ret == C.LIBRAW_CANCELLED_BY_CALLBACK {
		// LibRaw recycled the handle on its way out. Only our flag returns
		// nonzero from the callback, so this is always a ctx cancellation;
		// the lrErr return is a defensive fallback.
		p.unpacked = false
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, lrErr("dcraw_process", ret)
	}
	if ret != 0 {
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

// watch arms the cancel flag against ctx and, when an observer is set,
// samples LibRaw's progress state while the C call runs. The returned stop
// must be called as soon as the C call returns: it joins the watcher, so
// after stop() no flag write or observer call can happen — a late
// cancellation can never leak into the next Process's freshly zeroed flag.
func (p *Processor) watch(ctx context.Context) (stop func()) {
	atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.cancel)), 0)
	if ctx.Done() == nil && p.onProgress == nil {
		return func() {} // nothing to watch: no goroutine, zero overhead
	}
	// Clear the previous decode's final stage so it can't read as instant
	// high progress.
	atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.stage)), 0)
	atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.iter)), 0)
	atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.expected)), 0)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		var tick <-chan time.Time
		if p.onProgress != nil {
			t := time.NewTicker(120 * time.Millisecond)
			defer t.Stop()
			tick = t.C
		}
		last := -1.0
		for {
			select {
			case <-ctx.Done(): // nil Done blocks forever: progress-only watch
				atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.cancel)), 1)
				return
			case <-tick:
				if f := p.progressFrac(); f > last {
					last = f
					p.onProgress(f)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-exited
		// The join above means no writer is left; clear the flag so it can
		// never leak past this call — Open() also fires progress callbacks,
		// and a stale 1 would cancel an innocent open. This covers both a
		// cancelled decode and a cancellation that landed in the gap between
		// the C call returning and this stop.
		atomic.StoreInt32((*int32)(unsafe.Pointer(&p.cb.cancel)), 0)
	}
}

// progressSpans maps a LibRaw pipeline stage to its (base, span) share of the
// whole dcraw_process run, in pipeline order. The weights are rough elapsed-
// time shares of a Bayer decode: demosaic dominates, output color conversion
// is the runner-up, the rest is thin. Stages not listed (X-Trans, Foveon,
// thumb stages) keep the last known fraction — progress stalls rather than
// jumps backwards.
var progressSpans = map[int32][2]float64{
	int32(C.LIBRAW_PROGRESS_RAW2_IMAGE):      {0.00, 0.02},
	int32(C.LIBRAW_PROGRESS_SCALE_COLORS):    {0.02, 0.04},
	int32(C.LIBRAW_PROGRESS_PRE_INTERPOLATE): {0.06, 0.04},
	int32(C.LIBRAW_PROGRESS_INTERPOLATE):     {0.10, 0.65},
	int32(C.LIBRAW_PROGRESS_MIX_GREEN):       {0.75, 0.01},
	int32(C.LIBRAW_PROGRESS_MEDIAN_FILTER):   {0.76, 0.02},
	int32(C.LIBRAW_PROGRESS_HIGHLIGHTS):      {0.78, 0.04},
	int32(C.LIBRAW_PROGRESS_FUJI_ROTATE):     {0.82, 0.03},
	int32(C.LIBRAW_PROGRESS_CONVERT_RGB):     {0.85, 0.12},
	int32(C.LIBRAW_PROGRESS_STRETCH):         {0.97, 0.03},
}

// progressFrac converts the callback's latest (stage, iteration, expected)
// into a coarse 0..1 fraction of the dcraw pipeline.
func (p *Processor) progressFrac() float64 {
	stage := atomic.LoadInt32((*int32)(unsafe.Pointer(&p.cb.stage)))
	span, ok := progressSpans[stage]
	if !ok {
		return 0
	}
	iter := atomic.LoadInt32((*int32)(unsafe.Pointer(&p.cb.iter)))
	expected := atomic.LoadInt32((*int32)(unsafe.Pointer(&p.cb.expected)))
	sub := 0.0
	if expected > 0 && iter > 0 {
		sub = min(float64(iter)/float64(expected), 1)
	}
	return span[0] + span[1]*sub
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
	if p.cb != nil {
		C.free(unsafe.Pointer(p.cb))
		p.cb = nil
	}
}
