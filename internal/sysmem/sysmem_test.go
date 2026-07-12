//go:build windows

package sysmem

import "testing"

func TestQuery(t *testing.T) {
	st, err := Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if st.TotalPhys == 0 {
		t.Fatal("TotalPhys is zero")
	}
	if st.AvailPhys == 0 || st.AvailPhys > st.TotalPhys {
		t.Fatalf("AvailPhys %d out of range (total %d)", st.AvailPhys, st.TotalPhys)
	}
}
