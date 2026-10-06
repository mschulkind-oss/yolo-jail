package cli

// buildreport_test.go pins a jail launch's build report (buildreport.go, docs/design/patched-forks.md
// PF-D78): a successful build is its start line, its disclosure line and its result line on the
// terminal, and everything its build jail printed is in launch.log and the build's own log; the
// nested launch's warnings stay on the terminal; a failed build prints its last lines and its log
// under its failure line; and a build jail that refused is relayed with what it said.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// inProcessForkBuildChild stands in for the fork-build-jail child in this package's tests (TestMain):
// a test binary never self-execs (forkBuildChildCommand), so a build a launch runs as a child runs
// here, in this process, through the fake capture jail a test installs.
func inProcessForkBuildChild(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
	color bool) (int, bool) {
	return forkBuildRunJail(staging, b, s, color), false
}

// launchStream is a launch's stderr as the run pipeline tees it (run's teeLog): the terminal takes
// every write but a log-only one, the log every write but a transient one, with color stripped.
type launchStream struct {
	mu        sync.Mutex
	term, log bytes.Buffer
}

var testANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

func (s *launchStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Write(p)
	s.log.Write(testANSI.ReplaceAll(p, nil))
	return len(p), nil
}

func (s *launchStream) WriteLog(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Write(testANSI.ReplaceAll(p, nil))
	return len(p), nil
}

func (s *launchStream) WriteTransient(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.Write(p)
}

func (s *launchStream) terminal() string { s.mu.Lock(); defer s.mu.Unlock(); return s.term.String() }
func (s *launchStream) logged() string   { s.mu.Lock(); defer s.mu.Unlock(); return s.log.String() }

// The stand-in's lines: what a real build jail's streams carry.
const (
	jailOutLine     = "added 212 packages in 9s"
	jailErrLine     = "src/index.ts(3,1): compiled"
	jailBootLine    = "cgroup delegate: not available (no host daemon socket)"
	jailBuildError  = "Error: a build line's own error, which is not the nested launch's"
	nestedFlakeLine = "Flake source: /opt/yolo-jail/share/yolo-jail (the bundle beside this binary)"
	nestedJailLine  = "Jail: yolo-fork-0123456789abcdef"
	nestedWarning   = "Warning: a nested launch's warning"
	nestedDetail    = "  its detail, indented"
)

// talkingChild is a child that prints on each of its streams as a build jail does — its launch's own
// lines, its jail's boot, the ready, then the build line's output on the child's own stdout and
// stderr, which the launch's lines shared until the ready — then runs the fake build jail in this
// process; rc, when non-zero, ends it there with the toolchain written, a build line that ran and
// failed, after lines more lines on its stderr.
func talkingChild(t *testing.T, rc, lines int) {
	t.Helper()
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		if s.jailOut == nil || s.jailErr == nil || s.jailReady == nil {
			t.Error("a jail launch's build ran its child with no writers for the jail's own lines")
			return 1, false
		}
		fmt.Fprintln(s.errw, nestedFlakeLine)
		fmt.Fprintln(s.errw, nestedJailLine)
		fmt.Fprintln(s.errw, nestedWarning)
		fmt.Fprintln(s.errw, nestedDetail)
		fmt.Fprintln(s.jailErr, jailBootLine)
		s.jailReady()
		fmt.Fprintln(s.out, jailOutLine)
		fmt.Fprintln(s.errw, jailErrLine)
		fmt.Fprintln(s.errw, jailBuildError)
		if rc != 0 {
			writeFile(t, filepath.Join(staging, forkToolchainLeaf), "image-identity\n")
			for i := 1; i <= lines; i++ {
				fmt.Fprintf(s.errw, "build output line %d\n", i)
			}
			return rc, false
		}
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
}

// deliverWithStream runs one launch's tree arm for the fixture's extension, its stream the tee.
func (fx *treeFixture) deliverWithStream(t *testing.T, ws string) (run.TreeDelivery, *launchStream) {
	t.Helper()
	stream := &launchStream{}
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: ws, Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Stderr: stream,
		Progress: progress.Config{}}
	got := deliverTreesForLaunch(req, stream, stream, false)
	return got[treeKeyCLI], stream
}

