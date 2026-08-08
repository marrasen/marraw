package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/marrasen/marraw/internal/store"
)

// PairingTokenKey is the settings-table key holding the persistent pairing
// token remote clients authenticate with.
const PairingTokenKey = "pairingToken"

// RemoteDevicesKey is the settings-table key holding the approved-device list
// as JSON. A JSON blob rather than a table: the list is small, always read and
// written whole, and this needs no schema migration (see ui:folderViews for
// the same trade).
const RemoteDevicesKey = "remoteDevices"

// Device is one machine the user approved through the pairing dialog. Each
// gets its own token so a single laptop can be revoked without disturbing the
// others — unlike the shared pairing token, where regenerating locks out
// everyone at once.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Token is the credential itself. It never leaves the daemon except in the
	// pairing approval that mints it — ListRemoteDevices must not carry it.
	Token string `json:"token"`
	// Addr is where the pairing request came from, kept only so the devices
	// list can say something recognisable about an unfamiliar entry.
	Addr     string `json:"addr"`
	AddedAt  int64  `json:"addedAt"`
	LastSeen int64  `json:"lastSeen"`
}

// TokenMatch says which credential a token turned out to be. Callers use it to
// tell a local window (launch token) from a remote client, which is what gates
// the approval RPCs — approving your own pairing request from the machine
// asking to be let in would defeat the whole exercise.
// A Guest match is the odd one out: it is not the user at all, but someone
// holding a share link, and it carries the link so callers can read the folder
// and capabilities it is confined to (see guest.go).
type TokenMatch struct {
	OK       bool
	Launch   bool
	Pairing  bool
	DeviceID string
	Guest    *GuestLink
}

// AuthTokens validates client credentials: the per-launch token the Electron
// shell passes its own windows, the persistent pairing token remote clients
// may store, and one token per approved device. The pairing token and device
// list are swappable at runtime so regenerating or revoking takes effect
// without re-wiring handlers.
type AuthTokens struct {
	launch  string
	pairing atomic.Value // string
	devices atomic.Value // []Device
	guests  atomic.Value // []GuestLink
}

// NewAuthTokens builds the holder. Either token may be empty; an empty token
// never matches.
func NewAuthTokens(launch, pairing string) *AuthTokens {
	t := &AuthTokens{launch: launch}
	t.pairing.Store(pairing)
	t.devices.Store([]Device(nil))
	t.guests.Store([]GuestLink(nil))
	return t
}

// Match reports which credential tok is. It is on the hot path — every /img
// request runs through it — so it allocates nothing.
//
// The device scan runs to completion rather than returning on the first hit:
// bailing early would make the time taken depend on where in the list a token
// sits, which is exactly the signal constant-time comparison exists to hide.
func (t *AuthTokens) Match(tok string) TokenMatch {
	if tok == "" {
		return TokenMatch{}
	}
	if t.launch != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(t.launch)) == 1 {
		return TokenMatch{OK: true, Launch: true}
	}
	if p, _ := t.pairing.Load().(string); p != "" &&
		subtle.ConstantTimeCompare([]byte(tok), []byte(p)) == 1 {
		return TokenMatch{OK: true, Pairing: true}
	}
	devices, _ := t.devices.Load().([]Device)
	m := TokenMatch{}
	for i := range devices {
		if devices[i].Token != "" &&
			subtle.ConstantTimeCompare([]byte(tok), []byte(devices[i].Token)) == 1 {
			m = TokenMatch{OK: true, DeviceID: devices[i].ID}
		}
	}
	if m.OK {
		return m
	}
	// Share links last: an expired one matches nothing at all, so a lapsed
	// link is refused at the door rather than reaching a handler that would
	// have to remember to check.
	//
	// Folder 0 is refused for the same reason, and it is the more dangerous of
	// the two: 0 is the unconfined sentinel, so a link carrying it would widen
	// to the whole library in imghttp rather than narrow to nothing. CreateLink
	// never mints one, so it can only come from a corrupt or hand-edited
	// settings blob — the same reasoning liveGuest applies on the RPC path.
	// Both surfaces have to agree here: this is the door /img and /dl come
	// through, and they never consult liveGuest.
	guests, _ := t.guests.Load().([]GuestLink)
	if g := matchGuest(guests, tok); g != nil && g.FolderID != 0 && !g.Expired(time.Now()) {
		return TokenMatch{OK: true, Guest: g}
	}
	return m
}

// Valid reports whether tok is any accepted credential.
func (t *AuthTokens) Valid(tok string) bool { return t.Match(tok).OK }

// Pairing returns the current pairing token.
func (t *AuthTokens) Pairing() string {
	p, _ := t.pairing.Load().(string)
	return p
}

// SetPairing swaps the pairing token. Live WS connections keep working until
// they disconnect; image URLs carrying the old token start failing.
func (t *AuthTokens) SetPairing(tok string) {
	t.pairing.Store(tok)
}

// Devices returns a copy of the approved-device list.
func (t *AuthTokens) Devices() []Device {
	d, _ := t.devices.Load().([]Device)
	return append([]Device(nil), d...)
}

// SetDevices swaps the approved-device list. Revoking here stops the token
// authenticating anything new; evicting the device's live connection is the
// caller's job (see System.RevokeRemoteDevice).
func (t *AuthTokens) SetDevices(devices []Device) {
	t.devices.Store(append([]Device(nil), devices...))
}

// Guests returns a copy of the share-link list.
func (t *AuthTokens) Guests() []GuestLink {
	g, _ := t.guests.Load().([]GuestLink)
	return append([]GuestLink(nil), g...)
}

// SetGuests swaps the share-link list. As with SetDevices, revoking here stops
// the token authenticating anything new; dropping the guest's live connection
// is the caller's job (see Deps.DisconnectGuest).
func (t *AuthTokens) SetGuests(links []GuestLink) {
	t.guests.Store(append([]GuestLink(nil), links...))
}

// LoadDevices reads the approved-device list from the settings table. A
// corrupt blob reads as an empty list rather than failing startup: locking the
// user out of their own daemon over unparseable JSON would be worse than
// making them pair again.
func LoadDevices(ctx context.Context, db *store.DB) []Device {
	raw, err := db.GetSetting(ctx, RemoteDevicesKey)
	if err != nil || raw == "" {
		return nil
	}
	var devices []Device
	if err := json.Unmarshal([]byte(raw), &devices); err != nil {
		return nil
	}
	return devices
}

// SaveDevices persists the approved-device list.
func SaveDevices(ctx context.Context, db *store.DB, devices []Device) error {
	raw, err := json.Marshal(devices)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, RemoteDevicesKey, string(raw))
}

// NewDevice mints an approved device with a fresh token.
func NewDevice(name, addr string) (Device, error) {
	id, err := GeneratePairingToken()
	if err != nil {
		return Device{}, err
	}
	tok, err := GeneratePairingToken()
	if err != nil {
		return Device{}, err
	}
	now := time.Now().UnixMilli()
	return Device{ID: id, Name: name, Token: tok, Addr: addr, AddedAt: now, LastSeen: now}, nil
}

// GeneratePairingToken returns a fresh 32-hex-char random token — long enough
// to be unguessable, short enough to eyeball when pairing a laptop.
func GeneratePairingToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
