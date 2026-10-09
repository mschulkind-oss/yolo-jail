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
	"reflect"
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
	// Generation is this key's monotonic background-outcome generation, allocated under its key lock.
	Generation uint64 `json:"generation"`
	// PatchFailure is classified application evidence, independent of CheckRecord persistence.
	PatchFailure *packsrc.PatchFailure `json:"patch_failure,omitempty"`
	// Resolution is durable proof of an exact foreground replay that repaired this generation.
	Resolution *backgroundResolution `json:"resolution,omitempty"`
}

// backgroundResolution binds repair proof to one detached generation and the successful guarded
// foreground replay/check that recorded the exact selected target cleanly.
type backgroundResolution struct {
	Generation    uint64              `json:"generation"`
	Owner         string              `json:"owner"`
	Inputs        packsrc.CheckInputs `json:"inputs"`
	Series        string              `json:"series"`
	Target        packsrc.ListEntry   `json:"target"`
	FailureSeq    int64               `json:"failure_seq"`
	FailureKind   string              `json:"failure_kind"`
	FailureMember string              `json:"failure_member,omitempty"`
	FailurePaths  []string            `json:"failure_paths,omitempty"`
	CheckSeq      int64               `json:"check_seq"`
	Yolo          string              `json:"yolo"`
	Git           string              `json:"git"`
}

type backgroundRepairToken struct {
	Key        string
	Generation uint64
	Failure    packsrc.PatchFailure
	CheckSeq   int64
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

func backgroundArtifactLock(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".artifacts.lock")
}

func backgroundOutcomePath(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".json")
}

func backgroundSaidPath(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(key)+".said")
}

func backgroundClaimPattern(key string) string {
	return filepath.Join(paths.BackgroundAdvanceDir(), ".claimed-"+run.PatchedCopySlug(key)+"-*")
}

// These pass-through filesystem seams keep the notice's claim failure paths testable in process.
var (
	createBackgroundClaim          = os.CreateTemp
	publishBackgroundClaim         = os.Rename
	publishBackgroundEvidenceClaim = os.Link
	// The callback is always invoked before taking the artifact lock, so tests can pause a prepared
	// transition without extending the per-key exclusion.
	backgroundArtifactTestHook = func(phase, key string) {}
)

func withBackgroundArtifact(key string, mode pidlock.Mode, phase string, fn func() error) error {
	backgroundArtifactTestHook(phase, key)
	lk, err := pidlock.Acquire(backgroundArtifactLock(key), mode, nil)
	if err != nil {
		return err
	}
	defer lk.Release()
	return fn()
}

func backgroundArtifactWait(ctx context.Context) pidlock.Mode {
	mode := pidlock.Wait
	if ctx != nil {
		mode.Cancel = ctx.Done()
	}
	return mode
}

func backgroundArtifactMode(ctx context.Context, background bool) pidlock.Mode {
	if background {
		return pidlock.NoWait
	}
	return backgroundArtifactWait(ctx)
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

// withBackgroundKey runs fn under the long background lock, while every outcome transition holds
// the separate artifact lock only for its bounded read/replace transaction.
func withBackgroundKey(key, label string, w io.Writer, fn func(*backgroundOutcome)) {
	lockPath := backgroundKeyLock(key)
	lk, err := pidlock.Acquire(lockPath, pidlock.NoWait, nil)
	if err != nil {
		fmt.Fprintf(w, "%s: skipped — another background advance (pid %d) runs it\n", label, pidlock.Holder(lockPath))
		return
	}
	defer lk.Release()

	var o backgroundOutcome
	var pendingGeneration uint64
	startErr := withBackgroundArtifact(key, pidlock.NoWait, "background.start", func() error {
		files, err := backgroundOutcomeFilesUnlocked(key)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.Path == backgroundOutcomePath(key) && file.Outcome.State == bgFailed && file.Outcome.PatchFailure != nil {
				pendingGeneration = file.Outcome.Generation
				return errBackgroundFailurePending
			}
		}
		generation, err := nextBackgroundGeneration(files)
		if err != nil {
			return err
		}
		o = backgroundOutcome{Key: key, Label: label, State: bgRunning, Pid: os.Getpid(), At: patchedNow().Unix(),
			Generation: generation, Log: backgroundAdvanceLog()}
		return writeBackgroundOutcomeAt(backgroundOutcomePath(key), o)
	})
	if startErr != nil {
		if errors.Is(startErr, errBackgroundFailurePending) {
			fmt.Fprintf(w, "%s: kept its unclaimed typed failure generation %d in .json — the next fresh launch claims it and recovers it\n",
				label, pendingGeneration)
		} else {
			fmt.Fprintf(w, "%s: could not establish its background outcome generation (%v) — its next launch retries the check\n", label, startErr)
		}
		return
	}

	fn(&o)
	o.Pid = 0
	finishErr := withBackgroundArtifact(key, pidlock.NoWait, "background.finish", func() error {
		files, err := backgroundOutcomeFilesUnlocked(key)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.Path != backgroundOutcomePath(key) || file.Outcome.Key != key || file.Outcome.Generation != o.Generation ||
				file.Outcome.Pid != os.Getpid() || file.Outcome.State != bgRunning {
				continue
			}
			return writeBackgroundOutcomeAt(backgroundOutcomePath(key), o)
		}
		return errBackgroundOutcomeSuperseded
	})
	if finishErr != nil {
		preserveErr := preserveBackgroundOutcomeClaim(o)
		if preserveErr != nil {
			fmt.Fprintf(w, "%s: could not conditionally record its background outcome (%v) and could not preserve generation %d as typed evidence (%v) — .json remains running with log %s; a fresh launch retries, and `yolo capture %s` can check it now\n",
				label, finishErr, o.Generation, preserveErr, o.Log, key)
		} else {
			fmt.Fprintf(w, "%s: could not conditionally record its background outcome (%v); retained generation %d as an immutable typed claim — the next launch recovers it; its log is %s\n",
				label, finishErr, o.Generation, o.Log)
		}
	}
}

