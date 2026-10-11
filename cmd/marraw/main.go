// Command marraw is a test build of marraw as one Go program drawn with
// gunim: the library grid and the cull view, beside the Electron app, to
// learn what gunim needs for photos before any wider port.
//
// It runs the backend as a process of its own, this same program started
// again with -backend, at a lower priority, on a private loopback port, and
// talks to it exactly as it would to another machine's marraw: the
// generated aprot Go client over WebSocket, and HTTP for the images. The
// backend's work then never takes the window's time. -in-process runs it
// inside this process instead, as before, and -connect points the same
// code at another instance.
//
//	go run ./cmd/marraw -folder ~/Pictures/shoot
//	go run ./cmd/marraw -connect 192.168.1.20:8482 -token … -folder D:\Photos\shoot
//	go run ./cmd/marraw -folder ~/Pictures/shoot -skim 40 -every 150ms
//
// It opens on the folder's grid, or without -folder on an empty one, with
// the library's shoots in a sidebar: a click on one opens it. The arrow keys
// and the mouse select, 0 to 5 rate the photos selected, P picks, X rejects
// and U clears the flag, as marraw's keys do, or a click on a tile's stars,
// flag or cross, Ctrl and the wheel size the tiles, and Enter or a double
// click opens the cull view. There Left and
// Right step through the folder, Home and End go to its ends, the same keys
// rate and flag, Z or Space goes between fit and one to one, + and - zoom,
// Shift and the arrows pan, D opens the develop panel, where Up and Down
// walk its controls, + and - step the one chosen and Ctrl+Z undoes, and
// Escape goes back.
// The wheel zooms about the pointer, a drag pans and a flick glides, and a
// click on the filmstrip goes to that photo. Ctrl+K opens the command
// palette, Ctrl+E exports, and ? lists the keys.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"github.com/marrasen/aprot/client"
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/install"

	"github.com/marrasen/marraw/internal/marrawclient"
)

