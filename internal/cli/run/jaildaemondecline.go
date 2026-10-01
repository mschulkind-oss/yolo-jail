package run

// jaildaemondecline.go is the macos-user arm's TRUTHFUL DECLINE for the jail daemons its
// Seatbelt guest does NOT run: one line per declined daemon, naming it, its argv and why
// (docs/reference/macos-user-nix-and-features.md; shape (a) of
// docs/design/declaration-parity.md's OQ-DP5 — a coded decline, NOT a warning).
//
// # What changed with OQ-DP8 and OQ-DP9
//
// Until 2026-09-28 this printed EVERY composed daemon, because no jail daemon ran on this
// backend at all: `yolo-jaild` was not built for darwin and there was no supervisor to run one
// under. Both rulings are built now — the guest gets darwin in-jail binaries in its own prefix
// and a confined `yolo-jaild supervise` (internal/macosuser's jaildaemon.go) — so a loophole's
// jail daemon RUNS in the guest, and the decline shrank to the daemons the guest does not run,
// each for a reason that is a fact about the declaration: the intercepting OAuth terminator, a
// pack service's daemon (its host half runs instead), a credential DOORWAY that declares a host
// argv (it opens outside the sandbox instead, HS-D15: the OpenAI refresh adapter a bare
// `"packs": ["claude"]` selects, and the AWS credential adapter when a `bedrock` profile is
// selected), and an argv naming the container's loophole mount (internal/loopholes' guestrun.go
// states each).
//
// THE CLASSIFIER THIS FILE ONCE DECLINED TO WRITE now exists, in internal/loopholes, and for
// the reason this file gave for not writing it: the verdict used to be one branch; it now
// varies per declaration. It lives there rather than here so the served set, `yolo check`'s
// prediction and this printer read one split.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// jailDaemonDeclineLines renders one line per declined daemon: its name, its resolved argv
// (JailDaemonSpec.ResolvedCmd, so a port is never shown as a raw {listen}), and why.
func jailDaemonDeclineLines(declined []loopholes.DeclinedJailDaemon) []string {
	if len(declined) == 0 {
		return nil
	}
	lines := make([]string, 0, len(declined))
	for _, d := range declined {
		lines = append(lines, d.Spec.Name+": "+strings.Join(d.Spec.ResolvedCmd(), " ")+" — "+d.Why)
	}
	return lines
}

// noteMacosUserJailDaemonDeclines prints the decline for a macos-user launch.
//
// ⚠ THE CALL SITE IS THE BACKEND GATE, the rule noteMacosUserPlatformGaps states for the same
// reason: this is called from the macos-user arm of run.Run and nowhere else, so it needs no
// branch on the runtime's identity (and adds no row to backendparity_test.go's census).
//
// BOTH the live path and `--dry-run`, from one call site below the lifecycle block, because a
// decline is not a spawn: it says what the launch will NOT do, which is as true of a plan
// render as of a launch.
//
// NOT SUPPRESSIBLE (report-tiers.md P4; AGENTS.md's "a launch has no quiet mode"). A launch
// whose guest declines nothing prints nothing at all, which is what keeps this from being the
// notice OQ-BP-3 says people learn to skip.
func (o *Options) noteMacosUserJailDaemonDeclines(declined []loopholes.DeclinedJailDaemon) {
	lines := jailDaemonDeclineLines(declined)
	if len(lines) == 0 {
		return
	}
	// A pack service whose HOST HALF this launch runs (macosuserservices.go) is declined only as
	// a jail daemon; the line says the service itself runs, so it is never read as off.
	for i, d := range declined {
		if o.launchServiceRunning(d.Spec.Name) {
			lines[i] += " (its host half runs for this launch)"
		}
		// And a DOORWAY this launch opens outside the sandbox (macosuserdoorways.go, HS-D15):
		// declined only as a jail daemon, so the line says the doorway itself runs.
		if o.launchDoorwayPlanned(d.Spec.Name) {
			lines[i] += " (its doorway runs for this launch, outside the sandbox)"
		}
	}
	out := o.pr(o.Stderr)
	out.print("[yellow]Declined: these jail daemons do not run in the macos-user sandbox[/yellow] " +
		"— the ones a selected pack declared that this backend cannot run as declared:")
	for _, l := range lines {
		out.print("[yellow]  " + l + "[/yellow]")
	}
}
