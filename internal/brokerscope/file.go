package brokerscope

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// file.go is the per-launch scope file (BB-D32): once the config-change gate passes, the
// fresh launch writes the approved scope, and what the user's widening entry adds, to
// broker/<source>/scope/<launch-id>.json and only then spawns the daemon, handing it the
// file's name. The daemon reads that file alone — never the remotes, never the approval
// record, never the user config — so no host pre-approval, user config edit or other
// session's launch changes a running daemon's scope.
//
// Keyed by a random launch id, never the workspace or the container name: on macos-user
// two terminals in one workspace are two concurrent sessions sharing both, and one
// session's approval must not replace the scope under the other's daemon.

// FileVersion is the scope file's schema version.
const FileVersion = 1

// File is one launch's scope file.
type File struct {
	Version  int    `json:"version"`
	Source   string `json:"source"`
	LaunchID string `json:"launch_id"`
	// PID is the launching yolo process; the file is collected once that process is known
	// gone (Sweep), never by age.
	PID       int    `json:"pid"`
	Container string `json:"container"`
	Workspace string `json:"workspace"`
	// Repos is the approved scope: the workspace's remotes on the forge, as a human
	// approved them in this launch's config-change gate.
	Repos []string `json:"repos"`
	// Widened is what a user-scope widening entry added for this workspace beyond Repos
	// (OQ-BB6, BB-D33): the launch reads it from the user config (config.BrokeredWidening),
	// never from the workspace, and needs no approval for it. As OQ-BB9 ruled (A), its
	// repositories join the scope for every set, which is how the broker reads them.
	Widened []string `json:"widened,omitempty"`
}

// NewLaunchID draws a random launch id.
func NewLaunchID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Write writes a launch's scope file, 0600 in 0700 directories, atomically, and returns its
// path.
func Write(f File) (string, error) {
	if f.Source == "" || f.LaunchID == "" || strings.ContainsAny(f.LaunchID+f.Source, "/\\.") {
		return "", fmt.Errorf("scope file: bad source %q or launch id %q", f.Source, f.LaunchID)
	}
	f.Version = FileVersion
	if f.Repos == nil {
		f.Repos = []string{}
	}
	path := paths.BrokerScopeFile(f.Source, f.LaunchID)
	dir := filepath.Dir(path)
	for _, d := range []string{paths.BrokerDir(), paths.BrokerSourceDir(f.Source), dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		_ = os.Chmod(d, 0o700)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".scope-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// ReadFile reads a scope file, refusing a symlink or a file of another version.
func ReadFile(path string) (File, error) {
	var f File
	fi, err := os.Lstat(path)
	if err != nil {
		return f, err
	}
	if !fi.Mode().IsRegular() {
		return f, fmt.Errorf("scope file %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("scope file %s: %w", path, err)
	}
	if f.Version != FileVersion {
		return f, fmt.Errorf("scope file %s has version %d, this build reads %d", path, f.Version, FileVersion)
	}
	return f, nil
}

// Sweep removes the scope files of a source whose launching process is KNOWN gone: kill(pid,
// 0) answers ESRCH. Any other answer — alive, or not ours to signal — keeps the file, so a
// live launch's file is never collected and a file is never aged out.
func Sweep(source string) (removed []string) {
	dir := filepath.Dir(paths.BrokerScopeFile(source, "x"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		f, err := ReadFile(p)
		if err != nil || f.PID <= 0 {
			continue
		}
		if errors.Is(syscall.Kill(f.PID, 0), syscall.ESRCH) {
			if os.Remove(p) == nil {
				removed = append(removed, p)
			}
		}
	}
	return removed
}
