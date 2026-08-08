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

// A nil proc means the entry is a reservation: some goroutine has claimed
// this photo's slot and is opening the file under e.mu. It never outlives
// its opener — the last reference to an entry that failed to open removes
// it — so anything walking the map still has to tolerate seeing one.
type handleEntry struct {
	mu      sync.Mutex // serializes use of proc, and covers opening it
	proc    *libraw.Processor
	refs    int
	lastUse time.Time
}

func NewHandleCache(max int) *HandleCache {
	return &HandleCache{max: max, entries: make(map[int64]*handleEntry)}
}

// Acquire returns the photo's processor (opening the file on first use) and
// a release func. The processor is exclusively held until release is called.
//
// The open happens under the entry's own lock, never hc.mu. hc.mu is the lock
// every photo's Acquire and release contend for, and opening a RAW reads from
// the disk it lives on — on a spun-down drive or a stalled network share that
// is seconds. Holding the cache-global lock across it would stall culling
// through photos that are already open and have nothing to do with the file
// being waited on.
func (hc *HandleCache) Acquire(photoID int64, path string) (*libraw.Processor, func(), error) {
	hc.mu.Lock()
	e, ok := hc.entries[photoID]
	if !ok {
		e = &handleEntry{} // a reservation; the processor arrives below
		hc.entries[photoID] = e
	}
	e.refs++
	hc.mu.Unlock()

	e.mu.Lock() // may block while another request uses this handle
	if e.proc == nil {
		proc, err := libraw.New()
		if err == nil {
			if err = proc.Open(path); err != nil {
				proc.Close()
			}
		}
		if err != nil {
			e.mu.Unlock()
			hc.drop(photoID, e)
			return nil, nil, err
		}
		e.proc = proc
	}
	release := func() {
		e.mu.Unlock()
		hc.drop(photoID, e)
	}
	return e.proc, release, nil
}

// drop releases one reference. An entry still carrying no processor once its
// last reference goes is one whose open failed for everybody waiting on it:
// remove it, so the next Acquire starts over instead of inheriting a
// reservation that will never be filled.
func (hc *HandleCache) drop(photoID int64, e *handleEntry) {
	hc.mu.Lock()
	e.refs--
	e.lastUse = time.Now()
	if e.proc == nil && e.refs == 0 {
		if cur, ok := hc.entries[photoID]; ok && cur == e {
			delete(hc.entries, photoID)
		}
	}
	hc.evictLocked()
	hc.mu.Unlock()
}

// Invalidate drops the handle for a photo (file changed on disk).
func (hc *HandleCache) Invalidate(photoID int64) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if e, ok := hc.entries[photoID]; ok && e.refs == 0 {
		delete(hc.entries, photoID)
		if e.proc != nil {
			e.proc.Close()
		}
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
		if oldest.proc != nil {
			oldest.proc.Close()
		}
	}
}

// Close releases every idle handle. Handles still held are left to their
// holder: closing a LibRaw processor another goroutine is inside a C call with
// frees memory that call is still reading, and a use-after-free in native code
// at shutdown reads as a crash on exit rather than the clean stop it was.
// Whatever is still in use goes away with the process moments later anyway.
func (hc *HandleCache) Close() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for id, e := range hc.entries {
		if e.refs > 0 {
			continue
		}
		delete(hc.entries, id)
		if e.proc != nil {
			e.proc.Close()
		}
	}
}
