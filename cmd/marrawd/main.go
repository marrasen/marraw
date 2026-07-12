// marrawd is the marraw backend daemon. It serves the aprot API over
// WebSocket and pyramid images over HTTP on one localhost port, and prints
// "MARRAW_READY port=N" on stdout once listening so the Electron shell can
// connect.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/api"
	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/imghttp"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/watermark"
)

func main() {
	var (
		port     = flag.Int("port", 0, "listen port (0 = pick a free one)")
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

	if logFile := setupLogging(*dataDir); logFile != nil {
		defer logFile.Close()
	}
	log.Printf("marrawd starting (pid %d, data: %s)", os.Getpid(), *dataDir)

	token := os.Getenv("MARRAW_TOKEN")
	if token == "" && !*dev {
		log.Fatal("MARRAW_TOKEN must be set (or run with --dev)")
	}

	db, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer db.Close()

	pool := decode.NewPool(runtime.NumCPU())
	defer pool.Close()

	// The preview cache lives under the data dir by default, but the user can
	// relocate it (Settings). A stored custom directory wins at startup.
	defaultCacheDir := filepath.Join(*dataDir, "cache", "previews")
	cacheDir := defaultCacheDir
	if custom := db.CacheDir(context.Background()); custom != "" {
		cacheDir = custom
	}
	cache, err := pyramid.New(cacheDir, pool, db)
	if err != nil {
		log.Fatalf("open cache: %v", err)
	}
	handles := decode.NewHandleCache(3)
	defer handles.Close()

	scanner := &scan.Scanner{DB: db, Cache: cache, Pool: pool}

	// Cache size cap: the flag is the default; a Settings-dialog override
	// persists in the store and wins at startup.
	janitor := &pyramid.Janitor{Cache: cache}
	janitor.SetCap(*cacheCap << 30)
	if raw, err := db.GetSetting(context.Background(), "cacheCapGB"); err == nil && raw != "" {
		if gb, err := strconv.ParseInt(raw, 10, 64); err == nil && gb > 0 {
			janitor.SetCap(gb << 30)
		}
	}

	watermarkDir := filepath.Join(*dataDir, "watermarks")
	if err := os.MkdirAll(watermarkDir, 0o755); err != nil {
		log.Fatalf("create watermark dir: %v", err)
	}

	deps := &api.Deps{DB: db, Pool: pool, Cache: cache, Handles: handles, Scanner: scanner, Janitor: janitor, DefaultCacheDir: defaultCacheDir, WatermarkDir: watermarkDir}
	registry, library, _, _ := api.NewRegistry(deps)
	// StreamChunking batches streamed items into stream_chunk frames
	// (defaults: 128 items / 64 KiB / 20 ms) — cheap insurance for any
	// future large streams; the generated client understands both framings.
	server := aprot.NewServer(registry, aprot.ServerOptions{
		StreamChunking: &aprot.StreamChunking{},
	})
	deps.SetServer(server)
	// Log every handler error (except normal client cancellations) so a
	// failure that only flashed past in the UI — e.g. a WB pick on too-dark a
	// patch — is recoverable from the log file afterward.
	server.Use(func(next aprot.Handler) aprot.Handler {
		return func(ctx context.Context, req *aprot.Request) (any, error) {
			res, err := next(ctx, req)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("rpc %s failed: %v", req.Method, err)
			}
			return res, err
		}
	})
	scanner.OnPhotosChanged = func(folderID int64) {
		server.TriggerRefresh(fmt.Sprintf("photos:%d", folderID))
	}
	cache.OnPhotoChanged = scanner.OnPhotosChanged
	// 1:1 render progress → the loupe's decoding indicator.
	cache.Progress = deps.BroadcastRenderProgress

	// After SetServer, so the watcher's refresh pushes reach subscribers. A
	// watcher that will not start is not fatal — folders keep their manual
	// rescan.
	if watcher, err := api.StartWatcher(context.Background(), library); err != nil {
		log.Printf("watch: disabled (%v); folders rely on manual rescan", err)
	} else {
		defer watcher.Close()
	}

	// The renderer runs on file:// (Origin "null") in production; trust is
	// established by the shared token, not the origin.
	isDev := *dev
	server.SetCheckOrigin(func(r *http.Request) bool {
		if isDev {
			return true
		}
		return r.URL.Query().Get("t") == token
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go janitor.Run(ctx)

	imgToken := token
	if isDev {
		imgToken = ""
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	img := &imghttp.Handler{DB: db, Cache: cache, Token: imgToken}
	mux.Handle("GET /img/{id}/{level}", img)
	mux.Handle("GET /img/{id}/tile/{tx}/{ty}", http.HandlerFunc(img.ServeTile))
	mux.Handle("GET /wm/{name}", &imghttp.Assets{Dir: watermarkDir, Token: imgToken})
	// The bundled watermark fonts, so the editor preview renders with the
	// byte-identical faces the exporter uses. Fonts are CORS-gated even on
	// file:// — the wildcard origin is required, and safe under the same
	// token-in-URL trust model as the images (the files are public anyway).
	mux.HandleFunc("GET /fonts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if imgToken != "" && r.URL.Query().Get("t") != imgToken && r.Header.Get("X-Marraw-Token") != imgToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		raw, ok := watermark.FontBytes(watermark.FontID(r.PathValue("id")))
		if !ok {
			http.Error(w, "unknown font", http.StatusNotFound)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "font/ttf")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Write(raw)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	httpServer := &http.Server{Handler: cors(isDev, mux)}
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	// The handshake line the Electron main process waits for.
	fmt.Printf("MARRAW_READY port=%d\n", actualPort)
	log.Printf("marrawd listening on 127.0.0.1:%d (data: %s)", actualPort, *dataDir)

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

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Stop(shutdownCtx)
	httpServer.Shutdown(shutdownCtx)
}

// setupLogging tees the daemon's log output to a file under <dataDir>/logs so
// an error that only flashed past in the UI can still be found afterward.
// Output still goes to stderr, which the Electron shell forwards to its own
// console. The log rotates once at startup past ~5 MiB (one .1 backup kept),
// so it never grows without bound. Returns the open file to close on exit, or
// nil when the file couldn't be opened (then logging stays on stderr only).
func setupLogging(dataDir string) *os.File {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("logging: cannot create %s: %v (stderr only)", dir, err)
		return nil
	}
	path := filepath.Join(dir, "marrawd.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("logging: cannot open %s: %v (stderr only)", path, err)
		return nil
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return f
}

// cors allows the Vite dev origin to fetch /img during browser development.
func cors(dev bool, next http.Handler) http.Handler {
	if !dev {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		next.ServeHTTP(w, r)
	})
}
