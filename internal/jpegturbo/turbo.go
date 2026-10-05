//go:build cgo

package jpegturbo

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/libjpeg-turbo/include
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/libjpeg-turbo/lib -lturbojpeg
#include <stdlib.h>
#include <turbojpeg.h>

// tj3Init is a macro since libjpeg-turbo 3.2, which cgo cannot call.
static tjhandle mw_tj_init(int type) { return tj3Init(type); }
*/
import "C"

import (
	"bytes"
	"errors"
	"image"
	"io"
	"unsafe"
)

// DecodeRGBA decodes the JPEG in data to an *image.RGBA, opaque.
func DecodeRGBA(data []byte) (*image.RGBA, error) {
	if len(data) == 0 {
		return nil, errors.New("jpegturbo: no data")
	}
	h := C.mw_tj_init(C.TJINIT_DECOMPRESS)
	if h == nil {
		return nil, errors.New("jpegturbo: cannot start a decompressor")
	}
	defer C.tj3Destroy(h)
	src := (*C.uchar)(unsafe.Pointer(&data[0]))
	if C.tj3DecompressHeader(h, src, C.size_t(len(data))) != 0 {
		return nil, turboErr(h)
	}
	if C.tj3Get(h, C.TJPARAM_COLORSPACE) == C.TJCS_CMYK || C.tj3Get(h, C.TJPARAM_COLORSPACE) == C.TJCS_YCCK {
		return decodeGo(bytes.NewReader(data))
	}
	w, ht := int(C.tj3Get(h, C.TJPARAM_JPEGWIDTH)), int(C.tj3Get(h, C.TJPARAM_JPEGHEIGHT))
	if w <= 0 || ht <= 0 {
		return nil, errors.New("jpegturbo: no size in the header")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, ht))
	dst := (*C.uchar)(unsafe.Pointer(&img.Pix[0]))
	if C.tj3Decompress8(h, src, C.size_t(len(data)), dst, C.int(img.Stride), C.TJPF_RGBA) != 0 {
		// A warning, as for a truncated file, still decodes what it can;
		// image/jpeg would refuse it, and so does this.
		return nil, turboErr(h)
	}
	return img, nil
}

// Decode reads a JPEG from r, decoded to an *image.RGBA.
func Decode(r io.Reader) (*image.RGBA, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return DecodeRGBA(data)
}

// EncodeRGBA writes img to w as a JPEG of quality from 1 to 100, its
// chroma at half resolution both ways, as image/jpeg writes it.
func EncodeRGBA(w io.Writer, img *image.RGBA, quality int) error {
	b := img.Rect
	if b.Empty() {
		return errors.New("jpegturbo: empty image")
	}
	h := C.mw_tj_init(C.TJINIT_COMPRESS)
	if h == nil {
		return errors.New("jpegturbo: cannot start a compressor")
	}
	defer C.tj3Destroy(h)
	C.tj3Set(h, C.TJPARAM_QUALITY, C.int(max(1, min(quality, 100))))
	C.tj3Set(h, C.TJPARAM_SUBSAMP, C.TJSAMP_420)
	src := (*C.uchar)(unsafe.Pointer(&img.Pix[img.PixOffset(b.Min.X, b.Min.Y)]))
	var out *C.uchar
	var size C.size_t
	if C.tj3Compress8(h, src, C.int(b.Dx()), C.int(img.Stride), C.int(b.Dy()), C.TJPF_RGBA, &out, &size) != 0 {
		if out != nil {
			C.tj3Free(unsafe.Pointer(out))
		}
		return turboErr(h)
	}
	defer C.tj3Free(unsafe.Pointer(out))
	_, err := w.Write(unsafe.Slice((*byte)(unsafe.Pointer(out)), int(size)))
	return err
}

// turboErr is the library's last error on h.
func turboErr(h C.tjhandle) error {
	return errors.New("jpegturbo: " + C.GoString(C.tj3GetErrorStr(h)))
}
