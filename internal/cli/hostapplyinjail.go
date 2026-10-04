package cli

// hostapplyinjail.go refuses a HOST apply run inside a jail: `yolo host apply`, its --assert and
// --revert, and `yolo apply` whenever its notch is the host (`--at host`, or `confinement: host`).
//
// THE BUG IT CLOSES (measured 2026-10-04). A host apply renders into the home of whoever runs it
// (os.UserHomeDir), and inside a jail that is the jail's own home, which the jail's launch has
// already rendered at the JAIL notch. Run there, `yolo host apply --assert` re-rendered that home
// at the host notch: it rewrote a jail-shaped ~/.claude/settings.json's `defaultMode` from the
// jail's posture to the host's `default`, under the agent running in it. Nothing the host verb
// writes is right for a jail, and nothing it could write there reaches the real host.
//
// THE VERBS THAT ONLY READ STAY. `yolo host -- <cmd>` and `yolo host env` compose a process's
// environment and write no home, and the in-jail no-ops the apply's own stages already carry
// (applyHostFloor, hostApplyGate, the pack update) are left as they are: this is the one place
// the WHOLE apply stops, above all of them.
//
// No hatch: the operation has no in-jail meaning to opt back into (the escape-hatch rule — a
// hatch is for a user's broken config, never for a command run where it does nothing right).

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// refuseHostApplyInJail refuses verb (the spelling the user typed, for the message) when this
// process runs inside a jail, printing why and the next step; it reports the exit code and
// whether it refused. Exit 1: the argv was well formed, and the place it ran is what is wrong.
func refuseHostApplyInJail(verb string, errw io.Writer) (int, bool) {
	if !config.InJail() {
		return 0, false
	}
	fmt.Fprintf(errw, "%s: refusing — this process is inside a jail, and a host apply renders "+
		"into the home of whoever runs it: here the jail's own (%s), which this jail's launch "+
		"already rendered for the jail. Nothing was written.\n"+
		"  Run `yolo host apply` (a dry run) or `yolo host apply --assert` in a terminal on the "+
		"host. This jail's own config is rendered again by its next launch.\n",
		verb, paths.Home())
	return 1, true
}
