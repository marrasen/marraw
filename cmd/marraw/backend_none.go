//go:build nobackend

package main

import (
	"context"
	"errors"
)

// startBackend is not built in: a nobackend build only connects to another
// marraw, with -connect, and needs no C compiler for LibRaw.
func startBackend(context.Context, string, string) (string, func(), error) {
	return "", nil, errors.New("this build has no backend of its own: give -connect host:port and -token")
}
