//go:build !windows

package trash

import "errors"

// MoveToTrash is only implemented on Windows (marraw's target platform).
func MoveToTrash(paths []string) error {
	return errors.New("trash: not supported on this platform")
}
