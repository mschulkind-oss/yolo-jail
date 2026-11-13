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
	"io"
	"os"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
)

// JailKeeper is the `jail-keeper` member's entry, set by internal/cli (run.KeeperMain with the
// seams the CLI wires into a launch). nil in a binary that never set it, which refuses the member.
var JailKeeper func(args []string) int

// startLineWriter is where a daemon's start line goes; nil is os.Stderr at the time of the call.
// A test points it at a buffer.
var startLineWriter io.Writer

// logStart writes the helper's start line (helperpin.StartLine, PH-D9) before the daemon runs.
// jail-keeper is left out: its stdio is /dev/null, so it writes the line to its own log
// (internal/cli/run/keeper.go). A --self-check or --settings-check (aws-auth's) is a check, not a start, and `yolo check`'s doctor
// output is compared; a usage error never reaches here.
func logStart(name string, rest []string) {
	if name == "jail-keeper" || slices.Contains(rest, "--self-check") || slices.Contains(rest, "--settings-check") {
		return
	}
	w := startLineWriter
	if w == nil {
		w = os.Stderr
	}
	_, _ = io.WriteString(w, helperpin.StartLine(time.Now(), name)+"\n")
}

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
// comment. Each runs as one jail's own host daemon (HD-R1 retired the machine-wide
// `scope: "host"`), and whether a jail's end lets it finish its work in flight is the manifest's
// `detach`, a fact per loophole (`rg -n '^detach = true' packs/*/loopholes/*/loophole.toml`).
//
// `broker-relay` was a member and is GONE. It fronted the broker singleton for one jail and
// stamped a host-asserted jail_id into the request; both jobs are the framework's now —
// svcendpoint's front publishes the endpoint, and its connection preamble carries the
// identity — so the daemon was deleted rather than moved (docs/reference/loophole-system.md,
// "The per-jail relay is gone"). A name removed from this switch reports "unknown daemon", which is the right answer
// for an argv nothing emits any more.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal daemon <aws-auth|aws-credential-adapter|call-broker|claude-oauth-broker|git-broker|github-broker|host-processes|jail-keeper|journal|macos-log|mcp-auth-broker|openai-auth-adapter|openai-auth-broker|serial|wire-bridge> [args...]")
		return 2
	}
	rest := args[1:]
	var serve func([]string) int
	switch args[0] {
	case "jail-keeper":
		// A container jail's KEEPER (internal/cli/run/keeper.go): the process a fresh launch spawns
		// to own the jail's host services and its life. Its code is the run pipeline's, whose tests
		// dispatch through this function, so this package cannot import it: the CLI sets JailKeeper.
		if JailKeeper == nil {
			fmt.Fprintln(os.Stderr, "yolo internal daemon: jail-keeper is not wired into this binary")
			return 2
		}
		serve = JailKeeper
	case "aws-auth":
		serve = awsauthdaemon.Main
	case "aws-credential-adapter":
		// aws-auth's DOORWAY outside a sandbox that shares the host's loopback (the loophole's
		// `jail_daemon.host_cmd`): a launch-owned listener a macos-user launch starts, handed its
		// inputs in a file (docs/design/host-notch-services.md HS-D15).
		serve = awscredadapter.DoorwayMain
	case "openai-auth-adapter":
		// openai-auth's Codex refresh DOORWAY, the same way (HS-D15; OQ-OA6 route (b)).
		serve = openaiauthhost.DoorwayMain
	case "claude-oauth-broker":
		serve = oauthbroker.Main
	case "call-broker":
		serve = callbroker.Main
	case "github-broker":
		// packs/github's per-jail broker: runs the host's gh for one jail, fenced to the
		// workspace's own repositories (docs/design/boundary-broker.md).
		serve = ghbroker.Main
	case "git-broker":
		// packs/git's per-jail broker: git fetch and push for one jail over the host's own ssh,
		// fenced to the workspace's approved remotes (docs/reference/git-broker.md).
		serve = gitbroker.Main
	case "host-processes":
		serve = hostprocesses.Main
	case "journal":
		serve = journald.Main
	case "macos-log":
		// packs/macos-log's bridge: Apple's `log`, run as the host user for a jail whose own
		// account cannot read the unified log (packs/macos-log/README.md).
		serve = journald.MacosLogMain
	case "mcp-auth-broker":
		serve = mcpauthdaemon.Main
	case "openai-auth-broker":
		serve = openaiauthdaemon.Main
	case "serial":
		serve = serialdaemon.Main
	case "wire-bridge":
		// The wire bridge's HOST HALF (packs/wire-bridge's `host_daemon`): a launch-owned
		// service a host or macos-user launch starts for the one agent it runs, handed its
		// inputs in a file, never on this argv (docs/design/host-notch-services.md).
		serve = wirebridged.HostMain
	default:
		fmt.Fprintf(os.Stderr, "yolo internal daemon: unknown daemon %q\n", args[0])
		return 2
	}
	logStart(args[0], rest)
	return serve(rest)
}
