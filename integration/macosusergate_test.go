package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE macos-user GATE — and the one property the whole suite rests on:
// A SUITE THAT SKIPS MUST NOT BE INDISTINGUISHABLE FROM A SUITE THAT PASSES.
//
// THE PROBLEM. The macos-user backend is darwin-only and privilege-bearing: every
// crossing into the sandbox account is `/usr/bin/sudo -n --user=_yolojail /usr/bin/env …`
// (macosuser.RunAsSandboxArgv), through the one rule `yolo yolo-account create` installs, and
// confines what follows with `sandbox-exec`. No machine that develops this repo can
// run one — the development jail is Linux and has no `sudo` at all — so every test
// written against that backend SKIPS everywhere it is normally run. `go test` reports
// a skip as a pass. Left there, the reward for writing thirty of these tests is a
// green CI job that asserts nothing, and nobody looks at a green job.
//
// THE ANSWER IS THE ONE THIS REPO ALREADY USES on its macOS nightly, where a computed
// shard that selects no tests fails on purpose rather than passing vacuously
// (.github/workflows/nightly-macos.yml, "A SHARD THAT RUNS NOTHING MUST NOT BE
// GREEN"). Here it is three parts:
//
//  1. requireMacosUser(t) is the gate. It records every test's FINAL disposition —
//     executed or skipped, and for a skip, why — in a ledger, observed from a
//     t.Cleanup so a test that passes the gate and then skips for a reason of its own
//     cannot be miscounted as executed.
//  2. A run DECLARES itself the macos-user job by setting YOLO_TEST_MACOS_USER. The
//     declaration is the CI step's, because only the step knows what it was scheduled
//     to do: a developer's `just test` on Linux is supposed to skip these, and a job
//     that exists to run them is not.
//  3. TestMain turns a declared run that executed ZERO of them into a non-zero exit,
//     printing the per-test reasons. The EXIT CODE is the mechanism — not a log line
//     a CI step greps, because a grep pattern living in YAML is a second copy of the
//     count that can silently stop matching, which is exactly the failure this file
//     exists to remove (see imagebuildfailure_test.go's header for the same argument
//     about a harness guard that quietly stops matching).
//
// The decision half is PURE and covered under -short, on Linux, for that same
// reason: a skip-counter that stops counting returns this suite to the behavior it
// was built to end. macosUserVacuityVerdict and macosUserHost.gate are plain
// functions over plain data, and TestMacosUserGateFailsARunThatExecutedNothing
// drives the REAL TestMain in a subprocess so the call site is pinned too — a
// verdict function nothing calls is the shape AGENTS.md names as "not a test".
//
// NAMING IS PART OF THE GATE. Every test behind it must be called TestMacosUser…,
// enforced by the gate itself, so a CI step can select the whole suite with
// `-run '^TestMacosUser'` and that partition is exhaustive BY CONSTRUCTION rather
// than by a list somebody maintains.

// macosUserDeclareEnv is how a run says "I am the macos-user job, and executing none
// of these tests is a failure". Unset — every ordinary `go test` — the ledger is still
// kept and still reported, but a run that skips them all is not an error: that is the
// correct outcome on Linux and on a Mac with no sandbox account.
const macosUserDeclareEnv = "YOLO_TEST_MACOS_USER"

// macosUserTestPrefix is the required name prefix (see the header: it is what makes a
// CI `-run` filter exhaustive by construction).
const macosUserTestPrefix = "TestMacosUser"

// macosUserOutcome is one test's final disposition at the gate. reason is set only
// when ran is false, and is what the vacuity report prints — "3 skipped" without the
// reasons is a number nobody can act on.
//
// gate names WHICH gate the test sits behind, and it is load-bearing rather than
// bookkeeping: this suite now has two of them with very different preconditions, and a
// run that satisfied the cheap one while every test behind the expensive one skipped is
// the vacuity failure wearing a disguise (see macosUserVacuityVerdict).
type macosUserOutcome struct {
	test   string
	ran    bool
	reason string
	gate   string
}

// The gates. macosUserGateLaunch needs the _yolojail account, a shared root and the
// run-as rule (sudo runs commands as _yolojail with no password), because it starts a
// real launch; macosUserGateSeatbelt (declared
// in macosuserseatbelt_test.go, where its own gate lives) needs only sandbox-exec.
const macosUserGateLaunch = "launch"

// macosUserLedger accumulates outcomes across a run. The mutex is not decoration
// despite this package's no-t.Parallel() rule: t.Cleanup functions can run off the
// test's own goroutine, and a data race here would be a flake in the one mechanism
// that must never be doubted.
type macosUserLedger struct {
	mu    sync.Mutex
	index map[string]int
	list  []macosUserOutcome
}

// record files an outcome, replacing any earlier one for the same test name. LAST
// WRITE WINS, which is what lets a caller record optimistically and correct itself — with
// one exception: a skip carrying only the gate's generic past-gate reason never replaces a
// skip that named its own. A host-fact check after the gate (requireUnifiedLog) records the
// reason it skipped for from its own cleanup, which runs BEFORE the gate's (cleanups run
// last-registered first), and the gate, which cannot know why, must not erase it.
func (l *macosUserLedger) record(o macosUserOutcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.index == nil {
		l.index = map[string]int{}
	}
	if i, ok := l.index[o.test]; ok {
		if prev := l.list[i]; !o.ran && !prev.ran && o.reason == macosUserSkippedPastGate &&
			prev.reason != "" && prev.reason != macosUserSkippedPastGate {
			return
		}
		l.list[i] = o
		return
	}
	l.index[o.test] = len(l.list)
	l.list = append(l.list, o)
}

func (l *macosUserLedger) snapshot() []macosUserOutcome {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]macosUserOutcome(nil), l.list...)
}

// macosUserTests is the run's ledger. Package-level because TestMain has no other way
// to reach it, which is also why the verdict below takes a snapshot rather than
// reading this: the decision stays testable without touching global state.
//
// A pointer so that fakeReadyMacosUserHost can stand a ledger of its own in for one test:
// nothing else assigns it.
var macosUserTests = &macosUserLedger{}

// macosUserSkippedPastGate is the reason a gate records for a test that passed it and
// then skipped for a reason of its own, which its own skip message gives.
const macosUserSkippedPastGate = "the test skipped after passing the gate — see its own skip message"

