//go:build !windows && !linux && !darwin

package trash

import "errors"

// MoveToTrash is implemented on Windows, Linux and macOS only.
func MoveToTrash(paths []string) error {
	return errors.New("trash: not supported on this platform")
}
