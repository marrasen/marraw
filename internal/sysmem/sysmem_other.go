//go:build !windows && !linux

package sysmem

import "errors"

// Query is implemented on Windows and Linux. Elsewhere (macOS) callers fall
// back to their conservative defaults — see export.go's fallbackAvail.
func Query() (Stats, error) {
	return Stats{}, errors.New("sysmem: not supported on this platform")
}