// macosUserVacuityVerdict decides whether a run may pass, and what to say about it.
// PURE — no globals, no env, no clock — because this is the guard, and a guard that
// cannot be tested on the machine that develops it is a guard nobody re-checks.
//
// fail is true in exactly one case: the run DECLARED itself the macos-user job and
// executed none of them. A declared run that executed one and failed it is already
// red through the ordinary channel and must not be double-reported here; an
// undeclared run that skipped everything is correct and silent.
func macosUserVacuityVerdict(declared bool, outcomes []macosUserOutcome) (report string, fail bool) {
	var ran, skipped []macosUserOutcome
	for _, o := range outcomes {
		if o.ran {
			ran = append(ran, o)
		} else {
			skipped = append(skipped, o)
		}
	}
	if !declared {
		if len(ran) == 0 && len(skipped) == 0 {
			return "", false
		}
		return fmt.Sprintf("[integration] macos-user: executed=%d skipped=%d "+
			"(%s unset, so a run that executes none of them is not an error here)",
			len(ran), len(skipped), macosUserDeclareEnv), false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[integration] macos-user: executed=%d skipped=%d\n", len(ran), len(skipped))
	for _, o := range skipped {
		fmt.Fprintf(&b, "  SKIPPED %s [%s]: %s\n", o.test, o.gate, o.reason)
	}
	for _, o := range ran {
		fmt.Fprintf(&b, "  ran     %s [%s]\n", o.test, o.gate)
	}
	if len(ran) > 0 {
		// PER GATE, AND THIS IS THE HALF THAT ARRIVED WITH THE SECOND ONE. The policy
		// suite's gate is much cheaper to satisfy than the launch gate — sandbox-exec and
		// a writable /Users/Shared, against an account plus the run-as rule — so a
		// machine where `yolo yolo-account create` silently did not take would run the policy
		// tests, skip every launch test, and report GREEN on the count above. That is
		// exactly the outcome this file exists to prevent, reintroduced by adding tests to
		// it. A gate with outcomes and no executions fails the run and says which.
		if starved := macosUserStarvedGates(outcomes); len(starved) > 0 {
			b.WriteString("\n")
			fmt.Fprintf(&b, "%s is set, and every test behind the %s gate SKIPPED while "+
				"others ran.\n", macosUserDeclareEnv, strings.Join(starved, " and "))
			b.WriteString("A gate nothing got through asserts nothing, and the executed " +
				"count above hides it behind\nthe tests that did run — so this run is " +
				"FAILED rather than reported green. The reasons are\nlisted above; fix the " +
				"environment they name.")
			return strings.TrimRight(b.String(), "\n"), true
		}
		return strings.TrimRight(b.String(), "\n"), false
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s is set, so this run was scheduled to exercise the macos-user "+
		"backend — and it exercised NONE of it.\n", macosUserDeclareEnv)
	if len(skipped) == 0 {
		fmt.Fprintf(&b, "No test even reached the gate: the -run filter selected none of "+
			"them, or every %s… test was renamed or deleted.\n", macosUserTestPrefix)
	}
	b.WriteString("A suite that skips is indistinguishable from a suite that passes, so " +
		"this run is FAILED rather\nthan reported green. Fix the environment named above, " +
		"or unset " + macosUserDeclareEnv + " on a job that\nwas never meant to run these.")
	return strings.TrimRight(b.String(), "\n"), true
}

// macosUserStarvedGates returns, in first-seen order, the gates that have outcomes and
// no executions. Split out of the verdict so the rule is one readable loop rather than
// three nested ones, and so a caller can be tested against it directly.
func macosUserStarvedGates(outcomes []macosUserOutcome) []string {
	var order []string
	ranBy, seen := map[string]int{}, map[string]bool{}
	for _, o := range outcomes {
		if !seen[o.gate] {
			seen[o.gate] = true
			order = append(order, o.gate)
		}
		if o.ran {
			ranBy[o.gate]++
		}
	}
	var starved []string
	for _, g := range order {
		if ranBy[g] == 0 {
			starved = append(starved, g)
		}
	}
	return starved
}

// macosUserExitCode applies the verdict to a finished run's exit code. Called from
// TestMain — both of its exit paths — which is the only place with the run's code and
// the completed ledger in the same scope.
//
// An already-red run keeps its own code: this guard turns a vacuous GREEN red, and
// has nothing to add to a run that is failing for a real reason.
func macosUserExitCode(code int, w io.Writer) int {
	report, fail := macosUserVacuityVerdict(os.Getenv(macosUserDeclareEnv) != "", macosUserTests.snapshot())
	if report != "" {
		fmt.Fprintln(w, report)
	}
	if fail && code == 0 {
		return 1
	}
	return code
}

// macosUserHost is what the gate observes about the machine it is running on. It is a
// struct of FACTS so that gate() — the decision — stays pure and table-testable on
// Linux, where none of the facts can be produced.
type macosUserHost struct {
	short       bool
	goos        string
	euid        int
	sandboxExec bool // sandbox-exec resolvable (built into macOS)
	sandboxUser bool // the _yolojail account exists (`yolo yolo-account create` ran)
	sharedRoot  bool // the shared root exists, so a granted workspace can be made in it
	runAsQuiet  bool // the G2 probe (macosuser.RunAsGrantProbeArgv) succeeds: sudo runs commands as _yolojail with no password
}

// gate returns the reason this host cannot run a macos-user test, or ok. The order is
// most-fundamental-first, so the reason a reader gets is the one to act on rather than
// the last one checked.
func (h macosUserHost) gate() (reason string, ok bool) {
	switch {
	case h.short:
		return "skipping under -short: this launches a real Seatbelt sandbox", false
	case h.goos != "darwin":
		return fmt.Sprintf("this backend is darwin-only and GOOS is %s — there is no "+
			"sandbox-exec, no _yolojail account and no sudo here", h.goos), false
	case h.euid == 0:
		return "running as root: the macos-user launch self-escalates and REFUSES to " +
			"start under sudo (it would misassign the per-user identity and ACL)", false
	case !h.sandboxExec:
		return "sandbox-exec is not on PATH, so nothing can be confined", false
	case !h.sandboxUser:
		return fmt.Sprintf("the %s account does not exist — run `yolo yolo-account create` "+
			"(one-time, interactive, creates a hidden system account). This suite will "+
			"not create it for you: a test run must not silently add an account to "+
			"somebody's Mac", macosuser.SandboxUser), false
	case !h.sharedRoot:
		return fmt.Sprintf("%s does not exist, so there is nowhere to put a workspace "+
			"the sandbox user can write — run `yolo yolo-account create`",
			macosuser.SharedRootDefault()), false
	case !h.runAsQuiet:
		return fmt.Sprintf("`%s` fails, so sudo cannot run commands as %s without a "+
			"password, and every macos-user launch crosses into the sandbox that way. Run "+
			"the one-time setup as your normal admin user, not with sudo: `yolo yolo-account create` "+
			"installs the one rule (%s) that runs commands as %s, never as root, and asks "+
			"for your password once", strings.Join(macosuser.RunAsGrantProbeArgv(), " "),
			macosuser.SandboxUser, macosuser.SudoersFile, macosuser.SandboxUser), false
	}
	return "", true
}

// probeMacosUserHost measures the facts gate() decides on. Impure by design and
// deliberately thin: everything that could be got wrong lives in gate().
//
// The G2 probe CANNOT HANG — -n makes sudo fail rather than prompt — which is what makes
// it safe to probe before anything else spends a password. It is the launch's own readiness
// probe, not `sudo -n true`: a launch needs only the run-as rule, and the narrow-sudo CI
// step (.github/workflows/macos-user.yml) takes root sudo away on purpose, so a root probe
// would skip the very tests that measure a launch without it. ⚠ A cached credential from a
// recent password passes this probe through a broad rule; the tests that measure the rule
// itself run `/usr/bin/sudo -k` first.
func probeMacosUserHost() macosUserHost {
	h := macosUserHost{short: testing.Short(), goos: goruntime.GOOS, euid: os.Geteuid()}
	if h.goos != "darwin" {
		return h // every remaining fact is a macOS fact; probing them elsewhere says nothing
	}
	_, err := exec.LookPath("sandbox-exec")
	h.sandboxExec = err == nil
	h.sandboxUser = runQuiet(5*time.Second, "id", macosuser.SandboxUser)
	if fi, err := os.Stat(macosuser.SharedRootDefault()); err == nil && fi.IsDir() {
		h.sharedRoot = true
	}
	h.runAsQuiet = runQuiet(20*time.Second, macosuser.RunAsGrantProbeArgv()...)
	return h
}

// runQuiet reports whether argv ran and exited 0 within d, discarding its output.
func runQuiet(d time.Duration, argv ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	return cmd.Run() == nil
}

// THE SUITE TAKES NO PRIVILEGE `yolo yolo-account create` DOES NOT INSTALL (OQ-DM1 in
// docs/design/macos-user-suite-on-a-developer-mac.md, ruled 2026-10-18). Setup installs one sudo
// rule, `<host user> ALL=(_yolojail) NOPASSWD: /usr/bin/env`, and a launch needs nothing else, so
// a test needs nothing else either: every step that acts as another account is a crossing through
// that rule, built by the launch's own builder (macosuser.RunAsSandboxArgv), and every other step
// is the invoking user's own file operation. There is no root sudo anywhere in the suite, and
// TestMacosUserSuiteSpendsNoRootSudo fails if one is spelled.

// sandboxCommand runs argv as the sandbox account through the one run-as rule —
// macosuser.RunAsSandboxArgv, `/usr/bin/sudo -n --user=_yolojail /usr/bin/env <argv…>` — started
// in "/", as the launch's own runner starts every crossing (internal/macosuser's commandFor): a
// test's cwd is under the invoking user's home, which the account cannot traverse. A bare
// `sudo --user=_yolojail <cmd>` is NOT the rule — sudo matches the command's path, and the rule
// names /usr/bin/env — so it asks for a password.
func sandboxCommand(ctx context.Context, argv ...string) *exec.Cmd {
	full := macosuser.RunAsSandboxArgv(nil, argv...)
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Dir = "/"
	cmd.Stdin = nil
	return cmd
}

// runAsSandboxQuiet is runQuiet through sandboxCommand.
func runAsSandboxQuiet(d time.Duration, argv ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return sandboxCommand(ctx, argv...).Run() == nil
}

// removeSandboxWritten removes dir, a fixture the sandbox account may have written into, with no
// root. First as the invoking user, after making every directory of ours writable again (a
// launch seals what it stages, and nobody unlinks inside a directory with no write bit); then the
// guest pass a capture's cleanup runs (macosuser.CaptureScratchGuestCommands, PL-7): as the
// sandbox account, through the run-as rule, `chmod -R u+w` on what it owns and `rm -rf`, which
// removes what it made and we cannot unlink; then as the invoking user again, for the rest. It
// returns the last removal's error.
//
// The guest pass is an `rm -rf` as another account, so every caller first checks that dir is a
// fixture it minted. The account reaches dir only where it can search every directory above it:
// under the shared root, /Users/Shared and /private/tmp it can; under the invoking user's 0700
// home it cannot, and what it wrote there is left for the caller to report.
func removeSandboxWritten(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.IsDir() {
			_ = os.Chmod(p, fi.Mode().Perm()|0o700)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	for _, cmd := range macosuser.CaptureScratchGuestCommands(macosuser.RunAs{}, dir) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		c.Dir = "/"
		_ = c.Run()
		cancel()
	}
	return os.RemoveAll(dir)
}

// removeByHandHint is the next step a cleanup names when removeSandboxWritten left something: the
// one command an administrator runs. The suite never runs it itself (OQ-DM1).
func removeByHandHint(dir string) string {
	return fmt.Sprintf("neither you nor %s could remove it; an administrator can, with "+
		"`sudo rm -rf -- '%s'`", macosuser.SandboxUser, dir)
}

// THE UNIFIED LOG STORE IS THE MAC'S POLICY, NOT A PRIVILEGE YOLO ASKS FOR (OQ-DM2 in
// docs/design/macos-user-suite-on-a-developer-mac.md, ruled 2026-10-18). A managed Mac closes the
// store to a non-admin user (`log show` answers "Could not open local log store: Operation not
// permitted"), and nothing yolo installs opens it. So a test whose control is a read of the store
// asks for it here, after its gate, and skips — countably, with this reason in the ledger — when
// this Mac refuses it. The GitHub runner's user is an admin with an open store, so CI still runs
// every such test.

// unifiedLogProbe answers whether the invoking user can read the unified log store. A variable
// only so that a -short test can stand a refusing Mac in; nothing else assigns it.
var unifiedLogProbe = probeUnifiedLogOnce

var unifiedLogOnce struct {
	sync.Once
	readable bool
	detail   string
}

// probeUnifiedLogOnce runs macosLogStoreProbe — the store read the seatbelt control makes — once
// per process, as the invoking user, from "/". The store does not open and close during a run,
// and `log show` can take seconds.
func probeUnifiedLogOnce() (readable bool, detail string) {
	unifiedLogOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", macosLogStoreProbe)
		cmd.Dir = "/"
		out, err := cmd.CombinedOutput()
		unifiedLogOnce.readable = err == nil
		unifiedLogOnce.detail = strings.TrimSpace(string(out))
		if err != nil && unifiedLogOnce.detail == "" {
			unifiedLogOnce.detail = err.Error()
		}
	})
	return unifiedLogOnce.readable, unifiedLogOnce.detail
}

