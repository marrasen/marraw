// Package infer is marraw's ML inference foundation: it locates and
// initializes the ONNX Runtime shared library, downloads hash-pinned model
// weights on first use, and caches ready-to-run sessions.
//
// It is deliberately feature-agnostic — AI masks are the first consumer,
// denoise and super-resolution are known future ones. Full-resolution tiled
// inference (overlapping tiles + seam blending) is NOT here yet; it lands
// with the denoise milestone. Consumers that iterate (tiles, batches) must
// check ctx between Run calls — a single forward pass is not interruptible.
package infer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
	"golang.org/x/sync/singleflight"
)

// maxCachedSessions bounds resident ORT sessions; each holds the full model
// weights in RAM (tens to hundreds of MB). Cycling through the three mask
// models fits without eviction.
const maxCachedSessions = 3

// Progress reports download progress: bytes fetched so far and the expected
// total (0 when the server sent no Content-Length and the spec has no size).
type Progress func(done, total int64)

// Manager owns the models directory (<dataDir>/models) and a small LRU of
// live sessions. Methods are safe for concurrent use, but see Session's note
// on eviction before sharing one Session across goroutines.
type Manager struct {
	modelsDir string

	mu       sync.Mutex // guards sessions+order
	sf       singleflight.Group
	sessions map[ModelID]*Session
	order    []ModelID // LRU order, most recently used last
}

func NewManager(modelsDir string) *Manager {
	return &Manager{
		modelsDir: modelsDir,
		sessions:  make(map[ModelID]*Session),
	}
}

