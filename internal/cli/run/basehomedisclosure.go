package run

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// legacyBaseHomeHatch lets a launch proceed with legacy bytes still in the base home.
//
// Documented HERE because this is where it is enforced, per AGENTS.md's rule that YOLO_*
// dials have no authority row. It is named in the refusal itself, which is the only place
// a user meets this condition.
const legacyBaseHomeHatch = "YOLO_ALLOW_LEGACY_BASE_HOME"

// noteLegacyBaseHome REFUSES the launch while the base home still holds legacy
// per-workspace runtime state, and prints the two commands that clear it.
//
// # Why a refusal and not a verb
//
// The obvious shape was `yolo base-home --archive`, and it was built and then deleted
// (maintainer's call, 2026-09-21). This condition is a fossil of an older yolo whose jail
// home was shared and writable; it affects one or two hosts, once. A permanent verb — four
// registration points, a usage string, a --help line and a test suite — is a lot of surface
// to carry forever for a cleanup someone does once and never thinks about again. A `mv` the
// refusal prints costs nothing after the day it is used.
//
// # Why fatal rather than a warning
//
// A warning on every launch is a line people learn to scroll past, and this one has a real
// cost while it sits there: every jail can read the bytes, and seedAgentDir copies them into
// each new workspace. Refusing makes it a thirty-second job instead of a permanent notice.
//
// # Why it cannot recur, which is what makes one-shot the right shape
//
// The base is bound `:ro` into every podman jail (assemble_parts.go), and every write the
// host CLI makes into GlobalHome is an os.MkdirAll of a DIRECTORY, never a file
// (hostfiles.go, prepare.go). Nothing can put a transcript back. So there is no prevention
// half to build and no state to track — once cleared, this code is unreachable forever.
//
// # What does NOT refuse
//
// Zero bytes. Empty directories are the steady state on every host that never ran the old
// shared-writable home, and refusing a launch over a directory with nothing in it would
// brick every one of them for a condition with nothing to fix. A REFUSED root still warns,
// because a symlinked `.claude` in the base home is an anomaly whatever its size.
//
// HOST-ONLY: inside a jail paths.GlobalHome() resolves under the jail's own disposable home,
// so an in-jail walk either measures nothing or measures host bytes it cannot act on.
func (o *Options) noteLegacyBaseHome() error {
	if o.Getenv("YOLO_VERSION") != "" {
		return nil
	}
	globalHome := paths.GlobalHome()
	rep := basehome.Detect(globalHome, basehome.ShippedDecls())
	out := o.pr(o.Stderr)

	// A refused root is an anomaly rather than a quantity, so it is said whatever was
	// measured — and it never refuses, because the user cannot act on it with an `mv`.
	for _, w := range rep.Warnings() {
		out.print("[yellow]" + w + "[/yellow]")
	}
	if rep.Bytes() == 0 {
		return nil
	}

	// The roots carrying bytes are the only ones worth an `mv`: a root with candidates but
	// no bytes is empty directories, which the caller is not being asked to move.
	var roots []string
	for _, s := range rep.ByRoot() {
		if s.Bytes > 0 {
			roots = append(roots, s.Root)
		}
	}
	archive := filepath.Join(paths.GlobalStorage(), "archive", "base-home")

	// Printed to STDERR rather than returned in the error, so it reaches the launch log tee
	// and so the multi-line body is not squeezed through the caller's one-line red printf.
	out.print("[bold yellow]" + rep.Summary() + "[/bold yellow]")
	out.print("This is left over from an older yolo whose jail home was shared and writable. " +
		"It cannot happen again — the base home is mounted read-only into every jail now — but " +
		"while it is there every jail can read it, and each new workspace gets a copy seeded from it.")
	out.print("")
	out.print("Move it aside, then relaunch:")
	out.print("")
	out.print("  mkdir -p " + archive)
	for _, r := range roots {
		out.print("  mv " + filepath.Join(globalHome, r) + " " + archive + "/")
	}
	out.print("")
	out.print("They are recreated empty on the next launch. Do NOT move " +
		".claude-shared-credentials or .gemini-shared-credentials — those are your logins.")

	if o.Getenv(legacyBaseHomeHatch) != "" {
		out.print("[yellow]" + legacyBaseHomeHatch + " is set — launching anyway.[/yellow]")
		return nil
	}
	return fmt.Errorf("legacy state in the base home (%s): clear it with the commands above, "+
		"or set %s=1 to launch anyway", strings.Join(roots, ", "), legacyBaseHomeHatch)
}