var (
	errBackgroundFailurePending    = errors.New("unclaimed typed failure is pending")
	errBackgroundOutcomeSuperseded = errors.New("the running outcome was replaced before completion")
)

// A unique claim is the narrow, append-only exception to short-lock publication: the finish already
// allocated this generation under exclusion, and a nonblocking finish could not replace its .json.
// Publish only a fully written, closed payload at a new key-owned path with atomic no-replace link;
// never mutate, move or replace existing authority here. Recovery validates the payload normally.
func preserveBackgroundOutcomeClaim(o backgroundOutcome) error {
	dir := paths.BackgroundAdvanceDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".outcome-pending-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	claim := filepath.Join(dir, ".claimed-"+run.PatchedCopySlug(o.Key)+"-"+filepath.Base(tmpPath))
	if err := publishBackgroundEvidenceClaim(tmpPath, claim); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Remove(tmpPath)
}

// backgroundResult sets o from a background advance's result: a move, an interrupt, a skip, a
// retained build, a failure, or nothing to do.
func backgroundResult(o *backgroundOutcome, res advanceResult, interrupted bool) {
	o.PatchFailure = cloneBackgroundPatchFailure(res.patchFailure, o.Log)
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
	if o.PatchFailure != nil && o.State == bgFailed {
		if o.Error == "" {
			o.Error = o.PatchFailure.Error()
		}
		if o.RetryAt == 0 {
			o.RetryAt = time.Unix(o.At, 0).Add(packsrc.BranchRefreshInterval).Unix()
		}
	}
}

func cloneBackgroundPatchFailure(failure *packsrc.PatchFailure, log string) *packsrc.PatchFailure {
	if failure == nil {
		return nil
	}
	copy := *failure
	copy.Paths = append([]string(nil), failure.Paths...)
	if copy.Log == "" {
		copy.Log = log
	}
	return &copy
}

// sameBackgroundPatchFailureIdentity excludes only Log, which is an outcome annotation rather
// than classified failure data; all other fields, including nil-versus-empty slices, stay exact.
func sameBackgroundPatchFailureIdentity(left, right *packsrc.PatchFailure) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftCopy, rightCopy := *left, *right
	leftCopy.Log, rightCopy.Log = "", ""
	return reflect.DeepEqual(leftCopy, rightCopy)
}

// writeBackgroundOutcome writes o's record by rename, so a reader never sees half of one.
func writeBackgroundOutcome(o backgroundOutcome) error {
	return writeBackgroundOutcomeAt(backgroundOutcomePath(o.Key), o)
}

var writeBackgroundOutcomeFile = writeBackgroundOutcomeAtomic

func writeBackgroundOutcomeAt(path string, o backgroundOutcome) error {
	return writeBackgroundOutcomeFile(path, o)
}

