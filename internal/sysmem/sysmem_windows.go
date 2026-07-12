//go:build windows

package sysmem

import (
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// memoryStatusEx mirrors MEMORYSTATUSEX; field order and Go's natural
// alignment reproduce the MSVC layout on amd64/arm64.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// Query reads the current physical-memory snapshot via GlobalMemoryStatusEx.
func Query() (Stats, error) {
	var ms memoryStatusEx
	ms.length = uint32(unsafe.Sizeof(ms))
	ret, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return Stats{}, err
	}
	return Stats{TotalPhys: ms.totalPhys, AvailPhys: ms.availPhys}, nil
}