// A SUCCESSFUL BUILD IS ITS START, ITS DISCLOSURE AND ITS RESULT ON THE TERMINAL, and the nested
// launch's warning under it: no line its build jail printed, of its launch, its boot or its build,
// reaches the terminal, and every one of them is in launch.log, marked with the build's key, and in
// the build's own log. Red when the build act's streams stop being the report's (buildForkUnderLock)
// or the tree arm stops making a report (deliverTreesForLaunch).
func TestASuccessfulBuildsTerminalCarriesOnlyItsProgressLines(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	talkingChild(t, 0, 0)
	ws := t.TempDir()
	d, stream := fx.deliverWithStream(t, ws)
	term, logged := stream.terminal(), stream.logged()
	if d.Dir == "" {
		t.Fatalf("nothing was delivered: %+v\n%s", d, term)
	}
	logName := filepath.Join(".yolo", buildLogName(treeKeyCLI))
	label := "v1.0.0 (" + shortSHA(fx.base) + ")"
	wantLines := []string{
		// The start line: what is built and why, which a line of its own said before, and its log.
		"build extension " + treeKeyCLI + ": " + label + " + 2 patches (series ",
		"), the first build of it on this machine; log: " + logName,
		// The disclosure: the seal and the build line, whole.
		"  " + sealDisclosure + "; it runs, then copies the checkout: true",
		// The result: the move line, with the store's clause.
		"built extension " + treeKeyCLI + ": " + label + " + 2 patches; this jail runs it",
		" — store key " + d.Entry + ", ",
		// The nested launch's warning, and its detail.
		"  its build jail: " + nestedWarning,
		"  its build jail: " + nestedDetail,
	}
	for _, w := range wantLines {
		if !strings.Contains(term, w) {
			t.Errorf("the terminal lacks %q:\n%s", w, term)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(term), "\n") {
		if !strings.HasPrefix(l, "build extension ") && !strings.HasPrefix(l, "  sealed: ") &&
			!strings.HasPrefix(l, "built extension ") && !strings.HasPrefix(l, "  its build jail: ") {
			t.Errorf("the terminal carries a line that is not the build's progress: %q\n%s", l, term)
		}
	}
	for _, line := range []string{jailOutLine, jailErrLine, jailBuildError, jailBootLine, nestedFlakeLine, nestedJailLine} {
		if strings.Contains(term, line) {
			t.Errorf("the build jail's line %q reached the terminal:\n%s", line, term)
		}
		if !strings.Contains(logged, "  ["+treeKeyCLI+"] "+line+"\n") {
			t.Errorf("launch.log lacks the build jail's line %q:\n%s", line, logged)
		}
	}
	own, err := os.ReadFile(filepath.Join(ws, logName))
	if err != nil {
		t.Fatalf("the build's own log: %v", err)
	}
	for _, line := range []string{jailOutLine, jailErrLine, nestedFlakeLine, nestedWarning, "it runs, then copies the checkout: true"} {
		if !strings.Contains(string(own), line) {
			t.Errorf("the build's own log lacks %q:\n%s", line, own)
		}
	}
}

// A FAILED BUILD PRINTS ITS LAST LINES AND ITS LOG under its failure line, which the terminal
// otherwise never shows; one that leaves nothing serving leaves its cause to the launch's refusal,
// which says it once, so its own warning is not printed. Red with buildFailed's printRunFailure call
// deleted.
func TestAFailedBuildPrintsItsTailAndItsLog(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	talkingChild(t, 3, 30)
	ws := t.TempDir()
	d, stream := fx.deliverWithStream(t, ws)
	term := stream.terminal()
	if d.Dir != "" {
		t.Fatalf("a failed build delivered %+v", d)
	}
	own := filepath.Join(ws, ".yolo", buildLogName(treeKeyCLI))
	for _, w := range []string{
		"Building extension " + treeKeyCLI + ": failed (",
		"  its last 20 lines:",
		"    build output line 30",
		"    build output line 11",
		"  its whole output: " + own + ", and in " + filepath.Join(ws, ".yolo", run.LaunchLogName),
	} {
		if !strings.Contains(term, w) {
			t.Errorf("the failure lacks %q:\n%s", w, term)
		}
	}
	if !d.Unsaid || !strings.Contains(d.Reason, "failed on the host") || strings.Contains(term, ": the build of ") {
		t.Errorf("the failed build's cause is said by the act, or not left to the launch (%+v):\n%s", d, term)
	}
	if strings.Contains(term, "build output line 10\n") || strings.Contains(term, "its output is above") {
		t.Errorf("the failure printed more than the last 20 lines, or pointed above:\n%s", term)
	}
	if data, err := os.ReadFile(own); err != nil || !strings.Contains(string(data), "build output line 1\n") {
		t.Errorf("the build's own log does not hold its whole output (%v):\n%s", err, data)
	}
}

// A BUILD JAIL THAT REFUSED before its build line ran is relayed with what it said (PPX-D39), from
// the stream it was printed on, each line its own (PPX-D42), in the cause the launch is handed, which
// says it once (run's missingbuilds.go); the build's result line says the jail stopped. Red with the
// relay's tail on the launch's streams (jailTail.tee) or the cause's hand in notStarted deleted.
func TestABuildJailsRefusalIsHandedToTheLaunch(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, _ string, _ forkBuild, s captureStreams, _ bool) (int, bool) {
		fmt.Fprintln(s.errw, nestedFlakeLine)
		fmt.Fprintln(s.out, "Refusing to launch: the config changed and was not approved")
		fmt.Fprintln(s.out, "  key: packs")
		return 1, false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	d, stream := fx.deliverWithStream(t, t.TempDir())
	term := stream.terminal()
	if !strings.Contains(term, "Building extension "+treeKeyCLI+": its build jail exited 1 before its build line ran (") {
		t.Errorf("the build's result does not say its jail stopped:\n%s", term)
	}
	if want := []string{"Refusing to launch: the config changed and was not approved", "key: packs"}; d.Cause == nil ||
		!slices.Equal(d.Cause.Lines, want) {
		t.Errorf("the launch is handed the cause %+v, want the refusal's lines %q", d.Cause, want)
	}
	if strings.Contains(term, " / ") || strings.Contains(term, "Refusing to launch") {
		t.Errorf("the act said the cause the launch says once, or joined its lines:\n%s", term)
	}
}

// A PLAIN FORK'S LAUNCH BUILD is one progress line as well: its start line with the build line,
// its result with the store's clause, and its build jail's output in launch.log alone. Red with
// buildPlainForkForLaunch's report deleted.
func TestAPlainForksLaunchBuildIsOneProgressLine(t *testing.T) {
	f := forkBuildHome(t)
	var jailSeen run.Options
	withFakeCaptureJail(t, fakeBuildJail(t, &jailSeen, probetoolBuilt))
	talkingChild(t, 0, 0)
	stream := &launchStream{}
	got := buildForksForLaunch(run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: forkTestCommit}},
		Platform: "linux/arm64", Workspace: t.TempDir(), Stderr: stream}, stream, stream, false)
	term := stream.terminal()
	if got["probetool"].Key == "" {
		t.Fatalf("no build was delivered: %+v\n%s", got, term)
	}
	for _, w := range []string{"fork builds  1 fork never built at its pin on this machine",
		"build fork " + f.Key() + ": " + f.Source + " at " + shortSHA(forkTestCommit),
		"  " + sealDisclosure + "; it runs: " + f.Build,
		"built fork " + f.Key() + " at " + shortSHA(forkTestCommit) + "; this jail runs it — store key " + got["probetool"].Key,
	} {
		if !strings.Contains(term, w) {
			t.Errorf("the terminal lacks %q:\n%s", w, term)
		}
	}
	if strings.Contains(term, jailOutLine) || !strings.Contains(stream.logged(), jailOutLine) {
		t.Errorf("the build jail's output is on the terminal or missing from the log:\n%s", term)
	}
}

