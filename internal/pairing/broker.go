// Package pairing holds the daemon's pending remote-connection requests: a
// laptop asks to connect, the person sitting at this machine approves or
// denies it in a dialog, and the laptop is handed a device token.
//
// The HTTP endpoints that drive this are necessarily unauthenticated — the
// whole point is that the caller has no credential yet — so the broker, not
// the transport, is where abuse is bounded. Every limit here exists to stop a
// stranger who can reach the port from papering the host's screen with
// dialogs.
package pairing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Status is where one request stands. Only StatusPending is not terminal.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusExpired  Status = "expired"
)

// TTL is how long a request waits for a human, and also how long its outcome
// stays collectable afterwards. Long enough to walk to the other machine,
// short enough that a forgotten dialog clears itself.
const TTL = 2 * time.Minute

// maxPending bounds the dialogs one host can be made to show at once. A
// second request from the same peer IP replaces its own rather than adding to
// this count, so the cap is about distinct sources.
const maxPending = 3

// ErrTooMany is returned when maxPending requests are already waiting.
var ErrTooMany = errors.New("pairing: too many pending requests")

// ErrUnknown is returned for an ID that never existed, was already resolved,
// or has aged out.
var ErrUnknown = errors.New("pairing: unknown request")

// Request is one machine asking to connect. Addr is the peer address the
// daemon actually saw — never anything the caller told us — so the dialog
// cannot be lied to about where the request came from.
type Request struct {
	ID        string
	Code      string
	Name      string
	Platform  string
	Addr      string
	CreatedAt time.Time
	ExpiresAt time.Time

	// done is closed once the outcome is settled. status and token are
	// written before that close and read only after it, which is what makes
	// them safe to read without the broker lock.
	done   chan struct{}
	status Status
	token  string
}

// Broker holds the live requests. The zero value is ready to use.
//
// A resolved request is deliberately *kept* until its original expiry rather
// than deleted on the spot: the client POSTs /pair/request and only then GETs
// /pair/wait, so an approval landing in that gap would otherwise mint a token
// nobody could collect. Holding it for the same TTL closes that window, and
// the outcome is only readable by whoever holds the unguessable ID.
type Broker struct {
	mu       sync.Mutex
	requests map[string]*Request

	// OnChange fires after any change to the pending set — a new request, a
	// resolution, or a sweep — so the UI subscription can be refreshed. It is
	// called without the lock held and may block.
	OnChange func()
}

// Create records a new request from addr and returns it. name and platform
// are caller-supplied and therefore sanitised; addr is not.
//
// A pending request from the same peer IP is superseded (resolved as denied,
// so its long-poll returns at once) rather than counted twice: a laptop whose
// user clicked Connect again should replace its own stale dialog, not be told
// the host is busy. Matching is by IP, not host:port — a retry comes from a
// fresh source port.
func (b *Broker) Create(name, platform, addr string) (*Request, error) {
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	code, err := randomCode()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	req := &Request{
		ID:        id,
		Code:      code,
		Name:      sanitize(name, 64),
		Platform:  sanitize(platform, 32),
		Addr:      addr,
		CreatedAt: now,
		ExpiresAt: now.Add(TTL),
		done:      make(chan struct{}),
		status:    StatusPending,
	}
	if req.Name == "" {
		req.Name = "Unnamed computer"
	}
	ip := peerIP(addr)

	b.mu.Lock()
	swept := b.sweepLocked(now)
	pending := 0
	var superseded *Request
	for _, p := range b.requests {
		if p.status != StatusPending {
			continue
		}
		if superseded == nil && peerIP(p.Addr) == ip {
			superseded = p
			continue
		}
		pending++
	}
	if superseded != nil {
		b.resolveLocked(superseded, StatusDenied, "")
	} else if pending >= maxPending {
		b.mu.Unlock()
		if swept || superseded != nil {
			b.changed()
		}
		return nil, ErrTooMany
	}
	if b.requests == nil {
		b.requests = make(map[string]*Request)
	}
	b.requests[req.ID] = req
	b.mu.Unlock()

	b.changed()
	return req, nil
}

// Wait blocks until the request is resolved, ages out, or ctx is done, then
// reports the outcome and (for an approval) the minted device token. An
// unknown ID reports StatusExpired: a caller that waited too long and a caller
// that made the ID up look the same from here, deliberately.
func (b *Broker) Wait(ctx context.Context, id string) (Status, string) {
	b.mu.Lock()
	req := b.requests[id]
	b.mu.Unlock()
	if req == nil {
		return StatusExpired, ""
	}

	select {
	case <-req.done:
	case <-time.After(time.Until(req.ExpiresAt)):
		b.expire(req)
		<-req.done
	case <-ctx.Done():
		// The caller gave up (long-poll deadline). The request stays pending
		// so the host's dialog survives the client's next poll.
		return StatusPending, ""
	}

	return req.status, req.token
}

