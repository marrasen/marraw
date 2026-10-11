package main

import (
	"testing"

	"github.com/marrasen/gunim/driver"
)

func TestColourShortNamesWhoConverts(t *testing.T) {
	for detail, want := range map[string]string{
		"Windows manages the display's colour (HDR)":                   "colour: Windows (HDR)",
		"Windows manages the display's colour (Auto Color Management)": "colour: Windows (Auto Color)",
		"Windows manages the display's colour (advanced colour)":       "colour: Windows",
		"macOS converts the window from sRGB":                          "colour: macOS",
		"":                                                             "colour: by the system",
	} {
		if got := colourShort(driver.ColorInfo{State: driver.ColorBySystem, Detail: detail}); got != want {
			t.Errorf("%q: got %q, want %q", detail, got, want)
		}
	}
}
