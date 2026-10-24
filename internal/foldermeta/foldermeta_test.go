package foldermeta

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadOfAFolderWithNoFileIsEmpty(t *testing.T) {
	f, err := Read(t.TempDir())
	if err != nil || len(f.Photos) != 0 || len(f.Deleted) != 0 {
		t.Fatalf("Read = %+v, %v; want an empty file", f, err)
	}
}

func TestWriteAndReadBack(t *testing.T) {
	dir := t.TempDir()
	f := Empty()
	f.SetPhoto("A.ARW", Photo{Size: 10, FP: "1:aa", Rating: 3, Flag: 1, Edit: json.RawMessage(`{"expEV":1}`), UpdatedAt: 100})
	if err := Write(dir, f); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Photos["A.ARW"]
	var edit bytes.Buffer
	_ = json.Compact(&edit, p.Edit)
	if p.Rating != 3 || p.Flag != 1 || edit.String() != `{"expEV":1}` || p.FP != "1:aa" || p.UpdatedAt != 100 {
		t.Fatalf("read back %+v", p)
	}
	// Nothing but the file is left in the folder.
	des, _ := os.ReadDir(dir)
	if len(des) != 1 || des[0].Name() != Name {
		t.Fatalf("the folder holds %v, want marraw.json alone", des)
	}
}

func TestTheNewerEntryWinsAMergeAndAFingerprintIsKept(t *testing.T) {
	f := Empty()
	f.SetPhoto("A.ARW", Photo{FP: "1:aa", Size: 10, Rating: 5, UpdatedAt: 200})
	f.SetPhoto("A.ARW", Photo{Rating: 1, UpdatedAt: 100})
	if p := f.Photos["A.ARW"]; p.Rating != 5 {
		t.Fatalf("an older change won: %+v", p)
	}
	f.SetPhoto("A.ARW", Photo{Rating: 2, UpdatedAt: 300})
	if p := f.Photos["A.ARW"]; p.Rating != 2 || p.FP != "1:aa" || p.Size != 10 {
		t.Fatalf("a newer change %+v, want rating 2 with the fingerprint kept", p)
	}
	f.SetFingerprint("B.ARW", 20, "1:bb")
	if p := f.Photos["B.ARW"]; p.FP != "1:bb" || p.HasIntent() || p.UpdatedAt != 0 {
		t.Fatalf("a fingerprint alone made %+v", p)
	}
}

func TestAConflictedCopyMergesPhotoByPhoto(t *testing.T) {
	dir := t.TempDir()
	mine := Empty()
	mine.SetPhoto("A.ARW", Photo{Rating: 5, UpdatedAt: 300})
	mine.SetPhoto("B.ARW", Photo{Rating: 1, UpdatedAt: 100})
	if err := Write(dir, mine); err != nil {
		t.Fatal(err)
	}
	theirs := Empty()
	theirs.SetPhoto("A.ARW", Photo{Rating: 2, UpdatedAt: 200})
	theirs.SetPhoto("B.ARW", Photo{Flag: -1, UpdatedAt: 400})
	theirs.SetPhoto("C.ARW", Photo{Flag: 1, UpdatedAt: 50})
	b, _ := json.Marshal(theirs)
	if err := os.WriteFile(filepath.Join(dir, "marraw (Marcus's conflicted copy 2026-10-12).json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// An old per-photo sidecar is not a conflicted copy.
	if err := os.WriteFile(filepath.Join(dir, "B.ARW.marraw.json"), []byte(`{"v":1,"rating":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a, b, c := f.Photos["A.ARW"], f.Photos["B.ARW"], f.Photos["C.ARW"]; a.Rating != 5 || b.Flag != -1 || b.Rating != 0 || c.Flag != 1 {
		t.Fatalf("merged A %+v, B %+v, C %+v", a, b, c)
	}
}

func TestADeletedPhotoStaysDeletedThroughAMerge(t *testing.T) {
	f := Empty()
	f.SetPhoto("A.ARW", Photo{FP: "1:aa", Rating: 3, UpdatedAt: 100})
	f.Delete("A.ARW", Deleted{FP: "1:aa", Name: "A.ARW", DeletedAt: 200})
	// Another computer's copy still has it, edited before the deletion.
	other := Empty()
	other.SetPhoto("A.ARW", Photo{FP: "1:aa", Rating: 4, UpdatedAt: 150})
	f.merge(other)
	if _, ok := f.Photos["A.ARW"]; ok {
		t.Fatal("a merge brought back a photo deleted after its last change")
	}
	if len(f.Deleted) != 1 || f.Deleted[0].Name != "A.ARW" {
		t.Fatalf("deleted %+v", f.Deleted)
	}
	f.AddDeleted(Deleted{FP: "1:aa", Name: "A.ARW", DeletedAt: 50})
	if len(f.Deleted) != 1 {
		t.Fatal("a deletion was recorded twice")
	}
}

func TestTheWriterBatchesAndMergesWithTheDisk(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(time.Hour)
	w.Change(dir, func(f *File) { f.SetPhoto("A.ARW", Photo{Rating: 1, UpdatedAt: 100}) })
	w.Change(dir, func(f *File) { f.SetPhoto("A.ARW", Photo{Rating: 2, UpdatedAt: 200}) })
	if _, err := os.Stat(filepath.Join(dir, Name)); err == nil {
		t.Fatal("the file was written before the changes rested")
	}
	// Meanwhile another computer wrote a newer change to another photo.
	disk := Empty()
	disk.SetPhoto("B.ARW", Photo{Flag: 1, UpdatedAt: 300})
	if err := Write(dir, disk); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	f, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Photos["A.ARW"].Rating != 2 || f.Photos["B.ARW"].Flag != 1 {
		t.Fatalf("after the flush %+v", f.Photos)
	}
}

func TestAnUnreadableFileIsNotWrittenOver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Update(dir, func(f *File) { f.SetPhoto("A.ARW", Photo{Rating: 1, UpdatedAt: 1}) }); err == nil {
		t.Fatal("Update wrote over a file it could not read")
	}
	b, _ := os.ReadFile(filepath.Join(dir, Name))
	if string(b) != "{not json" {
		t.Fatalf("the file became %q", b)
	}
}

func TestAFolderGoneIsLeftGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	if err := Update(dir, func(f *File) { f.SetPhoto("A.ARW", Photo{Rating: 1, UpdatedAt: 1}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("writing a gone folder's file made the folder")
	}
}
