package decode

import (
	"sync"
	"time"

	"github.com/marrasen/marraw/internal/libraw"
)

// HandleCache keeps a small LRU of open, unpacked LibRaw handles so the
// interactive edit loop can re-process a photo without re-reading the file.
// Unpacked sensor data for a 42 MP file is ~100-200 MB, hence the tiny cap.
type HandleCache struct {
	mu      sync.Mutex
	max     int
	entries map[int64]*handleEntry
}

type handleEntry struct {
	mu      sync.Mutex // serializes use of proc
	proc    *libraw.Processor
	refs    int
	lastUse time.Time
}

func NewHandleCache(max int) *HandleCache {
	return &HandleCache{max: max, entries: make(map[int64]*handleEntry)}
}

// Acquire returns the photo's processor (opening the file on first use) and
// a release func. The processor is exclusively held until release is called.
func (hc *HandleCache) Acquire(photoID int64, path string) (*libraw.Processor, func(), error) {
	hc.mu.Lock()
	e, ok := hc.entries[photoID]
	if !ok {
		proc, err := libraw.New()
		if err != nil {
			hc.mu.Unlock()
			return nil, nil, err
		}
		if err := proc.Open(path); err != nil {
			proc.Close()
			hc.mu.Unlock()
			return nil, nil, err
		}
		e = &handleEntry{proc: proc}
		hc.entries[photoID] = e
	}
	e.refs++
	hc.mu.Unlock()

	e.mu.Lock() // may block while another request uses this handle
	release := func() {
		e.mu.Unlock()
		hc.mu.Lock()
		e.refs--
		e.lastUse = time.Now()
		hc.evictLocked()
		hc.mu.Unlock()
	}
	return e.proc, release, nil
}

// Invalidate drops the handle for a photo (file changed on disk).
func (hc *HandleCache) Invalidate(photoID int64) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if e, ok := hc.entries[photoID]; ok && e.refs == 0 {
		delete(hc.entries, photoID)
		e.proc.Close()
	}
}

func (hc *HandleCache) evictLocked() {
	for len(hc.entries) > hc.max {
		var oldestID int64
		var oldest *handleEntry
		for id, e := range hc.entries {
			if e.refs > 0 {
				continue
			}
			if oldest == nil || e.lastUse.Before(oldest.lastUse) {
				oldest, oldestID = e, id
			}
		}
		if oldest == nil {
			return // everything in use; try again on next release
		}
		delete(hc.entries, oldestID)
		oldest.proc.Close()
	}
}

func (hc *HandleCache) Close() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for id, e := range hc.entries {
		delete(hc.entries, id)
		e.proc.Close()
	}
}
