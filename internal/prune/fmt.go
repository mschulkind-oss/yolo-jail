package prune

import "fmt"

// fmtBytesUnits are the human-readable byte-count units, frozen from
// prune_cmd._fmt_bytes.
var fmtBytesUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// FmtBytes renders a byte count human-readably: 1536 → "1.5 KiB",
// 1_500_000_000 → "1.4 GiB". Mirrors prune_cmd._fmt_bytes exactly — divide by
// 1024 until below 1024 (capped at TiB); at i==0 print the integer count, else
// one decimal place (%.1f, round-half-to-even, matching Python's format spec).
func FmtBytes(n int64) string {
	size := float64(n)
	i := 0
	for size >= 1024 && i < len(fmtBytesUnits)-1 {
		size /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", int64(size), fmtBytesUnits[i])
	}
	return fmt.Sprintf("%.1f %s", size, fmtBytesUnits[i])
}
