package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/marrasen/marraw/internal/api"
	"github.com/marrasen/marraw/internal/pairing"
)

// pairWaitTimeout bounds one long-poll. Shorter than the request TTL so a
// client that is still interested checks back in and proves it, and short
// enough that a proxy or NAT idle timer does not eat the response.
const pairWaitTimeout = 30 * time.Second

// maxPairBody caps the pairing request body. It carries two short strings; a
// megabyte of JSON from an unauthenticated caller is not a thing we accept.
const maxPairBody = 4 << 10

// registerRemoteRoutes adds the discovery and pairing endpoints.
//
// These are the daemon's only unauthenticated routes with a side effect, and
// they exist because a machine that has never connected has no credential to
// present. Three things keep that honest:
//
//   - They are registered only on a daemon that is actually reachable from
//     another machine. A loopback-only daemon (the default, and every dev
//     build) never serves them at all.
//   - Nothing here grants access. /pair/request only queues a dialog for a
//     human on this machine; the token is minted by System.ResolvePairing,
//     which answers local windows only.
//   - No CORS headers, and a JSON content type is required — together those
//     force a preflight that will fail for any caller in a browser, so only a
//     native client (the Electron main process) can drive the flow. Without
//     the content-type check a plain HTML form could POST here cross-origin.
func registerRemoteRoutes(mux *http.ServeMux, deps *api.Deps, broker *pairing.Broker) {
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"app":     "marraw",
			"name":    deps.DeviceName(r.Context()),
			"version": buildVersion(),
			"pairing": deps.PairingOpen(r.Context()),
		})
	})

	mux.HandleFunc("POST /pair/request", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		if !deps.PairingOpen(r.Context()) {
			http.Error(w, "this computer is not accepting new connections", http.StatusForbidden)
			return
		}

		var body struct {
			Name     string `json:"name"`
			Platform string `json:"platform"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPairBody)).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		req, err := broker.Create(body.Name, body.Platform, r.RemoteAddr)
		if err != nil {
			// A full queue is the expected answer to a flood, not an error
			// worth logging per attempt.
			http.Error(w, "too many pending requests", http.StatusTooManyRequests)
			return
		}
		log.Printf("pairing: %q at %s asked to connect (code %s)", req.Name, req.Addr, req.Code)
		writeJSON(w, http.StatusOK, map[string]any{
			"requestId": req.ID,
			"code":      req.Code,
			"hostName":  deps.DeviceName(r.Context()),
			"expiresAt": req.ExpiresAt.UnixMilli(),
		})
	})

	mux.HandleFunc("GET /pair/wait", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), pairWaitTimeout)
		defer cancel()

		status, token := broker.Wait(ctx, id)
		out := map[string]any{"status": string(status)}
		if status == pairing.StatusApproved {
			out["token"] = token
			out["hostName"] = deps.DeviceName(r.Context())
			log.Printf("pairing: request %s approved", id)
		}
		writeJSON(w, http.StatusOK, out)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
