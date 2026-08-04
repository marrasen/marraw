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
	"github.com/marrasen/marraw/internal/discovery"
	"github.com/marrasen/marraw/internal/diskio"
	"github.com/marrasen/marraw/internal/export"
	"github.com/marrasen/marraw/internal/guestui"
	"github.com/marrasen/marraw/internal/imghttp"
	"github.com/marrasen/marraw/internal/infer"
	"github.com/marrasen/marraw/internal/inpaint"
	"github.com/marrasen/marraw/internal/pairing"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/sysmem"
	"github.com/marrasen/marraw/internal/tsfunnel"
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
	// Share links: one folder each, for someone culling in a browser. Confined
	// to the culling RPCs by api.GuestGate below.
	tokens.SetGuests(api.LoadGuestLinks(context.Background(), db))

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
	// mDNS lives here rather than in the Electron shell so that only ONE
	// program ever opens a listening socket — on Windows every program that
	// does gets its own firewall prompt, and a declined second prompt left
	// the machine reachable by address but invisible to a local scan.
	advertiser := &discovery.Advertiser{}
	defer advertiser.Stop()

	// The funnel publishes this daemon on the public internet for share links.
	// Its port is filled in once the listener is bound, below.
	funnel := tsfunnel.New(0)

	deps := &api.Deps{DB: db, Pool: pool, Cache: cache, Handles: handles, Scanner: scanner, Janitor: janitor, DefaultCacheDir: defaultCacheDir, WatermarkDir: watermarkDir,
		Infer: infer.NewManager(filepath.Join(*dataDir, "models")), IOGate: ioGate, Tokens: tokens, Pairing: broker,
		Advertiser: advertiser, Funnel: funnel, LoopbackOnly: loopbackOnly}
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
	// Share links reach the same registry as the owner, so the confinement has
	// to sit in front of it: everything outside the culling allowlist is
	// refused here, before the handler runs. Inside the logging middleware, so
	// a refusal is recorded.
	server.Use(api.GuestGate(tokens))
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
			m := tokens.Match(tok)
			// A connection that came in through the funnel may only be a share
			// link. The owner's launch and pairing tokens are refused there
			// outright rather than merely being unlikely to be guessed.
			if ctx.Value(funnelConn{}) != nil && m.Guest == nil {
				return aprot.ErrAuthFailed("invalid token")
			}
			switch {
			case m.Launch:
				conn.SetUserID(api.ConnLocal)
			case m.DeviceID != "":
				conn.SetUserID(api.ConnDevicePrefix + m.DeviceID)
				go deps.TouchDevice(context.Background(), m.DeviceID, conn.RemoteAddr())
			case m.Pairing:
				conn.SetUserID(api.ConnPairing)
			case m.Guest != nil:
				conn.SetUserID(api.ConnGuestPrefix + m.Guest.ID)
				// Presence for the rail's "someone is looking" dot, and the
				// link's last-opened time. Detached context: this outlives the
				// auth call, and must not be cancelled by it.
				go deps.MarkGuestOnline(context.Background(), m.Guest.ID, conn.ID())
			default:
				return aprot.ErrAuthFailed("invalid token")
			}
			return nil
		})
	}
	// The other half of guest presence. aprot keeps the connection's identity
	// readable here, which is the only way to tell which share link has just
	// gone away.
	server.OnDisconnect(func(_ context.Context, conn *aprot.Conn) {
		if id, ok := strings.CutPrefix(conn.UserID(), api.ConnGuestPrefix); ok {
			deps.MarkGuestOffline(id, conn.ID())
		}
	})
	if broker != nil {
		broker.OnChange = deps.NotifyPairingChanged
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go janitor.Run(ctx)
	// Link expiry is checked when a guest authenticates, but an already-open
	// page holds its socket indefinitely. This is what makes a lapsed link
	// actually stop working for the person still looking at it.
	go deps.RunGuestSweep(ctx)

	// Three credential checks for the HTTP endpoints, because a share link is a
	// valid credential that may see almost nothing. imgAuth resolves what a
	// credential is allowed to see (one folder, base renditions only) for the
	// photo endpoints; tokenValid answers the endpoints that are not
	// photo-scoped, and refuses share links outright; guestAuth is imgAuth with
	// the owner's own credentials removed, for the funnel-facing listener.
	accessFor := func(m api.TokenMatch) (imghttp.Access, bool) {
		switch {
		case !m.OK:
			return imghttp.Access{}, false
		case m.Guest != nil:
			acc := imghttp.Access{
				FolderID:     m.Guest.FolderID,
				BaseEditOnly: !m.Guest.Caps.Edits,
				Downloads:    m.Guest.Caps.Downloads,
			}
			if e := m.Guest.Export; e != nil {
				acc.Download = imghttp.DownloadSpec{
					LongEdge:       e.LongEdge,
					JpegQuality:    e.JpegQuality,
					ColorSpace:     e.ColorSpace,
					SharpenTarget:  e.SharpenTarget,
					SharpenAmount:  e.SharpenAmount,
					ExifMode:       e.ExifMode,
					RemoveLocation: e.RemoveLocation,
					WatermarkID:    e.WatermarkID,
				}
			}
			return acc, true
		}
		return imghttp.Access{}, true
	}
	var imgAuth, guestAuth imghttp.Authorizer
	var tokenValid func(string) bool
	if !isDev {
		imgAuth = func(tok string) (imghttp.Access, bool) { return accessFor(tokens.Match(tok)) }
		guestAuth = func(tok string) (imghttp.Access, bool) {
			m := tokens.Match(tok)
			if m.Guest == nil {
				// Not a lesser credential on this listener — one that has no
				// business arriving from the public internet at all.
				return imghttp.Access{}, false
			}
			return accessFor(m)
		}
		tokenValid = func(tok string) bool {
			m := tokens.Match(tok)
			return m.OK && m.Guest == nil
		}
	}
	// Two muxes, because the funnel publishes a whole port and there is no
	// version-stable way to publish less of one. mux is the daemon: every
	// endpoint, reachable by the Electron shell over loopback and — if the user
	// turned remote access on — by their own other machines. guestMux is what
	// the funnel points at, and carries only what a shared album needs.
	//
	// Nothing on guestMux answers an owner credential. Sharing one shoot with a
	// friend should not put the pairing token's front door on the public
	// internet, and the difference between "the token is 128 bits so guessing
	// it is hopeless" and "the endpoint is not there" is the difference between
	// surviving a future bug and not.
	mux := http.NewServeMux()
	mux.Handle("/ws", server)
	// The same aprot server, with the connection marked as having arrived from
	// the internet; the auth hook refuses everything but a share link on it.
	funnelWS := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), funnelConn{}, true)))
	})
	img := &imghttp.Handler{DB: db, Cache: cache, Authorize: imgAuth}
	guestImg := &imghttp.Handler{DB: db, Cache: cache, Authorize: guestAuth}
	mux.Handle("GET /img/{id}/{level}", img)
	mux.Handle("GET /img/{id}/tile/{tx}/{ty}", http.HandlerFunc(img.ServeTile))
	// Deliberately not on the funnel: watermark assets belong to the library
	// rather than to any one folder, and the shared page has no editor to
	// preview them in.
	mux.Handle("GET /wm/{name}", &imghttp.Assets{Dir: watermarkDir, Authorize: imgAuth})
	// Downloads: a full develop-pipeline render per photo, so the visitor gets
	// the same pixels an export would produce rather than a preview upscaled.
	// sRGB and the owner's credit, since these files leave the library.
	dl := imghttp.NewDownloads(db, imgAuth, func(ctx context.Context, photoID int64, spec imghttp.DownloadSpec) ([]byte, error) {
		artist, copyright := api.ExportCredit(ctx, db)
		// The defaults, for a share minted without an export preset: the whole
		// frame, near-transparent quality, and the owner's credit.
		req := export.Request{
			Format:      "jpeg",
			JpegQuality: 92,
			ColorSpace:  "srgb",
			ExifMode:    "copyright",
			Artist:      artist,
			Copyright:   copyright,
			AIMaps:      cache.AIMaps,
			Lenses:      cache.Lenses,
			Fills:       cache.Fills,
		}
		// A preset overrides them field by field, so one it does not carry
		// (an older preset missing a newer setting) keeps the default rather
		// than rendering at zero.
		req.LongEdge = spec.LongEdge
		req.SharpenTarget = spec.SharpenTarget
		req.SharpenAmount = spec.SharpenAmount
		req.RemoveLocation = spec.RemoveLocation
		if spec.JpegQuality > 0 {
			req.JpegQuality = spec.JpegQuality
		}
		if spec.ColorSpace != "" {
			req.ColorSpace = spec.ColorSpace
		}
		if spec.ExifMode != "" {
			req.ExifMode = spec.ExifMode
		}
		if spec.WatermarkID != "" {
			req.Watermark = api.WatermarkSpecFor(ctx, db, watermarkDir, spec.WatermarkID)
		}
		return export.RenderJPEG(ctx, db, photoID, req)
	})
	mux.Handle("GET /dl/{id}", dl)
	mux.HandleFunc("GET /dl.zip", dl.ServeZip)
	guestDl := imghttp.NewDownloads(db, guestAuth, dl.Render)
	// The share page itself. The token rides in the path so the page can read
	// it without a query string, and so its relative asset URLs land under the
	// same prefix.
	//
	// On mux as well as the funnel's own mux: without a funnel a link falls
	// back to the node's tailnet name on the daemon's own port (see
	// Share.shareBase), and that URL has to answer.
	guestPage := &guestui.Handler{TokenValid: func(tok string) bool {
		return tokens.Match(tok).Guest != nil
	}}
	mux.HandleFunc("GET /s/{token}", guestui.Redirect)
	mux.Handle("GET /s/{token}/{path...}", guestPage)
	guestMux := newGuestMux(funnelWS, guestImg, guestDl, guestPage)
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

	// The funnel's own listener, always on loopback: `tailscale funnel` proxies
	// from the tailnet's edge to a local port, so it never needs a routable
	// bind — and binding one would put this reduced surface on the LAN too, for
	// nothing. A failure here is not fatal: the daemon works, and the share UI
	// reports the funnel as unavailable, exactly as it does without Tailscale.
	//
	// Never in dev: --dev disables every auth check, and the startup guard
	// above refuses to let it bind beyond loopback. Publishing it through a
	// funnel would be that same mistake by a longer route.
	guestPort := 0
	var guestLn net.Listener
	var guestErr error
	if !isDev {
		guestLn, guestErr = net.Listen("tcp", "127.0.0.1:0")
	}
	switch {
	case isDev:
		log.Print("share: funnel disabled in dev mode")
	case guestErr != nil:
		log.Printf("share: no funnel listener (%v); links fall back to this machine's own address", guestErr)
	default:
		guestPort = guestLn.Addr().(*net.TCPAddr).Port
		guestServer := &http.Server{Handler: guestMux}
		defer guestServer.Close()
		go func() {
			if err := guestServer.Serve(guestLn); err != nil && err != http.ErrServerClosed {
				log.Printf("share: funnel listener stopped: %v", err)
			}
		}()
		log.Printf("share: funnel listener on 127.0.0.1:%d (share routes only)", guestPort)
	}

	// Announce on the local network, now that the real port is known.
	deps.StartAdvertising(context.Background())

	// Share links outlive the process, so the tunnel that serves them has to
	// come back with it — otherwise a link handed out yesterday is dead until
	// the owner notices and mints another. In the background: raising a funnel
	// runs the tailscale CLI, and the app must not wait on it to start.
	//
	// The funnel points at guestPort, not the daemon's own: publishing a whole
	// port is the only thing the CLI does, so the port it publishes has to be
	// one where every route is meant to be public. Without that listener there
	// is nothing safe to publish, so the funnel stays down.
	funnel.SetPort(guestPort)
	if len(tokens.Guests()) > 0 {
		go func() {
			if err := funnel.Enable(context.Background()); err != nil {
				log.Printf("funnel: share links are not reachable from the internet: %v", err)
			}
		}()
	}

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
	// Withdraw the tunnel before going away: with the daemon down a published
	// port answers nothing, and leaving the machine advertised on the public
	// internet for a service that is not running is worth avoiding. Startup
	// puts it back when there are still links to serve.
	if err := funnel.Disable(shutdownCtx); err != nil {
		log.Printf("funnel: withdrawing on shutdown: %v", err)
	}
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

