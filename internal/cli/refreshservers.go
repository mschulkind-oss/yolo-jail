package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// refreshservers.go is `yolo internal refresh-servers` — the transitive half of evergreen
// agent updates (docs/design/program-delivery.md §3.5, OQ-PD12a;
// docs/plans/evergreen-agent-updates.md build-order step 7).
//
// # Who calls it, and from where
//
// The generated agent launchers, once, after they are done with their own program and BEFORE
// they exec it. Not a boot genStep: §3.5 rules that a server inherits the trigger of the
// agent that connects to it, so a launch that starts no agent refreshes nothing and a
// workspace that never types `claude` never pays for claude's servers.
//
// The precedent for a generated launcher calling a hidden subcommand is
// `capture-materialize`, three doors down, and the reason is the same one turned around: the
// walk is a filesystem-and-registry decision per package, and the alternative is a second
// copy of it in each of the two bash templates.
//
// # Hidden, for capture-materialize's reason
//
// Nothing but the generated launcher should emit this argv: it installs into the home it is
// pointed at, which is right from a launcher and is a way to write into someone's prefixes
// from anywhere else.
//
// # Its exit status is advisory
//
// Non-zero means an ABSENT server could not be installed — the one outcome with no working
// copy behind it. The launcher does not treat that as fatal (it execs the agent anyway, which
// is present and runnable); the status exists so `yolo internal refresh-servers` is usable by
// hand and so the failure has somewhere to go besides a message.

const refreshServersUsage = "usage: yolo internal refresh-servers [--home=DIR] " +
	"[--npm=\"pkg pkg\"] [--go=\"mod mod\"] [--updates=0|1]"

// runRefreshServers is the `yolo internal refresh-servers` entry.
func runRefreshServers(args []string) int {
	home := os.Getenv("HOME")
	npmSpecs, goSpecs := "", ""
	// The POLICY DEFAULT IS TRUE, and it inverts host_apply_on_launch's fail-closed
	// precedent on purpose (the plan's trap 9): an absent or unreadable agent_updates means
	// updates are ON, because the failure mode of the other default is every agent in every
	// jail silently frozen. A launcher always bakes the flag, so this default is only ever
	// reached by a hand invocation.
	updates := true
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--home="):
			home = strings.TrimPrefix(a, "--home=")
		case strings.HasPrefix(a, "--npm="):
			npmSpecs = strings.TrimPrefix(a, "--npm=")
		case strings.HasPrefix(a, "--go="):
			goSpecs = strings.TrimPrefix(a, "--go=")
		case strings.HasPrefix(a, "--updates="):
			updates = strings.TrimPrefix(a, "--updates=") == "1"
		default:
			fmt.Fprintf(os.Stderr, "refresh-servers: unexpected argument %q\n%s\n",
				a, refreshServersUsage)
			return 2
		}
	}
	if home == "" {
		fmt.Fprintln(os.Stderr, refreshServersUsage)
		return 2
	}

	// NewEnv rather than a second path resolution: Home, NpmPrefix and GoPath are exactly
	// the three values this needs, and their defaults ($HOME/.npm-global, $HOME/go) are the
	// same ones the launcher templates spell in bash. --home is authoritative over the
	// environment because the launcher passes its own $HOME, which under macos-user is the
	// sandbox home and not whatever this process inherited.
	vars := envMap(os.Environ())
	vars["HOME"] = home
	vars["JAIL_HOME"] = home
	e := entrypoint.NewEnv(vars)

	if err := entrypoint.RefreshServers(entrypoint.ServerRefreshRequest{
		Env:      e,
		NpmSpecs: npmSpecs,
		GoSpecs:  goSpecs,
		Updates:  updates,
		Stderr:   os.Stderr,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "refresh-servers:", err)
		return 1
	}
	return 0
}
