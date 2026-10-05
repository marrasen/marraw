//go:build !nobackend

package main

import (
	"testing"

	"github.com/marrasen/marraw/internal/pyramid"
)

func TestRenderVersionMatchesTheBackend(t *testing.T) {
	if renderVersion != pyramid.RenderVersion {
		t.Fatalf("cmd/marraw asks for render version %q, and the backend renders %q", renderVersion, pyramid.RenderVersion)
	}
}
