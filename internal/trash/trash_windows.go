// Package trash moves files to the OS recycle bin (undoable delete).
package trash

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	shell32          = syscall.NewLazyDLL("shell32.dll")
	shFileOperationW = shell32.NewProc("SHFileOperationW")
)

const (
	foDelete          = 3
	fofAllowUndo      = 0x0040
	fofNoConfirmation = 0x0010
	fofSilent         = 0x0004
	fofNoErrorUI      = 0x0400
)

// shFileOpStruct mirrors SHFILEOPSTRUCTW; field order and Go's natural
// alignment reproduce the MSVC layout on amd64/arm64.
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// errSharingViolation is SHFileOperation's DE_SHAREVIOLATION-mapped code:
// another process holds the file open. Background calibrate/pre-render jobs
// keep a photo open for up to a couple of seconds (LibRaw's fopen has no
// share-delete), so a delete racing one is normal — retry briefly.
const errSharingViolation = 0x20

// MoveToTrash sends the given absolute paths to the recycle bin in one
// operation. Returns an error if the shell reports failure or aborts.
// Sharing violations are retried for a few seconds: a background render
// finishing releases the file.
func MoveToTrash(paths []string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := moveToTrashOnce(paths)
		if err == nil || !isSharingViolation(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type shellOpError struct {
	code uintptr
}

func (e *shellOpError) Error() string {
	return fmt.Sprintf("trash: SHFileOperation failed with code 0x%x", e.code)
}

func isSharingViolation(err error) bool {
	se, ok := err.(*shellOpError)
	return ok && se.code == errSharingViolation
}

func moveToTrashOnce(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// pFrom is a double-null-terminated list of null-terminated paths.
	var from []uint16
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p) // includes the terminating NUL
		if err != nil {
			return fmt.Errorf("trash: bad path %q: %w", p, err)
		}
		from = append(from, u...)
	}
	from = append(from, 0)

	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}
	ret, _, _ := shFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return &shellOpError{code: ret}
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("trash: operation aborted")
	}
	return nil
}
