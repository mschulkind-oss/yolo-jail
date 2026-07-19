package prune

import (
	"testing"
)

func TestFmtBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1023:       "1023 B",
		1024:       "1.0 KiB",
		1536:       "1.5 KiB",
		1500000000: "1.4 GiB",
		1 << 40:    "1.0 TiB",
		1 << 50:    "1024.0 TiB", // capped at TiB
	}
	for in, want := range cases {
		if got := FmtBytes(in); got != want {
			t.Errorf("FmtBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
