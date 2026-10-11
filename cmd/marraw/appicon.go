package main

import (
	"bytes"
	"embed"
	"image"
	"image/png"
	"io/fs"
)

// appIconFiles are marraw's icon at the sizes a title bar and a taskbar
// take, made from assets/icon.png. The program's own icon, in Explorer,
// comes from rsrc_windows_amd64.syso, made from assets/icon.ico.
//
//go:embed appicon/*.png
var appIconFiles embed.FS

// appIcons are marraw's icon as pictures, for the windows.
func appIcons() []image.Image {
	var out []image.Image
	names, _ := fs.Glob(appIconFiles, "appicon/*.png")
	for _, n := range names {
		b, err := appIconFiles.ReadFile(n)
		if err != nil {
			continue
		}
		if m, err := png.Decode(bytes.NewReader(b)); err == nil {
			out = append(out, m)
		}
	}
	return out
}
