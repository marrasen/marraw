//go:build windows

package libraw

/*
#include <libraw/libraw.h>
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

// openFile opens via libraw_open_wfile: Windows paths are UTF-16, and the
// narrow libraw_open_file would mangle non-ANSI characters in them.
func (p *Processor) openFile(path string) error {
	w, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("libraw: bad path %q: %w", path, err)
	}
	if ret := C.libraw_open_wfile(p.h, (*C.wchar_t)(unsafe.Pointer(w))); ret != 0 {
		return lrErr("open "+path, ret)
	}
	return nil
}
