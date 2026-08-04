// Package api defines the aprot handler surface of marrawd.
package api

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/aprot"
	"github.com/marrasen/aprot/tasks"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/discovery"
	"github.com/marrasen/marraw/internal/diskio"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/infer"
	"github.com/marrasen/marraw/internal/pairing"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/tsfunnel"
	"github.com/marrasen/marraw/internal/watch"
)

// Deps carries the backend services handlers operate on. For TypeScript
// generation a zero-value Deps is fine — handlers are never invoked.
type Deps struct {
	DB      *store.DB
	Pool    *decode.Pool
	Cache   *pyramid.Cache
	Handles *decode.HandleCache
	Scanner *scan.Scanner
	// Janitor bounds the preview cache; Settings adjusts its cap live.
	Janitor *pyramid.Janitor
	// DefaultCacheDir is the built-in preview-cache location (under the app
	// data dir); System.SetCacheDir("") restores it.
	DefaultCacheDir string
	// WatermarkDir stores the user's watermark images (under the app data
	// dir); AddWatermarkAsset writes it, exports and GET /wm/{name} read it.
	WatermarkDir string
	// Watch notices new folders and new photos on disk. Nil is valid: a
	// zero-value Deps is used for TypeScript generation, and a daemon whose
	// watch handle failed to open still works through manual rescans.
	Watch *watch.Watcher
	// Avail caches which roots' storage is currently reachable. Nil until the
	// availability poller starts; rootOnline falls back to a live stat.
	Avail *availability
	// Infer runs ONNX models for the AI features (mask map generation). Nil
	// is valid — the AI RPCs then report inference as unconfigured.
	Infer *infer.Manager
	// IOGate stages background full decodes through sequential per-device
	// reads (see internal/diskio). Nil is valid — opens stay direct.
	IOGate *diskio.Gate
	// Tokens validates WS auth frames and image-URL tokens. Nil is valid (dev
	// mode: no auth anywhere).
	Tokens *AuthTokens
	// Pairing holds remote-connection requests waiting for someone at this
	// machine to approve them. Nil on a loopback-only daemon — nothing can
	// reach it to ask.
	Pairing *pairing.Broker
	// Advertiser announces this daemon on the local network while remote
	// access is on. Nil is valid (loopback-only: nothing to announce).
	Advertiser *discovery.Advertiser
	// Funnel publishes this daemon on the public internet so a share link can
	// be opened by someone with no Tailscale of their own. Nil is valid: the
	// share UI then reports sharing as unavailable rather than failing.
	Funnel *tsfunnel.Manager
	// ListenAddr is the address the daemon actually bound (set by main before
	// serving); LoopbackOnly reports whether it is unreachable from other
	// machines. Surfaced to the Settings UI via System.GetRemoteAccess.
	ListenAddr   string
	LoopbackOnly bool

	mu     sync.RWMutex
	server *aprot.Server

	// guestMu serializes every read-modify-write of the share-link list —
	// minting, revoking, and stamping a link's last-opened time. Three
	// mutations from two types over one list, so the lock lives with the list
	// rather than with any one of them: a guest connecting mid-revoke would
	// otherwise write back the link that was just withdrawn.
	guestMu sync.Mutex
	// guestConns maps a share link's ID to the connections currently
	// authenticated as it. A set of connection IDs rather than a count: the
	// auth hook also runs on a mid-session re-auth, and a counter would climb
	// with nothing to balance it and leave a link reading as "viewing now"
	// forever.
	guestConns map[string]map[uint64]struct{}

	// jobMu guards the single folder-jobs slot: opening a folder cancels the
	// previous folder's metadata/pre-render passes.
	jobMu            sync.Mutex
	folderJobsCancel context.CancelFunc

	// focusPhotoID is the photo the client's viewport is centred on, set by
	// SetFocus. The pre-render pass renders outward from it so the loupe-ready
	// rendition warms nearest where the user is looking first. Zero means no
	// focus hint yet — the pass falls back to front-to-back order.
	focusPhotoID atomic.Int64

	// ingestMu guards the per-folder ingest state used by watcher-driven
	// rescans, which deliberately run outside the folder-jobs slot.
	ingestMu sync.Mutex
	ingest   map[int64]*ingestState

	// warmMu guards warmCancels: the in-flight post-save 512 thumb warm per
	// photo. A newer commit for the same photo cancels the previous warm
	// mid-decode, so a burst of quick-dial edits (or a paste/reset across
	// photos) supersedes stale warms instead of stacking uncancellable decodes
	// on the pool — the context.Background() no-watcher footgun.
	warmMu      sync.Mutex
	warmCancels map[int64]*warmSlot
}

// ingestState serialises watcher-driven passes for one folder. A pass already
// in flight snapshotted the photos needing work when it started, so it can
// never pick up files that landed after it — hence dirty, which re-runs it once
// afterwards rather than stacking a duplicate pass.
type ingestState struct {
	running bool
	dirty   bool
}

// SetServer wires the aprot server in after construction (the registry must
// exist before the server does).
func (d *Deps) SetServer(s *aprot.Server) {
	d.mu.Lock()
	d.server = s
	d.mu.Unlock()
}

