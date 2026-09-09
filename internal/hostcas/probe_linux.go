//go:build linux

package hostcas

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// DefaultProbe answers [Presence] for one host path.
//
// WRITABILITY IS access(2), NOT THE MODE BITS. access checks with the process's
// REAL uid/gid, which under rootless podman is exactly the identity the
// container's root maps to — so "can the launching user write this?" and "can
// the jail write this?" are the same question, answered by the kernel including
// ACLs and read-only mounts. Reading st_mode instead would get ACLs and a `:ro`
// filesystem wrong, and it would get them wrong in the DANGEROUS direction:
// aliasing a store the jail then cannot write, which surfaces inside the jail as
// the tool's own error rather than as a launch notice.
//
// It writes nothing. A probe file created and removed would be the obvious
// alternative and is refused: it mutates the user's cache on every launch, and
// leaves a stray file behind if the process dies between the two calls.
func DefaultProbe(path string) Presence {
	st, err := os.Stat(path)
	if err != nil {
		return Presence{}
	}
	p := Presence{Exists: true, IsDir: st.IsDir()}
	if !p.IsDir {
		return p
	}
	// W_OK alone is not enough for a directory yolo intends to be traversed AND
	// written through: without X_OK the jail cannot enter it.
	p.Writable = unix.Access(path, unix.W_OK|unix.X_OK) == nil
	p.Empty = isEmptyDir(path)
	return p
}

// isEmptyDir reports whether a directory has no entries, reading AT MOST ONE
// name — the cold-start gate has to cost nothing, or its cost becomes a reason
// not to have it.
//
// A directory it cannot read is reported EMPTY, which makes the cold-start gate
// decline the alias. That is the status quo (the jail keeps its own copy), which
// is the direction every failure in this package takes.
func isEmptyDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	return len(names) == 0
}
