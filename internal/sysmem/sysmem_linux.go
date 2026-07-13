//go:build linux

package sysmem

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strconv"
)

// Query reads the current physical-memory snapshot from /proc/meminfo.
// MemAvailable is the kernel's own estimate of allocatable-without-swapping
// memory (present since 3.14), the same semantic as Windows' AvailPhys.
func Query() (Stats, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := bytes.Fields(sc.Bytes())
		if len(f) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(string(f[1]), 10, 64)
		if err != nil {
			continue
		}
		switch string(f[0]) {
		case "MemTotal:":
			st.TotalPhys = kb << 10
		case "MemAvailable:":
			st.AvailPhys = kb << 10
		}
	}
	if st.TotalPhys == 0 || st.AvailPhys == 0 {
		return Stats{}, errors.New("sysmem: MemTotal/MemAvailable not found in /proc/meminfo")
	}
	return st, nil
}
