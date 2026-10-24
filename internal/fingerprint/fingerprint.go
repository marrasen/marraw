// Package fingerprint recognises a photo file by its content, whatever it is
// called and wherever it lies: a copy on a memory card and a copy on disk give
// the same fingerprint, and two cameras' DSC0001.ARW give different ones.
//
// It reads the file's size, its first 64 KiB, which hold the camera's header
// (capture time, serial number, settings), and its last 64 KiB, which are
// image data and differ from shot to shot even where the files' sizes are the
// same, as an uncompressed RAW's are. Two small reads a file, done once.
package fingerprint

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
)

// span is how much of each end of the file is read.
const span = 64 << 10

// Version starts every fingerprint, so a later way of taking them can tell
// its own from these.
const Version = "1:"

// Of is the fingerprint of the file at path.
func Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return of(f, st.Size())
}

func of(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(size))
	h.Write(n[:])
	buf := make([]byte, min(size, span))
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return "", err
	}
	h.Write(buf)
	if size > 2*span {
		if _, err := r.ReadAt(buf, size-span); err != nil && err != io.EOF {
			return "", err
		}
		h.Write(buf)
	} else if size > span {
		tail := make([]byte, size-span)
		if _, err := r.ReadAt(tail, span); err != nil && err != io.EOF {
			return "", err
		}
		h.Write(tail)
	}
	return Version + hex.EncodeToString(h.Sum(nil)[:16]), nil
}