// Resolve settles a pending request. token is stored for the waiter and must
// be non-empty when approve is true.
func (b *Broker) Resolve(id string, approve bool, token string) error {
	if approve && token == "" {
		return errors.New("pairing: approval needs a token")
	}

	b.mu.Lock()
	req := b.requests[id]
	switch {
	case req == nil || req.status != StatusPending:
		b.mu.Unlock()
		return ErrUnknown
	case time.Now().After(req.ExpiresAt):
		// The dialog may have sat on screen for an hour; by now the code on
		// the other machine means nothing.
		b.resolveLocked(req, StatusExpired, "")
		b.mu.Unlock()
		b.changed()
		return fmt.Errorf("%w: expired", ErrUnknown)
	case approve:
		b.resolveLocked(req, StatusApproved, token)
	default:
		b.resolveLocked(req, StatusDenied, "")
	}
	b.mu.Unlock()

	b.changed()
	return nil
}

// Find returns a copy of one still-pending request. Used by the approval path
// to read the requester's name and address before minting a token for it; the
// authoritative "is it still pending" check is Resolve, which settles it.
func (b *Broker) Find(id string) (Request, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.requests[id]
	if p == nil || p.status != StatusPending || time.Now().After(p.ExpiresAt) {
		return Request{}, false
	}
	return Request{
		ID: p.ID, Code: p.Code, Name: p.Name, Platform: p.Platform,
		Addr: p.Addr, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
	}, true
}

// Pending returns the requests still waiting on a human, oldest first, after
// ageing out any that timed out.
func (b *Broker) Pending() []Request {
	now := time.Now()
	b.mu.Lock()
	swept := b.sweepLocked(now)
	out := make([]Request, 0, len(b.requests))
	for _, p := range b.requests {
		if p.status != StatusPending {
			continue
		}
		out = append(out, Request{
			ID: p.ID, Code: p.Code, Name: p.Name, Platform: p.Platform,
			Addr: p.Addr, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
		})
	}
	b.mu.Unlock()

	if swept {
		b.changed()
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.Before(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// expire ages out a request whose waiter outlasted it.
func (b *Broker) expire(req *Request) {
	b.mu.Lock()
	changed := b.resolveLocked(req, StatusExpired, "")
	b.mu.Unlock()
	if changed {
		b.changed()
	}
}

// sweepLocked settles requests that ran out of time and drops those whose
// outcome is no longer collectable. Returns whether anything changed. Callers
// hold b.mu.
func (b *Broker) sweepLocked(now time.Time) bool {
	changed := false
	for id, p := range b.requests {
		if !now.After(p.ExpiresAt) {
			continue
		}
		if b.resolveLocked(p, StatusExpired, "") {
			changed = true
			continue
		}
		// Already settled and now past its collection window.
		delete(b.requests, id)
	}
	return changed
}

// resolveLocked settles a pending request and wakes its waiters. Returns
// false if it was already settled. Callers hold b.mu — that lock is what makes
// the transition happen exactly once; writing status and token before
// close(done) is what makes them safe to read on the far side of the channel.
func (b *Broker) resolveLocked(req *Request, status Status, token string) bool {
	if req.status != StatusPending {
		return false
	}
	req.status = status
	req.token = token
	close(req.done)
	return true
}

func (b *Broker) changed() {
	if b.OnChange != nil {
		b.OnChange()
	}
}

// peerIP reduces a "host:port" peer address to its host, so a retry from a
// fresh source port still counts as the same machine. An address that will not
// split (already bare) is used as-is.
func peerIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// randomHex returns n random bytes as hex. Request IDs use this: /pair/wait is
// unauthenticated, so an ID must be unguessable.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomCode returns the 4 digits shown on both machines. It is not a secret —
// it exists so a person approving one request cannot be tricked into approving
// a different one that arrived at the same moment.
func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// sanitize makes a caller-supplied string safe to put in a dialog: no control
// characters (a newline could forge a second line of UI text), no leading or
// trailing space, and a hard length cap.
func sanitize(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		if b.Len() > max*4 {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	runes := []rune(out)
	if len(runes) > max {
		out = string(runes[:max])
	}
	return out
}
