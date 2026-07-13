//go:build linux

package trash

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MoveToTrash sends the given absolute paths to the freedesktop trash.
// `gio trash` is preferred — it implements the full spec, including the
// per-volume .Trash-$uid directories that photos on external drives need.
// Without gio (headless/minimal distros) a home-trash fallback covers files
// on the same filesystem as $HOME; anything else is a hard error rather
// than a silent permanent delete.
func MoveToTrash(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if gio, err := exec.LookPath("gio"); err == nil {
		args := append([]string{"trash", "--"}, paths...)
		out, err := exec.Command(gio, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("trash: gio trash: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	for _, p := range paths {
		if err := homeTrash(p); err != nil {
			return err
		}
	}
	return nil
}

// homeTrash implements the home-trash half of the freedesktop trash spec:
// claim a unique name in Trash/info via O_EXCL, then rename the file into
// Trash/files. A cross-device rename fails with EXDEV; copying a RAW file
// "to trash" would double its disk usage, so that case stays an error.
func homeTrash(path string) error {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("trash: no home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	trashDir := filepath.Join(dataHome, "Trash")
	filesDir := filepath.Join(trashDir, "files")
	infoDir := filepath.Join(trashDir, "info")
	for _, d := range []string{filesDir, infoDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("trash: %w", err)
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	// The spec wants the Path= value percent-encoded like a file URI.
	escaped := (&url.URL{Path: abs}).EscapedPath()
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		escaped, time.Now().Format("2006-01-02T15:04:05"))

	base := filepath.Base(abs)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			ext := filepath.Ext(base)
			name = fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), i, ext)
		}
		f, err := os.OpenFile(filepath.Join(infoDir, name+".trashinfo"),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue // name taken; try the next suffix
		}
		if err != nil {
			return fmt.Errorf("trash: %w", err)
		}
		_, werr := f.WriteString(info)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Rename(abs, filepath.Join(filesDir, name))
		}
		if werr != nil {
			os.Remove(f.Name())
			return fmt.Errorf("trash: %s: %w (install gio for cross-filesystem trash)", path, werr)
		}
		return nil
	}
}
