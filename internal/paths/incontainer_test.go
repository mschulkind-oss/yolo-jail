package paths

import "testing"

func TestInsideContainerReadsEitherMarker(t *testing.T) {
	for _, tc := range []struct {
		present []string
		want    bool
	}{
		{nil, false},
		{[]string{"/run/.containerenv"}, true},
		{[]string{"/.dockerenv"}, true},
		{[]string{"/etc/hostname"}, false},
	} {
		got := InsideContainer(func(p string) bool {
			for _, q := range tc.present {
				if p == q {
					return true
				}
			}
			return false
		})
		if got != tc.want {
			t.Errorf("present %v: InsideContainer = %v, want %v", tc.present, got, tc.want)
		}
	}
}