func writeBackgroundOutcomeAtomic(path string, o backgroundOutcome) error {
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

type backgroundOutcomeFile struct {
	Path    string
	Outcome backgroundOutcome
}

// backgroundOutcomeFiles reads only the records and key-owned claims for key under the same short
// exclusion used by publication. A failed acquire/read is unavailable authority, never a clean set.
func backgroundOutcomeFiles(key string) ([]backgroundOutcomeFile, error) {
	var files []backgroundOutcomeFile
	err := withBackgroundArtifact(key, pidlock.Wait, "artifacts.read", func() error {
		var err error
		files, err = backgroundOutcomeFilesUnlocked(key)
		return err
	})
	return files, err
}

// backgroundOutcomeFilesUnlocked is called while the key's artifact lock is held. A finish that
// loses a NoWait acquire may concurrently add one unique immutable claim without replacement; an
// empty glob is not a repair proof. Generation allocation additionally remains serialized by the
// long background key lock while such a finish is publishing.
func backgroundOutcomeFilesUnlocked(key string) ([]backgroundOutcomeFile, error) {
	paths := []string{backgroundOutcomePath(key), backgroundSaidPath(key)}
	claims, err := filepath.Glob(backgroundClaimPattern(key))
	if err != nil {
		return nil, err
	}
	paths = append(paths, claims...)
	files := make([]backgroundOutcomeFile, 0, len(paths))
	var readErrs []error
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		o, err := readBackgroundOutcome(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			readErrs = append(readErrs, fmt.Errorf("reading %s: %w", path, err))
			continue
		}
		if o.Key == key {
			files = append(files, backgroundOutcomeFile{Path: path, Outcome: o})
		}
	}
	return files, errors.Join(readErrs...)
}

func nextBackgroundGeneration(files []backgroundOutcomeFile) (uint64, error) {
	var latest uint64
	for _, file := range files {
		if file.Outcome.Generation > latest {
			latest = file.Outcome.Generation
		}
	}
	if latest == ^uint64(0) {
		return 0, errors.New("background outcome generation is exhausted")
	}
	return latest + 1, nil
}

func latestBackgroundPatchFailure(files []backgroundOutcomeFile) *backgroundOutcomeFile {
	var latest *backgroundOutcomeFile
	for i := range files {
		file := &files[i]
		if file.Outcome.State != bgFailed || file.Outcome.PatchFailure == nil {
			continue
		}
		if latest == nil || file.Outcome.Generation > latest.Outcome.Generation {
			latest = file
			continue
		}
		if file.Outcome.Generation < latest.Outcome.Generation {
			continue
		}
		if file.Outcome.Resolution != nil && latest.Outcome.Resolution == nil ||
			(file.Outcome.Resolution != nil) == (latest.Outcome.Resolution != nil) &&
				backgroundOutcomePathRank(file.Path) > backgroundOutcomePathRank(latest.Path) {
			latest = file
		}
	}
	return latest
}

func backgroundOutcomePathRank(path string) int {
	switch {
	case strings.HasSuffix(path, ".json"):
		return 2
	case strings.HasPrefix(filepath.Base(path), ".claimed-"):
		return 1
	default:
		return 0
	}
}

// observeBackgroundRepairToken captures the detached failure that the upcoming replay may repair.
// It binds generation and failure identity before WalkSeries/RecordReplay, under the same artifact
// exclusion later used for conditional marker publication.
func observeBackgroundRepairToken(key string, store *packsrc.Store, snapshot packsrc.ReplaySnapshot,
	list []packsrc.ListEntry, ctx context.Context, background bool) (*backgroundRepairToken, error) {
	if store == nil || snapshot.Owner != key || snapshot.Series == "" || snapshot.Seq <= 0 {
		return nil, nil
	}
	var token *backgroundRepairToken
	err := withBackgroundArtifact(key, backgroundArtifactMode(ctx, background), "repair.observe", func() error {
		fresh, err := store.LoadCheckRecord(key)
		if err != nil || fresh == nil || fresh.Owner != key || fresh.Seq != snapshot.Seq ||
			fresh.Read != snapshot.Inputs || fresh.Check == nil || fresh.Check.Seq != snapshot.Seq {
			return err
		}
		files, err := backgroundOutcomeFilesUnlocked(key)
		if err != nil {
			return err
		}
		candidate := latestBackgroundPatchFailure(files)
		if candidate == nil || candidate.Outcome.Generation == 0 {
			return nil
		}
		failure := cloneBackgroundPatchFailure(candidate.Outcome.PatchFailure, candidate.Outcome.Log)
		if failure.Owner != key || failure.Inputs != snapshot.Inputs || failure.Series != snapshot.Series ||
			failure.Seq <= 0 || failure.Seq > snapshot.Seq || failure.Target.Commit == "" ||
			!selectedInList(list, failure.Target) || backgroundResolutionCurrent(candidate.Outcome, failure, fresh, snapshot.Inputs, snapshot.Series) {
			return nil
		}
		if fresh.PatchFailure != nil && !sameBackgroundPatchFailureIdentity(fresh.PatchFailure, failure) {
			return nil
		}
		if current := fresh.CurrentPatchFailure(snapshot.Inputs, snapshot.Series); current != nil &&
			!sameBackgroundPatchFailureIdentity(current, failure) {
			return nil
		}
		token = &backgroundRepairToken{Key: key, Generation: candidate.Outcome.Generation,
			Failure: *failure, CheckSeq: snapshot.Seq}
		return nil
	})
	return token, err
}

