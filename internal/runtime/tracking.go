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
// path — the caller resolves it; the FS side (mkdir of CONTAINER_DIR) is
// ensured here.
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
// absent/empty. The inspect fallback stays in the caller since it execs the
// runtime.
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

// SourceState is how one source of running jails answered "what is running"
// (named-setups.md §12.1): a technology's listing, kept per source so one that did not
// answer is never read as "no jails" (D11).
type SourceState string

const (
	// SourceListed: asked, and it answered; its Names are every yolo jail it runs.
	SourceListed SourceState = "listed"
	// SourceNotInstalled: its binary is not on this machine, so it runs nothing.
	SourceNotInstalled SourceState = "not-installed"
	// SourceUnreachable: installed, and the ask failed or was never made. Its jails are unknown.
	SourceUnreachable SourceState = "unreachable"
	// SourceTimedOut: installed, asked, and no answer came within its budget. Unknown too.
	SourceTimedOut SourceState = "timed-out"
)

// SourceAnswer is one source's answer. Confinement is the setup vocabulary
// ("podman", "apple-container"); Names holds the jails it listed, meaningful only when
// State is SourceListed.
type SourceAnswer struct {
	Confinement string
	State       SourceState
	Names       map[string]struct{}
}

// PruneStaleTrackingFiles removes the tracking files under CONTAINER_DIR that the
// answers prove belong to no running jail, and returns the names removed (in
// directory order). A missing CONTAINER_DIR is a no-op.
//
// answers must hold one entry per container source of this OS (named-setups-plan.md
// SUP-D22): a tracking file names no technology, so it can be judged gone only when
// EVERY container source answered, each either listed or not installed, and none
// listed it (SU-D10). One source that did not answer keeps every file: its jails are
// unknown, and a file pruned here is a live jail's record lost — the Mac defect where
// a Podman-only `yolo ps` deleted every running Apple Container jail's file.
func PruneStaleTrackingFiles(answers []SourceAnswer) []string {
	running := map[string]struct{}{}
	for _, a := range answers {
		switch a.State {
		case SourceListed:
			for n := range a.Names {
				running[n] = struct{}{}
			}
		case SourceNotInstalled:
		default:
			return nil
		}
	}
	dir := paths.ContainerDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if _, live := running[e.Name()]; live {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed = append(removed, e.Name())
		}
	}
	return removed
}

// trimTrailingSpace strips surrounding ASCII whitespace from the tracking-file
// contents; only trailing matters in practice since the file is "<path>\n".
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
	// Also strip leading whitespace for a full strip.
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
