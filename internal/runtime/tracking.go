package runtime

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// WriteContainerTracking writes a tracking file under CONTAINER_DIR named for
// the container, holding the resolved workspace path + a trailing newline, so
// `yolo ps` can map containers→workspaces.
// workspaceResolved must already be the resolved (absolute, symlinks-followed)
// path — the caller resolves it the way Python's Path.resolve() does; the FS
// side (mkdir of CONTAINER_DIR) is ensured here.
func WriteContainerTracking(name, workspaceResolved string) error {
	dir := paths.ContainerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(workspaceResolved+"\n"), 0o644)
}

// CleanupContainerTracking removes a container's tracking file (missing_ok).
func CleanupContainerTracking(name string) {
	_ = os.Remove(filepath.Join(paths.ContainerDir(), name))
}

// ReadContainerWorkspace returns the workspace recorded in a container's
// tracking file (trailing whitespace trimmed), or ("", false) when the file is
// absent/empty.
// first; the inspect fallback stays in the caller since it execs the runtime).
func ReadContainerWorkspace(name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(paths.ContainerDir(), name))
	if err != nil {
		return "", false
	}
	ws := trimTrailingSpace(string(data))
	if ws == "" {
		return "", false
	}
	return ws, true
}

// PruneStaleTrackingFiles removes every tracking file under CONTAINER_DIR whose
// name is not in runningNames.
// iterate CONTAINER_DIR, unlink any entry not currently running. A missing
// CONTAINER_DIR is a no-op. Returns the names removed (in directory order).
func PruneStaleTrackingFiles(runningNames map[string]struct{}) []string {
	dir := paths.ContainerDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if _, live := runningNames[e.Name()]; live {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed = append(removed, e.Name())
		}
	}
	return removed
}

// trimTrailingSpace strips trailing ASCII whitespace (matches the .strip() the
// Python readers apply to the tracking-file contents; only trailing matters
// since the file is "<path>\n").
func trimTrailingSpace(s string) string {
	end := len(s)
	for end > 0 {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			end--
			continue
		}
		break
	}
	// Also strip leading whitespace to match Python str.strip() fully.
	start := 0
	for start < end {
		c := s[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			start++
			continue
		}
		break
	}
	return s[start:end]
}
