// Package stateguard notices that a running jail's workspace state directory (`<workspace>/.yolo`)
// is no longer the one it started on, and copies the agents' homes out of the running jail before
// it stops when it is not (docs/design/workspace-state-safety.md §3.4 and §3.5, SD-D5 and SD-D13).
//
// # The identity stamp
//
// An IDENTITY STAMP (a term the design coined) is the device and inode of `.yolo`, of its
// `durable/` directory, and of each host directory the jail's home is bound from, taken when the
// jail's keeper starts. A check is an lstat of the same names compared against it. A name that is
// gone, or that now names another inode, is a mismatch: the jail still holds the old directory
// through its bind mounts, and nothing on the host reaches it by path any more.
//
// # Where the stamp is kept, and why not under `.yolo`
//
// The design suggested the boot record under `.yolo` (`config-boot.json`). A record kept inside
// the directory it describes moves with it: after the very `mv` this exists to notice, the
// workspace has no record, which reads the same as a jail that never wrote one. So the stamp is a
// field of the keeper's start record, which lives in yolo's own state directory, outside the
// workspace and outside every jail's reach, and which every attach already reads.
//
// # When it is checked
//
// On events only, never on a timer: a session's attach (a warning on that terminal), the keeper's
// stop of the jail (the rescue, then one line), and `yolo check` (a finding). A mismatch never
// refuses anything: stopping the jail is what loses the bytes.
package stateguard

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Role says what a stamped name is to the jail.
type Role string

const (
	// RoleState is `<workspace>/.yolo` itself.
	RoleState Role = "state"
	// RoleDurable is `<workspace>/.yolo/durable`, the agents' own scratch that survives restarts.
	RoleDurable Role = "durable"
	// RoleHome is a host directory (or file) the jail's home is bound from.
	RoleHome Role = "home"
)

// Entry is one stamped name and the identity it had when the stamp was taken.
type Entry struct {
	Role Role   `json:"role"`
	Path string `json:"path"`
	Dev  uint64 `json:"dev"`
	Ino  uint64 `json:"ino"`
}

// Stamp is a jail's identity stamp. Workspace and Home say which workspace and which of its homes
// it describes; Entries are the names stamped. A name that did not exist when the stamp was taken
// is not an entry: it cannot have moved.
type Stamp struct {
	Workspace string  `json:"workspace"`
	Home      string  `json:"home,omitempty"`
	Entries   []Entry `json:"entries"`
}

// statIdentity is the device and inode of path, without following a link at it. It is a seam so
// a test can name an identity without a filesystem that has one.
var statIdentity = lstatIdentity

// Take stamps workspace's `.yolo`, its `durable/`, and each of homeSources, the host paths the
// jail's home is bound from. A source that is not under `.yolo` is not stamped: it is not state
// a `mv` of `.yolo` can take. It returns nil when `.yolo` itself cannot be read, since then there
// is nothing to compare against later.
func Take(workspace, home string, homeSources []string) *Stamp {
	state := paths.WorkspaceStateDir(workspace)
	dev, ino, err := statIdentity(state)
	if err != nil {
		return nil
	}
	s := &Stamp{Workspace: workspace, Home: home}
	s.Entries = append(s.Entries, Entry{Role: RoleState, Path: state, Dev: dev, Ino: ino})
	add := func(role Role, p string) {
		if slices.ContainsFunc(s.Entries, func(e Entry) bool { return e.Path == p }) {
			return
		}
		if dev, ino, err := statIdentity(p); err == nil {
			s.Entries = append(s.Entries, Entry{Role: role, Path: p, Dev: dev, Ino: ino})
		}
	}
	add(RoleDurable, filepath.Join(state, "durable"))
	for _, src := range homeSources {
		src = filepath.Clean(src)
		if rel, err := filepath.Rel(state, src); err != nil || rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		add(RoleHome, src)
	}
	return s
}

// Mismatch is one stamped name that no longer names what it did.
type Mismatch struct {
	Entry
	// Gone is true when nothing is at the name now; false when another file or directory is.
	Gone bool
}

// Finding is a check's answer.
type Finding struct {
	Mismatches []Mismatch
}

// Moved reports whether anything stamped no longer names what it did.
func (f Finding) Moved() bool { return len(f.Mismatches) > 0 }

// StateMoved reports whether `.yolo` itself is not the directory the jail started on.
func (f Finding) StateMoved() bool { return f.has(RoleState) }

// HomesLost reports whether the host has lost its path to any of the jail's homes: `.yolo` or a
// home source moved. Those are the bytes only the running jail still holds, and the rescue's.
func (f Finding) HomesLost() bool { return f.has(RoleState) || f.has(RoleHome) }

// DurableMoved reports whether `durable/` is not the directory the jail started with.
func (f Finding) DurableMoved() bool { return f.has(RoleDurable) }

func (f Finding) has(r Role) bool {
	return slices.ContainsFunc(f.Mismatches, func(m Mismatch) bool { return m.Role == r })
}

// Check compares every stamped name against what is there now. A nil stamp finds nothing: a jail
// whose stamp could not be taken has nothing to compare against, and "could not tell" is never
// reported as moved.
func Check(s *Stamp) Finding {
	var f Finding
	if s == nil {
		return f
	}
	for _, e := range s.Entries {
		dev, ino, err := statIdentity(e.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			f.Mismatches = append(f.Mismatches, Mismatch{Entry: e, Gone: true})
		case err != nil:
			// Unreadable is not moved: a permission change says nothing about identity.
		case dev != e.Dev || ino != e.Ino:
			f.Mismatches = append(f.Mismatches, Mismatch{Entry: e})
		}
	}
	return f
}
