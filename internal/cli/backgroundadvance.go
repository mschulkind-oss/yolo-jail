package cli

// backgroundadvance.go is THE BACKGROUND ADVANCE (docs/design/pi-extension-store-builds.md §7.4,
// XB-D19, XB-D20): `yolo internal background-advance`, the detached host process a fresh jail launch
// starts for the built trees that update FOR THE NEXT LAUNCH (`agent_updates`' "next-launch"). The
// launch hands each of those keys what it already has and leaves this process to check and build
// them; what it builds lands for the next fresh launch through the check record's compare-and-swap,
// and never touches a running jail's copy.
//
//   - DETACHED: a session of its own, a /dev/null stdin and its two streams on one log
//     (backgroundAdvanceLog), so it neither holds a piped launch's output open nor dies with the
//     terminal (notty.StartDetached). Normal priority: a deprioritized holder of a lock a launch at the
//     default timing waits on would stretch that wait.
//   - IT RESOLVES THE SELECTION ITSELF, from the user config, as `yolo capture <pack>/<name>` does
//     (captureTree), never from the launch's staged tree, which goes when the jail stops. A key it
//     cannot find there gets the outcome "unresolved", which the next launch says.
//   - IT NEVER WAITS FOR A LOCK (§6.2 rule 5): a key whose background lock another advance holds is
//     skipped, and so is one whose check record, mirror or build lock is held (advanceOptions.
//     background); only a completed build's settle record write waits (rule 1).
//   - A SIGTERM OR SIGHUP — or a SIGINT, should one reach it — cancels every key's check and build:
//     each build child's process group is told and then killed (forkbuildchild.go), and each build
//     jail is removed by name (advance.removeBuildJailOf). It never uses run.InterruptScope, which
//     re-raises the signal it caught.
//   - EACH KEY'S OUTCOME is a record under paths.BackgroundAdvanceDir, which the next fresh launch
//     reads and claims, so one launch says it (noteBackgroundOutcome).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// backgroundAdvanceVerb is the hidden subcommand: `yolo internal background-advance`.
const backgroundAdvanceVerb = "background-advance"

// backgroundSignals are what end a background advance: a SIGTERM or SIGHUP (a logout, a shutdown, a
// kill), and a SIGINT, should one reach a process no terminal signals.
var backgroundSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}

// The outcome states a background advance records per key (backgroundOutcome.State).
const (
	bgRunning     = "running"
	bgMoved       = "moved"
	bgFailed      = "failed"
	bgRetained    = "retained"
	bgSkipped     = "skipped"
	bgNothing     = "nothing"
	bgInterrupted = "interrupted"
	bgUnresolved  = "unresolved"
)

// backgroundOutcome is one key's record: what its background advance is doing, or did.
type backgroundOutcome struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	State string `json:"state"`
	// Pid is the background advance's, while it runs.
	Pid int `json:"pid,omitempty"`
	// At is when the background advance began the key, in unix seconds.
	At int64 `json:"at"`
	// Log is the background advance's log, which every line about it names.
	Log string `json:"log"`
	// From and To are a move's: the good build it left, "" for a first build, and the one it built.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// What and Error are a failure's: what could not be built ("" when the check failed), and why.
	What  string `json:"what,omitempty"`
	Error string `json:"error,omitempty"`
	// RetryAt is when the failure is tried again, in unix seconds.
	RetryAt int64 `json:"retry_at,omitempty"`
	// Retained is what a build jail not known gone left pending, and why.
	Retained string `json:"retained,omitempty"`
}

// backgroundAdvanceLog is the background advance's log: one file beside the tree builds' logs,
// appended to by every background advance and moved to <log>.prev past a MiB (notty.OpenDetachLog).
func backgroundAdvanceLog() string {
	return filepath.Join(paths.GlobalStorage(), "logs", "background-advance.log")
}

// backgroundKeyLock is the lock a background advance holds while it runs key, and backgroundOutcomePath
// the key's outcome record; a claimed record is read from a unique file before becoming backgroundSaidPath.
func backgroundKeyLock(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".lock")
}

func backgroundOutcomePath(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".json")
}

func backgroundSaidPath(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".said")
}

// backgroundAdvanceArgv is the argv, after the executable, that starts the background advance of
// keys for a jail of runtime and platform.
func backgroundAdvanceArgv(keys []string, rt, platform string) []string {
	argv := []string{"internal", backgroundAdvanceVerb, "--runtime=" + rt, "--platform=" + platform}
	for _, k := range keys {
		argv = append(argv, "--key="+k)
	}
	return argv
}

// errBackgroundFromTest refuses the one spawn a unit test must never make (startBackgroundAdvance).
var errBackgroundFromTest = errors.New("a test binary does not self-exec the background advance")