func selectedInList(list []packsrc.ListEntry, target packsrc.ListEntry) bool {
	for _, entry := range list {
		if entry == target {
			return true
		}
	}
	return false
}

// recordBackgroundPatchRepair marks only the detached generation observed before this exact replay,
// after its guarded RecordReplay and fresh-record verification. A newer artifact is never adopted as
// the repair target, even when it carries identical failure text.
func recordBackgroundPatchRepair(ctx context.Context, token *backgroundRepairToken, store *packsrc.Store,
	snapshot packsrc.ReplaySnapshot, yolo string, walk packsrc.WalkResult, recorded packsrc.RecordReplayResult,
	background bool) error {
	if token == nil || token.Key != snapshot.Owner || token.CheckSeq != snapshot.Seq ||
		token.Failure.Owner != snapshot.Owner || token.Failure.Inputs != snapshot.Inputs ||
		token.Failure.Series != snapshot.Series || recorded.Err != nil || !recorded.Recorded ||
		recorded.Failure != nil || walk.Err != nil || walk.PatchFailure() != nil || snapshot.Series == "" || snapshot.Seq <= 0 {
		return nil
	}
	var target packsrc.ListEntry
	foundClean := false
	for _, result := range walk.Results {
		if result.Clean && result.Err == nil && result.Conflict == nil && result.PatchFailure == nil && result.Entry == token.Failure.Target {
			target, foundClean = result.Entry, true
			break
		}
	}
	if !foundClean || target.Commit == "" || walk.Git == "" {
		return nil
	}
	resolution := &backgroundResolution{Generation: token.Generation, Owner: snapshot.Owner, Inputs: snapshot.Inputs,
		Series: snapshot.Series, Target: target, FailureSeq: token.Failure.Seq, FailureKind: token.Failure.Kind,
		FailureMember: token.Failure.Member, FailurePaths: append([]string(nil), token.Failure.Paths...),
		CheckSeq: snapshot.Seq, Yolo: yolo, Git: walk.Git}
	return withBackgroundArtifact(token.Key, backgroundArtifactMode(ctx, background), "repair.publish", func() error {
		fresh, err := store.LoadCheckRecord(token.Key)
		if err != nil {
			return fmt.Errorf("reading the recorded clean replay: %w", err)
		}
		if fresh == nil || fresh.Owner != snapshot.Owner || fresh.Seq != snapshot.Seq || fresh.Read != snapshot.Inputs ||
			fresh.Check == nil || fresh.Check.Seq != snapshot.Seq || fresh.CurrentPatchFailure(snapshot.Inputs, snapshot.Series) != nil ||
			!selectedByCheck(fresh.Check, target, snapshot.Inputs.Base) ||
			!hasAppliedOutcome(fresh, target.Commit, snapshot.Series, yolo, walk.Git) {
			return nil
		}
		files, err := backgroundOutcomeFilesUnlocked(token.Key)
		if err != nil {
			return fmt.Errorf("reading detached failure evidence: %w", err)
		}
		var updated bool
		var writeErrs []error
		for _, file := range files {
			o := file.Outcome
			if o.Generation != token.Generation || o.State != bgFailed || o.PatchFailure == nil ||
				!reflect.DeepEqual(o.PatchFailure, &token.Failure) {
				continue
			}
			if o.Resolution != nil {
				if reflect.DeepEqual(o.Resolution, resolution) {
					updated = true
				}
				continue
			}
			updated = true
			o.Resolution = resolution
			if err := writeBackgroundOutcomeAt(file.Path, o); err != nil {
				writeErrs = append(writeErrs, fmt.Errorf("updating %s: %w", file.Path, err))
			}
		}
		if !updated {
			return nil
		}
		return errors.Join(writeErrs...)
	})
}

