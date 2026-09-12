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
)

// THE macos-user GATE — and the one property the whole suite rests on:
// A SUITE THAT SKIPS MUST NOT BE INDISTINGUISHABLE FROM A SUITE THAT PASSES.
//
// THE PROBLEM. The macos-user backend is darwin-only and privilege-bearing: every
// launch leads with `sudo --user=_yolojail` (internal/macosuser/runplan.go) and
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
type macosUserOutcome struct {
	test   string
	ran    bool
	reason string
}

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
// WRITE WINS, which is what lets a caller record optimistically and correct itself.
func (l *macosUserLedger) record(o macosUserOutcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.index == nil {
		l.index = map[string]int{}
	}
	if i, ok := l.index[o.test]; ok {
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
var macosUserTests macosUserLedger

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
		fmt.Fprintf(&b, "  SKIPPED %s: %s\n", o.test, o.reason)
	}
	for _, o := range ran {
		fmt.Fprintf(&b, "  ran     %s\n", o.test)
	}
	if len(ran) > 0 {
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
	sandboxUser bool // the _yolojail account exists (`yolo macos-setup` ran)
	sharedRoot  bool // the shared root exists, so a granted workspace can be made in it
	sudoQuiet   bool // `sudo -n true` succeeds: sudo will not stop to ask for a password
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
		return fmt.Sprintf("the %s account does not exist — run `yolo macos-setup` "+
			"(one-time, interactive, creates a hidden system account). This suite will "+
			"not create it for you: a test run must not silently add an account to "+
			"somebody's Mac", macosuser.SandboxUser), false
	case !h.sharedRoot:
		return fmt.Sprintf("%s does not exist, so there is nowhere to put a workspace "+
			"the sandbox user can write — run `yolo macos-setup`",
			macosuser.SharedRootDefault()), false
	case !h.sudoQuiet:
		return "`sudo -n true` fails, so sudo would stop and ask for a password. Every " +
			"macos-user argv leads with `sudo --user=_yolojail`, and a test that blocks " +
			"on a password prompt is worse than one that skips — it hangs the run until " +
			"its deadline. Authenticate once first (`sudo -v`) and re-run within the " +
			"timestamp window, or run this on a host with passwordless sudo (a " +
			"GitHub-hosted macOS runner is one)", false
	}
	return "", true
}

// probeMacosUserHost measures the facts gate() decides on. Impure by design and
// deliberately thin: everything that could be got wrong lives in gate().
//
// `sudo -n true` CANNOT HANG — -n makes sudo fail rather than prompt — which is what
// makes it safe to probe before anything else spends a password.
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
	h.sudoQuiet = runQuiet(20*time.Second, "sudo", "-n", "true")
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
	reason := "the test skipped after passing the gate — see its own skip message"
	t.Cleanup(func() { macosUserTests.record(macosUserOutcomeFor(t.Name(), t.Skipped(), reason)) })
	if why, ok := probeMacosUserHost().gate(); !ok {
		reason = why
		t.Skip("macos-user: " + why)
	}
	isolateHome(t, "{}")
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
// refuses before doing anything, naming `yolo macos-fix-permissions`. A workspace has
// to live under the shared root, where macos-setup left an INHERITABLE ACE that every
// newly-created directory picks up (internal/macosuser/macosuser.go documents what
// does and does not inherit: everything that creates an object does; `mv` does not).
//
// The explicit `macos-fix-permissions` afterwards is belt and braces, not ceremony: it
// is idempotent, needs no sudo (it applies the grant as the invoking user), and makes
// the fixture self-sufficient on a Mac whose shared root predates the current account
// — where an inherited-looking ACE can name a UUID that resolves to nobody.
//
// t.TempDir() is still used for the resolved-path trap it solves elsewhere: the path
// here is resolved with EvalSymlinks at the point it is MINTED, per AGENTS.md's darwin
// PATH-RESOLUTION rule, so no later comparison has to remember.
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
	t.Cleanup(func() { removeMacosUserWorkspace(t, resolved) })
	if err := os.WriteFile(filepath.Join(resolved, "yolo-jail.jsonc"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("writing yolo-jail.jsonc: %v", err)
	}
	if r := runCommand(t, resolved, []string{"macos-fix-permissions", resolved}); r.rc != 0 {
		t.Fatalf("`yolo macos-fix-permissions %s` failed (rc %d) — the sandbox user "+
			"cannot write here, so the launch would refuse:\n%s", resolved, r.rc, r.combined())
	}
	return resolved
}

// removeMacosUserWorkspace deletes a workspace the SANDBOX USER has written into.
//
// A plain RemoveAll is not enough and the reason is structural rather than incidental:
// the sandbox runs as _yolojail, so a directory it creates is owned by _yolojail and
// (umask 022) is not writable by us — we can see its children and cannot unlink them.
// The ACL grants the _yolojail GROUP, which the invoking user is not in.
//
// So the escalation is `sudo /bin/rm -rf`, available because the gate already required
// passwordless sudo. The prefix assertion before it is not paranoia theatre: this is
// the one `rm -rf` in the suite that runs as root, and it must be impossible for it to
// name anything but a directory this file minted under the shared root.
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
	if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", dir) {
		t.Logf("could not remove the macos-user workspace %s even with sudo; it holds "+
			"files owned by %s and will need removing by hand", dir, macosuser.SandboxUser)
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
// darwin nix closure — the FLOOR, ~27 packages, before the config declares anything
// (docs/design/macos-user-provisioning.md §9) — and on a cold machine that is a
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
func macosUserOutcomeFor(name string, skipped bool, reason string) macosUserOutcome {
	if !skipped {
		return macosUserOutcome{test: name, ran: true}
	}
	return macosUserOutcome{test: name, ran: false, reason: reason}
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
		sandboxExec: true, sandboxUser: true, sharedRoot: true, sudoQuiet: true,
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
		{"no-account", func() macosUserHost { h := macosUserRunnableHost(); h.sandboxUser = false; return h }(), "yolo macos-setup"},
		{"no-shared-root", func() macosUserHost { h := macosUserRunnableHost(); h.sharedRoot = false; return h }(), "yolo macos-setup"},
		{"sudo-would-prompt", func() macosUserHost { h := macosUserRunnableHost(); h.sudoQuiet = false; return h }(), "sudo -v"},
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
	if o := macosUserOutcomeFor("TestMacosUserX", false, gateReason); !o.ran || o.reason != "" {
		t.Errorf("a test that finished without skipping must count as executed, with no "+
			"reason attached: %+v", o)
	}
	o := macosUserOutcomeFor("TestMacosUserX", true, gateReason)
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
