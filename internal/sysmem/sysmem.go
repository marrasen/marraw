// Package sysmem reports physical memory on the host.
package sysmem

// Stats is a point-in-time snapshot of physical RAM.
type Stats struct {
	TotalPhys uint64 // bytes of installed physical RAM
	AvailPhys uint64 // bytes currently available without paging
}
