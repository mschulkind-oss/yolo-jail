package brokercore

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerhook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// daemon.go is what every broker source's host daemon, `yolo internal daemon <name>`, does
// around its own handler: one per jail, a loophole's host daemon behind yolo's own
// loopback-TLS front, so every connection carries the host-asserted jail id in its preamble
// (BB-D1). It reads the launch's scope file and refuses to start without one, leaves the
// workspace for the source's store directory, runs the user's on_request hook, serves the
// fronted socket, and on SIGTERM or SIGINT stops the source's work and drains.

// Daemon is one source's host daemon. The source fills in its name, its store, and how it
// builds a handler for one launch.
type Daemon struct {
	// Name is the daemon's name, `<source>-broker`: the flag set's name and every log line's
	// prefix.
	Name string
	// Source names the store directory the daemon moves into.
	Source string
	// SelfCheck, when set, is what `--self-check` runs instead of serving (the loophole's
	// `doctor_cmd`), with SelfCheckUsage as the flag's help.
	SelfCheck      func(io.Writer) int
	SelfCheckUsage string
	// NewHandler builds the handler for the launch whose scope file is at scopeFile, read as
	// sf. spawnCwd is the directory the launch spawned the daemon from, the workspace. ctx
	// ends when the daemon is told to stop. A nonzero code refuses to serve: NewHandler has
	// said why on stderr, and the daemon runs Launch.Cleanup, when set, and exits with it.
	NewHandler func(ctx context.Context, scopeFile string, sf brokerscope.File, spawnCwd string, stderr io.Writer) (Launch, int)
	// OnStop runs when the daemon is told to stop, after ctx is cancelled and before the
	// listener drains: it ends whatever the source started that the stop signal does not
	// reach (gh runs its programs in their own process groups).
	OnStop func()
}

// Launch is what a source's NewHandler hands the daemon for one launch.
type Launch struct {
	Handle hostservice.Handler
	// Store and Start are the request store and broker start id the user's on_request runs
	// for; with either unset, no hook runs.
	Store *brokerstore.Store
	Start string
	// Log is where the hook runner reports.
	Log io.Writer
	// Cleanup runs when the daemon returns.
	Cleanup func()
}

// Main runs the daemon with argv, the flags after `yolo internal daemon <name>`.
func (d Daemon) Main(argv []string) int {
	fs := flag.NewFlagSet(d.Name, flag.ContinueOnError)
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	scopeFile := fs.String("scope-file", "", "this launch's scope file, written by the launch")
	var selfCheck *bool
	if d.SelfCheck != nil {
		selfCheck = fs.Bool("self-check", false, d.SelfCheckUsage)
	}
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if selfCheck != nil && *selfCheck {
		return d.SelfCheck(os.Stdout)
	}
	if *socket == "" || *scopeFile == "" {
		fmt.Fprintln(os.Stderr, d.Name+": --socket and --scope-file are required")
		return 2
	}
	// FAIL CLOSED: a broker whose launch handed it no readable scope runs nothing, rather
	// than guessing a scope from anywhere else (BB-D32: it never reads the remotes or the
	// approval record).
	sf, err := brokerscope.ReadFile(*scopeFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, d.Name+": refusing to start:", err)
		return 1
	}
	// The launch spawns this daemon from the workspace. Leave it for a directory the broker
	// owns, so nothing this process does later resolves a relative path in the agent's tree;
	// the workspace is still what a host program's placement is checked against.
	spawnCwd, _ := os.Getwd()
	if dir := paths.BrokerSourceDir(d.Source); os.MkdirAll(dir, 0o700) == nil {
		_ = os.Chdir(dir)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, code := d.NewHandler(ctx, *scopeFile, sf, spawnCwd, os.Stderr)
	if l.Cleanup != nil {
		defer l.Cleanup()
	}
	if code != 0 {
		return code
	}
	// TOLD TO STOP, END THE SOURCE'S WORK FIRST. The launcher stops a daemon with SIGTERM to
	// its process group and SIGKILLs it after a grace; the listener then waits for every call
	// in flight, and a program in its own process group never saw the SIGTERM. So the source
	// kills its children itself (OnStop), the calls return inside the grace, and the deferred
	// cleanup runs.
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		cancel()
		if d.OnStop != nil {
			d.OnStop()
		}
		close(stop)
	}()
	hookDone := StartOnRequest(ctx, d.Name, l.Store, l.Start, config.OnRequest(), l.Log)
	err = hostservice.ServeFrontedUnix(l.Handle, *socket, stop)
	cancel()
	// A hook runs in its own process group, which the launcher's stop signal does not reach,
	// so wait for the runner to kill what it started: past the runner's own wait on a killed
	// hook's output. A launcher that SIGKILLs sooner still ends them, by Pdeathsig on Linux.
	select {
	case <-hookDone:
	case <-time.After(brokerhook.KillWait + time.Second):
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, d.Name+":", err)
		return 1
	}
	return 0
}

