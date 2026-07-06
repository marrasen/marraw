package sidecar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadAbsentReturnsNil(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "IMG1.ARW")
	f, err := Read(raw)
	if err != nil {
		t.Fatalf("Read absent: %v", err)
	}
	if f != nil {
		t.Fatalf("expected nil for absent sidecar, got %+v", f)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "IMG1.ARW")
	in := Build("IMG1.ARW", 4242, 4, -1, `{"expEV":0.5}`, 1700000000000)
	if err := Write(raw, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(PathFor(raw)); err != nil {
		t.Fatalf("sidecar not at expected path: %v", err)
	}

	got, err := Read(raw)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.V != Version || got.File != "IMG1.ARW" || got.FileSize != 4242 ||
		got.Rating != 4 || got.Flag != -1 || got.UpdatedAt != 1700000000000 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// The edit payload is pretty-printed on write; compare it semantically
	// (the import path re-parses it, so indentation is irrelevant).
	var compact map[string]float64
	if err := json.Unmarshal(got.Edit, &compact); err != nil {
		t.Fatalf("edit payload not valid JSON: %v", err)
	}
	if compact["expEV"] != 0.5 {
		t.Fatalf("edit payload mismatch: %s", got.Edit)
	}
}

func TestBuildNeutralOmitsEdit(t *testing.T) {
	f := Build("IMG1.ARW", 1, 0, 0, "", 5)
	if f.Edit != nil {
		t.Fatalf("neutral edit should be nil, got %s", f.Edit)
	}
	b, _ := json.Marshal(f)
	if got := string(b); containsKey(got, `"edit"`) || containsKey(got, `"rating"`) || containsKey(got, `"flag"`) {
		t.Fatalf("zero-value intent fields should be omitted: %s", got)
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "IMG1.ARW")
	if err := Write(raw, Build("IMG1.ARW", 1, 1, 0, "", 1)); err != nil {
		t.Fatal(err)
	}
	if err := Write(raw, Build("IMG1.ARW", 1, 5, 0, "", 2)); err != nil {
		t.Fatal(err)
	}
	got, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rating != 5 || got.UpdatedAt != 2 {
		t.Fatalf("overwrite lost: %+v", got)
	}
	// No stray temp files left in the directory.
	ents, _ := os.ReadDir(filepath.Dir(raw))
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}

func TestReadRejectsMissingVersion(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "IMG1.ARW")
	if err := os.WriteFile(PathFor(raw), []byte(`{"file":"IMG1.ARW"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(raw); err == nil {
		t.Fatal("expected error for sidecar without version")
	}
}

func containsKey(s, key string) bool {
	for i := 0; i+len(key) <= len(s); i++ {
		if s[i:i+len(key)] == key {
			return true
		}
	}
	return false
}
