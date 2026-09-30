// Package internaldaemon is the one dispatch table for the hidden `yolo internal daemon
// <name>` group: every host daemon a pack's `host_daemon.cmd` spells as
// `["yolo", "internal", "daemon", "<name>", …]`.
//
// IT IS A PACKAGE OF ITS OWN so a test binary can dispatch through the same table the CLI
// does. A launch self-execs that argv (execx.SelfExecArgv swaps the leading "yolo" for
// os.Executable()), and inside `go test` the executable is the TEST BINARY. A package whose
// tests reach a launch therefore needs a TestMain that routes the argv to the daemon, and a
// hand-kept list there drifts from this one: internal/cli/run's TestMain named four daemons
// and aws-auth was not one, so a launch that started the aws-auth daemon re-ran that package's
// WHOLE SUITE in a detached child sharing the parent's host-singleton directory, whose tests'
// cleanups killed the parent's daemons. Dispatching through Run makes a missed name
// impossible rather than merely unlikely.
package internaldaemon

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
)

// JailKeeper is the `jail-keeper` member's entry, set by internal/cli (run.KeeperMain with the
// seams the CLI wires into a launch). nil in a binary that never set it, which refuses the member.
var JailKeeper func(args []string) int

// IsDaemonArgv reports whether argv (os.Args, program name first) is the self-exec of a
// daemon in this group: `<program> internal daemon …`. A TestMain asks it before m.Run.
func IsDaemonArgv(argv []string) bool {
	return len(argv) >= 3 && argv[1] == "internal" && argv[2] == "daemon"
}

// Run dispatches `yolo internal daemon <name> [args...]`, args being everything after
// "daemon". The remaining argv is passed through verbatim, so each daemon's flag surface
// (--socket, --self-check, --init-ca, …) is byte-identical to its standalone binary.
//
// The MEMBERS are the switch below and the usage line beside it, never a count in this
// comment. Nor are they all host-scoped — `scope: "host"` is a manifest fact per loophole
// (`rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc`), and a daemon in this group
// may be either.
//
// `broker-relay` was a member and is GONE. It fronted the broker singleton for one jail and
// stamped a host-asserted jail_id into the request; both jobs are the framework's now —
// svcendpoint's front publishes the endpoint, and its connection preamble carries the
// identity — so the daemon was deleted rather than moved (docs/design/broker-as-a-pack.md
// §7). A name removed from this switch reports "unknown daemon", which is the right answer
// for an argv nothing emits any more.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal daemon <aws-auth|aws-credential-adapter|claude-oauth-broker|github-broker|host-processes|jail-keeper|journal|openai-auth-adapter|openai-auth-broker|serial|wire-bridge> [args...]")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "jail-keeper":
		// A container jail's KEEPER (internal/cli/run/keeper.go): the process a fresh launch spawns
		// to own the jail's host services and its life. Its code is the run pipeline's, whose tests
		// dispatch through this function, so this package cannot import it: the CLI sets JailKeeper.
		if JailKeeper == nil {
			fmt.Fprintln(os.Stderr, "yolo internal daemon: jail-keeper is not wired into this binary")
			return 2
		}
		return JailKeeper(rest)
	case "aws-auth":
		return awsauthdaemon.Main(rest)
	case "aws-credential-adapter":
		// aws-auth's DOORWAY outside a sandbox that shares the host's loopback (the loophole's
		// `jail_daemon.host_cmd`): a launch-owned listener a macos-user launch starts, handed its
		// inputs in a file (docs/design/host-notch-services.md HS-D15).
		return awscredadapter.DoorwayMain(rest)
	case "openai-auth-adapter":
		// openai-auth's Codex refresh DOORWAY, the same way (HS-D15; OQ-OA6 route (b)).
		return openaiauthhost.DoorwayMain(rest)
	case "claude-oauth-broker":
		return oauthbroker.Main(rest)
	case "github-broker":
		// packs/github's per-jail broker: runs the host's gh for one jail, fenced to the
		// workspace's own repositories (docs/design/boundary-broker.md).
		return ghbroker.Main(rest)
	case "host-processes":
		return hostprocesses.Main(rest)
	case "journal":
		return journald.Main(rest)
	case "openai-auth-broker":
		return openaiauthdaemon.Main(rest)
	case "serial":
		return serialdaemon.Main(rest)
	case "wire-bridge":
		// The wire bridge's HOST HALF (packs/wire-bridge's `host_daemon`): a launch-owned
		// service a host or macos-user launch starts for the one agent it runs, handed its
		// inputs in a file, never on this argv (docs/design/host-notch-services.md).
		return wirebridged.HostMain(rest)
	default:
		fmt.Fprintf(os.Stderr, "yolo internal daemon: unknown daemon %q\n", args[0])
		return 2
	}
}
