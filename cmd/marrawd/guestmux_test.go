package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marrasen/marraw/internal/guestui"
	"github.com/marrasen/marraw/internal/imghttp"
)

// denyAll stands in for the guest-only authorizer refusing an owner token. It
// also keeps these tests off the database: every handler below answers 403
// before it looks anything up, and 403 is not 404, which is the distinction
// both tests turn on.
func denyAll(string) (imghttp.Access, bool) { return imghttp.Access{}, false }

// The funnel publishes a whole port, so the list of routes on that port IS the
// public attack surface. These are the ones that must not be on it: an owner
// credential is the only thing that can use any of them, and an owner
// credential has no business arriving from the public internet.
func TestFunnelListenerDoesNotServeOwnerRoutes(t *testing.T) {
	mux := newGuestMux(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		&imghttp.Handler{Authorize: denyAll},
		&imghttp.Downloads{Authorize: denyAll},
		&guestui.Handler{},
	)

	for _, path := range []string{
		"/authz",
		"/healthz",
		"/wm/0123456789abcdef.png",
		"/fonts/inter",
		// Pairing and discovery (remote.go). Letting a stranger open a pairing
		// request would put a dialog on the owner's screen from the internet.
		"/hello",
		"/pair/request",
		"/pair/withdraw",
		"/pair/wait",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s answered %d on the funnel listener, want 404", method, path, w.Code)
			}
		}
	}
}

// The routes that ARE published have to stay published, or every share link
// breaks the moment the funnel comes up.
func TestFunnelListenerServesTheShareRoutes(t *testing.T) {
	reachedWS := false
	mux := newGuestMux(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedWS = true }),
		&imghttp.Handler{Authorize: denyAll},
		&imghttp.Downloads{Authorize: denyAll},
		// Accept any token: a refused one answers 404, which is the same code
		// an unrouted path gives and would make this assertion vacuous.
		&guestui.Handler{TokenValid: func(string) bool { return true }},
	)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if !reachedWS {
		t.Error("/ws is not routed on the funnel listener")
	}

	// Routed (not 404) is the claim here; the handlers' own auth is tested in
	// their own packages.
	for _, path := range []string{
		"/s/0123456789abcdef0123456789abcdef",
		"/s/0123456789abcdef0123456789abcdef/",
		"/img/1/512",
		"/img/1/tile/0/0",
		"/dl/1",
		"/dl.zip",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusNotFound {
			t.Errorf("%s is not routed on the funnel listener", path)
		}
	}
}
