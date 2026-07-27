package libraw

/*
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

// FileBuf is a C-allocated copy of a RAW file's bytes. LibRaw's buffer
// datastream stores the pointer without copying, so the memory must live
// outside the Go heap and outlive the open — OpenBuffer transfers ownership
// to the Processor, which frees it on Recycle/Close/next open.
type FileBuf struct {
	ptr  unsafe.Pointer // C-allocated; nil after Free
	size int
	// onFree runs exactly once, when the memory is actually released; the
	// diskio gate uses it to return the bytes to its budget.
	onFree func()
}

// liveFileBufs counts outstanding buffers; tests assert it drains to zero
// since cgo allocations are invisible to the race detector and GC.
var liveFileBufs atomic.Int64

// LiveFileBufs reports the number of allocated, not-yet-freed FileBufs.
func LiveFileBufs() int64 { return liveFileBufs.Load() }

// NewFileBuf allocates C memory for size bytes. onFree, if non-nil, runs
// exactly once when the buffer is freed.
func NewFileBuf(size int, onFree func()) (*FileBuf, error) {
	if size <= 0 {
		return nil, errors.New("libraw: filebuf size must be positive")
	}
	ptr := C.malloc(C.size_t(size))
	if ptr == nil {
		return nil, errors.New("libraw: filebuf alloc failed")
	}
	liveFileBufs.Add(1)
	return &FileBuf{ptr: ptr, size: size, onFree: onFree}, nil
}

// Bytes returns a Go view of the C memory, for reading the file into.
// Invalid after Free.
func (b *FileBuf) Bytes() []byte {
	return unsafe.Slice((*byte)(b.ptr), b.size)
}

func (b *FileBuf) Len() int { return b.size }

// Free releases the C memory and runs onFree. Idempotent.
func (b *FileBuf) Free() {
	if b.ptr == nil {
		return
	}
	C.free(b.ptr)
	b.ptr = nil
	liveFileBufs.Add(-1)
	if b.onFree != nil {
		b.onFree()
		b.onFree = nil
	}
}
