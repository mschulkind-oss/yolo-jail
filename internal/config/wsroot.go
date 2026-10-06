package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// wsroot.go confines a workspace config load's reads to the workspace, so the provenance record
// can say which values came from inside it (docs/design/workspace-widening.md §3.1, WW-D17).
//
// THE OPEN DECIDES, NEVER A CHECK BEFORE IT. A `brokered` entry is honored only from the
// workspace's own files: followed blindly, an include or a link would point the repository scope
// at another project's private list. Resolving a path, checking it is inside, and reading it
// afterwards is a race the agent wins: on macos-user the sessions already running share the
// workspace while the next one runs its gate, so `yolo-jail.local.jsonc` can become a link out
// between the check and the read. So each file is opened beneath an os.Root on the workspace,
// which follows a link that stays inside and refuses one that leaves (or is absolute) in the same
// step that yields the bytes, and the file is marked contained on that open alone. A file the
// root refuses is read exactly as before and marked outside: every other key keeps today's
// behavior, and only the `brokered` readers care.

// wsRoot is one workspace load's root.
type wsRoot struct {
	root *os.Root
	// given and resolved are the workspace absolute, as the caller named it and with its links
	// resolved: the top-level files are joined to the first, and an include is resolved at join
	// time (resolveJoin), so a name inside is found against either (macOS's /var is a link to
	// /private/var).
	given, resolved string
	// reads counts the files read, numbering each in merge order (srcFile.seq).
	reads int
}

// nextSeq numbers the next file read, 0 on a nil receiver.
func (w *wsRoot) nextSeq() int {
	if w == nil {
		return 0
	}
	w.reads++
	return w.reads
}

// readAt is where one config file is read: beneath ws (nil for a plain read), and via is the
// include that reached it, "" for a top-level file.
type readAt struct {
	ws  *wsRoot
	via string
}

// openWorkspaceRoot opens the root for a workspace load, nil when the workspace cannot be
// opened, in which case every file reads as before and counts as outside.
func openWorkspaceRoot(workspace string) *wsRoot {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil
	}
	resolved := abs
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = ev
	}
	return &wsRoot{root: r, given: abs, resolved: resolved}
}

func (w *wsRoot) close() {
	if w != nil {
		_ = w.root.Close()
	}
}

// rel is path's name inside the workspace, ok false when it names no file inside.
func (w *wsRoot) rel(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for _, base := range []string{w.given, w.resolved} {
		r, err := filepath.Rel(base, abs)
		if err != nil || r == "." || r == ".." || filepath.IsAbs(r) ||
			strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			continue
		}
		return r, true
	}
	return "", false
}

// read reads path, beneath the root when it names a file inside the workspace. rel is its
// workspace-relative name when the bytes came from inside, "" when they were read any other way.
// On a nil receiver, or any refusal by the root, it is os.ReadFile, whose error is the caller's.
func (w *wsRoot) read(path string) (data []byte, rel string, err error) {
	if w != nil {
		if r, ok := w.rel(path); ok {
			if f, err := w.root.Open(r); err == nil {
				data, err := io.ReadAll(f)
				_ = f.Close()
				if err == nil {
					return data, r, nil
				}
			}
		}
	}
	data, err = os.ReadFile(path)
	return data, "", err
}