// funnelConn marks a WebSocket connection that arrived on the funnel-facing
// listener. It rides the request context, which aprot hands to the auth hook as
// the connection's own context.
type funnelConn struct{}

// newGuestMux is everything the funnel publishes: the share page, the socket
// its client speaks, the two image endpoints it draws from, and the downloads.
//
// `tailscale funnel` publishes a whole port and nothing smaller, so the
// reduction has to happen on our side of it — which makes the list below the
// actual boundary. A route absent here is a route the public internet cannot
// reach, whatever credential it presents. Notably absent: /wm, /fonts,
// /healthz, /authz, and the pairing and discovery routes.
//
// The handlers passed in are the guest-only variants, whose authorizers refuse
// the owner's own credentials outright. Two locks, one door: the wrong route
// is not there, and the right route will not take an owner token.
func newGuestMux(ws http.Handler, img *imghttp.Handler, dl *imghttp.Downloads, page *guestui.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws)
	mux.Handle("GET /img/{id}/{level}", img)
	mux.Handle("GET /img/{id}/tile/{tx}/{ty}", http.HandlerFunc(img.ServeTile))
	mux.Handle("GET /dl/{id}", dl)
	mux.HandleFunc("GET /dl.zip", dl.ServeZip)
	mux.HandleFunc("GET /s/{token}", guestui.Redirect)
	mux.Handle("GET /s/{token}/{path...}", page)
	return mux
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