func main() {
	// The backend process this program starts is the same program: it
	// is no install, and looks for no updates.
	if !slices.Contains(os.Args[1:], "-backend") {
		install.Run(installApp())
	}
	addLibrary := flag.String("add-library", "", "add this folder to the library: as a library folder, its subfolders shoots, as marraw's Add library folder does, or as a shoot where its photos are in it")
	folder := flag.String("folder", "", "the folder of photos to open; without it, the library opens, to choose one")
	connect := flag.String("connect", "", "another marraw to cull on, as host:port; by default this program runs its own backend")
	token := flag.String("token", "", "the token for -connect")
	dataDir := flag.String("data-dir", "", "the backend's data folder (default: the config folder's marraw, as marrawd uses; a release's own, marraw-gunim)")
	skim := flag.Int("skim", 0, "step right this many times on its own, -every apart, report the timings, and quit")
	every := flag.Duration("every", 150*time.Millisecond, "how far apart -skim steps")
	shot := flag.String("shot", "", "write the window to this PNG file once the script is done, and quit")
	winSize := flag.String("size", "1400x900", "the window's size, as 3840x2050, for a test run as large as a big screen")
	keys := flag.String("keys", "", "once -skim is done, press these keys, a comma-separated list such as 3,p,right,x; also click:x:y, move:x:y, type:text, shift+plus, ctrl+z, w, wait:ms and shot:name")
	wait := flag.Duration("wait", time.Second, "how long the window shows the grid before the script starts")
	burst := flag.Int("burst", 0, "write this many pictures, from the last of -keys on, to -shot's name with -01, -02 and on, instead of one")
	edit := flag.String("edit", "", "once -keys are pressed, with the develop panel open, set adjustments as the panel does, such as contrast=0.6,expEV=2, choices by their option, such as wbMode=2 for Kelvin, or auto=tone or auto=wb+color, and save them")
	zoom := flag.Bool("zoom", false, "once -skim is done, zoom to one to one with Z, and wait for the full resolution before the shot")
	backend := flag.Bool("backend", false, "run as the backend process the window starts, and nothing else (internal)")
	backendPort := flag.Int("backend-port", 0, "with -backend, the port to listen on, nought for a free one (internal)")
	inProcess := flag.Bool("in-process", false, "run the backend inside this process, as before, instead of a process of its own")
	flag.Parse()
	if *dataDir == "" && install.IsRelease(version) {
		// A release keeps a catalog of its own, apart from the Electron
		// app's.
		*dataDir = defaultDataDir()
	}
	if install.IsRelease(version) {
		// A release has no console: its log, and its backend's, go to a
		// file in its data folder, begun anew each start.
		name := "marraw-gunim.log"
		if *backend {
			name = "marraw-gunim-backend.log"
		}
		if err := os.MkdirAll(*dataDir, 0o755); err == nil {
			if f, err := os.Create(filepath.Join(*dataDir, name)); err == nil {
				log.SetOutput(f)
				logOut = f
			}
		}
	}
	if *backend {
		if err := runBackendProcess(*dataDir, *backendPort); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(options{folder: *folder, connect: *connect, token: *token, dataDir: *dataDir, inProcess: *inProcess,
		skim: *skim, every: *every, wait: *wait, burst: *burst, edit: *edit, addLibrary: *addLibrary, shot: *shot, zoom: *zoom, keys: *keys, size: *winSize}); err != nil {
		log.Fatal(err)
	}
}

type options struct {
	folder, connect, token, dataDir string
	skim, burst                     int
	every, wait                     time.Duration
	shot                            string
	zoom                            bool
	keys                            string
	edit                            string
	addLibrary                      string
	size                            string
	inProcess                       bool
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	host, token := o.connect, o.token
	var proc *backendProc
	switch {
	case host != "":
	case o.inProcess:
		// The backend in this process: loopback only, with a token of its
		// own, as the Electron shell starts marrawd.
		token = randomToken()
		addr, stopBackend, err := startBackend(ctx, o.dataDir, token, 0)
		if err != nil {
			return fmt.Errorf("start the backend: %w", err)
		}
		defer stopBackend()
		host = addr
	default:
		// The backend in a process of its own: this program again, with
		// -backend, on loopback, with a token of its own.
		token = randomToken()
		p, err := spawnBackend(ctx, o.dataDir, token)
		if err != nil {
			return fmt.Errorf("start the backend: %w", err)
		}
		defer p.stop()
		proc, host = p, p.addr
	}

	cc, err := client.Dial(ctx, "ws://"+host+"/ws", client.Options{
		AuthToken: func(context.Context) (string, error) { return token, nil },
	})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", host, err)
	}
	defer cc.Close()
	api := marrawclient.New(cc)

	if o.addLibrary != "" {
		if err := addLibraryFolder(ctx, api, o.addLibrary); err != nil {
			return fmt.Errorf("add %s to the library: %w", o.addLibrary, err)
		}
	}
	// The folder asked for opens before the window, so the script's
	// timings start with it there; without one the library opens empty.
	var path string
	var info marrawclient.FolderInfo
	var photos []marrawclient.Photo
	if o.folder != "" {
		if path, err = filepath.Abs(o.folder); err != nil {
			return err
		}
		inf, err := api.Library.OpenFolder(ctx, path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		info = *inf
		if photos, err = api.Library.ListPhotos(ctx, info.FolderID); err != nil {
			return fmt.Errorf("list %s: %w", path, err)
		}
		if len(photos) == 0 {
			return fmt.Errorf("%s has no photos", path)
		}
	}
	log.Printf("marraw: build %s; %d photos in %q, backend at %s", build(), len(photos), path, host)

	err = gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "marraw (gunim test build " + build() + ")", Size: parseSize(o.size), Root: newFullSurface(),
			Icons: appIcons(), TitleBar: bareTitleBar{},
			// The photo runs under the title bar, as in marraw.
			UnderTitleBar: true})
		if err != nil {
			return err
		}
		registerViews(w)
		c := w.Client()
		cu := newCuller(ctx, c, api, newImages("http://"+host, token), info.FolderID, path, photos)
		cu.viewer.app = a
		if proc != nil {
			proc.onRestart = func(why string) { cu.onDo(func() { cu.fail("The backend restarted", errors.New(why)) }) }
		}
		if o.skim > 0 || o.shot != "" || o.zoom || o.keys != "" {
			go cu.script(o)
		}
		return cu.serve()
	})
	if errors.Is(err, driver.ErrNoDriver) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// randomToken is a launch token for the backend in this process.
func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// build is the commit this program was built from, as Go stamps it, with
// "+" after it for changes not committed, so a test build says what it is.
func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+"
			}
		}
	}
	if rev == "" {
		return "unknown"
	}
	return rev[:min(7, len(rev))] + dirty
}

// parseSize reads a window's size as 1400x900, or the usual for none.
func parseSize(s string) geom.Size {
	var w, h float32
	if _, err := fmt.Sscanf(s, "%gx%g", &w, &h); err != nil || w < 200 || h < 200 {
		return geom.Sz(1400, 900)
	}
	return geom.Sz(w, h)
}
