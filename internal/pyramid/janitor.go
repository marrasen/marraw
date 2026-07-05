package pyramid

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Janitor bounds the preview cache's disk usage: when the total size
// exceeds cap bytes, the least-recently-served files are deleted until
// usage drops to 80% of cap. The image handler touches mtimes on serve,
// so mtime approximates last access.
type Janitor struct {
	Cache    *Cache
	CapBytes int64
	Interval time.Duration
}

// Run sweeps once immediately, then on every interval tick until ctx ends.
func (j *Janitor) Run(ctx context.Context) {
	interval := j.Interval
	if interval == 0 {
		interval = time.Hour
	}
	j.sweep()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.sweep()
		}
	}
}

func (j *Janitor) sweep() {
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	var files []entry
	var total int64
	filepath.WalkDir(j.Cache.Dir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, entry{path, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= j.CapBytes {
		return
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mtime.Before(files[b].mtime) })
	target := j.CapBytes * 8 / 10
	for _, f := range files {
		if total <= target {
			return
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
