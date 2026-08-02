package discovery

import (
	"strconv"
	"testing"
)

func TestNormalize(t *testing.T) {
	port := strconv.Itoa(DefaultPort)
	tests := []struct{ in, want string }{
		{"desktop", "desktop:" + port},
		{"192.168.1.44", "192.168.1.44:" + port},
		{"desktop:9000", "desktop:9000"},
		{"http://desktop", "desktop:" + port},
		{"https://desktop:9000", "desktop:9000"},
		{"desktop/", "desktop:" + port},
		{"  desktop  ", "desktop:" + port},
		{"studio.tail1234.ts.net", "studio.tail1234.ts.net:" + port},
		// Already carries a port, or is an IPv6 literal: passed through. The
		// shell's normalizeHost agrees on exactly this, and a disagreement
		// would make one machine look like two.
		{"[fe80::1]:8482", "[fe80::1]:8482"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHostOnly(t *testing.T) {
	tests := []struct{ in, want string }{
		{"192.168.1.44:8482", "192.168.1.44"},
		{"desktop:9000", "desktop"},
		{"[fe80::1]:8482", "fe80::1"},
		{"bare-host", "bare-host"},
	}
	for _, tt := range tests {
		if got := HostOnly(tt.in); got != tt.want {
			t.Errorf("HostOnly(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLocalAddressesCoversLoopbackAndHostname(t *testing.T) {
	self := LocalAddresses()
	for _, want := range []string{"127.0.0.1", "::1", "localhost"} {
		if !self[want] {
			t.Errorf("LocalAddresses() missing %q — a scan would offer this machine to itself", want)
		}
	}
}

func TestReachableAddressesSkipsLoopback(t *testing.T) {
	// Whatever this machine has, none of it should be an address another
	// computer cannot use.
	for _, addr := range ReachableAddresses(DefaultPort) {
		host := HostOnly(addr)
		if host == "127.0.0.1" || host == "::1" {
			t.Errorf("ReachableAddresses returned loopback %q", addr)
		}
		if _, portStr, err := splitPort(addr); err != nil || portStr != strconv.Itoa(DefaultPort) {
			t.Errorf("ReachableAddresses(%d) returned %q without that port", DefaultPort, addr)
		}
	}
}

// splitPort keeps the test readable without importing net twice over.
func splitPort(addr string) (string, string, error) {
	host := HostOnly(addr)
	return host, addr[len(host)+1:], nil
}

func TestAdvertiserStatusBeforeStart(t *testing.T) {
	var a Advertiser
	if on, err := a.Status(); on || err != "" {
		t.Errorf("Status() on a fresh advertiser = %v/%q, want false and no error", on, err)
	}
}

func TestNilAdvertiserStatus(t *testing.T) {
	// A loopback-only daemon leaves this nil; Settings still asks for status.
	var a *Advertiser
	if on, err := a.Status(); on || err != "" {
		t.Errorf("Status() on nil = %v/%q, want false and no error", on, err)
	}
}

func TestAdvertiseRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("binds a multicast socket")
	}
	var a Advertiser
	a.Start("marraw-test-instance", 18482)
	defer a.Stop()

	on, errText := a.Status()
	if !on {
		// A sandbox with no multicast is a legitimate environment, and the
		// product handles it by telling the user. Do not fail the suite for
		// it — but do prove the failure is reported rather than swallowed.
		if errText == "" {
			t.Error("advertising is off but Status() reports no reason")
		}
		t.Skipf("no multicast here: %v", errText)
	}

	// Restarting under the same name must not error or leave it off.
	a.Start("marraw-test-renamed", 18482)
	if on, errText := a.Status(); !on {
		t.Errorf("re-announcing under a new name turned it off: %v", errText)
	}
	a.Stop()
	if on, _ := a.Status(); on {
		t.Error("Stop() left the announcement running")
	}
}