// startBackgroundAdvance starts the background advance, argv after this very binary (run.SelfExecPath,
// as the fork build child is), detached, its streams appended to log. A test binary never self-execs.
// A var so a test can count the spawn.
var startBackgroundAdvance = func(argv []string, log string) error {
	if flag.Lookup("test.v") != nil {
		return errBackgroundFromTest
	}
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return err
	}
	f, err := notty.OpenDetachLog(log)
	if err != nil {
		return err
	}
	defer f.Close()
	c := exec.Command(run.SelfExecPath(), argv...)
	c.Stdout, c.Stderr = f, f
	return notty.StartDetached(c)
}

// removeBuildJail removes a build jail by name, running or not: a var so a test can see it.
var removeBuildJail = run.ForceRemoveContainer

// removeBuildJailOf removes the build jail of staging, which a SIGTERM stopped, on the runtime its
// build recorded, else the advance's (XB-D19).
func (a *advance) removeBuildJailOf(staging string) {
	cname := runtime.FromWorkspace(staging)
	rt, err := readForkBuildRuntime(staging)
	if err != nil {
		rt = a.o.runtime
	}
	if rt == "" {
		rt = "podman"
	}
	if removeBuildJail(cname, rt) {
		a.dim("%s: removed its build jail %s", a.f.Label(), cname)
		return
	}
	a.warn("%s: could not confirm its build jail %s is gone — `%s rm --force %s` removes it, and the next "+
		"build of it waits until it is", a.f.Label(), cname, rt, cname)
}

// backgroundTreeAdvance starts the background advance of the keys a launch handed what it had
// (XB-D19), and says so at once, naming the keys and the log (XB-D20). A key another background
// advance runs right now is left to it.
var backgroundTreeAdvance = func(trees []packload.Fork, req run.TreeBuildRequest, errw io.Writer, color bool) {
	pr := richtext.Printer{W: errw, Color: color}
	var keys, labels []string
	for _, f := range trees {
		if pidlock.Held(backgroundKeyLock(f.Key())) {
			continue // its outcome line said it is running (noteBackgroundOutcome)
		}
		keys, labels = append(keys, f.Key()), append(labels, f.Label())
	}
	if len(keys) == 0 {
		return
	}
	log := backgroundAdvanceLog()
	them := "them"
	if len(keys) == 1 {
		them = "it"
	}
	if err := startBackgroundAdvance(backgroundAdvanceArgv(keys, req.Runtime, req.Platform), log); err != nil {
		step := "`yolo capture " + keys[0] + "`"
		if len(keys) > 1 {
			step = "`yolo capture <pack>/<name>` for each"
		}
		pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("⚠ %s: updates for the next launch, and the "+
			"background advance that checks and builds %s could not start (%v) — the next launch tries again, or %s now",
			strings.Join(labels, ", "), them, err, step)))
		return
	}
	pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: updates for the next launch — a background advance "+
		"is checking and building %s now; its log is %s", strings.Join(labels, ", "), them, log)))
}

// backgroundArgs are the background advance's flags.
type backgroundArgs struct {
	runtime, platform string
	keys              []string
}

type keyList []string

func (k *keyList) String() string     { return strings.Join(*k, ",") }
func (k *keyList) Set(v string) error { *k = append(*k, v); return nil }

func parseBackgroundArgs(args []string, errw io.Writer) (backgroundArgs, error) {
	fs := flag.NewFlagSet("yolo internal "+backgroundAdvanceVerb, flag.ContinueOnError)
	fs.SetOutput(errw)
	var a backgroundArgs
	var keys keyList
	fs.StringVar(&a.runtime, "runtime", "", "the jail runtime the keys are built for")
	fs.StringVar(&a.platform, "platform", "", "the jail platform the keys are built for (linux/<arch>)")
	fs.Var(&keys, "key", "a built tree's <pack>/<name> (repeated)")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	a.keys = keys
	switch {
	case a.runtime == "" || a.platform == "" || len(a.keys) == 0:
		return a, errors.New("--runtime, --platform and at least one --key are required")
	case !run.BackgroundBuildsOn(a.runtime):
		return a, fmt.Errorf("%s runs no background builds in this yolo — use a fresh at-launch update or `yolo capture <pack>/<name>`", runtimeName(a.runtime))
	case fs.NArg() > 0:
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return a, nil
}

// runBackgroundAdvance is `yolo internal background-advance`. It runs on the host only: inside a
// jail there is no capture store of the host's to build into, and a launch never starts it there.
func runBackgroundAdvance(args []string) int {
	if config.InJail() {
		fmt.Fprintln(os.Stderr, "yolo internal "+backgroundAdvanceVerb+": runs on the host only — a fresh `yolo` "+
			"launch on the host starts it")
		return 2
	}
	a, err := parseBackgroundArgs(args, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo internal %s: %v\nusage: yolo internal %s --runtime=<runtime> "+
			"--platform=linux/<arch> --key=<pack>/<name> [--key=…]\n", backgroundAdvanceVerb, err, backgroundAdvanceVerb)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), backgroundSignals...)
	defer stop()
	fmt.Fprintf(os.Stdout, "=== background advance started %s (yolo pid %d): %s\n",
		time.Now().Format(time.RFC3339), os.Getpid(), strings.Join(a.keys, ", "))
	runBackgroundAdvanceUnder(ctx, a, os.Stdout)
	fmt.Fprintf(os.Stdout, "=== background advance ended %s (yolo pid %d)\n", time.Now().Format(time.RFC3339), os.Getpid())
	return 0
}

