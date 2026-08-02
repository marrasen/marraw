package pairing

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateAndApprove(t *testing.T) {
	var b Broker
	req, err := b.Create("Laptop", "darwin", "192.168.1.9:51234")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(req.ID) != 32 {
		t.Errorf("ID = %q, want 32 hex chars", req.ID)
	}
	if len(req.Code) != 4 || strings.Trim(req.Code, "0123456789") != "" {
		t.Errorf("Code = %q, want 4 digits", req.Code)
	}
	if got := b.Pending(); len(got) != 1 || got[0].ID != req.ID {
		t.Fatalf("Pending() = %+v, want the one request", got)
	}

	done := make(chan [2]string, 1)
	go func() {
		st, tok := b.Wait(context.Background(), req.ID)
		done <- [2]string{string(st), tok}
	}()

	if err := b.Resolve(req.ID, true, "devtoken"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := <-done
	if got[0] != string(StatusApproved) || got[1] != "devtoken" {
		t.Errorf("Wait = %v, want approved/devtoken", got)
	}
	if p := b.Pending(); len(p) != 0 {
		t.Errorf("Pending() after resolve = %+v, want empty", p)
	}
}

func TestDenyCarriesNoToken(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	if err := b.Resolve(req.ID, false, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The outcome stays collectable after resolution: the client only calls
	// /pair/wait *after* /pair/request, so a decision landing in that gap must
	// not be lost.
	st, tok := b.Wait(context.Background(), req.ID)
	if st != StatusDenied || tok != "" {
		t.Errorf("Wait after deny = %v/%q, want denied with no token", st, tok)
	}
}

// The window a client is most likely to hit: the host approves between the
// POST that created the request and the GET that waits on it.
func TestApprovalBeforeWaitIsNotLost(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	if err := b.Resolve(req.ID, true, "devtoken"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	st, tok := b.Wait(context.Background(), req.ID)
	if st != StatusApproved || tok != "devtoken" {
		t.Errorf("Wait after a pre-emptive approval = %v/%q, want approved/devtoken", st, tok)
	}
}

func TestResolveTwiceIsRejected(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	if err := b.Resolve(req.ID, false, ""); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if err := b.Resolve(req.ID, true, "devtoken"); err == nil {
		t.Error("a denied request could be approved afterwards")
	}
	if st, tok := b.Wait(context.Background(), req.ID); st != StatusDenied || tok != "" {
		t.Errorf("outcome changed after a second Resolve: %v/%q", st, tok)
	}
}

func TestApproveNeedsToken(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	if err := b.Resolve(req.ID, true, ""); err == nil {
		t.Error("Resolve(approve, no token) succeeded, want error")
	}
	if p := b.Pending(); len(p) != 1 {
		t.Errorf("a rejected approval must leave the request pending, got %+v", p)
	}
}

func TestPendingCap(t *testing.T) {
	var b Broker
	for i, addr := range []string{"a:1", "b:1", "c:1"} {
		if _, err := b.Create("L", "linux", addr); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if _, err := b.Create("L", "linux", "d:1"); err != ErrTooMany {
		t.Errorf("4th request from a new address: err = %v, want ErrTooMany", err)
	}
}

func TestRepeatFromSameAddressSupersedes(t *testing.T) {
	var b Broker
	first, _ := b.Create("Laptop", "linux", "10.0.0.4:5000")
	second, err := b.Create("Laptop", "linux", "10.0.0.4:5001")
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if p := b.Pending(); len(p) != 1 || p[0].ID != second.ID {
		t.Fatalf("Pending() = %+v, want only the second request", p)
	}
	// The superseded request's waiter must be released, not left hanging.
	st, _ := b.Wait(context.Background(), first.ID)
	if st != StatusDenied {
		t.Errorf("superseded Wait = %v, want denied", st)
	}
}

func TestSameAddressRepeatDoesNotConsumeCap(t *testing.T) {
	var b Broker
	b.Create("L", "linux", "a:1")
	b.Create("L", "linux", "b:1")
	for range 5 {
		if _, err := b.Create("L", "linux", "a:2"); err != nil {
			t.Fatalf("repeat from a: %v", err)
		}
	}
	if _, err := b.Create("L", "linux", "c:1"); err != nil {
		t.Errorf("third distinct address: %v, want room", err)
	}
}

func TestExpiredCannotBeApproved(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	// Reach in and age it: TTL is two minutes and the test must not wait.
	b.mu.Lock()
	b.requests[req.ID].ExpiresAt = time.Now().Add(-time.Second)
	b.mu.Unlock()

	if err := b.Resolve(req.ID, true, "devtoken"); err == nil {
		t.Error("approving an expired request succeeded, want error")
	}
	st, tok := b.Wait(context.Background(), req.ID)
	if st == StatusApproved || tok != "" {
		t.Errorf("expired request handed out %v/%q", st, tok)
	}
}

func TestPendingSweepsExpired(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	b.mu.Lock()
	b.requests[req.ID].ExpiresAt = time.Now().Add(-time.Second)
	b.mu.Unlock()

	if p := b.Pending(); len(p) != 0 {
		t.Errorf("Pending() = %+v, want the expired request swept", p)
	}
	if st, _ := b.Wait(context.Background(), req.ID); st != StatusExpired {
		t.Errorf("Wait after sweep = %v, want expired", st)
	}
}

func TestUnknownIDs(t *testing.T) {
	var b Broker
	if err := b.Resolve("nope", true, "t"); err == nil {
		t.Error("Resolve(unknown) succeeded, want error")
	}
	if st, _ := b.Wait(context.Background(), "nope"); st != StatusExpired {
		t.Errorf("Wait(unknown) = %v, want expired", st)
	}
}

func TestWaitReturnsPendingOnCallerTimeout(t *testing.T) {
	var b Broker
	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	st, _ := b.Wait(ctx, req.ID)
	if st != StatusPending {
		t.Errorf("Wait past its own deadline = %v, want pending", st)
	}
	// The host's dialog must survive a client that gave up polling.
	if p := b.Pending(); len(p) != 1 {
		t.Errorf("Pending() = %+v, want the request still waiting", p)
	}
}

func TestConcurrentWaitAndResolve(t *testing.T) {
	for range 50 {
		var b Broker
		req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")

		var wg sync.WaitGroup
		results := make([]Status, 4)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], _ = b.Wait(context.Background(), req.ID)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Resolve(req.ID, true, "devtoken")
		}()
		wg.Wait()

		for _, st := range results {
			if st != StatusApproved && st != StatusExpired {
				t.Fatalf("waiter saw %v, want approved or expired", st)
			}
		}
	}
}

func TestOnChangeFires(t *testing.T) {
	var mu sync.Mutex
	n := 0
	b := Broker{OnChange: func() { mu.Lock(); n++; mu.Unlock() }}

	req, _ := b.Create("Laptop", "linux", "10.0.0.4:1")
	b.Resolve(req.ID, false, "")

	mu.Lock()
	defer mu.Unlock()
	if n < 2 {
		t.Errorf("OnChange fired %d times, want at least one per create and resolve", n)
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Marcus's laptop  ", "Marcus's laptop"},
		{"line\nbreak", "linebreak"},
		{"tab\tsep", "tabsep"},
		{"nul\x00byte", "nulbyte"},
		{strings.Repeat("x", 200), strings.Repeat("x", 64)},
		{"日本語のラップトップ", "日本語のラップトップ"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in, 64); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCreateNamesTheUnnamed(t *testing.T) {
	var b Broker
	req, _ := b.Create("   ", "linux", "10.0.0.4:1")
	if req.Name == "" {
		t.Error("a blank name must not reach the dialog as an empty string")
	}
}

func TestAddrIsNotSanitized(t *testing.T) {
	// Addr comes from the socket, not the caller, so it is passed through
	// verbatim — the dialog shows exactly what the daemon saw.
	var b Broker
	req, _ := b.Create("L", "linux", "[fe80::1%eth0]:8482")
	if req.Addr != "[fe80::1%eth0]:8482" {
		t.Errorf("Addr = %q, want it untouched", req.Addr)
	}
}
