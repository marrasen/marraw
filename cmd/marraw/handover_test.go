package main

import (
	"path/filepath"
	"testing"

	"github.com/marrasen/gunim/single"
)

func TestHandoverFolder(t *testing.T) {
	dir := filepath.FromSlash("/home/me")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-size", "1400x900"}, ""},
		{[]string{"-folder", filepath.FromSlash("/photos/shoot")}, filepath.FromSlash("/photos/shoot")},
		{[]string{"--folder=shoot", "-size", "800x600"}, filepath.Join(dir, "shoot")},
		{[]string{"-size", "800x600", "-folder", "trip"}, filepath.Join(dir, "trip")},
	} {
		if got := handoverFolder(single.Handover{Args: c.args, Dir: dir}); got != c.want {
			t.Errorf("handoverFolder(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}
