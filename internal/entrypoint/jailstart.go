package entrypoint

// jailstart.go is the boot's JAIL-START RECORD (docs/design/launcher-update-per-jail-start.md,
// LU-D3, LU-D4, LU-D6): one random id, written whole by the boot step write_jail_start, that the
// launchers read to decide whether a program has had its update check "since this jail started".
//
// A jail start (coined there) is one run of the jail's own boot: a container's main process, or
// one macos-user bootstrap. A session's pass of the table (an attach) does not write it, so an
// attach is never a start. The boot is the ONLY writer: a launcher that finds no record checks
// nothing and says so, because a second writer minting its own would break one-writer state.
//
// The reader is shell, `$(<file)` in the launcher templates: a bash builtin that works under
// `env -i` and in bash 3.2, so no environment variable carries the id.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// JailStartRel is the record's path under the home. The launch-checks directory beside it holds
// the per-program check markers named for the record's id.
const (
	JailStartRel       = ".local/state/yolo/jail-start"
	LaunchChecksDirRel = ".local/state/yolo/launch-checks"
)

// writeJailStart replaces the record with a fresh id, atomically: a reader sees the old id or the
// new one. The last writer wins by design (two macos-user sessions of one home share the later
// start's id).
func writeJailStart(home string) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	path := filepath.Join(home, filepath.FromSlash(JailStartRel))
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jail-start-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(hex.EncodeToString(raw[:]) + "\n")
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o644)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return werr
	}
	return nil
}

// writeJailStartStep is the table's body. A failed write is not fatal to the boot: the launchers
// handle an absent record (they check nothing and say how to write it), so the boot says it and
// goes on.
func writeJailStartStep(b *bootRun) {
	if err := writeJailStart(b.e.Home); err != nil {
		b.e.warn(fmt.Sprintf("yolo: could not write the jail-start record (%v): programs will not check for updates in this jail. Relaunch the jail (yolo stop on the host, then launch) to write it.", err))
	}
}
