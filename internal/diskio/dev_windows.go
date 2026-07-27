package diskio

import (
	"path/filepath"
	"strings"
)

// deviceKey identifies the storage device holding path — the volume name
// (`C:` or `\\server\share`) — or "" when the path has none (the caller then
// uses one shared gate).
func deviceKey(path string) string {
	return strings.ToLower(filepath.VolumeName(path))
}