// BroadcastRenderProgress pushes 1:1 render progress to every connected
// window; the loupe filters on the photo it is showing.
func (d *Deps) BroadcastRenderProgress(photoID int64, editHash string, frac float64) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.Broadcast(RenderProgressEvent{PhotoID: photoID, EditHash: editHash, Fraction: frac})
	}
}

// BroadcastAIMapsGenerated tells every window that photoID's rendered pixels
// changed under an unchanged edit hash — an AI map landed for a saved edit
// that already references it (see AIMapsGeneratedEvent).
func (d *Deps) BroadcastAIMapsGenerated(photoID int64) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.Broadcast(AIMapsGeneratedEvent{PhotoID: photoID})
	}
}

// Connection identities, set by the auth hook in cmd/marrawd. They are what
// lets a handler tell a window on this machine from a client somewhere else —
// the whole approve-a-remote-connection flow rests on the distinction.
const (
	// ConnLocal authenticated with the per-launch token, which only the
	// Electron shell that spawned this daemon knows.
	ConnLocal = "local"
	// ConnPairing authenticated with the shared pairing token: a remote
	// client that was set up by copying the token by hand.
	ConnPairing = "pairing"
	// ConnDevicePrefix + device ID identifies a remote client holding a token
	// minted for it by the approval flow.
	ConnDevicePrefix = "device:"
	// ConnGuestPrefix + link ID identifies someone holding a share link. Not
	// the user: see guest.go.
	ConnGuestPrefix = "guest:"
)

// ConnIsLocal reports whether the calling connection is a window on this
// machine.
//
// An authenticated connection is judged on its identity alone. The bind
// address used to stand in for that — a loopback daemon being unreachable from
// anywhere else — but a share link is served through a tunnel that arrives on
// loopback like any local window, so binding proves nothing once guests exist.
//
// The LoopbackOnly fallback survives for the case it was really covering: dev
// mode registers no auth hook, so connections carry no identity at all, and
// there a loopback daemon is still the whole of the trust boundary.
//
// The default is deny: a call with no connection in context on a daemon that
// *is* reachable does not get local rights.
func (d *Deps) ConnIsLocal(ctx context.Context) bool {
	c := aprot.Connection(ctx)
	if c == nil || c.UserID() == "" {
		return d.LoopbackOnly
	}
	return c.UserID() == ConnLocal
}

// callerCtx is the lifetime of the connection that made this request, for work
// that must outlive the request but not the caller — a prefetch warmed on a
// viewport hint, say. It is the middle ground between the request context
// (cancelled the moment the fire-and-forget handler returns) and
// context.Background() (cancelled by nothing, so the work cannot be stopped
// even after whoever asked for it has gone).
//
// Falls back to Background only where there is no connection at all: a
// background pass, or a test.
func callerCtx(ctx context.Context) context.Context {
	if c := aprot.Connection(ctx); c != nil {
		return c.Context()
	}
	return context.Background()
}

// DisconnectDevice drops every live connection belonging to one approved
// device, so revoking access takes effect immediately rather than at the
// client's next reconnect.
func (d *Deps) DisconnectDevice(id string) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.DisconnectUser(ConnDevicePrefix + id)
	}
}

// DisconnectGuest drops every live connection holding one share link, so
// revoking a share takes effect while the guest is still looking at it.
func (d *Deps) DisconnectGuest(id string) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.DisconnectUser(ConnGuestPrefix + id)
	}
}

// StartAdvertising announces this daemon on the local network under its
// current name. A no-op on a loopback-only daemon: announcing a machine
// nobody can reach would just offer connections that cannot be made.
func (d *Deps) StartAdvertising(ctx context.Context) {
	if d.Advertiser == nil || d.LoopbackOnly {
		return
	}
	_, portStr, err := net.SplitHostPort(d.ListenAddr)
	if err != nil {
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}
	d.Advertiser.Start(d.DeviceName(ctx), port)
}

// NotifyPairingChanged refreshes the pending-request subscription, which is
// what opens, closes and clears the approval dialog. Wired to the broker's
// OnChange in cmd/marrawd.
func (d *Deps) NotifyPairingChanged() { d.TriggerRefresh(pairingKey) }

// DeviceNameKey holds the name this machine answers discovery with; empty
// means "use the hostname".
const DeviceNameKey = "deviceName"

// PairingOpenKey holds whether new pairing requests are accepted. Absent means
// yes — a daemon that has never been configured still pairs.
const PairingOpenKey = "pairingOpen"

