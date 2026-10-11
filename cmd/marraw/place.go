package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/marrasen/gunim/driver"
)

// placeFile is where the window's place on the screen is kept between
// starts: in the release's own data folder, whatever folder the backend
// is given.
func placeFile() string { return filepath.Join(defaultDataDir(), "window.json") }

// savedPlace is where the window was when marraw last ran, or nil.
// gunim fits it to the monitors attached now as the window opens.
func savedPlace() *driver.Placement {
	b, err := os.ReadFile(placeFile())
	if err != nil {
		return nil
	}
	var p driver.Placement
	if json.Unmarshal(b, &p) != nil || p.Bounds.Empty() {
		return nil
	}
	return &p
}

// keepPlace writes where the window is as it moves or changes size,
// looking each second while ctx lasts. It looks rather than waiting for
// the window to close, as a closed window can no longer say where it was.
func keepPlace(ctx context.Context, place func() (driver.Placement, bool)) {
	last, _ := place()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p, ok := place()
		if !ok || p == last || p.Bounds.Empty() {
			continue
		}
		last = p
		b, _ := json.Marshal(p)
		err := os.MkdirAll(filepath.Dir(placeFile()), 0o755)
		if err == nil {
			err = os.WriteFile(placeFile(), b, 0o644)
		}
		if err != nil {
			log.Printf("marraw: keep the window's place: %v", err)
		}
	}
}
