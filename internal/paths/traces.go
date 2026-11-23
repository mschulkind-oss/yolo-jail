package paths

import "path/filepath"

// TracesDir is the machine-wide host trace root, not a workspace's guest-writable logs.
// Naming it does not create storage or select a recording gate.
func TracesDir() string {
	return filepath.Join(LogsDir(), "traces")
}

// TraceSourcesDir names host-private trace source bookkeeping outside logs and retired service state.
// Naming the root performs no I/O; recording code establishes it only when tracing is enabled.
func TraceSourcesDir() string {
	return filepath.Join(GlobalStorage(), "trace-sources")
}
