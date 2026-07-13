//go:build !windows

package libraw

/*
#include <stdlib.h>
#include <libraw/libraw.h>
*/
import "C"

import "unsafe"

// openFile opens via the narrow libraw_open_file: Unix paths are byte
// strings, which Go already stores as UTF-8.
func (p *Processor) openFile(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if ret := C.libraw_open_file(p.h, cpath); ret != 0 {
		return lrErr("open "+path, ret)
	}
	return nil
}