func selectedByCheck(check *packsrc.CheckFound, target packsrc.ListEntry, base string) bool {
	if check == nil {
		return false
	}
	for _, entry := range check.List {
		if entry == target {
			return true
		}
	}
	return target.Commit == base && check.BaseOnBranch
}

func hasAppliedOutcome(record *packsrc.CheckRecord, commit, series, yolo, git string) bool {
	if record == nil {
		return false
	}
	for _, outcome := range record.Outcomes {
		if outcome.Commit == commit && outcome.Kind == packsrc.OutcomeApplies && outcome.Series == series &&
			outcome.Yolo == yolo && outcome.Git == git {
			return true
		}
	}
	return false
}

func backgroundResolutionCurrent(outcome backgroundOutcome, failure *packsrc.PatchFailure,
	fresh *packsrc.CheckRecord, inputs packsrc.CheckInputs, series string) bool {
	resolution := outcome.Resolution
	if resolution == nil || outcome.Generation == 0 || resolution.Generation != outcome.Generation ||
		resolution.Owner != outcome.Key || resolution.Owner != failure.Owner || resolution.Inputs != failure.Inputs ||
		resolution.Inputs != inputs || resolution.Series != failure.Series || resolution.Series != series ||
		resolution.Target != failure.Target || resolution.FailureSeq != 0 && resolution.FailureSeq != failure.Seq ||
		resolution.FailureKind != "" && resolution.FailureKind != failure.Kind ||
		resolution.FailureMember != "" && resolution.FailureMember != failure.Member ||
		len(resolution.FailurePaths) != 0 && !reflect.DeepEqual(resolution.FailurePaths, failure.Paths) ||
		resolution.CheckSeq <= 0 || resolution.CheckSeq > fresh.Seq ||
		fresh.Owner != resolution.Owner || fresh.Read != resolution.Inputs || fresh.Check == nil || fresh.Check.Seq != fresh.Seq ||
		!selectedByCheck(fresh.Check, resolution.Target, resolution.Inputs.Base) ||
		!hasAppliedOutcome(fresh, resolution.Target.Commit, resolution.Series, resolution.Yolo, resolution.Git) {
		return false
	}
	return true
}

func readBackgroundOutcome(path string) (backgroundOutcome, error) {
	var o backgroundOutcome
	data, err := os.ReadFile(path)
	if err != nil {
		return o, err
	}
	return o, json.Unmarshal(data, &o)
}

