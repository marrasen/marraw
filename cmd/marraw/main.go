// Command marraw is a test build of marraw as one Go program drawn with
// gunim: the cull view, beside the Electron app, to learn what gunim needs
// for photos before any wider port.
//
// It runs the backend inside itself, on a private loopback port, and talks
// to it exactly as it would to another machine's marraw: the generated aprot
// Go client over WebSocket, and HTTP for the images. -connect points the same
// code at another instance instead.
//
//	go run ./cmd/marraw -folder ~/Pictures/shoot
//	go run ./cmd/marraw -connect 192.168.1.20:8482 -token … -folder D:\Photos\shoot
//	go run ./cmd/marraw -folder ~/Pictures/shoot -skim 40 -every 150ms
//
// Keys: Left and Right step through the folder, Home and End go to its ends,
// Escape quits.
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
	"time"

	"github.com/marrasen/aprot/client"
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"

	"github.com/marrasen/marraw/internal/daemon"
	"github.com/marrasen/marraw/internal/marrawclient"
)

func main() {
	folder := flag.String("folder", "", "the folder of photos to cull")
	connect := flag.String("connect", "", "another marraw to cull on, as host:port; by default this program runs its own backend")
	token := flag.String("token", "", "the token for -connect")
	dataDir := flag.String("data-dir", "", "the backend's data folder (default: the config folder's marraw, as marrawd uses)")
	skim := flag.Int("skim", 0, "step right this many times on its own, -every apart, report the timings, and quit")
	every := flag.Duration("every", 150*time.Millisecond, "how far apart -skim steps")
	shot := flag.String("shot", "", "write the window to this PNG file once -skim is done, or after a second")
	flag.Parse()
	if *folder == "" {
		log.Fatal("marraw: -folder is required")
	}
	if err := run(options{folder: *folder, connect: *connect, token: *token, dataDir: *dataDir,
		skim: *skim, every: *every, shot: *shot}); err != nil {
		log.Fatal(err)
	}
}

type options struct {
	folder, connect, token, dataDir string
	skim                            int
	every                           time.Duration
	shot                            string
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	host, token := o.connect, o.token
	if host == "" {
		// The backend in this process: loopback only, with a token of its
		// own, as the Electron shell starts marrawd.
		token = randomToken()
		daemon.LimitMemory()
		d, err := daemon.Start(ctx, daemon.Options{Listen: "127.0.0.1", DataDir: o.dataDir, CacheCapGB: 20, Token: token})
		if err != nil {
			return fmt.Errorf("start the backend: %w", err)
		}
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d.Close(sctx)
		}()
		host = d.Addr
	}

	cc, err := client.Dial(ctx, "ws://"+host+"/ws", client.Options{
		AuthToken: func(context.Context) (string, error) { return token, nil },
	})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", host, err)
	}
	defer cc.Close()
	api := marrawclient.New(cc)

	path, err := filepath.Abs(o.folder)
	if err != nil {
		return err
	}
	info, err := api.Library.OpenFolder(ctx, path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	photos, err := api.Library.ListPhotos(ctx, info.FolderID)
	if err != nil {
		return fmt.Errorf("list %s: %w", path, err)
	}
	if len(photos) == 0 {
		return fmt.Errorf("%s has no photos", path)
	}
	log.Printf("marraw: %d photos in %s, backend at %s", len(photos), path, host)

	err = gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "marraw — cull (gunim test build)", Size: geom.Sz(1400, 900)})
		if err != nil {
			return err
		}
		registerViews(w)
		c := w.Client()
		cu := newCuller(ctx, c, api, newImages("http://"+host, token), info.FolderID, photos)
		if o.skim > 0 || o.shot != "" {
			go cu.script(o.skim, o.every, o.shot)
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