// runBackgroundAdvanceUnder is the background advance under ctx, its lines on w: every key resolved
// from the user config, and each one's check and advance at once in a pool of its own (treepool.go).
func runBackgroundAdvanceUnder(ctx context.Context, a backgroundArgs, w io.Writer) {
	want := map[string]bool{}
	for _, k := range a.keys {
		want[k] = true
	}
	var trees []packload.Fork
	found := map[string]bool{}
	// Selection may itself materialize a fetched pack whose checkout is incomplete. Apply the
	// background policy BEFORE entering that resolver too, not only to the later tree advance.
	var deferred error
	sel := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		p, err := resolveConfiguredPackWithSpec(e, config.ResolvePackSpec{Ctx: ctx, NoWait: true, Detached: true})
		if errors.Is(err, packsrc.ErrLockHeld) {
			deferred = err
		}
		return p, err
	}, config.UserScopeSelection())
	if sel.loadErr == nil {
		for _, f := range packload.PatchedTrees(sel.packs) {
			if want[f.Key()] && !found[f.Key()] {
				trees, found[f.Key()] = append(trees, f), true
			}
		}
	}
	for _, k := range a.keys {
		if found[k] {
			continue
		}
		withBackgroundKey(k, "extension "+k, w, func(o *backgroundOutcome) {
			switch {
			case ctx.Err() != nil:
				o.State = bgInterrupted
				fmt.Fprintf(w, "extension %s: selection interrupted — a launch with next-launch updates retries it\n", k)
			case deferred != nil:
				o.State, o.Error = bgSkipped, oneLineErr(deferred)
				fmt.Fprintf(w, "extension %s: selection deferred — %s; the next launch retries it\n", k, o.Error)
			default:
				o.State = bgUnresolved
				fmt.Fprintf(w, "extension %s: not among your user config's packs' extensions — `yolo capture %s` builds it\n", k, k)
			}
		})
	}
	runTreesUnder(ctx, trees, a.runtime, w, w, func(_ int, f packload.Fork, lane treeLane) {
		withBackgroundKey(f.Key(), f.Label(), lane.errw, func(o *backgroundOutcome) {
			res := treeAdvance(f, lane.options(advanceOptions{platform: a.platform, runtime: a.runtime, launch: true,
				background: true}))
			backgroundResult(o, res, ctx.Err() != nil)
		})
	})
}

// withBackgroundKey runs fn for key under its background lock, tried once — another background
// advance running it skips it here — with its outcome record written as running first and as fn left
// it last.
func withBackgroundKey(key, label string, w io.Writer, fn func(*backgroundOutcome)) {
	lockPath := backgroundKeyLock(key)
	lk, err := pidlock.Acquire(lockPath, pidlock.NoWait, nil)
	if err != nil {
		fmt.Fprintf(w, "%s: skipped — another background advance (pid %d) runs it\n", label, pidlock.Holder(lockPath))
		return
	}
	defer lk.Release()
	o := backgroundOutcome{Key: key, Label: label, State: bgRunning, Pid: os.Getpid(), At: patchedNow().Unix(),
		Log: backgroundAdvanceLog()}
	if err := writeBackgroundOutcome(o); err != nil {
		fmt.Fprintf(w, "%s: could not record that it runs: %v\n", label, err)
	}
	fn(&o)
	o.Pid = 0
	if err := writeBackgroundOutcome(o); err != nil {
		fmt.Fprintf(w, "%s: could not record its outcome: %v — the next launch says it did not finish\n", label, err)
	}
}

