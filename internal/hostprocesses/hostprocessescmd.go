package hostprocesses

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// Main is the allowlisted host-process viewer daemon entry point. It keeps
// exec'ing the real `ps` — the output format is the contract.
//
// CLI contract: exactly one of --endpoint (publish a jail-facing loopback-TLS
// endpoint file here) or --socket (bind a plain AF_UNIX socket here, which only
// yolo's own front dials), plus --config and --self-check. Config defaults to
// $YOLO_HOST_PROCESSES_CONFIG or CWD/yolo-jail.jsonc.
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
	config := fs.String("config", "", "yolo-jail.jsonc path (defaults to $YOLO_HOST_PROCESSES_CONFIG)")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit (used by `yolo doctor`)")
	_ = fs.Parse(argv)

	if *selfCheck {
		return SelfCheck()
	}
	switch {
	case *socket == "" && *endpoint == "":
		fmt.Fprintln(os.Stderr, "ERROR: one of --endpoint or --socket is required")
		return 2
	case *socket != "" && *endpoint != "":
		fmt.Fprintln(os.Stderr, "ERROR: --endpoint and --socket are mutually exclusive")
		return 2
	}
	cfg := *config
	if cfg == "" {
		if env := os.Getenv("YOLO_HOST_PROCESSES_CONFIG"); env != "" {
			cfg = env
		} else {
			cwd, _ := os.Getwd()
			cfg = filepath.Join(cwd, "yolo-jail.jsonc")
		}
	}

	// The handler is identical either way: this daemon never learns which
	// transport carried its bytes, which is the property the black-box suite
	// exists to keep true.
	stop := make(chan struct{})
	serve := func() error { return hostservice.ServeEndpoint(BuildHandler(cfg), *endpoint, stop) }
	if *socket != "" {
		serve = func() error { return hostservice.ServeFrontedUnix(BuildHandler(cfg), *socket, stop) }
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-host-processes:", err)
		return 1
	}
	return 0
}
