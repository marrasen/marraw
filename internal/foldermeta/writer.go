package foldermeta

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer batches the changes to folders' marraw.json: a folder's file is
// written once its changes have rested for Delay, and every waiting change
// is written by Flush, as the program closes. Each write reads the file
// first, as another computer may have changed it, and merges into it, so a
// change waiting here never undoes a newer one made there.
type Writer struct {
	// Delay is how long a folder's changes rest before its file is written.
	Delay time.Duration

	mu      sync.Mutex
	pending map[string][]func(*File)
	timers  map[string]*time.Timer
	// writing serializes the writes, so two never read and write one
	// file at once.
	writing sync.Mutex
}

// NewWriter returns a Writer that writes a folder's file delay after its
// last change.
func NewWriter(delay time.Duration) *Writer {
	return &Writer{Delay: delay, pending: map[string][]func(*File){}, timers: map[string]*time.Timer{}}
}

// Change queues fn to change dir's file: fn is run on the file as it is on
// disk when it is written.
func (w *Writer) Change(dir string, fn func(*File)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[dir] = append(w.pending[dir], fn)
	if t := w.timers[dir]; t != nil {
		t.Reset(w.Delay)
		return
	}
	w.timers[dir] = time.AfterFunc(w.Delay, func() { w.flush(dir) })
}

// Flush writes every folder's waiting changes now, and returns once a
// write a timer started before it has ended too.
func (w *Writer) Flush() {
	w.mu.Lock()
	dirs := make([]string, 0, len(w.pending))
	for d := range w.pending {
		dirs = append(dirs, d)
	}
	w.mu.Unlock()
	for _, d := range dirs {
		w.flush(d)
	}
	w.writing.Lock()
	w.writing.Unlock() //nolint:staticcheck // waits for a write under way
}

// flush writes dir's waiting changes.
func (w *Writer) flush(dir string) {
	w.mu.Lock()
	fns := w.pending[dir]
	delete(w.pending, dir)
	if t := w.timers[dir]; t != nil {
		t.Stop()
		delete(w.timers, dir)
	}
	w.mu.Unlock()
	if len(fns) == 0 {
		return
	}
	w.writing.Lock()
	defer w.writing.Unlock()
	if err := Update(dir, func(f *File) {
		for _, fn := range fns {
			fn(f)
		}
	}); err != nil {
		log.Printf("foldermeta: %s: %v", dir, err)
	}
}

// Update reads dir's file, as it is on disk now, changes it with fn and
// writes it back. A folder gone from the disk is left gone, and a folder
// with no file gets none while there is nothing to keep in it.
func Update(dir string, fn func(*File)) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, err := os.Stat(filepath.Join(dir, Name))
	had := err == nil
	f, err := Read(dir)
	if err != nil {
		// Unreadable as it stands: write nothing over it, which would
		// lose what it holds.
		return err
	}
	fn(f)
	if !had && len(f.Photos) == 0 && len(f.Deleted) == 0 {
		return nil
	}
	return Write(dir, f)
}
