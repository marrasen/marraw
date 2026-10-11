package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// nameDated is a date at the start of a folder's name, as photographers
// name their shoots: 2019-11-08, 2019_11_08, 2019.11.08 or 20191108,
// then a space, a dash, an underscore or a dot before the rest.
var nameDated = regexp.MustCompile(`^(\d{4})[-_.]?(\d{2})[-_.]?(\d{2})(?:[\s_.-]+|$)`)

// splitNameDate parts a folder's name into the date it starts with and
// the rest, for the sidebar to show the rest as the name and the date
// under it. ok is false where the name starts with no real date, or is
// nothing but one, and the name is best shown whole.
func splitNameDate(name string) (date time.Time, rest string, ok bool) {
	m := nameDated.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, name, false
	}
	// Atoi, not Sscan, which reads 08 as octal.
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.Local)
	// A month or day out of range rolls over in time.Date: not a date.
	if date.Year() != y || int(date.Month()) != mo || date.Day() != d || y < 1900 {
		return time.Time{}, name, false
	}
	rest = strings.TrimSpace(name[len(m[0]):])
	if rest == "" {
		return time.Time{}, name, false
	}
	return date, rest, true
}

// dayLabel is a date as the sidebar shows it: 8 Nov 2019.
func dayLabel(t time.Time) string { return t.Format("2 Jan 2006") }

// shootLabels are a shoot's name as the sidebar shows it, and the line
// under it: its date, from its name where it starts with one, or else
// from its earliest photo, and how many photos it holds.
func shootLabels(name string, earliest int64, count int) (title, sub string) {
	title = name
	var day string
	if d, rest, ok := splitNameDate(name); ok {
		title, day = rest, dayLabel(d)
	} else if earliest > 0 {
		day = dayLabel(time.Unix(earliest, 0))
	}
	photos := ""
	switch {
	case count == 1:
		photos = "1 photo"
	case count > 1:
		photos = fmt.Sprintf("%d photos", count)
	}
	switch {
	case day != "" && count > 0:
		sub = fmt.Sprintf("%s · %d", day, count)
	case day != "":
		sub = day
	default:
		sub = photos
	}
	return title, sub
}