// backgroundResult sets o from a background advance's result: a move, an interrupt, a skip, a
// retained build, a failure, or nothing to do.
func backgroundResult(o *backgroundOutcome, res advanceResult, interrupted bool) {
	switch {
	case res.built && !res.lost && res.to != "":
		o.State, o.From, o.To = bgMoved, res.from, res.to
	case interrupted:
		o.State = bgInterrupted
	case res.skipped != "":
		o.State = bgSkipped
	case res.retained != "":
		o.State, o.Retained = bgRetained, res.retained
	case res.failWhat != "":
		o.State, o.What, o.Error, o.RetryAt = bgFailed, res.failWhat, res.failErr, res.retryAt.Unix()
	case res.failed || res.problem != "" || res.delivery.Key == "" && res.delivery.Reason != "":
		o.State, o.Error = bgFailed, res.problem
		if o.Error == "" {
			o.Error = res.delivery.Reason
		}
		if o.Error == "" {
			o.Error = "its check or replay failed"
		}
		o.RetryAt = time.Unix(o.At, 0).Add(packsrc.BranchRefreshInterval).Unix()
	default:
		o.State = bgNothing
	}
}

// writeBackgroundOutcome writes o's record by rename, so a reader never sees half of one.
func writeBackgroundOutcome(o backgroundOutcome) error {
	path := backgroundOutcomePath(o.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".outcome-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func readBackgroundOutcome(path string) (backgroundOutcome, error) {
	var o backgroundOutcome
	data, err := os.ReadFile(path)
	if err != nil {
		return o, err
	}
	return o, json.Unmarshal(data, &o)
}

// noteBackgroundOutcome is THE NEXT LAUNCH'S REPORT of f's background advance (XB-D20), said once:
// the record is claimed by a rename, so of two launches one says it. One still running says so and
// is left for a later launch. It reports again when the advance did not finish — killed, or stopped
// by a signal — so this launch starts another.
func noteBackgroundOutcome(f packload.Fork, pr richtext.Printer) (again bool) {
	path := backgroundOutcomePath(f.Key())
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if pidlock.Held(backgroundKeyLock(f.Key())) {
		o, err := readBackgroundOutcome(path)
		if err != nil {
			return false
		}
		pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the background advance started at %s is still "+
			"checking and building it — its log is %s", f.Label(), clock(o.At), o.Log)))
		return false
	}
	// Claim into a unique path, not the shared .said: a second launch claiming a newer outcome
	// must not replace the bytes this launch is about to read.
	claimed, err := os.CreateTemp(filepath.Dir(path), ".claimed-*")
	if err != nil {
		return false
	}
	claimed.Close()
	defer os.Remove(claimed.Name())
	if err := os.Rename(path, claimed.Name()); err != nil {
		return false // another launch claimed it
	}
	o, err := readBackgroundOutcome(claimed.Name())
	_ = os.Rename(claimed.Name(), backgroundSaidPath(f.Key()))
	if err != nil {
		return true
	}
	warn := func(s string) { pr.Printf("[yellow]%s[/yellow]", richtext.Escape("⚠ "+s)) }
	switch o.State {
	case bgMoved:
		was := ""
		if o.From != "" {
			was = " (was " + o.From + ")"
		}
		pr.Printf("[bold]%s[/bold]", richtext.Escape(fmt.Sprintf("%s: the background advance at %s built %s%s; available "+
			"for this launch", f.Label(), clock(o.At), o.To, was)))
	case bgFailed:
		what := "could not update it"
		if o.What != "" {
			what = "could not build " + o.What
		}
		warn(fmt.Sprintf("%s: the background advance at %s %s: %s — its output is in %s; it is retried after %s, or "+
			"`yolo capture %s` now", f.Label(), clock(o.At), what, o.Error, o.Log,
			time.Unix(o.RetryAt, 0).Local().Format("2006-01-02 15:04"), f.CaptureArg()))
	case bgRetained:
		pr.Printf("%s", richtext.Escape(fmt.Sprintf("%s: the background advance at %s left %s", f.Label(), clock(o.At),
			o.Retained)))
	case bgUnresolved:
		pr.Printf("%s", richtext.Escape(fmt.Sprintf("%s: the background advance could not find it in your user "+
			"config's packs — `yolo capture %s` builds it", f.Label(), f.CaptureArg())))
	case bgRunning:
		if pidlock.Held(backgroundKeyLock(f.Key())) {
			// An advance may have started after the initial lock probe. Its final write will
			// publish another outcome; do not mistake its running record for a dead process.
			pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the background advance is still checking and "+
				"building it — its log is %s", f.Label(), o.Log)))
			return false
		}
		fallthrough
	case bgInterrupted:
		warn(fmt.Sprintf("%s: the background advance started at %s did not finish — its output is in %s; a launch "+
			"with next-launch updates retries it", f.Label(), clock(o.At), o.Log))
		return true
	}
	return false
}

// clock is a record's time as a line names it: the hour and minute, local.
func clock(unix int64) string { return time.Unix(unix, 0).Local().Format("15:04") }
