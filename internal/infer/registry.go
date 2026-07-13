package infer

import "fmt"

// ModelID names a model in the registry ("segformer", "isnet", "depth", …).
type ModelID string

// ModelSpec pins one downloadable model: a versioned, hash-verified URL.
// Registered specs must be Apache-2.0 (or deliberately excepted and recorded
// in THIRD_PARTY_NOTICES.md — see design/ml-roadmap.md).
type ModelSpec struct {
	ID      ModelID
	Version string // bump when weights change; part of the cached file name
	URL     string
	SHA256  string // lowercase hex of the .onnx file
	Bytes   int64  // expected size; progress total before headers arrive (0 = unknown)
	License string
	// PreferGPU asks for the platform GPU execution provider (DirectML on
	// Windows, CoreML on macOS) with silent CPU fallback. Set it on
	// restoration-class models only: they are ~100x too slow on CPU, while
	// the mask models are comfortably fast without the GPU warmup cost.
	PreferGPU bool
}

// fileName is the on-disk name under the models dir. Version is baked in so
// a model upgrade never overwrites the file an existing edit references.
func (s ModelSpec) fileName() string {
	return fmt.Sprintf("%s-%s.onnx", s.ID, s.Version)
}

// registry holds the pinned model set. Real entries land with the AI-masks
// milestone; tests construct specs directly.
var registry = map[ModelID]ModelSpec{}

// Register pins a model spec at init time. Duplicate IDs are a programming
// error.
func Register(s ModelSpec) {
	if _, dup := registry[s.ID]; dup {
		panic(fmt.Sprintf("infer: duplicate model registration: %s", s.ID))
	}
	registry[s.ID] = s
}

// Lookup returns the pinned spec for id.
func Lookup(id ModelID) (ModelSpec, bool) {
	s, ok := registry[id]
	return s, ok
}
