// marrawd is the marraw backend daemon. It serves the aprot API over
// WebSocket and pyramid images over HTTP on one localhost port, and prints
// "MARRAW_READY port=N" on stdout once listening so the Electron shell can
// connect. The backend itself is package daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/marrasen/marraw/internal/daemon"
)

func main() {
	var (
		port     = flag.Int("port", 0, "listen port (0 = pick a free one)")
		listen   = flag.String("listen", "127.0.0.1", "bind address (e.g. 0.0.0.0 or a Tailscale IP to allow remote connections)")
		dev      = flag.Bool("dev", false, "development mode: no token required, permissive origin")
		dataDir  = flag.String("data-dir", "", "app data directory (default %APPDATA%/marraw)")
		cacheCap = flag.Int64("cache-cap-gb", 20, "preview cache size cap in GiB")
	)
	flag.Parse()

	if *dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			log.Fatalf("resolve data dir: %v", err)
		}
		*dataDir = filepath.Join(base, "marraw")
	}
	if logFile := daemon.SetupLogging(*dataDir); logFile != nil {
		defer logFile.Close()
	}
	log.Printf("marrawd starting (pid %d, data: %s)", os.Getpid(), *dataDir)
	daemon.LimitMemory()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	d, err := daemon.Start(ctx, daemon.Options{
		Listen: *listen, Port: *port, Dev: *dev, DataDir: *dataDir, CacheCapGB: *cacheCap,
		Token: os.Getenv("MARRAW_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	// The handshake line the Electron main process waits for.
	fmt.Printf("MARRAW_READY port=%d\n", d.Port)

	// Exit when the parent dies: Electron holds our stdin open; EOF means
	// the shell is gone and we must not linger.
	if os.Getenv("MARRAW_PARENT_WATCH") == "1" {
		go func() {
			buf := make([]byte, 1)
			for {
				if _, err := os.Stdin.Read(buf); err != nil {
					log.Println("stdin closed; shutting down")
					stop()
					return
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
	case <-d.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Close(shutdownCtx)
}
