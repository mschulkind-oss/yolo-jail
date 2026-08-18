package entrypoint

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// bootlog.go persists what the entrypoint says at boot, because until now it said
// it exactly once, to a terminal, and nowhere else.
//
// # The gap it closes
//
// Every warning and notice the boot path emits — the reachability witness, the
// host-loopback disposition, config-render notices, a generator that failed — goes
// to e.Stderr, which is os.Stderr, which is the LAUNCH TERMINAL. Nothing captured
// it. The provisioning log that does exist (`.yolo/startup.log`, written by a shell
// `tee` in internal/cli/run/command.go) covers a strictly later phase: mise and
// bootstrap, long after the generators have finished. So an agent inside the jail —
// the reader most likely to be debugging the jail — could not see a single line of
// its own boot, and neither could anyone who had since closed the terminal.
//
// # Why the workspace, and not somewhere in the container
//
// `<workspace>/.yolo/` is on the BIND-MOUNTED host filesystem, which is the only
// property that matters here: the log survives the container. That is what makes it
// readable after a boot that REFUSED — the state OQ-R2's flip is about to make
// reachable, where there is no jail left to read anything from. An in-jail-only log
// would be missing in exactly the case it was written for. `.yolo/` is already the
// established home for per-boot state (`config-boot.json`) and is gitignored, so a
// boot log cannot be committed by accident.
//
// # Why it is never fatal
//
// A logger that can stop a boot is a worse bug than the blindness it fixes. Every
// failure here degrades to plain stderr: no workspace, a read-only mount, a
// full disk. The boot does not learn that the log failed, because there is nothing
// it could usefully do about it.
//
// # What it deliberately does not do
//
// It does not capture the two direct `os.Stderr` writes that remain in boot.go: the
// "⚡ Executing" banner, which is the handover line rather than a diagnostic, and
// anything a spawned subprocess writes to its own inherited stderr. Routing those
// would mean re-plumbing child processes for no diagnostic gain.

// bootLogName is the per-boot entrypoint log, a sibling of the provisioning log it
// deliberately does not merge with: this one is the ENTRYPOINT's own output and is
// complete before `startup.log`'s first line exists.
const bootLogName = "boot.log"

// bootLogPrevName holds the PREVIOUS boot's log. One boot of history is what makes
// "it worked last time" answerable, and it is the difference between diagnosing a
// failed launch and diagnosing it twice: the natural reaction to a broken jail is to
// launch it again, which without this would overwrite the only evidence.
const bootLogPrevName = "boot.log.prev"

// bootLog is the open per-boot log. A nil *bootLog is valid and does nothing, which
// is what every failure path returns.
type bootLog struct {
	f *os.File
}

// attachBootLog rotates the previous log aside, opens a fresh one, and INSTALLS the
// tee on e.Stderr itself.
//
// It sets e.Stderr rather than returning a writer for the caller to install, because
// the returned-writer shape had a silent failure mode with no test that could catch
// it: a caller that opened the log and then forgot to wire it produced a complete,
// correct-looking, permanently EMPTY log, and every test here still passed (they set
// e.Stderr themselves). Making the wiring unskippable is cheaper than a test that
// watches for someone skipping it. e.Stderr is ALWAYS left usable — on any failure
// it stays the stderr passed in, and the returned *bootLog is nil, which every
// method accepts.
func attachBootLog(e *Env, stderr io.Writer) *bootLog {
	e.Stderr = stderr

	dir := filepath.Join(e.WorkspaceDir(), ".yolo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	path := filepath.Join(dir, bootLogName)

	// Rotate rather than truncate. Ignore the error: a missing previous log is the
	// normal first-boot case, and a rotation that fails must not cost us the new log.
	_ = os.Rename(path, filepath.Join(dir, bootLogPrevName))

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil
	}
	bl := &bootLog{f: f}
	bl.writeHeader(e)
	e.Stderr = io.MultiWriter(stderr, f)
	// The log-only sink is the FILE, deliberately: a check reporting that it ran and
	// found nothing is what makes this log answer "did it happen?" rather than only
	// "what went wrong?", and it is noise on a terminal. See Env.LogOnly.
	e.LogOnly = f
	return bl
}

// writeHeader records what the boot WAS, so a log read later is self-describing.
// The version answers the first question anyone asks of a jail that misbehaves —
// "is this even running the code I think it is?" — which is otherwise answerable
// only by reading an env var inside a jail that may not exist any more.
func (bl *bootLog) writeHeader(e *Env) {
	if bl == nil {
		return
	}
	fmt.Fprintf(bl.f, "=== yolo entrypoint %s ===\n", time.Now().Format("2006-01-02T15:04:05-0700"))
	for _, k := range bootLogFacts {
		if v := e.Getenv(k); v != "" {
			fmt.Fprintf(bl.f, "  %s=%s\n", k, v)
		}
	}
	// Name the absent ones too. "yolo decided nothing about host loopback" is a
	// FACT with consequences (it is the value that can never escalate a service
	// failure), and an omitted line reads as "not recorded" rather than "empty".
	var absent []string
	for _, k := range bootLogFacts {
		if e.Getenv(k) == "" {
			absent = append(absent, k)
		}
	}
	if len(absent) > 0 {
		sort.Strings(absent)
		fmt.Fprintf(bl.f, "  (unset: %s)\n", strings.Join(absent, " "))
	}
}

// bootLogFacts are the launch-shaping decisions made OUTSIDE the jail, which is
// precisely why they are worth recording inside it: from in here they are
// unknowable, and each one changes how a later line in this same log should be read.
var bootLogFacts = []string{
	"YOLO_VERSION",
	"YOLO_RUNTIME",
	"YOLO_HOST_LOOPBACK",
	"YOLO_ALLOW_UNREACHABLE_SERVICES",
	"YOLO_ALLOW_STALE_IMAGE",
}

// finish records how the boot ENDED. Without it the log's last line is ambiguous in
// the one case that matters: a refused boot and a boot that was killed mid-generator
// both end with whatever happened to be printed last.
func (bl *bootLog) finish(err error) {
	if bl == nil {
		return
	}
	if err != nil {
		fmt.Fprintf(bl.f, "=== BOOT REFUSED: %v ===\n", err)
	} else {
		fmt.Fprintln(bl.f, "=== boot complete, handing over ===")
	}
	_ = bl.f.Close()
}
