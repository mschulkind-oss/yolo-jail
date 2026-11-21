package paths

import "path/filepath"

// TracesDir is the machine-wide host trace root, not a workspace's guest-writable logs.
// Naming it does not create storage or select a recording gate.
func TracesDir() string {
	return filepath.Join(LogsDir(), "traces")
}
