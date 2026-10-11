package main

import "testing"

func TestSplitNameDate(t *testing.T) {
	for _, c := range []struct {
		name, rest, day string
	}{
		{"2019-11-08 Jenny 27 och Safety Dance", "Jenny 27 och Safety Dance", "8 Nov 2019"},
		{"2026_07_11 Gedells Bröllop", "Gedells Bröllop", "11 Jul 2026"},
		{"2021.01.31 - Musikvideo Jacko", "Musikvideo Jacko", "31 Jan 2021"},
		{"20190930_Sebastians födelsedagsfest", "Sebastians födelsedagsfest", "30 Sep 2019"},
		{"2019-10-28 GHC-MODO", "GHC-MODO", "28 Oct 2019"},
		// Not dates: shown whole.
		{"2019-13-08 Thirteenth month", "", ""},
		{"2019-02-30 No such day", "", ""},
		{"2019-11-08", "", ""},
		{"Loose photos", "", ""},
		{"Phone", "", ""},
		{"1234 Main Street", "", ""},
		{"20191108Jenny", "", ""},
	} {
		d, rest, ok := splitNameDate(c.name)
		if c.day == "" {
			if ok {
				t.Errorf("%q: read %v and %q, want the name whole", c.name, d, rest)
			}
			continue
		}
		if !ok || rest != c.rest || dayLabel(d) != c.day {
			t.Errorf("%q: got %q, %q, %v; want %q, %q", c.name, dayLabel(d), rest, ok, c.day, c.rest)
		}
	}
}

func TestShootLabels(t *testing.T) {
	for _, c := range []struct {
		name       string
		earliest   int64
		count      int
		title, sub string
	}{
		{"2019-11-08 Jenny 27", 0, 422, "Jenny 27", "8 Nov 2019 · 422"},
		{"Phone", 0, 1, "Phone", "1 photo"},
		{"Loose photos", 0, 278, "Loose photos", "278 photos"},
		{"Unnamed", 0, 0, "Unnamed", ""},
	} {
		title, sub := shootLabels(c.name, c.earliest, c.count)
		if title != c.title || sub != c.sub {
			t.Errorf("%q: got %q, %q; want %q, %q", c.name, title, sub, c.title, c.sub)
		}
	}
}
