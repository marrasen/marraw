//go:build !nobackend

package main

import (
	"context"
	"time"

	"github.com/marrasen/marraw/internal/daemon"
)

// startBackend runs marraw's backend in this process, on loopback, and
// returns where it listens and how to stop it.
func startBackend(ctx context.Context, dataDir, token string) (string, func(), error) {
	daemon.LimitMemory()
	d, err := daemon.Start(ctx, daemon.Options{Listen: "127.0.0.1", DataDir: dataDir, CacheCapGB: 20, Token: token})
	if err != nil {
		return "", nil, err
	}
	return d.Addr, func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Close(sctx)
	}, nil
}