// launchReported runs one jail launch's advance with its build report, on one stream, as
// buildForksForLaunch runs it.
func (fx *patchedAdvanceFixture) launchReported(t *testing.T) (advanceResult, string) {
	t.Helper()
	stream := &launchStream{}
	r := advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman", out: stream,
		errw: stream, launch: true, report: newBuildReport("", stream, progress.Config{}, false)})
	return r, stream.terminal()
}

// A JAIL LAUNCH'S FIRST ADVANCE BUILDS IN THE CHILD, outside any interrupt scope — so its output is
// kept off the terminal, and the terminal's Ctrl-C still reaches the launch's own arm and ends the
// launch (§7) — and a fit that fails sends the advance to the series' base under the same start line,
// carrying the base's clause (PF-D23, PF-D78). Red with the advance's report-mode child runner or its
// start line deleted.
func TestAJailLaunchsFirstAdvanceBuildsInTheChildAndItsBaseGetsTheStartLine(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.failBuildsOf(t, "fourteen")
	r, term := fx.launchReported(t)
	if r.delivery.Key == "" || fx.child != 2 || !slices.Equal(fx.scoped, []bool{false, false}) {
		t.Fatalf("handed %+v after %d child builds (scoped %v), want the fit's and then the base's, unscoped\n%s",
			r.delivery, fx.child, fx.scoped, term)
	}
	fit, base := "v1.1.0 ("+shortSHA(v11)+") + 2 patches (series ", "v1.0.0 ("+shortSHA(fx.base)+") + 2 patches (series "
	held := "held at the series' base, since the newest version the series takes did not build"
	for _, w := range []string{
		"build fork forkpack/tool: " + fit,
		"), the first build of it on this machine",
		"  " + sealDisclosure + "; it runs: sh build.sh",
		"Building fork forkpack/tool: failed (",
		"so the series is built at its base v1.0.0 (" + shortSHA(fx.base) + ") instead",
		"build fork forkpack/tool: " + base,
		") — " + held,
		"built fork forkpack/tool: v1.0.0 (" + shortSHA(fx.base) + ") + 2 patches; this jail runs it — " + held + " — store key ",
	} {
		if !strings.Contains(term, w) {
			t.Errorf("the launch lacks %q:\n%s", w, term)
		}
	}
	for _, gone := range []string{"takes the series; building it", "in a sealed jail", "checking fork", "no build of it on this machine yet; replaying",
		"so it is built at its base"} {
		if strings.Contains(term, gone) {
			t.Errorf("the launch still prints %q, which the start line carries:\n%s", gone, term)
		}
	}
}

