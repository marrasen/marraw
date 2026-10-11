package main

import "path/filepath"

// placeFile is where gunim keeps the window's place on the screen
// between starts, written as the window closes: in the release's own
// data folder, whatever folder the backend is given.
func placeFile() string { return filepath.Join(defaultDataDir(), "window.json") }
