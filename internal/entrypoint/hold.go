package entrypoint

// hold.go keeps the container alive across a boot the entrypoint REFUSES, so the
// failure can be inspected with `podman exec` instead of being deleted with the
// container.
//
// # The gap it closes
//
// A refused boot takes its own evidence with it. The container runs with `--rm`
// (internal/cli/run/assemble.go's runFlags) and the refusal returns out of Main, so
// the runtime removes the container the moment PID 1 exits — the process table, the
// listeners and the jail daemons' state go with it. boot.log survives (it is on the
// workspace bind mount) and is what the refusal itself is read from, but a log is
// only ever the questions someone already thought to ask. MEASURED 2026-09-19: a
// jail refused with
//
//	start_jail_daemon_supervisor: jail daemon "wire-bridge" cannot publish its
//	required endpoint: cannot bind 127.0.0.1:8214: address already in use
//
// and three separate attempts to find out WHAT held the port failed, each for the
// same reason — by the time a human can type, there is no container left to type at.
//
// # Why it is not an ALLOW_ hatch, and why that hatch does not help
//
// YOLO_ALLOW_UNREACHABLE_SERVICES suppresses the reachability witness and nothing
// else. Measured on the incident above, it did exactly that and the launch still
// refused, because the generator failure is a SEPARATE gate — two gates, one hatch.
// This is not a third spelling of that one: it suppresses nothing. The failure is
// still reported, the boot still returns the error, and the exit code is still
// non-zero once the hold ends. What changes is WHEN the container dies.
//
// # Why only the generator-failure path
//
// Main can return an error two ways, and the hold sits on only one of them.
//
// genFailuresError is the refusal this exists for and the only one whose container
// is worth entering: every generator above it has run, the daemon supervisor has
// been started, and whatever broke is still broken and still resident. The other
// way — execBash failing — is a container that could not find or exec `bash`, which
// is the same binary the hold would be telling a user to run under `exec -it`. A
// hold there offers a shell that by construction does not open, so it would be a
// hang with a wrong instruction attached rather than a diagnostic.
//
// The macos-user boot (darwin.go, which ends in its own genFailuresError) is
// untouched for a blunter reason: there is no container, so there is nothing to
// hold open and nothing to exec into. Its launcher arm returns above
// assembleRunCmd and so never forwards the opt-in either.
//
// # How it blocks
//
// On a context with a real Done channel, never on `select {}` or a nil channel: a
// receive on nil with no other goroutine is a runtime-fatal deadlock
// ("all goroutines are asleep"), which is how internal/wirebridged's healthy idle
// crashed every credential-less boot until 2026-09-19. See its daemonContext, whose
// shape holdContext copies.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// holdReleaseFile is how a human ENDS the hold from inside the container.
//
// A file rather than a signal to spell, because the shell a user gets from
// `podman exec` cannot easily reach this process: `--init` makes catatonit PID 1
// and the entrypoint one of its children, so `kill 1` signals the init and not the
// holder. `touch` is one line and needs no pid.
//
// /tmp rather than /run: the boot has already chmod'd it 1777
// (configureScratchPermissions), so the file is writable whoever the exec'd shell
// turns out to run as, and it is a per-container tmpfs so nothing can carry in from
// a previous boot. The hold removes any pre-existing copy anyway — a release file
// that was already there would end the hold before anyone read the notice.
//
// It is a private spelling, not a contract: nothing else in this repo writes or
// reads it, and the only consumer is the human reading the line below that names it.
// A var rather than a const only so a test can relocate it into a t.TempDir() and
// exercise the real block/release rather than a stand-in for it.
var holdReleaseFile = "/tmp/yolo-hold-release"

// holdPollInterval is how often the wait looks for the release file. A variable so
// tests can shrink it; the value is a diagnostic latency nobody measures.
var holdPollInterval = 500 * time.Millisecond

// holdOffer is what a refusal that did NOT opt in says, and it is the whole of this
// feature's discoverability.
//
// A dial nobody knows about is a dial nobody uses: the incident this was built for
// cost three failed attempts by someone who owns the code, and the second run is the
// only chance to catch anything — by the third the container has been removed twice.
// The refusal is therefore the one place the offer belongs, which is the same
// argument the reachability witness's refusal makes for naming its own hatch
// (reachability.go). It is printed ONLY on a boot that already refused, never on a
// healthy one, and never when the user has already set it — telling someone to set a
// variable they set is the noise [OQ-RO3]'s readable launch stream is protecting.
const holdOffer = "\nyolo-entrypoint: this container is about to be removed WITH the failed state " +
	"inside it (--rm).\n  Re-run with " + paths.HoldOnRefusalEnv + "=1 to hold it open " +
	"instead and `exec` in to look around.\n"

