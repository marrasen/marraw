package fingerprint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAFingerprintFollowsTheContentNotTheName(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	big := bytes.Repeat([]byte("raw header and pixels "), 20000) // 440 KB
	a, err := Of(write("DSC0001.ARW", big))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, Version) {
		t.Fatalf("fingerprint %q lacks its version", a)
	}
	if b, _ := Of(write("Renamed.ARW", big)); b != a {
		t.Fatalf("a copy under another name gave %q, want %q", b, a)
	}
	// The same size and header, different pixels at the end, as two
	// uncompressed RAWs of one burst.
	other := bytes.Clone(big)
	other[len(other)-10] ^= 0xff
	if c, _ := Of(write("DSC0002.ARW", other)); c == a {
		t.Fatal("two files differing only at the end gave one fingerprint")
	}
	// Small files, read whole.
	small1, _ := Of(write("s1", []byte("tiny")))
	small2, _ := Of(write("s2", []byte("tinz")))
	if small1 == small2 {
		t.Fatal("two different small files gave one fingerprint")
	}
	mid := bytes.Repeat([]byte{7}, span+100)
	m1, _ := Of(write("m1", mid))
	mid[span+50] = 8
	if m2, _ := Of(write("m2", mid)); m1 == m2 {
		t.Fatal("a file between one and two spans long was not read whole")
	}
}
