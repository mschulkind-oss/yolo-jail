package cli

// hostmanagementgate.go is the `host_management` REFUSAL: the one place the declared
// ownership contract (docs/design/config-ownership-and-promotion.md §4) stops a host apply.
//
// # What the key decides here, and what it deliberately does not
//
// It selects the MODE the host notch renders in — `unrendered`, `rmw`, or `stateful` — and
// this file is the half of that which needs no render engine: at `none` there is no rendered
// host surface at all, so the command that would write one refuses and names the key that
// decided it (§4.1).
//
// > THE KEY ENABLES THE MECHANISM. IT DOES NOT GRANT THE APPROVAL (§4.4).
//
// That is host_apply_on_launch's ruling, and it holds identically: `assert` selects the mode,
// it does not pre-authorize the writes that mode performs. Every confirmation an apply runs
// today — confirmHostLosses, the skills and briefing adoption gates, the dropped-pack retire
// prompt — is untouched by this file. An apply under `assert` must be BYTE-IDENTICAL to one
// with the key unset, which is §11's non-regression criterion and is pinned by
// TestHostManagementAssertIsByteIdenticalToUnset.
//
// # `own` refuses too, and that is honest rather than strict
//
// `own` is §10's last build step — the only one that can lose data. Until
// it is, a host apply under `own` refuses. The alternative is worse than a refusal: silently
// rendering `rmw` would tell a user who declared their file DERIVED that it is derived while
// it still holds bytes existing nowhere else, which is the exact inference P1 exists to end.
// A refusal that names the value is a fact the user can act on; a silent downgrade is not.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostManagementRefusal is the message for a mode that cannot render, or "" when the apply
// may proceed. Split from the writer below so the check section and the tests read the same
// sentences the refusal prints, rather than a second copy of them.
func hostManagementRefusal(mode config.HostManagement) string {
	switch mode {
	case config.HostManagementNone:
		return "`host_management` is \"none\" in " + paths.UserConfigPath() + ", which says " +
			"your agents' config files are yours entirely — so there is nothing for yolo to " +
			"render into your home and nothing was written.\n" +
			"  Set it to \"assert\" to have yolo own the keys your packs declare (and only " +
			"those); `yolo config-ref` says what each value means."
	case config.HostManagementOwn:
		return "`host_management` is \"own\" in " + paths.UserConfigPath() + ", and whole-file " +
			"composition at the host notch is not built yet.\n" +
			"  yolo refuses rather than quietly rendering as \"assert\": that would leave you " +
			"believing the file is derived output while it still holds bytes that exist " +
			"nowhere else. Set it to \"assert\" to apply today."
	}
	return ""
}

// refuseHostManagement stops a host apply the declared contract does not permit, printing the
// refusal and returning its exit code.
//
// IT MUST BE CALLED ABOVE EVERY STAGE, not just above the render — the same rule
// jsonRefusedForPosture earned the hard way. `yolo host apply --revert --shell-init` under
// `none` that refused the render and left `--shell-init` running would append a PATH line to
// the user's shell rc as part of a command that wrote nothing, which is the write P3 forbids.
// Hence the two call sites (hostApply, and applyMain's host notch) rather than one inside
// applyHostSurveyed.
//
// EXIT 1, not 2. Nothing about the argv is wrong: the command is well-formed and the user's
// own configuration is what declined it.
func refuseHostManagement(errw io.Writer) (int, bool) {
	msg := hostManagementRefusal(config.HostManagementMode())
	if msg == "" {
		return 0, false
	}
	fmt.Fprintf(errw, "yolo host apply: %s\n", msg)
	return 1, true
}