// unifiedLogSkipReason is the ledger's reason for a test whose log-store control cannot run here.
// It calls the store closed only when `log` said so; any other empty read (a timeout, a quiet
// minute) is reported as what it was, since the next step differs.
func unifiedLogSkipReason(detail string) string {
	if strings.Contains(detail, "Operation not permitted") || strings.Contains(detail, "Could not open local log store") {
		return fmt.Sprintf("this Mac's unified log store is closed to you: `log show` as the invoking "+
			"user was refused (%s). That is the Mac's own policy (a managed Mac closes the store to a "+
			"non-admin user), not a privilege `yolo yolo-account create` grants, so this test's control cannot "+
			"run here; the macos-user CI job, whose user is an admin, runs it", detail)
	}
	return fmt.Sprintf("`log show` as the invoking user read no entry from the unified log store "+
		"(%s), and was not refused, so this test's control cannot run here. Run `log show --last 1m` "+
		"yourself to see why; if it lists entries, run the suite again", detail)
}

// requireUnifiedLog skips t, countably, when the invoking user cannot read the unified log store.
// It records its own outcome for t under gate, because t may be a subtest no gate recorded; on a
// top-level test the gate's own cleanup keeps this reason (macosUserLedger.record).
func requireUnifiedLog(t *testing.T, gate string) {
	t.Helper()
	readable, detail := unifiedLogProbe()
	if readable {
		return
	}
	macosUserSkipCountably(t, gate, unifiedLogSkipReason(detail))
}

