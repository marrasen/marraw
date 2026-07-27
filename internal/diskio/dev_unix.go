//go:build !windows

package diskio

import (
	"fmt"
	"os"
	"syscall"
)

// deviceKey identifies the storage device holding path, or "" when unknown
// (the caller then uses one shared gate).
func deviceKey(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("dev:%d", st.Dev)
}
