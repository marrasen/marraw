package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// locate shows the photo showing in the system's file manager, chosen
// there where the system can, as marraw's Locate on disk does. The file
// must be on this computer.
func (cu *culler) locate() {
	if !cu.culling || cu.at < 0 || cu.at >= len(cu.photos) {
		return
	}
	in := photoInfo(cu.photos[cu.at], cu.folderPath)
	path := filepath.Join(in.Folder, in.File)
	if _, err := os.Stat(path); err != nil {
		cu.notify("The file is not on this computer")
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		cu.notify("Could not open the file manager")
		return
	}
	go func() { _ = cmd.Wait() }()
}
