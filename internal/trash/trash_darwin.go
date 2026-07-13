//go:build darwin

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation

#include <stdlib.h>
#include <string.h>
#import <Foundation/Foundation.h>

// marraw_trash moves one file to the user's Trash. Returns NULL on success,
// else a strdup'ed error message the caller must free.
static char *marraw_trash(const char *cpath) {
	@autoreleasepool {
		NSString *path = [NSString stringWithUTF8String:cpath];
		NSURL *url = [NSURL fileURLWithPath:path];
		NSError *err = nil;
		BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:url
		                                        resultingItemURL:nil
		                                                   error:&err];
		if (ok) {
			return NULL;
		}
		const char *msg = err ? [[err localizedDescription] UTF8String] : "unknown error";
		return strdup(msg);
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// MoveToTrash sends the given absolute paths to the macOS Trash via
// NSFileManager, the same operation Finder's Move to Trash performs
// (undoable, works across volumes).
func MoveToTrash(paths []string) error {
	for _, p := range paths {
		cp := C.CString(p)
		msg := C.marraw_trash(cp)
		C.free(unsafe.Pointer(cp))
		if msg != nil {
			err := C.GoString(msg)
			C.free(unsafe.Pointer(msg))
			return fmt.Errorf("trash: %s: %s", p, err)
		}
	}
	return nil
}
