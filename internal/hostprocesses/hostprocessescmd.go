package hostprocesses

import (
	"flag"
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// Main is the allowlisted host-process viewer daemon entry point. It keeps
// exec'ing the real `ps` — the output format is the contract.
//
// CLI contract: exactly one of --endpoint (publish a jail-facing loopback-TLS
// endpoint file here) or --socket (bind a plain AF_UNIX socket here, which only
// yolo's own front dials), plus --settings and --self-check.
//
// --settings NAMES THE FILE YOLO WROTE, and it replaced --config. The old flag
// pointed at a raw yolo-jail.jsonc that this daemon parsed itself, re-reading it on
// every request from a cwd nobody set deliberately; the new one points at a flat
// JSON object of values yolo validated against the loophole manifest's `settings`
// declarations and wrote once, at launch. See the package comment for why the
// freeze is the point rather than a side effect.
//
// TWO TRANSPORTS, NAMED BY THE CALLER, never guessed from the path — the split
// internal/journald draws with the same two flags, and hostservice draws with
// three entry points.
//
// --socket WAS AN ALIAS FOR --endpoint AND IS NO LONGER ONE. Recorded rather
// than deleted, because the alias reads like compatibility and the next person
// to find a manifest spawning `--socket {socket}` will be tempted to restore it.
// It was the escape hatch for a host yolo OLDER than this binary — and that skew
// cannot happen: the run pipeline substitutes os.Executable() for argv[0] when
// it spawns a builtin daemon (SelfExecArgv, internal/cli/run/loopholesruntime.go),
// so the process reading these flags is always the same binary that read the
// manifest. What the alias would cost now is concrete: folding --socket into
// --endpoint publishes a token-bearing regular FILE where a socket was meant,
// which is the shape of issue #31 (see the note above hostservice.ServeUnix) and
// fails the run pipeline's socket-connectable readiness probe with no diagnosis.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-host-processes", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "Endpoint file to publish (jail-facing loopback-TLS)")
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	settings := fs.String("settings", "", "Resolved settings file written by yolo (see --help)")
	retiredConfig := fs.String("config", "", "RETIRED — use --settings")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit (used by `yolo doctor`)")
	_ = fs.Parse(argv)

	// A RETIRED flag REFUSES and names its replacement. It does not fall back to
	// --settings and it is not ignored, because the two flags do not name the same
	// kind of file: --config named a yolo-jail.jsonc this daemon parsed itself, and
	// silently treating one as the other would read `{"visible": …}` out of a file
	// that spells it `host_processes.visible` and find nothing — an EMPTY allowlist
	// reported as a working daemon. Failing to start says what happened; showing
	// nothing does not.
	if *retiredConfig != "" {
		fmt.Fprintln(os.Stderr, "ERROR: --config is retired — this daemon no longer reads a "+
			"yolo-jail.jsonc. Pass --settings <file>, the resolved settings file yolo writes "+
			"from loopholes.host-processes.settings; the manifest's host_daemon.cmd names it "+
			"with the {settings} token.")
		return 2
	}
	if *selfCheck {
		return SelfCheck(*settings)
	}
	switch {
	case *socket == "" && *endpoint == "":
		fmt.Fprintln(os.Stderr, "ERROR: one of --endpoint or --socket is required")
		return 2
	case *socket != "" && *endpoint != "":
		fmt.Fprintln(os.Stderr, "ERROR: --endpoint and --socket are mutually exclusive")
		return 2
	}

	// READ ONCE, HERE, before a single connection is accepted. This line is the
	// freeze: everything downstream holds values, not a path.
	cfg := LoadSettings(*settings)

	// The handler is identical either way: this daemon never learns which
	// transport carried its bytes, which is the property the black-box suite
	// exists to keep true.
	stop := make(chan struct{})
	handler := BuildHandler(cfg)
	serve := func() error { return hostservice.ServeEndpoint(handler, *endpoint, stop) }
	if *socket != "" {
		serve = func() error { return hostservice.ServeFrontedUnix(handler, *socket, stop) }
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-host-processes:", err)
		return 1
	}
	return 0
}
