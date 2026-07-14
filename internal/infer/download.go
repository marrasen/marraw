package infer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// downloadClient has no overall timeout: model files are large and progress
// is observable. Stalls are cut by the caller's ctx.
var downloadClient = &http.Client{}

// ensureModel returns the local path of spec's weights, downloading and
// SHA-256-verifying them on first use. The hash is checked only at download
// time — an existing file in the models dir is trusted (it is app-owned data;
// re-hashing 100+ MB on every session open buys nothing).
func (m *Manager) ensureModel(ctx context.Context, spec ModelSpec, progress Progress) (string, error) {
	path := filepath.Join(m.modelsDir, spec.FileName())
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if spec.URL == "" {
		return "", fmt.Errorf("infer: model %s not present at %s and spec has no URL", spec.ID, path)
	}
	if err := os.MkdirAll(m.modelsDir, 0o755); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("infer: downloading %s: %w", spec.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("infer: downloading %s: HTTP %s", spec.ID, resp.Status)
	}
	total := spec.Bytes
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	// Stream to a temp file next to the final path (same volume → atomic
	// rename), hashing as we go.
	tmp, err := os.CreateTemp(m.modelsDir, spec.FileName()+".part-*")
	if err != nil {
		return "", err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name()) // no-op after successful rename
	}()

	hash := sha256.New()
	var done int64
	lastReport := time.Time{}
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return "", werr
			}
			hash.Write(buf[:n])
			done += int64(n)
			if progress != nil && (time.Since(lastReport) > 100*time.Millisecond || done == total) {
				lastReport = time.Now()
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fmt.Errorf("infer: downloading %s: %w", spec.ID, rerr)
		}
	}

	if got := hex.EncodeToString(hash.Sum(nil)); got != spec.SHA256 {
		return "", fmt.Errorf("infer: %s download hash mismatch: got %s want %s", spec.ID, got, spec.SHA256)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	if progress != nil {
		progress(done, done)
	}
	return path, nil
}
