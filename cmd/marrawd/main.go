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
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/api"
	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/diskio"
	"github.com/marrasen/marraw/internal/imghttp"
	"github.com/marrasen/marraw/internal/infer"
	"github.com/marrasen/marraw/internal/inpaint"
	"github.com/marrasen/marraw/internal/pairing"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/sysmem"
	"github.com/marrasen/marraw/internal/watermark"
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

	// --dev disables every auth check; it must never be reachable from
	// another machine.
	loopbackOnly := isLoopback(*listen)
	if *dev && !loopbackOnly {
		log.Fatalf("--dev must not be exposed beyond loopback (got --listen %s)", *listen)
	}

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

	// Soft GC backstop: return dropped decode buffers promptly instead of
	// riding the GOGC 2× curve. LibRaw's C-side allocations are invisible to
	// the Go GC — the export admission budget is the real defense; this only
	// keeps the Go heap honest. An explicit GOMEMLIMIT env always wins.
	if os.Getenv("GOMEMLIMIT") == "" {
		if st, err := sysmem.Query(); err == nil {
			lim := int64(st.TotalPhys / 2)
			debug.SetMemoryLimit(lim)
			log.Printf("go memory limit set to %d MiB (half of %d MiB physical)", lim>>20, st.TotalPhys>>20)
		}
	}

	token := os.Getenv("MARRAW_TOKEN")
	if token == "" && !*dev {
		log.Fatal("MARRAW_TOKEN must be set (or run with --dev)")
	}

	db, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer db.Close()

	// The persistent pairing token remote clients authenticate with. Created
	// once and kept in the DB so a saved laptop connection survives restarts;
	// Settings can regenerate it (System.RegeneratePairingToken).
	pairingToken, err := db.GetSetting(context.Background(), api.PairingTokenKey)
	if err != nil {
		log.Fatalf("read pairing token: %v", err)
	}
	if pairingToken == "" {
		if pairingToken, err = api.GeneratePairingToken(); err != nil {
			log.Fatalf("generate pairing token: %v", err)
		}
		if err := db.SetSetting(context.Background(), api.PairingTokenKey, pairingToken); err != nil {
			log.Fatalf("store pairing token: %v", err)
		}
	}
	tokens := api.NewAuthTokens(token, pairingToken)
	// Devices approved through the pairing dialog. Each carries its own token
	// so one can be revoked without disturbing the others.
	tokens.SetDevices(api.LoadDevices(context.Background(), db))

	pool := decode.NewPool(runtime.NumCPU())
	defer pool.Close()

	// Staged I/O for background full decodes: sequential per-device reads
	// keep spinning disks at streaming speed. The budget bounds the C memory
	// held in staged buffers (invisible to the Go memory limit above).
	ioGate := diskio.NewGate()
	log.Printf("staged-read budget %d MiB", ioGate.Budget()>>20)

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
	// AI-mask maps and ML fill patches live beside (not inside) the preview
	// cache: they cost an inference to regenerate, so preview Clear/Relocate
	// must not touch them.
	cache.AIMaps = pyramid.NewAIMapStore(filepath.Join(*dataDir, "aimaps"))
	cache.Fills = pyramid.NewFillStore(filepath.Join(*dataDir, "fills"), inpaint.FillVer())
	cache.IOGate = ioGate
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

	// Pairing only exists where it can be used: a loopback-only daemon is
	// unreachable from another machine, so nothing can ask to be let in.
	var broker *pairing.Broker
	if !loopbackOnly && !*dev {
		broker = &pairing.Broker{}
	}

	deps := &api.Deps{DB: db, Pool: pool, Cache: cache, Handles: handles, Scanner: scanner, Janitor: janitor, DefaultCacheDir: defaultCacheDir, WatermarkDir: watermarkDir,
		Infer: infer.NewManager(filepath.Join(*dataDir, "models")), IOGate: ioGate, Tokens: tokens, Pairing: broker, LoopbackOnly: loopbackOnly}
	registry, library, _, _ := api.NewRegistry(deps)
	// StreamChunking batches streamed items into stream_chunk frames
	// (defaults: 128 items / 64 KiB / 20 ms) — cheap insurance for any
	// future large streams; the generated client understands both framings.
	// MaxMessageSize covers the largest inbound frame we accept: a watermark
	// asset uploaded as a base64 blob param (20 MB raw ≈ 27 MB encoded).
	server := aprot.NewServer(registry, aprot.ServerOptions{
		StreamChunking: &aprot.StreamChunking{},
		MaxMessageSize: 32 << 20,
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

	// The renderer runs on file:// (Origin "null") in production, so origin
	// checks prove nothing. Trust lives in the first-message auth frame: the
	// per-launch launch token (local windows) or the persistent pairing token
	// (remote connections). Dev registers no hook — connections are open.
	//
	// The hook also records WHICH credential matched, as the connection's user
	// ID. That is what lets handlers tell a window on this machine from a
	// client elsewhere — the approval RPCs answer local windows only, so that
	// a machine asking to be let in cannot approve itself — and it gives
	// revocation a handle to disconnect by.
	isDev := *dev
	server.SetCheckOrigin(func(r *http.Request) bool { return true })
	if !isDev {
		server.OnAuth(func(ctx context.Context, conn *aprot.Conn, tok string) error {
			switch m := tokens.Match(tok); {
			case m.Launch:
				conn.SetUserID(api.ConnLocal)
			case m.DeviceID != "":
				conn.SetUserID(api.ConnDevicePrefix + m.DeviceID)
				go deps.TouchDevice(context.Background(), m.DeviceID, conn.RemoteAddr())
			case m.Pairing:
				conn.SetUserID(api.ConnPairing)
			default:
				return aprot.ErrAuthFailed("invalid token")
			}
			return nil
		})
	}
	if broker != nil {
		broker.OnChange = deps.NotifyPairingChanged
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go janitor.Run(ctx)

	var tokenValid func(string) bool
	if !isDev {
		tokenValid = tokens.Valid
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	img := &imghttp.Handler{DB: db, Cache: cache, TokenValid: tokenValid}
	mux.Handle("GET /img/{id}/{level}", img)
	mux.Handle("GET /img/{id}/tile/{tx}/{ty}", http.HandlerFunc(img.ServeTile))
	mux.Handle("GET /wm/{name}", &imghttp.Assets{Dir: watermarkDir, TokenValid: tokenValid})
	// The bundled watermark fonts, so the editor preview renders with the
	// byte-identical faces the exporter uses. Fonts are CORS-gated even on
	// file:// — the wildcard origin is required, and safe under the same
	// token-in-URL trust model as the images (the files are public anyway).
	mux.HandleFunc("GET /fonts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if tokenValid != nil && !tokenValid(r.URL.Query().Get("t")) && !tokenValid(r.Header.Get("X-Marraw-Token")) {
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
	// Discovery and pairing, on a reachable daemon only (see remote.go).
	if broker != nil {
		registerRemoteRoutes(mux, deps, broker)
	}
	// The shell's reachability probe for a saved connection: proves both the
	// host and the token in one authenticated round trip. /healthz stays open
	// on purpose.
	mux.HandleFunc("GET /authz", func(w http.ResponseWriter, r *http.Request) {
		if tokenValid != nil && !tokenValid(r.URL.Query().Get("t")) && !tokenValid(r.Header.Get("X-Marraw-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"app":"marraw","version":%q}`+"\n", buildVersion())
	})

	ln, err := net.Listen("tcp", net.JoinHostPort(*listen, strconv.Itoa(*port)))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	deps.ListenAddr = net.JoinHostPort(*listen, strconv.Itoa(actualPort))

	httpServer := &http.Server{Handler: cors(isDev, mux)}
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	// The handshake line the Electron main process waits for.
	fmt.Printf("MARRAW_READY port=%d\n", actualPort)
	log.Printf("marrawd listening on %s (data: %s)", deps.ListenAddr, *dataDir)

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

// isLoopback reports whether host is unreachable from other machines.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// buildVersion is what GET /authz reports so a connecting client can show
// which version it reached. Release builds carry the module version via
// -buildvcs/embedded build info; a source build reports "dev".
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
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