// macosUserSkipCountably skips t for a fact about this host that a test learns after its gate,
// and files that reason in the ledger under gate — from its own cleanup, because t may be a
// subtest no gate records; on a top-level test the gate's own cleanup keeps this reason
// (macosUserLedger.record), so the run's report says why rather than "see its own skip message".
func macosUserSkipCountably(t *testing.T, gate, reason string) {
	t.Helper()
	t.Cleanup(func() {
		macosUserTests.record(macosUserOutcomeFor(t.Name(), t.Skipped(), reason, gate))
	})
	t.Skip("macos-user: " + reason)
}

// requireMacosUser is the first line of every test in this suite: it enforces the
// name prefix, skips (countably) when this host cannot run one, and isolates HOME.
//
// THE OUTCOME IS RECORDED FROM A CLEANUP, not from here, and that is the load-bearing
// detail. A test can pass this gate and then skip for a reason of its own — a config
// this machine cannot produce, a tool the launch reported absent — and counting it as
// "executed" at the gate would let the suite satisfy its own vacuity check while
// asserting nothing. t.Skipped() read at cleanup time is the test's FINAL disposition,
// so there is one place that can be wrong instead of one per test.
//
// HOME isolation is requireJail's, for requireJail's reason: a test's inputs should be
// what the test states rather than whatever ~/.config/yolo-jail/config.jsonc the Mac
// happens to carry (docs/reference/storage-and-config.md §10.5). It also redirects the
// darwin profile's nix GC root into the temp home, which costs nothing after the first
// launch — the store is shared, so the build is a cache hit.
func requireMacosUser(t *testing.T) {
	t.Helper()
	// The name check runs BEFORE the -short skip, and seeds nothing, so a misnamed
	// test is caught by the ordinary Linux integration job rather than waiting for a
	// Mac. It cannot be caught under -short, where every test here skips.
	if !macosUserTestNameOK(t.Name()) {
		t.Fatalf("%s does not start with %q. Every test behind this gate must, because "+
			"a CI job selects the whole suite with `-run '^%s'` — a test outside that "+
			"pattern never runs and every job stays green, which is the exact failure "+
			"this gate exists to remove.", t.Name(), macosUserTestPrefix, macosUserTestPrefix)
	}
	reason := macosUserSkippedPastGate
	t.Cleanup(func() {
		macosUserTests.record(macosUserOutcomeFor(t.Name(), t.Skipped(), reason, macosUserGateLaunch))
	})
	if why, ok := macosUserHostProbe().gate(); !ok {
		reason = why
		t.Skip("macos-user: " + why)
	}
	isolateHome(t, "{}")
}

// macosUserHostProbe is what requireMacosUser asks about the host. A variable only so that
// fakeReadyMacosUserHost can answer for one test: nothing else assigns it.
var macosUserHostProbe = probeMacosUserHost

// fakeReadyMacosUserHost makes requireMacosUser's gate PASS for the rest of t, on any
// machine and under -short, so a test can check what a gated test does once it is past
// the gate without a Mac. Three things are swapped until t ends:
//
//   - the probe, for a host that has every fact the gate asks for;
//   - the ledger, for a fresh one, which it returns. A faked pass is not an execution, and
//     the run's own ledger feeds the vacuity verdict, so it must never see one;
//   - HOME, for a temp dir. A passing gate isolates HOME (isolateHome), which re-links the
//     stores of the home it finds; this keeps those links, and the directories they need,
//     inside the test's own temp dirs instead of the machine's home.
func fakeReadyMacosUserHost(t *testing.T) *macosUserLedger {
	t.Helper()
	ready := macosUserHost{goos: "darwin", euid: 501, sandboxExec: true, sandboxUser: true,
		sharedRoot: true, runAsQuiet: true}
	if why, ok := ready.gate(); !ok {
		t.Fatalf("fakeReadyMacosUserHost's host does not pass the gate, which now also asks "+
			"for something it lacks: %s", why)
	}
	probe, ledger := macosUserHostProbe, macosUserTests
	scratch := &macosUserLedger{}
	macosUserHostProbe = func() macosUserHost { return ready }
	macosUserTests = scratch
	t.Cleanup(func() { macosUserHostProbe, macosUserTests = probe, ledger })
	t.Setenv("HOME", resolvedTempDir(t))
	return scratch
}

// macosUserTestNameOK is the name rule, split out so it is testable without a test
// that violates it. A subtest inherits its parent's prefix, so "TestMacosUserX/case"
// passes and needs no special case.
func macosUserTestNameOK(name string) bool {
	return strings.HasPrefix(name, macosUserTestPrefix)
}

