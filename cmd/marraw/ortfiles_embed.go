//go:build ortembed

package main

import (
	"embed"
	"io/fs"
)

// ortEmbed holds ONNX Runtime's libraries in a release build: the
// release workflow copies them into ortlib before it builds.
//
//go:embed ortlib/*
var ortEmbed embed.FS

func init() {
	sub, err := fs.Sub(ortEmbed, "ortlib")
	if err == nil {
		ortFiles = sub
	}
}
