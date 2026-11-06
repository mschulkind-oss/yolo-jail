package entrypoint

// misereprovision.go is the SESSION'S MISE INSTALL (docs/design/live-config-apply.md LA-S4a,
// LA-D20). Provisioning runs once per jail, in its first session (jailmain.go), and its stage is
// what runs `mise install`. Since the mise tools became session inputs (they ride the per-entry
// channel, so every session's pass renders the global mise config from its own entry's table), a
// later session can carry tools the first session never installed. That session installs them,
// on its own terminal, after its pass and before its command — the same place the first
// session's stage runs.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

const (
	// miseToolsRecordFile is the mise session inputs (miseSessionKey) the jail's tool store was
	// last installed from: written by provisioning that succeeded, and by a session install that
	// did. Absent until then.
	miseToolsRecordFile = "mise-tools"
	// miseInstallLockFile is flocked by a session running its mise install, so two sessions
	// arriving with the same change install it once, and two with different changes in turn.
	miseInstallLockFile = "mise-install.lock"
)

// miseSessionKey is what decides a session's mise install: the tool table its pass rendered the
// global mise config from, and the list mise must not install.
func miseSessionKey(e *Env) string {
	return strings.TrimSpace(e.Getenv("YOLO_MISE_TOOLS") + "\n" + e.Getenv("MISE_DISABLE_TOOLS"))
}

// recordMiseInstalled records that the jail's tool store holds e's mise session inputs. A failed
// write is dropped: the next session then installs again, which is idempotent.
func recordMiseInstalled(e *Env) { _ = writeMainState(miseToolsRecordFile, miseSessionKey(e)) }

// reprovisionMise runs the jail's mise install, through run, when this session's mise inputs are
// not the ones last installed, and records them once it succeeds. Under miseInstallLockFile, with
// the record read again inside it, so a session that waited for another's install of the same
// change runs nothing. It returns the install's status; the caller reports it and never ends the
// session on it, since the session's own command may need no tool at all.
func reprovisionMise(e *Env, run func(*Env, string) int, out io.Writer) int {
	key := miseSessionKey(e)
	if rec, ok := readMainState(miseToolsRecordFile); ok && rec == key {
		return 0
	}
	if err := os.MkdirAll(jailMainDir, 0o755); err != nil {
		fmt.Fprintf(out, "yolo: cannot check this session's mise tools (%v); run `mise install` to install them\n", err)
		return 1
	}
	lock, err := os.OpenFile(mainStatePath(miseInstallLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		fmt.Fprintf(out, "yolo: cannot check this session's mise tools (%v); run `mise install` to install them\n", err)
		return 1
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintf(out, "yolo: cannot check this session's mise tools (%v); run `mise install` to install them\n", err)
		return 1
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if rec, ok := readMainState(miseToolsRecordFile); ok && rec == key {
		return 0
	}
	fmt.Fprintln(out, "yolo: this session's mise tools changed since this jail installed its tools; installing them")
	rc := run(e, provision.SetupBypassingShims(provision.StepAnnounceMiseInstall, provision.StepMiseInstall))
	if rc != 0 {
		fmt.Fprintln(out, "yolo: this session's mise install failed (exit "+strconv.Itoa(rc)+"); "+
			"the next session tries again, or run `mise install` here")
		return rc
	}
	recordMiseInstalled(e)
	return 0
}