// macosUserWorkspace creates a workspace the sandbox user can actually write in, and
// returns its resolved path.
//
// IT CANNOT BE t.TempDir(), which is the whole reason this exists. On darwin that is
// under /var/folders, which carries no grant for the _yolojail group — so the launch
// refuses before doing anything, naming `yolo yolo-account share`. A workspace has
// to live under the shared root, where yolo-account create left an INHERITABLE ACE that every
// newly-created directory picks up (internal/macosuser/macosuser.go documents what
// does and does not inherit: everything that creates an object does; `mv` does not).
//
// The explicit `yolo-account share` afterwards is belt and braces, not ceremony: it
// is idempotent, needs no sudo (it applies the grant as the invoking user), and makes
// the fixture self-sufficient on a Mac whose shared root predates the current account
// — where an inherited-looking ACE can name a UUID that resolves to nobody.
//
// The path is resolved with EvalSymlinks at the point it is MINTED, per AGENTS.md's
// darwin PATH-RESOLUTION rule, so no later comparison has to remember to — and a
// launch that echoes its own workspace path back can be compared against this one.
func macosUserWorkspace(t *testing.T, configJSON string) string {
	t.Helper()
	if strings.Contains(configJSON, `"packs"`) {
		t.Fatalf("macosUserWorkspace got a `packs` key, which is USER SCOPE ONLY — a "+
			"workspace config naming one is a `yolo check` error. Write it into the "+
			"isolated user config instead (packHome):\n%s", configJSON)
	}
	root := macosuser.SharedRootDefault()
	dir, err := os.MkdirTemp(root, "yolo-it-")
	if err != nil {
		t.Fatalf("creating a workspace under the shared root %s: %v\n"+
			"The gate checked that the root exists; this is a permission or space "+
			"problem on it, not a missing setup.", root, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving the workspace path %s: %v", dir, err)
	}
	// MkdirTemp makes it 0700; a project directory a human made is 0755. The ACL is
	// what actually admits the sandbox user either way (macOS evaluates an explicit
	// ACE ahead of the mode bits), so this is about the fixture resembling the case
	// it stands for rather than about access.
	if err := os.Chmod(resolved, 0o755); err != nil {
		t.Fatalf("setting workspace mode on %s: %v", resolved, err)
	}
	t.Cleanup(func() { removeMacosUserWorkspace(t, resolved) })
	if err := os.WriteFile(filepath.Join(resolved, "yolo-jail.jsonc"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("writing yolo-jail.jsonc: %v", err)
	}
	if r := runCommand(t, resolved, []string{"yolo-account", "share", resolved}); r.rc != 0 {
		t.Fatalf("`yolo yolo-account share %s` failed (rc %d) — the sandbox user "+
			"cannot write here, so the launch would refuse:\n%s", resolved, r.rc, r.combined())
	}
	return resolved
}

// macosUserWorkspaceTOML is macosUserWorkspace holding yolo-jail.toml with body in place of
// the legacy yolo-jail.jsonc, so a grant request is written in the format that carries it.
func macosUserWorkspaceTOML(t *testing.T, body string) string {
	t.Helper()
	dir := macosUserWorkspace(t, `{}`)
	if err := os.Remove(filepath.Join(dir, "yolo-jail.jsonc")); err != nil {
		t.Fatalf("removing the seeded yolo-jail.jsonc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.WorkspaceConfigTOMLName), []byte(body), 0o644); err != nil {
		t.Fatalf("writing yolo-jail.toml: %v", err)
	}
	return dir
}

// removeMacosUserWorkspace deletes a workspace the SANDBOX USER has written into.
//
// A plain RemoveAll is not enough and the reason is structural rather than incidental:
// the sandbox runs as _yolojail, so a directory it creates is owned by _yolojail and
// (umask 022) is not writable by us — we can see its children and cannot unlink them.
// The ACL grants the _yolojail GROUP, which the invoking user is not in.
//
// So the escalation is removeSandboxWritten: `/bin/rm -rf` AS THE SANDBOX ACCOUNT, through
// the run-as rule the gate already required — _yolojail owns what it made, which is the part
// we cannot unlink — then a plain RemoveAll again for what is left. Never root `sudo`
// (OQ-DM1). The prefix assertion before it is not paranoia theatre: the guest pass is an
// `rm -rf` that runs as another account, and it must be impossible for it to name anything
// but a directory this file minted under the shared root.
func removeMacosUserWorkspace(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err == nil {
		return
	}
	root := macosuser.SharedRootDefault()
	if !strings.HasPrefix(dir, root+string(os.PathSeparator)) || strings.Contains(dir, "..") {
		t.Fatalf("refusing to escalate removal of %s: it is not a fixture directory "+
			"under %s", dir, root)
	}
	if err := removeSandboxWritten(dir); err != nil {
		t.Logf("could not remove the macos-user workspace %s (%v): %s", dir, err, removeByHandHint(dir))
	}
}

// macosUserRunArgs is jailRunArgs plus the runtime selection, spelled on the ARGV side
// via the launcher env rather than in the workspace config.
//
// Why the env and not `"runtime": "macos-user"` in yolo-jail.jsonc: YOLO_RUNTIME
// outranks the config (internal/cli/run/preflight.go resolveRuntime), so a fixture
// that used the config would be silently overridden on any machine that exports the
// variable — including a CI step that set it for a different job in the same workflow.
// Saying it per launch is the spelling that cannot be shadowed.
func macosUserRunEnv() runOption { return withEnv("YOLO_RUNTIME=macos-user") }

// runMacosUser launches the sandbox and runs script in a LOGIN shell, which is the
// shell macos-user's PATH re-prepend actually targets: the login rc files re-prepend
// $YOLO_DARWIN_LOGIN_PATH to beat macOS's own path_helper, so a non-login shell would
// be asking a different question.
func runMacosUser(t *testing.T, dir, script string, opts ...runOption) result {
	t.Helper()
	args := append(jailRunArgs(), "--", "bash", "-lc", script)
	opts = append([]runOption{withTimeout(macosUserTimeout()), macosUserRunEnv()}, opts...)
	return runCommand(t, dir, args, opts...)
}

// macosUserTimeoutEnv overrides the per-launch deadline below.
const macosUserTimeoutEnv = "YOLO_TEST_MACOS_USER_TIMEOUT"

// macosUserTimeout is the per-launch deadline, and it is MUCH larger than
// jailTimeout() for a reason that is not slowness: a macos-user launch builds a native
// darwin nix closure — the FLOOR, every package darwinpkg.FloorNames() lists, before the
// config declares anything (docs/reference/macos-user-provisioning.md, "The floor") — and on a cold
// machine that is a
// substitution or a compile rather than a container start.
//
// The number is a CEILING ON WASTE, not a target. Nobody has measured what a first
// launch costs (§10.8 item 5 says so outright), so this is sized to not be the thing
// that fails a first measurement, and the right way to revise it is the duration a
// green run reports rather than a guess.
func macosUserTimeout() time.Duration {
	if v := os.Getenv(macosUserTimeoutEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Minute
}

// macosUserOutcomeFor is what requireMacosUser's cleanup files. Split out of the
// closure so the rule it encodes — a skip is a skip whoever called Skip, and the
// gate's reason is only right when the GATE skipped — is testable without a test that
// has to enter the ledger to be observed.
func macosUserOutcomeFor(name string, skipped bool, reason, gate string) macosUserOutcome {
	if !skipped {
		return macosUserOutcome{test: name, ran: true, gate: gate}
	}
	return macosUserOutcome{test: name, ran: false, reason: reason, gate: gate}
}

// ---------------------------------------------------------------------------
// The gate's own tests. They run under -short, on Linux, on purpose: every fact
// they check is one the machines that develop this repo cannot otherwise reach, and
// a guard that silently stops working is indistinguishable from no guard.
//
// They carry the TestMacosUser prefix deliberately, so the macOS job that depends on
// this guard also verifies it.
// ---------------------------------------------------------------------------

// macosUserRunnableHost is a Mac with everything the backend needs. Each test below
// breaks exactly one fact, so a new precondition added to gate() without a case here
// shows up as the one field nobody flips.
func macosUserRunnableHost() macosUserHost {
	return macosUserHost{
		goos: "darwin", euid: 501,
		sandboxExec: true, sandboxUser: true, sharedRoot: true, runAsQuiet: true,
	}
}

// TestMacosUserGateAdmitsAFullyEquippedMac: the gate must actually let a real Mac
// through. A gate that skips everywhere is the failure this whole file is about, so
// the positive case is the first assertion, not an afterthought.
func TestMacosUserGateAdmitsAFullyEquippedMac(t *testing.T) {
	if reason, ok := macosUserRunnableHost().gate(); !ok {
		t.Fatalf("the gate refused a Mac with every precondition met: %s", reason)
	}
}

// TestMacosUserGateRefusesEveryHostThatCannotRunOne, one broken fact at a time. The
// reason is asserted to NAME something — an empty or generic reason turns a countable
// skip back into an unexplained one, which is the half of this design that makes a
// red CI job actionable rather than merely red.
func TestMacosUserGateRefusesEveryHostThatCannotRunOne(t *testing.T) {
	cases := []struct {
		name  string
		host  macosUserHost
		names string // a substring the reason must carry, so it points somewhere
	}{
		{"short", func() macosUserHost { h := macosUserRunnableHost(); h.short = true; return h }(), "-short"},
		{"linux", func() macosUserHost { h := macosUserRunnableHost(); h.goos = "linux"; return h }(), "darwin-only"},
		{"root", func() macosUserHost { h := macosUserRunnableHost(); h.euid = 0; return h }(), "root"},
		{"no-sandbox-exec", func() macosUserHost { h := macosUserRunnableHost(); h.sandboxExec = false; return h }(), "sandbox-exec"},
		{"no-account", func() macosUserHost { h := macosUserRunnableHost(); h.sandboxUser = false; return h }(), "yolo yolo-account create"},
		{"no-shared-root", func() macosUserHost { h := macosUserRunnableHost(); h.sharedRoot = false; return h }(), "yolo yolo-account create"},
		{"run-as-would-prompt", func() macosUserHost { h := macosUserRunnableHost(); h.runAsQuiet = false; return h }(), "yolo yolo-account create"},
		{"run-as-would-prompt-names-the-probe", func() macosUserHost { h := macosUserRunnableHost(); h.runAsQuiet = false; return h }(), "/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := tc.host.gate()
			if ok {
				t.Fatalf("the gate admitted a host that cannot run a macos-user launch (%s); "+
					"the test would hang or fail for an environment reason", tc.name)
			}
			if !strings.Contains(reason, tc.names) {
				t.Errorf("the skip reason does not name %q, so a reader of the CI log cannot "+
					"act on it:\n%s", tc.names, reason)
			}
		})
	}
}

// TestMacosUserVacuityVerdictFailsOnlyADeclaredRunThatRanNothing is the decision this
// suite rests on, in table form.
func TestMacosUserVacuityVerdictFailsOnlyADeclaredRunThatRanNothing(t *testing.T) {
	ran := macosUserOutcome{test: "TestMacosUserA", ran: true}
	skipped := macosUserOutcome{test: "TestMacosUserB", reason: "not darwin"}
	cases := []struct {
		name     string
		declared bool
		outcomes []macosUserOutcome
		wantFail bool
	}{
		{"undeclared and empty", false, nil, false},
		{"undeclared, everything skipped", false, []macosUserOutcome{skipped}, false},
		{"undeclared and something ran", false, []macosUserOutcome{ran}, false},
		{"declared and something ran", true, []macosUserOutcome{ran, skipped}, false},
		{"declared, everything skipped", true, []macosUserOutcome{skipped}, true},
		{"declared and nothing was even selected", true, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, fail := macosUserVacuityVerdict(tc.declared, tc.outcomes)
			if fail != tc.wantFail {
				t.Fatalf("fail=%v, want %v\nreport:\n%s", fail, tc.wantFail, report)
			}
			if !tc.wantFail {
				return
			}
			// A failing verdict has to explain itself: the reasons are the only thing
			// standing between a red job and somebody re-running it hoping.
			for _, want := range []string{macosUserDeclareEnv, "FAILED"} {
				if !strings.Contains(report, want) {
					t.Errorf("the failing report never mentions %q:\n%s", want, report)
				}
			}
			for _, o := range tc.outcomes {
				if !o.ran && !strings.Contains(report, o.reason) {
					t.Errorf("the failing report drops the skip reason %q, which is the "+
						"only actionable thing in it:\n%s", o.reason, report)
				}
			}
		})
	}
}

// TestMacosUserVacuityVerdictFailsAGateNothingGotThrough is the rule the second gate
// made necessary, and the one a reader is most likely to think is redundant: the run
// below EXECUTED a test, so the count says executed=1 and the old verdict passed it,
// while every test that needs the sandbox account skipped. That is a green macOS job
// asserting nothing about a launch.
func TestMacosUserVacuityVerdictFailsAGateNothingGotThrough(t *testing.T) {
	outcomes := []macosUserOutcome{
		{test: "TestMacosUserSeatbeltProfileEnforcesItsRules", ran: true, gate: macosUserGateSeatbelt},
		{test: "TestMacosUserFloorReachesTheSandboxPath", reason: "no _yolojail account", gate: macosUserGateLaunch},
	}
	report, fail := macosUserVacuityVerdict(true, outcomes)
	if !fail {
		t.Fatalf("a run in which every %s test skipped was reported green because a %s "+
			"test ran:\n%s", macosUserGateLaunch, macosUserGateSeatbelt, report)
	}
	for _, want := range []string{macosUserGateLaunch, "no _yolojail account", "FAILED"} {
		if !strings.Contains(report, want) {
			t.Errorf("the failing report never mentions %q, so a reader cannot tell which "+
				"gate starved or why:\n%s", want, report)
		}
	}
	// The inverse must stay green, or the rule is just "every gate must run", which
	// would make adding a gate to a machine that cannot satisfy it a permanent red.
	both := append(append([]macosUserOutcome(nil), outcomes...),
		macosUserOutcome{test: "TestMacosUserFloorProbe", ran: true, gate: macosUserGateLaunch})
	if report, fail := macosUserVacuityVerdict(true, both); fail {
		t.Errorf("a run with something through every gate was failed:\n%s", report)
	}
}

// TestMacosUserStarvedGatesNamesThemInOrder: the report lists gates in the order the
// ledger first saw them, so two runs of the same machine produce the same sentence.
func TestMacosUserStarvedGatesNamesThemInOrder(t *testing.T) {
	outcomes := []macosUserOutcome{
		{test: "A", gate: "alpha", reason: "x"},
		{test: "B", gate: "beta", ran: true},
		{test: "C", gate: "gamma", reason: "y"},
		{test: "D", gate: "alpha", reason: "z"},
	}
	got := macosUserStarvedGates(outcomes)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "gamma" {
		t.Errorf("starved gates = %v, want [alpha gamma]", got)
	}
	if starved := macosUserStarvedGates(nil); starved != nil {
		t.Errorf("an empty ledger starves no gate, got %v", starved)
	}
}

// TestMacosUserVacuityReportCountsWhatItSays guards the number itself. A counter that
// reports "executed=3" while three tests skipped is the failure mode with no symptom.
func TestMacosUserVacuityReportCountsWhatItSays(t *testing.T) {
	outcomes := []macosUserOutcome{
		{test: "TestMacosUserA", ran: true},
		{test: "TestMacosUserB", ran: true},
		{test: "TestMacosUserC", reason: "no sandbox account"},
	}
	report, fail := macosUserVacuityVerdict(true, outcomes)
	if fail {
		t.Fatalf("a run with two executed tests must not fail for vacuity:\n%s", report)
	}
	if !strings.Contains(report, "executed=2") || !strings.Contains(report, "skipped=1") {
		t.Errorf("the ledger report miscounts:\n%s", report)
	}
}

// TestMacosUserGateOutcomeTreatsALateSkipAsASkip: the one rule that stops this suite
// from satisfying its own check while asserting nothing. A test that clears the gate
// and then skips for its own reason has NOT exercised the backend.
func TestMacosUserGateOutcomeTreatsALateSkipAsASkip(t *testing.T) {
	const gateReason = "this host has no sandbox account"
	if o := macosUserOutcomeFor("TestMacosUserX", false, gateReason, macosUserGateLaunch); !o.ran ||
		o.reason != "" || o.gate != macosUserGateLaunch {
		t.Errorf("a test that finished without skipping must count as executed, with no "+
			"reason attached and its gate recorded: %+v", o)
	}
	o := macosUserOutcomeFor("TestMacosUserX", true, gateReason, macosUserGateLaunch)
	if o.ran {
		t.Fatal("a skipped test counted as executed — the suite can now satisfy its own " +
			"vacuity check while exercising nothing")
	}
	if o.reason != gateReason {
		t.Errorf("the skip lost its reason: %+v", o)
	}
}

// TestMacosUserLedgerKeepsTheLastWordPerTest: the ledger is written from a cleanup,
// and a correction has to win over the earlier optimistic entry.
func TestMacosUserLedgerKeepsTheLastWordPerTest(t *testing.T) {
	var l macosUserLedger
	l.record(macosUserOutcome{test: "TestMacosUserA", ran: true})
	l.record(macosUserOutcome{test: "TestMacosUserB", ran: true})
	l.record(macosUserOutcome{test: "TestMacosUserA", reason: "skipped after all"})
	got := l.snapshot()
	if len(got) != 2 {
		t.Fatalf("the ledger grew a duplicate entry per record(): %+v", got)
	}
	if got[0].test != "TestMacosUserA" || got[0].ran {
		t.Errorf("the correction did not replace the earlier entry: %+v", got[0])
	}
	if got[1].test != "TestMacosUserB" || !got[1].ran {
		t.Errorf("recording one test's outcome disturbed another's: %+v", got[1])
	}
}

// TestMacosUserLedgerKeepsASkipsOwnReason: the gate's cleanup runs after a later check's and
// knows only that the test skipped, so its generic reason must not replace the one the check
// named; anything else still replaces it.
func TestMacosUserLedgerKeepsASkipsOwnReason(t *testing.T) {
	var l macosUserLedger
	l.record(macosUserOutcome{test: "TestMacosUserA", reason: "the log store is closed"})
	l.record(macosUserOutcome{test: "TestMacosUserA", reason: macosUserSkippedPastGate})
	if got := l.snapshot()[0]; got.reason != "the log store is closed" {
		t.Errorf("the gate's generic reason replaced the skip's own: %+v", got)
	}
	l.record(macosUserOutcome{test: "TestMacosUserA", ran: true})
	if got := l.snapshot()[0]; !got.ran {
		t.Errorf("an execution no longer replaces an earlier skip: %+v", got)
	}
	l.record(macosUserOutcome{test: "TestMacosUserB", reason: macosUserSkippedPastGate})
	l.record(macosUserOutcome{test: "TestMacosUserB", reason: "a reason of its own"})
	if got := l.snapshot()[1]; got.reason != "a reason of its own" {
		t.Errorf("a named reason no longer replaces the generic one: %+v", got)
	}
}

// TestMacosUserUnifiedLogSkipIsCountedWithItsReason drives requireUnifiedLog on a stand-in Mac
// whose log store is closed, behind the real gate, through a top-level-shaped test and a subtest
// no gate records: both must skip, and the ledger must carry the policy reason for each — the
// half of OQ-DM2 that makes the skip countable rather than a quiet pass. Then on an open store it
// must not skip.
func TestMacosUserUnifiedLogSkipIsCountedWithItsReason(t *testing.T) {
	ledger := fakeReadyMacosUserHost(t)
	probe := unifiedLogProbe
	t.Cleanup(func() { unifiedLogProbe = probe })
	const detail = "log exited 1: Could not open local log store: Operation not permitted"
	unifiedLogProbe = func() (bool, string) { return false, detail }

	var gated, sub string
	t.Run("gated", func(t *testing.T) {
		gated = t.Name()
		requireMacosUser(t)
		requireUnifiedLog(t, macosUserGateLaunch)
		t.Error("requireUnifiedLog did not skip on a closed store")
	})
	t.Run("parent", func(t *testing.T) {
		t.Run("subtest", func(t *testing.T) {
			sub = t.Name()
			requireUnifiedLog(t, macosUserGateSeatbelt)
			t.Error("requireUnifiedLog did not skip a subtest on a closed store")
		})
	})
	got := map[string]macosUserOutcome{}
	for _, o := range ledger.snapshot() {
		got[o.test] = o
	}
	for name, gate := range map[string]string{gated: macosUserGateLaunch, sub: macosUserGateSeatbelt} {
		o, ok := got[name]
		switch {
		case !ok:
			t.Errorf("%s skipped and the ledger has no outcome for it, so the skip is not counted", name)
		case o.ran || o.gate != gate || !strings.Contains(o.reason, detail) ||
			!strings.Contains(o.reason, "Mac's own policy"):
			t.Errorf("%s's outcome does not record a skip for the closed log store under the %s gate: %+v",
				name, gate, o)
		}
	}

	unifiedLogProbe = func() (bool, string) { return true, "" }
	// An empty read that `log` did not refuse is not blamed on the Mac's policy.
	if r := unifiedLogSkipReason("log exited 0: "); strings.Contains(r, "policy") || !strings.Contains(r, "not refused") {
		t.Errorf("an unrefused empty read is reported as the store's policy: %s", r)
	}

	reached := false
	t.Run("open", func(t *testing.T) {
		requireUnifiedLog(t, macosUserGateLaunch)
		reached = true
	})
	if !reached {
		t.Error("requireUnifiedLog skipped on a store the invoking user can read")
	}
	for _, o := range ledger.snapshot() {
		if strings.HasSuffix(o.test, "/open") {
			t.Errorf("an open store recorded an outcome: %+v", o)
		}
	}
}

// TestMacosUserUnifiedLogGatesOnlyTheLogControls pins requireUnifiedLog's two call sites, which
// only a Mac with a closed store reaches: the log bridge test asks for the store after its gate,
// and the seatbelt rules test asks for it in the one case whose control reads the store — that
// case and no other, so a managed Mac still asserts every other rule (OQ-DM2). It reads the
// source for TestMacosUserHD10GuardsTheMachineWideBroker's reason.
func TestMacosUserUnifiedLogGatesOnlyTheLogControls(t *testing.T) {
	var flagged []string
	for _, tc := range seatbeltCases() {
		if tc.needsUnifiedLog {
			flagged = append(flagged, tc.id)
		}
	}
	if len(flagged) != 1 || flagged[0] != "macos-log-deny" {
		t.Errorf("the seatbelt cases that skip on a closed log store are %v, want exactly the "+
			"macos-log-deny control: any other skips a rule a managed Mac can measure", flagged)
	}
	for file, call := range map[string]string{
		"macosuserseatbelt_test.go":  "\t\t\tif tc.needsUnifiedLog {\n\t\t\t\trequireUnifiedLog(t, macosUserGateSeatbelt)\n",
		"macosuserlogbridge_test.go": "\trequireMacosUser(t)\n\trequireUnifiedLog(t, macosUserGateLaunch)\n",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if !strings.Contains(string(src), call) {
			t.Errorf("%s no longer asks requireUnifiedLog where its control reads the log store, "+
				"so a Mac whose store is closed fails it instead of skipping countably:\n%s", file, call)
		}
	}
}

// TestMacosUserGateNameRuleIsWhatTheCIFilterSelects. The prefix is not style: it is
// the claim that `-run '^TestMacosUser'` selects every test behind this gate.
func TestMacosUserGateNameRuleIsWhatTheCIFilterSelects(t *testing.T) {
	for _, ok := range []string{"TestMacosUserFloor", "TestMacosUserFloor/subcase"} {
		if !macosUserTestNameOK(ok) {
			t.Errorf("%q is rejected by the name rule but IS selected by the CI filter", ok)
		}
	}
	for _, bad := range []string{"TestMacOSUserFloor", "TestFloorOnMacosUser", "TestMacos"} {
		if macosUserTestNameOK(bad) {
			t.Errorf("%q is accepted by the name rule but is NOT selected by `-run "+
				"'^%s'`, so it would never run and the job would stay green",
				bad, macosUserTestPrefix)
		}
	}
}

// TestMacosUserGateFailsARunThatExecutedNothing is the LOAD-BEARING test of this file:
// it drives the REAL TestMain in a subprocess and asserts the process exit code.
//
// Everything above pins a pure function. AGENTS.md's standing question — "does it fail
// if I delete the call site?" — is answered NO for all of them: macosUserExitCode
// could be removed from TestMain tomorrow and every table above would stay green while
// the guard did nothing. This test is what makes that deletion loud, and it is the
// shape imagebuildfailure_test.go uses for the same reason.
//
// The child runs `-run '^$'`, which selects NO tests: TestMain still runs, the ledger
// is empty by construction, and there is no way for this test to recurse into itself.
func TestMacosUserGateFailsARunThatExecutedNothing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no `go` on PATH, so the guard's call site in TestMain cannot be exercised")
	}
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this package's directory")
	}
	pkgDir := filepath.Dir(thisFile)

	run := func(declared bool) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "test", "-short", "-count=1", "-run", "^$", ".")
		cmd.Dir = pkgDir
		env := os.Environ()[:0:0]
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, macosUserDeclareEnv+"=") {
				env = append(env, kv)
			}
		}
		if declared {
			env = append(env, macosUserDeclareEnv+"=1")
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		rc := 0
		if err != nil {
			if ee, isExit := err.(*exec.ExitError); isExit {
				rc = ee.ExitCode()
			} else {
				t.Fatalf("could not run the child `go test`: %v\n%s", err, out)
			}
		}
		return rc, string(out)
	}

	if rc, out := run(false); rc != 0 {
		t.Fatalf("an UNDECLARED run that executed no macos-user tests exited %d — the "+
			"guard fires on ordinary runs, which would turn every green suite into a "+
			"mystery:\n%s", rc, out)
	}
	rc, out := run(true)
	if rc == 0 {
		t.Fatalf("a run declaring %s=1 executed ZERO macos-user tests and still exited 0.\n"+
			"The guard is not wired into TestMain (macosUserExitCode), so this suite can "+
			"now ship a CI job that skips everything and reports success — the exact "+
			"outcome this file exists to prevent.\n%s", macosUserDeclareEnv, out)
	}
	if !strings.Contains(out, "FAILED rather") {
		t.Errorf("the child failed but never explained why; a bare non-zero exit sends "+
			"the reader to the wrong place:\n%s", out)
	}
}
