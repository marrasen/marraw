package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync/atomic"
)

// PairingTokenKey is the settings-table key holding the persistent pairing
// token remote clients authenticate with.
const PairingTokenKey = "pairingToken"

// AuthTokens validates client credentials: the per-launch token the Electron
// shell passes its own windows, and the persistent pairing token remote
// clients store. The pairing token is swappable at runtime so
// RegeneratePairingToken takes effect without re-wiring handlers.
type AuthTokens struct {
	launch  string
	pairing atomic.Value // string
}

// NewAuthTokens builds the holder. Either token may be empty; an empty token
// never matches.
func NewAuthTokens(launch, pairing string) *AuthTokens {
	t := &AuthTokens{launch: launch}
	t.pairing.Store(pairing)
	return t
}

// Valid reports whether tok matches the launch token or the current pairing
// token.
func (t *AuthTokens) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	if t.launch != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(t.launch)) == 1 {
		return true
	}
	p, _ := t.pairing.Load().(string)
	return p != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(p)) == 1
}

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

// GeneratePairingToken returns a fresh 32-hex-char random token — long enough
// to be unguessable, short enough to eyeball when pairing a laptop.
func GeneratePairingToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