// onRequestInterval is how often StartOnRequest's runner polls the request store; zero is the
// runner's own default (brokerhook.Runner.Interval). SetOnRequestIntervalForTest sets it.
var onRequestInterval time.Duration

// SetOnRequestIntervalForTest makes every on_request runner started after it poll every d, for a
// test's broker process that need not wait a production poll per request it files.
func SetOnRequestIntervalForTest(d time.Duration) { onRequestInterval = d }

// StartOnRequest runs the user's on_request command for each request this broker lifetime
// (start) files in store (BB-D75), when one is set. name is the daemon's name, for its log
// lines. The returned channel closes once its runner has stopped.
func StartOnRequest(ctx context.Context, name string, store *brokerstore.Store, start, command string, log io.Writer) <-chan struct{} {
	done := make(chan struct{})
	if command == "" || store == nil || start == "" {
		close(done)
		return done
	}
	// The hook runs in the user's home: this daemon has moved into the store's directory,
	// which is no place for a user's command to start.
	home, _ := os.UserHomeDir()
	// This daemon is `yolo internal daemon <name>`, so its own executable is the yolo the
	// hook calls to decide: YOLO_BIN, and the `yolo` the runner links first on its PATH.
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(log, name+": on_request gets no YOLO_BIN:", err, "; its `yolo approve` must be on PATH")
		bin = ""
	}
	r := &brokerhook.Runner{Store: store, Start: start, Command: command, Bin: bin, Dir: home, Log: log,
		Interval: onRequestInterval}
	fmt.Fprintln(log, name+": running your on_request for each request this jail files")
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	return done
}

// PlacementRefusal is the loophole placement rule (config.AgentWritableTreeOf, the tree
// comparison config.LoopholePlacementProblems makes, without its argv-reading skips;
// docs/reference/loophole-system.md#the-placement-rule) applied to a host program a broker
// runs, named by program (`gh`, `ssh`, `git`): one inside the workspace or the jail home tree
// is a program an agent can rewrite, and the broker runs it on the host as the user, so it
// refuses it by name. The returned func takes the program's resolved path and answers why it
// is refused, or "". Each workspace is checked as given and with its symlinks resolved,
// because a caller may ask about the program both ways. A directory no launch may use as a
// workspace (the home, or one holding it: paths.WorkspaceScopeBreach) is skipped, since
// `yolo check` run from the home would otherwise read every host program under it as the
// agent's.
func PlacementRefusal(program string, workspaces ...string) func(path string) string {
	var trees []string
	for _, ws := range workspaces {
		if ws == "" || paths.WorkspaceScopeBreach(ws) != nil {
			continue
		}
		trees = append(trees, ws)
		if real, err := filepath.EvalSymlinks(ws); err == nil && real != ws {
			trees = append(trees, real)
		}
	}
	if len(trees) == 0 {
		trees = []string{""} // the jail home tree alone
	}
	return func(path string) string {
		for _, ws := range trees {
			if what := config.AgentWritableTreeOf(path, ws); what != "" {
				return "the broker will not run the host " + program + ": " + path + " is inside " + what +
					", where an agent can rewrite it between launches (the placement rule, " +
					"docs/reference/loophole-system.md#the-placement-rule). Put a " + program + " installed outside " +
					"that tree first on the PATH yolo is launched with."
			}
		}
		return ""
	}
}