// Session returns a ready-to-run session for spec, initializing the runtime,
// downloading + verifying the model file, and loading it as needed.
// Concurrent calls for the same model share one download (singleflight).
//
// Eviction caveat: when more than maxCachedSessions distinct models are in
// flight, the least recently used session is Destroyed. Callers must not
// retain a *Session across long gaps — re-request it per operation.
func (m *Manager) Session(ctx context.Context, spec ModelSpec, progress Progress) (*Session, error) {
	if err := EnsureRuntime(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if s, ok := m.sessions[spec.ID]; ok {
		m.touch(spec.ID)
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	// Download (or find) the weights outside the lock; singleflight keyed by
	// file name so two callers never double-download one model.
	v, err, _ := m.sf.Do(spec.FileName(), func() (any, error) {
		return m.ensureModel(ctx, spec, progress)
	})
	if err != nil {
		return nil, err
	}
	path := v.(string)

	// Victims evicted below are destroyed AFTER m.mu is released: destroy()
	// blocks on any in-flight Run, and holding the manager lock across that
	// drain would stall every other Session caller. The defer is registered
	// before m.mu's so it runs after the unlock (defers are LIFO).
	var victims []*Session
	defer func() {
		for _, v := range victims {
			v.destroy()
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[spec.ID]; ok { // lost a race; someone loaded it
		m.touch(spec.ID)
		return s, nil
	}
	if spec.PreferGPU {
		// One resident GPU session at a time: two heavy DirectML sessions in
		// the same process crash natively in the driver (reproduced with
		// SCUNet + Swin2SR on Arc). Restoration models are used one at a
		// time anyway, so evict any other GPU session first.
		for i := 0; i < len(m.order); {
			id := m.order[i]
			if other := m.sessions[id]; other.OnGPU {
				victims = append(victims, other)
				delete(m.sessions, id)
				m.order = append(m.order[:i], m.order[i+1:]...)
				continue
			}
			i++
		}
	}
	s, err := newSession(spec.ID, path, spec.PreferGPU)
	if err != nil {
		return nil, err
	}
	m.sessions[spec.ID] = s
	m.order = append(m.order, spec.ID)
	for len(m.order) > maxCachedSessions {
		evict := m.order[0]
		m.order = m.order[1:]
		victims = append(victims, m.sessions[evict])
		delete(m.sessions, evict)
	}
	return s, nil
}

// HasModel reports whether spec's weights are already on disk — the check
// consent-gated callers make before a Session call that would download.
func (m *Manager) HasModel(spec ModelSpec) bool {
	_, err := os.Stat(filepath.Join(m.modelsDir, spec.FileName()))
	return err == nil
}

// Dir is the models directory (<dataDir>/models).
func (m *Manager) Dir() string {
	return m.modelsDir
}

// InstalledModel is one model file on disk, spec-known or not (a version bump
// leaves the old file behind until the user deletes it).
type InstalledModel struct {
	FileName string
	Bytes    int64
}

// InstalledModels lists the model files on disk, ignoring in-flight ".part-*"
// download temps. A missing models dir (nothing downloaded yet) is empty, not
// an error.
func (m *Manager) InstalledModels() ([]InstalledModel, error) {
	entries, err := os.ReadDir(m.modelsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []InstalledModel
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".onnx") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // deleted between ReadDir and Stat
		}
		out = append(out, InstalledModel{FileName: e.Name(), Bytes: info.Size()})
	}
	return out, nil
}

// DeleteModel removes one model file by its on-disk name, destroying any live
// session backed by it first. Weights re-download on the feature's next use.
func (m *Manager) DeleteModel(fileName string) error {
	if fileName != filepath.Base(fileName) || !strings.HasSuffix(fileName, ".onnx") {
		return fmt.Errorf("infer: invalid model file name %q", fileName)
	}
	// Evict only the session loaded from THIS exact file, so deleting an
	// orphaned old-version file (isnet-0.onnx) can't tear down the live
	// current-version session (isnet-1.onnx) that shares the id prefix.
	m.mu.Lock()
	var victim *Session
	for i, id := range m.order {
		if s := m.sessions[id]; s.file == fileName {
			victim = s
			delete(m.sessions, id)
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	// destroy() outside m.mu: it blocks until any in-flight Run drains, so the
	// OS-level delete never races a session holding the weights open.
	if victim != nil {
		victim.destroy()
	}
	return os.Remove(filepath.Join(m.modelsDir, fileName))
}

// touch moves id to the most-recently-used end. Caller holds m.mu.
func (m *Manager) touch(id ModelID) {
	for i, o := range m.order {
		if o == id {
			m.order = append(append(m.order[:i:i], m.order[i+1:]...), id)
			return
		}
	}
}

// Session is a loaded model with its I/O signature discovered from the file.
type Session struct {
	ID      ModelID
	Inputs  []ort.InputOutputInfo
	Outputs []ort.InputOutputInfo
	// OnGPU reports whether the platform GPU execution provider is active
	// (PreferGPU asked for it AND the loaded runtime supports it).
	OnGPU bool
	// file is the on-disk name (<id>-<version>.onnx) this session was loaded
	// from, so DeleteModel can evict the session backing the exact file it
	// deletes — not every session whose id shares a prefix.
	file string

	// mu guards sess against destroy() racing an in-flight Run: Run takes it
	// RLock (concurrent passes stay allowed — ORT sessions are Run-safe),
	// destroy() takes it Lock so it blocks until every pass drains before
	// freeing the native session.
	mu   sync.RWMutex
	sess *ort.DynamicAdvancedSession
}

func newSession(id ModelID, path string, preferGPU bool) (*Session, error) {
	inputs, outputs, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return nil, fmt.Errorf("infer: reading model I/O info for %s: %w", id, err)
	}
	inNames := make([]string, len(inputs))
	for i, in := range inputs {
		inNames[i] = in.Name
	}
	outNames := make([]string, len(outputs))
	for i, out := range outputs {
		outNames[i] = out.Name
	}

	if preferGPU {
		if opts, err := gpuSessionOptions(); err == nil {
			s, serr := ort.NewDynamicAdvancedSession(path, inNames, outNames, opts)
			opts.Destroy()
			if serr == nil {
				return &Session{ID: id, Inputs: inputs, Outputs: outputs, OnGPU: true, file: filepath.Base(path), sess: s}, nil
			}
			log.Printf("infer: GPU session for %s failed, falling back to CPU: %v", id, serr)
		} else {
			log.Printf("infer: GPU provider unavailable for %s, using CPU: %v", id, err)
		}
	}
	s, err := ort.NewDynamicAdvancedSession(path, inNames, outNames, nil)
	if err != nil {
		return nil, fmt.Errorf("infer: loading model %s: %w", id, err)
	}
	return &Session{ID: id, Inputs: inputs, Outputs: outputs, file: filepath.Base(path), sess: s}, nil
}

// gpuSessionOptions builds session options with the platform GPU execution
// provider appended: DirectML on Windows (requires the DirectML-enabled ORT
// build — the CPU-only library rejects the provider and the caller falls
// back), CoreML on macOS. Linux has no bundled GPU provider yet.
func gpuSessionOptions() (*ort.SessionOptions, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	var eperr error
	switch runtime.GOOS {
	case "windows":
		eperr = opts.AppendExecutionProviderDirectML(0)
	case "darwin":
		eperr = opts.AppendExecutionProviderCoreML(0)
	default:
		eperr = fmt.Errorf("no GPU execution provider on %s", runtime.GOOS)
	}
	if eperr != nil {
		opts.Destroy()
		return nil, eperr
	}
	return opts, nil
}

// Run executes one forward pass. Inputs must match the model's input order.
// The returned outputs are owned by the caller: Destroy every one of them.
// ctx is checked before the pass starts; a single pass runs to completion
// regardless of cancellation (loop consumers re-check between passes).
func (s *Session) Run(ctx context.Context, inputs ...ort.Value) ([]ort.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// RLock for the whole pass so a concurrent DeleteModel/eviction destroy()
	// cannot free the native session mid-Run (a cgo use-after-free crash).
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sess == nil {
		return nil, fmt.Errorf("infer: session %s was deleted", s.ID)
	}
	outputs := make([]ort.Value, len(s.Outputs))
	if err := s.sess.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("infer: running %s: %w", s.ID, err)
	}
	return outputs, nil
}

// destroy frees the native session. It blocks on any in-flight Run (mu is
// held Lock), so callers must NOT hold Manager.mu when calling it — collect
// victims under Manager.mu, release it, then destroy.
func (s *Session) destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != nil {
		s.sess.Destroy()
		s.sess = nil
	}
}
