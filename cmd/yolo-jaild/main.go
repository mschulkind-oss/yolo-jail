// Command yolo-jaild is the in-jail infrastructure daemon. It is baked into the
// jail image and dispatches on its first argument to one of the in-jail
// daemons:
//
//	yolo-jaild supervise          # read YOLO_JAIL_DAEMONS + supervise each entry
//	yolo-jaild oauth-terminator   # in-jail TLS terminator for Claude OAuth
//	yolo-jaild openai-auth-adapter # in-jail Codex refresh adapter
//	yolo-jaild aws-credential-adapter # in-jail AWS container-credentials endpoint
//	yolo-jaild call-broker-http-front # non-authoritative HTTP front for a selected host broker
//	yolo-jaild wire-bridge        # anthropic→openai wire bridge (selection-lazy)
//	yolo-jaild nix-roots          # the root watcher (docs/reference/in-jail-nix-roots.md "The root watcher")
//
// This is a plain subcommand dispatch on args[0]; it is NOT argv[0]/symlink
// dispatch. Unknown or missing subcommands print usage and exit 2.
package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthterminator"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
	"github.com/mschulkind-oss/yolo-jail/internal/worktreereap"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// yolo-jaild links internal/packload (wirebridged -> entrypoint -> config), and until
	// 2026-09-23 that meant every jaild process wrote an embedded pack tree at init and,
	// having no release anywhere, leaked it for the life of the jail. Nothing materializes
	// at init now, but a daemon that does read a pack must still give its hold back; here,
	// inside run, so main's os.Exit cannot skip it and embeddedtree_test.go can pin it.
	defer packload.ReleaseEmbedded()
	// A run-as-you macos-user session's HOME is <workspace>/.yolo/home, inside the workspace, so
	// a daemon creating the workspace's .yolo judges scope against the real home the launcher
	// named (paths.GuardGuestScope, MMP-D7). Anywhere else it changes nothing.
	defer paths.GuardGuestScope()()
	if len(args) == 0 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "supervise":
		return supervisor.Main(rest)
	case "oauth-terminator":
		return oauthterminator.Main(rest)
	case "openai-auth-adapter":
		return openaiauthadapter.Main(rest)
	case "aws-credential-adapter":
		return awscredadapter.Main(rest)
	case "call-broker-http-front":
		return callbroker.HTTPFrontMain(rest)
	case "wire-bridge":
		return wirebridged.Main(rest)
	case "nix-roots":
		return nixroots.WatchMain(rest)
	case "reap-worktrees":
		return worktreereap.Main(rest)
	case "durable-size":
		return durable.CountMain(rest)
	default:
		return usage()
	}
}

// usage is a var so a test can observe the scope rule run is under when it dispatches
// (TestRunJudgesGuestScopeAgainstTheRealHome).
var usage = func() int {
	fmt.Fprintln(os.Stderr, "usage: yolo-jaild <supervise|oauth-terminator|openai-auth-adapter|"+
		"aws-credential-adapter|call-broker-http-front|wire-bridge|nix-roots|reap-worktrees|durable-size> [args...]")
	return 2
}
