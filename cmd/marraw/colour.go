package main

import (
	"time"

	"github.com/marrasen/gunim/driver"
)

// ColourIn is how the window's colours reach the display, for the status
// bar: Short in a few words, Long in full.
type ColourIn struct{ Short, Long string }

// colourShort is info in a few words.
func colourShort(info driver.ColorInfo) string {
	switch info.State {
	case driver.ColorConverted:
		if info.Profile != "" {
			return "colour: " + info.Profile
		}
		return "colour: converted"
	case driver.ColorNoProfile:
		return "colour: no profile"
	case driver.ColorSRGB:
		return "colour: sRGB display"
	case driver.ColorBySystem:
		return "colour: by the system"
	case driver.ColorUnsupported:
		return "colour: profile not supported"
	case driver.ColorOff:
		return "colour: off"
	case driver.ColorUnmanaged:
		return "colour: not managed"
	}
	return ""
}

// watchColour tells the status bar how the window's colours reach the
// display, and again whenever that changes, as when the window goes to
// another monitor.
func (cu *culler) watchColour() {
	var last driver.ColorInfo
	tick := func() {
		info := cu.c.ColorProfile()
		if info == last {
			return
		}
		last = info
		_ = cu.c.Patch("grid", ColourIn{Short: colourShort(info), Long: info.String()})
	}
	tick()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				cu.onDo(tick)
			case <-cu.ctx.Done():
				return
			}
		}
	}()
}
