package guestui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const goodToken = "0123456789abcdef0123456789abcdef"

// serve drives the handler the way the mux does, with {token} and {path...}
// already parsed out of the URL.
func serve(h http.Handler, token, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/s/"+token+"/"+path, nil)
	r.SetPathValue("token", token)
	r.SetPathValue("path", path)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Everything under /s/ is reachable by anyone who finds the funnel hostname,
// so it is served like a public page rather than like the desktop bundle.
func TestSharePageCarriesItsSecurityHeaders(t *testing.T) {
	h := &Handler{TokenValid: func(string) bool { return true }}
	res := serve(h, goodToken, "").Result()

	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'self'", // no 'unsafe-inline' here: that is the half that matters
		"frame-ancestors 'none'",
		"base-uri 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("CSP allows inline script")
	}
	// The URL is the credential, so it must not ride out in a Referer.
	if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// A dead link gets the same page whatever it asks for, and the headers still
// apply — the refusal path is the one an attacker sees most.
func TestDeadLinkIsRefusedWithHeadersIntact(t *testing.T) {
	h := &Handler{TokenValid: func(string) bool { return false }}
	res := serve(h, goodToken, "").Result()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" {
		t.Error("the refusal page carries no CSP")
	}
}

// The bundle is an embed.FS, so traversal cannot reach the disk even in
// principle — but the cleaning step in front of it is load-bearing enough to
// pin down.
func TestBundlePathsCannotEscape(t *testing.T) {
	h := &Handler{TokenValid: func(string) bool { return true }}
	for _, path := range []string{
		"../guestui.go",
		"../../../etc/passwd",
		"..%2f..%2fetc%2fpasswd",
		"assets/../../guestui.go",
		"/etc/passwd",
	} {
		res := serve(h, goodToken, path).Result()
		if res.StatusCode == http.StatusOK {
			t.Errorf("%q was served with 200", path)
		}
	}
}

// Redirect answers before any token has been checked, so it validates the
// shape rather than echoing whatever it was handed into a Location header.
func TestRedirectValidatesTheTokenShape(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/s/"+goodToken, nil)
	r.SetPathValue("token", goodToken)
	w := httptest.NewRecorder()
	Redirect(w, r)
	if got := w.Result().Header.Get("Location"); got != "/s/"+goodToken+"/" {
		t.Errorf("Location = %q, want the trailing-slash form", got)
	}

	for _, bad := range []string{
		"", "nothex", strings.Repeat("a", 31), strings.Repeat("a", 33),
		"0123456789ABCDEF0123456789ABCDEF", // uppercase is not what we mint
		"../../etc/passwd",
		strings.Repeat("x", 8000),
	} {
		r := httptest.NewRequest(http.MethodGet, "/s/x", nil)
		r.SetPathValue("token", bad)
		w := httptest.NewRecorder()
		Redirect(w, r)
		if loc := w.Result().Header.Get("Location"); loc != "" {
			t.Errorf("Redirect(%q) set Location %q, want no redirect", bad, loc)
		}
	}
}

// The share page's CSP names the host the page came from, and that name
// arrives in a request header. A policy assembled out of request data is worth
// not writing, so a host that is not one is dropped rather than interpolated.
func TestSafeHost(t *testing.T) {
	for _, ok := range []string{
		"marraw.tail1234.ts.net",
		"127.0.0.1:8484",
		"[::1]:8484",
		"host-with-dashes.example:443",
	} {
		if safeHost(ok) != ok {
			t.Errorf("safeHost(%q) = %q, want it kept", ok, safeHost(ok))
		}
	}
	for _, bad := range []string{
		"",
		"evil.example; script-src *",
		"evil.example' 'unsafe-inline",
		"host with spaces",
		"host\nX-Injected: 1",
		strings.Repeat("a", 300),
	} {
		if got := safeHost(bad); got != "" {
			t.Errorf("safeHost(%q) = %q, want it dropped", bad, got)
		}
	}
}
