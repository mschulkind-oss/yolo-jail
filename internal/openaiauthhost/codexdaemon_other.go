//go:build !linux

package openaiauthhost

// processIdentity reads no native identity off Linux: Codex's macOS one comes from
// proc_pidinfo, which this package does not call, so a macOS record is judged by its start time
// alone (checkRecord).
func processIdentity(int) (string, uint64, bool, bool) { return "", 0, false, false }