// beginHold prints the hold notice and returns the WAIT.
//
// Two phases rather than one blocking call, because of what has to happen between
// them: Main closes boot.log after the refusal, and that close must not sit behind
// a hold that may never end — the ordinary way a hold ends is a human destroying
// the container, and a boot.log missing its "BOOT REFUSED" line would be a log
// truncated in exactly the case it exists for. So the notice goes out through
// e.Stderr (and therefore into boot.log) first, the log is finished, and only then
// does the returned func block.
//
// The returned func is never nil. A launch that did not opt in gets a no-op, which
// is what keeps the call site in Main three unconditional lines rather than a
// branch that could be got wrong.
// Both WriteString errors below stay dropped for the structural reason Env.warn states:
// e.Stderr IS the channel a report would travel on, so a failure to write it cannot be
// reported through it, and there is no second sink at this point in the boot (boot.log is
// the other half of that same MultiWriter). A lost write shows up as a refusal missing its
// hold notice, which is visible to the only reader there is.
func beginHold(e *Env) func() {
	if e.Getenv(paths.HoldOnRefusalEnv) == "" {
		if e.Stderr != nil {
			_, _ = io.WriteString(e.Stderr, holdOffer)
		}
		return func() {}
	}
	if e.Stderr != nil {
		_, _ = io.WriteString(e.Stderr, holdNotice(e))
	}
	// Before the wait, not inside it: a stale file would release the hold on the
	// first tick, which reads as the feature not working at all.
	//
	// The error stays dropped because the common case IS an error — ENOENT, no previous
	// hold — and the failure that is not ENOENT has the same visible outcome as the
	// feature working: the hold releases on the first tick, and the notice already
	// printed above tells the reader what a release looks like. Reporting here would
	// also mean writing to a log that is about to be closed for a refusal.
	_ = os.Remove(holdReleaseFile)
	return func() {
		ctx, stop := holdContext()
		defer stop()
		holdUntilReleased(ctx, holdReleaseFile, holdPollInterval)
		// os.Stderr, not e.Stderr: by now Main has closed boot.log, and e.Stderr is a
		// MultiWriter over the closed file. The terminal is the only reader left, and
		// it is the one that needs to see that the hold is over rather than wedged.
		fmt.Fprintln(os.Stderr, "yolo-entrypoint: hold released; the boot failure below stands.")
	}
}

// holdNotice is the whole of what a held boot tells a human, and it is a separate
// pure function so a test can read it without blocking on the wait it precedes.
//
// It has to say two things or it is just a hang: how to GET IN, and how to GET OUT.
// The exec line is printed verbatim from what the launcher composed
// (paths.HoldExecEnv), which is the only half of it this process can be right about
// — see that constant for why neither the runtime nor the container name is
// derivable in here.
func holdNotice(e *Env) string {
	var b strings.Builder
	rule := strings.Repeat("─", 72)
	b.WriteString("\n" + rule + "\n")
	b.WriteString("HOLDING THE CONTAINER OPEN — " + paths.HoldOnRefusalEnv + " is set.\n\n")
	b.WriteString("This boot REFUSED (the failures are above, and in .yolo/boot.log).\n")
	b.WriteString("The container is still running, so the state that failed — the process\n")
	b.WriteString("table, the listeners, the jail daemons — is still there to look at.\n\n")
	b.WriteString("From another terminal on the HOST:\n\n")
	b.WriteString("    " + holdExecLine(e) + "\n\n")
	b.WriteString("To END the hold (the jail then exits non-zero, exactly as it would have\n")
	b.WriteString("without this), from that shell:\n\n")
	b.WriteString("    touch " + holdReleaseFile + "\n\n")
	b.WriteString("Sending SIGINT or SIGTERM to this entrypoint ends it too, as does\n")
	b.WriteString("stopping the container from the host. Neither needs the launcher.\n")
	b.WriteString(rule + "\n")
	return b.String()
}

// holdExecLine is the command the notice tells a human to type.
//
// The fallback covers the one shape that can reach here without the launcher having
// composed a line: the opt-in set from inside an already-running jail rather than
// forwarded by a launch. A container's hostname is its own ID, which every runtime's
// `exec` accepts, so the fallback is usable — it just cannot know which runtime, and
// says so instead of guessing silently.
func holdExecLine(e *Env) string {
	if line := e.Getenv(paths.HoldExecEnv); line != "" {
		return line
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "<this container>"
	}
	return "<your container runtime> exec -it " + host + " bash"
}

// holdContext cancels on SIGINT/SIGTERM, and exists because context.Background()
// CANNOT BE HELD ON: its Done() is nil, and a receive on a nil channel with no
// other goroutine is a runtime-fatal deadlock rather than a wait. Same shape, and
// the same reason, as internal/wirebridged's daemonContext.
//
// Cancelling on those two signals is also the behaviour a held PID-1 child wants: a
// `podman stop` arrives as SIGTERM, and without this the hold would ignore it until
// the runtime's timeout escalated to SIGKILL.
func holdContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// holdUntilReleased blocks until the release file appears or ctx is done. Polling
// rather than watching: one stat every half second for the lifetime of a boot that
// has already failed is not a cost anyone can measure, and an inotify watch would
// add a failure mode to the one code path whose whole job is to not disappear.
func holdUntilReleased(ctx context.Context, release string, poll time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := os.Stat(release); err == nil {
				return
			}
		}
	}
}