// A SERVING ADVANCE'S START LINE carries the wait and what a Ctrl-C starts the jail on, and its
// build runs under the interrupt scope (PF-D25).
func TestAServingAdvancesStartLineCarriesItsWait(t *testing.T) {
	fx, v11, _, _, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 16: "sixteen"})
	fx.later(2 * time.Hour)
	fx.child, fx.scoped = 0, nil
	r, term := fx.launchReported(t)
	if !r.built || fx.child != 1 || !slices.Equal(fx.scoped, []bool{true}) {
		t.Fatalf("built %v in %d child builds (scoped %v), want one scoped build\n%s", r.built, fx.child, fx.scoped, term)
	}
	for _, w := range []string{
		"build fork forkpack/tool: v1.3.0 (" + shortSHA(v13) + ") + 2 patches (series ",
		"), upstream moved past the good build v1.1.0 (" + shortSHA(v11) + ") — this launch waits for it, at most " +
			forkBuildWaitBound.String() + ", and a Ctrl-C starts this jail on the good build v1.1.0 (" + shortSHA(v11) + ") instead",
		"updated fork forkpack/tool: v1.1.0 (" + shortSHA(v11) + ") → v1.3.0 (" + shortSHA(v13) + "), 2 patches; " +
			"this jail runs the new build — store key ",
	} {
		if !strings.Contains(term, w) {
			t.Errorf("the launch lacks %q:\n%s", w, term)
		}
	}
	if strings.Contains(term, "upstream moved — ") {
		t.Errorf("the launch still prints the line the start line carries:\n%s", term)
	}
}