// noteBackgroundOutcome is THE NEXT LAUNCH'S REPORT of f's background advance (XB-D20). Claim and
// publication are separate short transactions: a delayed claim cannot replace a newer generation.
func noteBackgroundOutcome(f packload.Fork, pr richtext.Printer) (again bool) {
	key := f.Key()
	warn := func(s string) { pr.Printf("[yellow]%s[/yellow]", richtext.Escape("⚠ "+s)) }
	var claimPath string
	var claimedOutcome backgroundOutcome
	var running bool
	claimErr := withBackgroundArtifact(key, pidlock.NoWait, "notice.claim", func() error {
		path := backgroundOutcomePath(key)
		o, err := readBackgroundOutcome(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading its background outcome: %w", err)
		}
		if pidlock.Held(backgroundKeyLock(key)) {
			claimedOutcome, running = o, true
			return nil
		}
		claimed, err := createBackgroundClaim(filepath.Dir(path), filepath.Base(backgroundClaimPattern(key)))
		if err != nil {
			return fmt.Errorf("creating its background outcome claim: %w", err)
		}
		claimPath = claimed.Name()
		if err := claimed.Close(); err != nil {
			_ = os.Remove(claimPath)
			claimPath = ""
			return fmt.Errorf("closing its background outcome claim: %w", err)
		}
		if err := os.Rename(path, claimPath); err != nil {
			_ = os.Remove(claimPath)
			claimPath = ""
			return fmt.Errorf("claiming its background outcome: %w", err)
		}
		return nil
	})
	if claimErr != nil {
		warn(fmt.Sprintf("%s: could not claim its background outcome (%v) — the .json remains for foreground recovery, and the next launch retries", f.Label(), claimErr))
		return false
	}
	if running {
		pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the background advance started at %s is still checking and building it — its log is %s",
			f.Label(), clock(claimedOutcome.At), claimedOutcome.Log)))
		return false
	}
	if claimPath == "" {
		return false
	}
	claimedOutcome, err := readBackgroundOutcome(claimPath)
	if err != nil {
		warn(fmt.Sprintf("%s: could not read its claimed background outcome (%v) — the claim is preserved for recovery", f.Label(), err))
		return false
	}

	publishErr := withBackgroundArtifact(key, pidlock.NoWait, "notice.publish", func() error {
		latest, err := readBackgroundOutcome(claimPath)
		if err != nil {
			return fmt.Errorf("reading its claimed background outcome: %w", err)
		}
		claimedOutcome = latest
		if claimedOutcome.Key != key {
			return errors.New("the claimed background outcome belongs to another key")
		}
		saidPath := backgroundSaidPath(key)
		said, err := readBackgroundOutcome(saidPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading the current .said outcome: %w", err)
		}
		if err == nil {
			if said.Generation > claimedOutcome.Generation ||
				said.Generation == claimedOutcome.Generation && said.Resolution != nil && claimedOutcome.Resolution == nil ||
				said.Generation == claimedOutcome.Generation && said.Resolution != nil && claimedOutcome.Resolution != nil &&
					!reflect.DeepEqual(said.Resolution, claimedOutcome.Resolution) {
				return errBackgroundNoticeSuperseded
			}
		}
		if err := publishBackgroundClaim(claimPath, saidPath); err != nil {
			return fmt.Errorf("publishing its background outcome as .said: %w", err)
		}
		return nil
	})
	if errors.Is(publishErr, errBackgroundNoticeSuperseded) {
		return false
	}
	if publishErr != nil {
		warn(fmt.Sprintf("%s: could not publish its background outcome as .said (%v) — its typed claim remains at %s for foreground recovery",
			f.Label(), publishErr, claimPath))
	}
	switch claimedOutcome.State {
	case bgMoved:
		was := ""
		if claimedOutcome.From != "" {
			was = " (was " + claimedOutcome.From + ")"
		}
		pr.Printf("[bold]%s[/bold]", richtext.Escape(fmt.Sprintf("%s: the background advance at %s built %s%s; available for this launch",
			f.Label(), clock(claimedOutcome.At), claimedOutcome.To, was)))
	case bgFailed:
		what := "could not update it"
		if claimedOutcome.What != "" {
			what = "could not build " + claimedOutcome.What
		}
		cause := claimedOutcome.Error
		if claimedOutcome.PatchFailure != nil {
			cause = claimedOutcome.PatchFailure.Error()
			if claimedOutcome.Error != "" && claimedOutcome.Error != cause {
				cause += "; " + claimedOutcome.Error
			}
		}
		warn(fmt.Sprintf("%s: the background advance at %s %s: %s — its output is in %s; it is retried after %s, or `yolo capture %s` now",
			f.Label(), clock(claimedOutcome.At), what, cause, claimedOutcome.Log,
			time.Unix(claimedOutcome.RetryAt, 0).Local().Format("2006-01-02 15:04"), f.CaptureArg()))
	case bgRetained:
		pr.Printf("%s", richtext.Escape(fmt.Sprintf("%s: the background advance at %s left %s", f.Label(), clock(claimedOutcome.At),
			claimedOutcome.Retained)))
	case bgUnresolved:
		pr.Printf("%s", richtext.Escape(fmt.Sprintf("%s: the background advance could not find it in your user config's packs — `yolo capture %s` builds it",
			f.Label(), f.CaptureArg())))
	case bgRunning:
		if pidlock.Held(backgroundKeyLock(key)) {
			pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the background advance is still checking and building it — its log is %s", f.Label(), claimedOutcome.Log)))
			return false
		}
		fallthrough
	case bgInterrupted:
		warn(fmt.Sprintf("%s: the background advance started at %s did not finish — its output is in %s; a launch with next-launch updates retries it",
			f.Label(), clock(claimedOutcome.At), claimedOutcome.Log))
		return true
	}
	return false
}

var errBackgroundNoticeSuperseded = errors.New("a newer background outcome already occupies .said")

// clock is a record's time as a line names it: the hour and minute, local.
func clock(unix int64) string { return time.Unix(unix, 0).Local().Format("15:04") }
