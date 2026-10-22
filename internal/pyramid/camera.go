package pyramid

import (
	"bytes"
	"image"
	"os"
	"strconv"

	"github.com/marrasen/marraw/internal/edit"
)

// A small level drawn from the camera's embedded JPEG (tryThumbRoute,
// deriveFromThumb) is a stand-in: marraw's own render of the unedited
// photo writes over it later under the same name (writeRendered), so the
// grid shows the photo as the photo view does. The file says what it is
// in a JPEG comment, so the image server can tell a browser to ask again
// for it rather than keep it for good, and the cache can tell clients
// when its render replaced one (OnBaseRendered).
//
// The mark lives in the file, not beside it: it goes when the file is
// written over or swept, never apart from it.

// cameraTag is the comment a camera-drawn level carries.
var cameraTag = []byte("marraw:camera")

// CameraHeadLen is how much of a file's start IsCameraJPEG reads: the
// start of image, a JFIF header, and the comment.
const CameraHeadLen = 64

// tagCamera puts the camera comment into jpg after its JFIF header, where
// decoders read past it.
func tagCamera(jpg []byte) []byte {
	at := jfifEnd(jpg)
	if at < 0 {
		return jpg
	}
	n := 2 + len(cameraTag)
	seg := append([]byte{0xFF, 0xFE, byte(n >> 8), byte(n)}, cameraTag...)
	out := make([]byte, 0, len(jpg)+len(seg))
	out = append(out, jpg[:at]...)
	out = append(out, seg...)
	return append(out, jpg[at:]...)
}

// IsCameraJPEG reports whether head, the first CameraHeadLen bytes of a
// JPEG file or all of a shorter one, carries the camera comment.
func IsCameraJPEG(head []byte) bool {
	at := jfifEnd(head)
	if at < 0 || at+4 > len(head) || head[at] != 0xFF || head[at+1] != 0xFE {
		return false
	}
	return bytes.HasPrefix(head[at+4:], cameraTag)
}

// jfifEnd is where the segments after jpg's start of image and JFIF
// header, if it has one, begin; -1 if jpg is no JPEG.
func jfifEnd(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return -1
	}
	at := 2
	if jpg[2] == 0xFF && jpg[3] == 0xE0 {
		if len(jpg) < 6 {
			return -1
		}
		at += 2 + (int(jpg[4])<<8 | int(jpg[5]))
		if at > len(jpg) {
			return -1
		}
	}
	return at
}

// isCameraFile reports whether the file at path is a camera-drawn level.
func isCameraFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, CameraHeadLen)
	n, _ := f.ReadAt(head, 0)
	return IsCameraJPEG(head[:n])
}

// cameraLevels reports whether any of the unedited small levels of
// cacheKey is camera-drawn.
func (c *Cache) cameraLevels(cacheKey string) bool {
	for _, l := range []string{"1024", "512", "256"} {
		if isCameraFile(c.PathFor(cacheKey, l, edit.BaseHash)) {
			return true
		}
	}
	return false
}

// writeCameraLevels is WriteLevels for levels drawn from the camera's
// embedded JPEG: each carries the camera comment.
func (c *Cache) writeCameraLevels(src *image.RGBA, cacheKey, editHash string, levels ...int) error {
	cur := src
	for _, l := range levels {
		cur = scaleToLongEdge(cur, l)
		q := 80
		if l >= 1024 {
			q = 85
		}
		if _, err := writeJPEGFileTagged(c.PathFor(cacheKey, strconv.Itoa(l), editHash), cur, q, false, true); err != nil {
			return err
		}
	}
	return nil
}