// DeviceName is what other machines see when they find this one: the user's
// chosen name, or the hostname.
func (d *Deps) DeviceName(ctx context.Context) string {
	if d.DB != nil {
		if name, err := d.DB.GetSetting(ctx, DeviceNameKey); err == nil && name != "" {
			return name
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "marraw"
}

// PairingOpen reports whether this daemon still accepts new pairing requests.
func (d *Deps) PairingOpen(ctx context.Context) bool {
	if d.DB == nil {
		return true
	}
	v, err := d.DB.GetSetting(ctx, PairingOpenKey)
	return err != nil || v != "false"
}

// TouchDevice records that an approved device just connected. Best-effort:
// the timestamp is only there to help the user recognise an entry in the
// devices list, so a failed write is not worth failing a connection over.
func (d *Deps) TouchDevice(ctx context.Context, id, addr string) {
	if d.Tokens == nil || d.DB == nil {
		return
	}
	devices := d.Tokens.Devices()
	for i := range devices {
		if devices[i].ID != id {
			continue
		}
		devices[i].LastSeen = time.Now().UnixMilli()
		if addr != "" {
			devices[i].Addr = addr
		}
		d.Tokens.SetDevices(devices)
		_ = SaveDevices(ctx, d.DB, devices)
		d.TriggerRefresh(devicesKey)
		return
	}
}

// TriggerRefresh fires subscription refresh keys from background goroutines.
func (d *Deps) TriggerRefresh(keys ...string) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.TriggerRefresh(keys...)
	}
}

// PatchPhotos pushes granular photo patches to the folder-list subscribers —
// O(patch) on the wire instead of a full list refresh. Subscribers without a
// patch reducer fall back to a full refresh automatically.
func (d *Deps) PatchPhotos(ctx context.Context, patches []PhotoPatch) {
	if len(patches) == 0 {
		return
	}
	ids := make([]int64, len(patches))
	for i, p := range patches {
		ids[i] = p.ID
	}
	folders, err := d.DB.PhotoFolders(ctx, ids)
	if err != nil {
		return
	}
	byFolder := map[int64][]PhotoPatch{}
	for _, p := range patches {
		f := folders[p.ID]
		byFolder[f] = append(byFolder[f], p)
	}
	for f, ps := range byFolder {
		d.patchFolderPhotos(f, ps)
	}
}

// patchFolderPhotos pushes patches to one folder's subscription key.
func (d *Deps) patchFolderPhotos(folderID int64, patches []PhotoPatch) {
	d.mu.RLock()
	s := d.server
	d.mu.RUnlock()
	if s != nil {
		s.PatchSubscription(PhotoPatchEvent{Patches: patches}, photosKey(folderID))
	}
}

func photosKey(folderID int64) string { return fmt.Sprintf("photos:%d", folderID) }
func editKey(photoID int64) string    { return fmt.Sprintf("edit:%d", photoID) }

// NewRegistry builds the aprot registry with all marraw handler groups.
func NewRegistry(deps *Deps) (*aprot.Registry, *Library, *Edits, *Export) {
	registry := aprot.NewRegistry()
	registry.SetValidator(aprot.NewPlaygroundValidator())

	library := &Library{deps: deps}
	edits := &Edits{deps: deps}
	export := &Export{deps: deps}
	settings := &Settings{deps: deps}
	registry.Register(library)
	registry.Register(edits)
	registry.Register(export)
	registry.Register(&System{deps: deps})
	registry.Register(settings)
	registry.Register(&Share{deps: deps, lib: library})

	registry.RegisterEnumFor(library, FlagValues())
	registry.RegisterEnumFor(settings, ThemeValues())
	registry.RegisterEnumFor(settings, ThumbFitValues())
	registry.RegisterEnumFor(settings, LibrarySortValues())
	registry.RegisterEnumFor(settings, ShootSortValues())
	registry.RegisterEnumFor(settings, ShootGroupValues())
	registry.RegisterEnumFor(settings, FlagFilterValues())
	registry.RegisterEnumFor(settings, WatermarkElementTypeValues())
	registry.RegisterEnumFor(settings, WatermarkAnchorValues())
	registry.RegisterEnumFor(settings, WatermarkFontIDValues())
	registry.RegisterEnumFor(settings, WatermarkFillValues())
	registry.RegisterEnumFor(settings, WatermarkGradientDirValues())
	registry.RegisterEnumFor(edits, edit.WBModeValues())
	registry.RegisterEnumFor(edits, edit.DemosaicValues())
	registry.RegisterEnumFor(edits, edit.AIKindValues())
	registry.RegisterEnumFor(edits, edit.LensModeValues())
	registry.RegisterEnumFor(export, ExportFormatValues())
	registry.RegisterEnumFor(export, ColorSpaceValues())
	registry.RegisterEnumFor(export, SharpenTargetValues())
	registry.RegisterEnumFor(export, SharpenAmountValues())
	registry.RegisterEnumFor(export, ExifModeValues())
	// PhotoPatchEvent is no longer broadcast as a push event — it is the
	// payload of subscription patches — but registering it keeps the
	// TypeScript types generated for the client-side patch reducer.
	registry.RegisterPushEventFor(library, PhotoPatchEvent{})
	registry.RegisterPushEventFor(library, RenderProgressEvent{})
	registry.RegisterPushEventFor(edits, AIMapsGeneratedEvent{})

	// marraw is single-user localhost: any window may cancel any task. This
	// also restores cancel rights after a reconnect, which the default
	// connection-keyed policy would drop.
	tasks.EnableWithMeta[TaskMeta](registry, tasks.WithCancelAuthorizer(
		func(context.Context, tasks.TaskCancelInfo) error { return nil },
	))
	return registry, library, edits, export
}
