package infer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	initOnce sync.Once
	initErr  error
)

// EnsureRuntime locates the ONNX Runtime shared library and initializes the
// ORT environment exactly once for the process lifetime. Safe to call from
// anywhere; every entry point that touches ORT goes through it.
func EnsureRuntime() error {
	initOnce.Do(func() {
		path, err := locateRuntime()
		if err != nil {
			initErr = err
			return
		}
		ort.SetSharedLibraryPath(path)
		if err := ort.InitializeEnvironment(); err != nil {
			initErr = fmt.Errorf("infer: initializing ONNX Runtime from %s: %w", path, err)
		}
	})
	return initErr
}

// locateRuntime resolves the ORT shared library, in order:
//  1. MARRAW_ORT_LIB — explicit file path (tests, unusual installs)
//  2. the executable's directory — packaged app (electron-builder ships the
//     library next to marrawd)
//  3. <repo root>/third_party/onnxruntime/lib — dev checkouts (npm run
//     setup:ort), found by walking up from the working directory to go.mod
func locateRuntime() (string, error) {
	if p := os.Getenv("MARRAW_ORT_LIB"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("infer: MARRAW_ORT_LIB=%s: %w", p, err)
		}
		return p, nil
	}

	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if root := repoRoot(); root != "" {
		dirs = append(dirs, filepath.Join(root, "third_party", "onnxruntime", "lib"))
	}

	for _, dir := range dirs {
		for _, pattern := range runtimeLibPatterns() {
			matches, _ := filepath.Glob(filepath.Join(dir, pattern))
			if len(matches) > 0 {
				return matches[0], nil
			}
		}
	}
	return "", fmt.Errorf("infer: ONNX Runtime library not found (searched %v); "+
		"run `npm run setup:ort` or set MARRAW_ORT_LIB", dirs)
}

// runtimeLibPatterns lists candidate file names per platform, exact name
// first so versioned symlink targets don't shadow the canonical one.
func runtimeLibPatterns() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"onnxruntime.dll"}
	case "darwin":
		return []string{"libonnxruntime.dylib", "libonnxruntime.*.dylib"}
	default:
		return []string{"libonnxruntime.so", "libonnxruntime.so.*"}
	}
}

// repoRoot walks up from the working directory looking for go.mod. Returns
// "" when not inside a checkout (packaged installs).
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
