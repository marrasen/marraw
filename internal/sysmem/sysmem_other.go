//go:build !windows

package sysmem

import "errors"

// Query is only implemented on Windows (marraw's target platform).
func Query() (Stats, error) {
	return Stats{}, errors.New("sysmem: not supported on this platform")
}
