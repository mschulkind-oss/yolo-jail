package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loopholesloudness_test.go covers ONE defect class in the loophole activation
// path: a branch that takes a decision — a timeout, a fallback, a drop, a
// discarded error — and emits nothing.
//
// Every test here drives a REAL call site rather than the reporting helper alone,
// except the two that say why they cannot (the front-close grace, which no
// in-process front can be made to blow, and which is therefore pinned by a
// source-level call-site test beside its behavioural one). That is the standard
// AGENTS.md sets: a test that pins the callee while the call site is unpinned is
// not a test, and this repo has shipped that shape repeatedly.
//
// None of these lines is gated. They are silent on a healthy launch because a
// healthy launch does not reach the branch — which is how the loudness fits inside
// OQ-RO3 (docs/reference/report-tiers.md): a launch has no quiet mode, and density
// is bought by not being in trouble rather than by a flag.

// TestStopLoopholesReportsAPanickingStop: teardown recovers from a panicking
// stop() so one wedged handle cannot abandon the rest of the loop — but the
// recover used to be `_ = recover()`, which erased the only evidence that a
// teardown step ran and failed. A daemon left alive by a panicked stop is exactly
// the state a later "port already in use" is a symptom of.
func TestStopLoopholesReportsAPanickingStop(t *testing.T) {
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	later := false
	o.stopLoopholes([]loopholeDaemon{
		{name: "wedged", stop: func() { panic("boom") }},
		{name: "after", stop: func() { later = true }},
	}, "", "", "")
	if !later {
		t.Error("a panicking stop() abandoned the rest of the teardown loop")
	}
	for _, want := range []string{"Warning", "wedged", "panicked", "boom"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("panic report %q missing %q", buf.String(), want)
		}
	}
}

// TestStopLoopholesSaysWhenItTearsDownUnlocked: the relaunch flock is what keeps
// this teardown's rmtree from deleting the endpoint files a CONCURRENT launch is
// publishing into. Failing to take it does not stop the teardown, so the fallback
// has to be audible or the next launch's missing service has no antecedent.
//
// The fault is injected with no privilege: the locks dir cannot be created because
// a path component of it is a regular file.
func TestStopLoopholesSaysWhenItTearsDownUnlocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "yolo-jail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "yolo-jail", "locks"),
		nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	// The container check runs after the lock failure and must not shell out. It
	// ANSWERS "no such container" (Ran, exit 0, nothing listed): a runtime that could
	// not be asked makes the teardown keep the dir, which is a different branch
	// (TestTeardownRemovesTheHostServicesDirOnlyOnAnAnswer).
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	o.stopLoopholes(nil, socketsDir, "yolo-ws-loud0000", "podman")
	for _, want := range []string{"Warning", "relaunch lock", "yolo-ws-loud0000", "without it"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("unlocked-teardown report %q missing %q", buf.String(), want)
		}
	}
	// And it really did proceed unlocked — the report is about a fallback that
	// happened, not a refusal.
	if fileExists(socketsDir) {
		t.Errorf("sockets dir %s survived an unlocked teardown", socketsDir)
	}
}

// TestStopLoopholesReportsASocketsDirItCouldNotRemove: a surviving endpoint file
// names a port nobody is on, and the NEXT launch's readiness wait can be satisfied
// by it instantly — so a failed rmtree is a fault whose only symptom appears one
// launch later, in another function.
//
// The fault injector is a path ending in a "." element, which os.RemoveAll refuses
// with EINVAL while os.Stat is happy: the only way to make the rmtree fail without
// privileges, on a box where the test may well be running as root.
func TestStopLoopholesReportsASocketsDirItCouldNotRemove(t *testing.T) {
	dir := t.TempDir()
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.stopLoopholes(nil, dir+"/.", "", "")
	for _, want := range []string{"Warning", "host-services dir", "mislead the next launch"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("failed-rmtree report %q missing %q", buf.String(), want)
		}
	}
}

// TestFrontCloseGraceReportsWhatOutlivesTheJail: the bounded wait for a front's
// listener Close. Past the grace the endpoint file still exists and the per-jail
// bearer token inside it is still valid, and the only thing that retires it is
// stopLoopholes' rmtree — which this teardown does not wait for and which declines
// outright while a container is still running.
//
// Both branches are asserted, because the silence on the healthy one is the OQ-RO3
// half of this change: a front that closes promptly must print nothing at all.
func TestFrontCloseGraceReportsWhatOutlivesTheJail(t *testing.T) {
	endpoint := "/tmp/yolo-host-services-abcd1234/svc" + paths.ServiceEndpointExt

	var slow strings.Builder
	o := &Options{Stdout: &slow}
	fillDefaults(o)
	o.awaitFrontClosed("host service", "svc", endpoint, make(chan struct{}), 25*time.Millisecond)
	for _, want := range []string{"Warning", "host service", "svc", "25ms", endpoint, "credential"} {
		if !strings.Contains(slow.String(), want) {
			t.Errorf("front-close report %q missing %q", slow.String(), want)
		}
	}

	var fast strings.Builder
	o2 := &Options{Stdout: &fast}
	fillDefaults(o2)
	done := make(chan struct{})
	close(done)
	o2.awaitFrontClosed("host service", "svc", endpoint, done, time.Hour)
	if fast.String() != "" {
		t.Errorf("a front that closed promptly printed %q", fast.String())
	}
}

// TestBothFrontedStopPathsReportTheFrontCloseGrace pins the two CALL SITES of that
// report, and it reads the source because nothing else can.
//
// A front closes when its listener closes, so there is no in-process way to wedge
// one: svcendpoint's accept loop returns as soon as Close lands, which makes the
// expiry branch unreachable from a test that drives the real teardown. The
// behavioural test above therefore pins the message, and this pins the fact that
// both stop() closures still route their bounded wait through it — the exact
// pairing AGENTS.md demands, since a test of the helper alone would stay green with
// the two stop paths back on a bare, empty `select` (which is what they were).
func TestBothFrontedStopPathsReportTheFrontCloseGrace(t *testing.T) {
	const src = "loopholesruntime.go"
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, b, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	// A silent bounded wait on the grace is the shape being deleted; re-introducing
	// one anywhere in this file fails here even if the helper keeps its callers. AST,
	// not text: the helper's own doc comment quotes the deleted spelling, and a
	// grep-shaped check would therefore fail on the documentation of its own fix.
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "After" || len(call.Args) != 1 {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "time" {
			return true
		}
		if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "frontStopGrace" {
			t.Errorf("%s:%d: a bare time.After(frontStopGrace) is back — the front-close "+
				"grace must expire through awaitFrontClosed, which is the only thing "+
				"that says it did", src, fset.Position(call.Pos()).Line)
		}
		return true
	})
	for _, fn := range []string{"startHostSingleton", "startExternalService"} {
		var decl *ast.FuncDecl
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == fn {
				decl = fd
				break
			}
		}
		if decl == nil {
			t.Fatalf("%s not found in %s — this pin has lost its subject and is now "+
				"vacuous; repoint it at whatever tears a front down", fn, src)
		}
		calls := 0
		ast.Inspect(decl, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "awaitFrontClosed" {
					calls++
				}
			}
			return true
		})
		if calls != 1 {
			t.Errorf("%s calls awaitFrontClosed %d times, want exactly 1 — its stop() "+
				"must wait for the front's Close AND report the grace expiring", fn, calls)
		}
	}
}

// TestExternalServiceReportsAFrontThatCouldNotBind is the highest-value line in
// this change.
//
// The front's goroutine used to be spelled `_ = svcendpoint.ServeFrontWithOptions(…)`
// at both call sites, so a front that could not come up produced exactly one
// observable: the publication wait timing out SECONDS later, saying "did not
// publish" and nothing about why. That is the shape a port collision takes, and it
// cost this repo an afternoon and four wrong hypotheses.
//
// So the assertion is two-part, and the second half is what makes it a behavioural
// test rather than a wording one: the failure must be reported with the bind error,
// AND it must arrive without burning the readiness deadline.
func TestExternalServiceReportsAFrontThatCouldNotBind(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	// A services dir that is a regular FILE: the daemon's upstream socket still
	// binds (it lives in /tmp), so the daemon-readiness wait passes and the failure
	// is isolated to the front, whose publish dir cannot exist.
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.ServiceReadyTimeout = 3 * time.Second
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	hd := &loopholes.HostDaemon{
		Publishes:  loopholes.PublishesSocket,
		RequestEnd: loopholes.RequestEndFramed,
	}
	start := time.Now()
	h, ok := o.startExternalService("frontbind", spec, notADir,
		loopholes.TransportLoopbackTLS, "127.0.0.1", hd)
	elapsed := time.Since(start)
	if ok {
		if h.stop != nil {
			h.stop()
		}
		t.Fatalf("a front that could not bind produced a handle; output: %q", buf.String())
	}
	out := buf.String()
	for _, want := range []string{"Warning", "frontbind", "could not bind its listener"} {
		if !strings.Contains(out, want) {
			t.Errorf("front bind-failure report %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "did not publish") {
		t.Errorf("the bind error was reported as a publication timeout instead: %q", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("the bind failure took %s to surface with a %s readiness deadline — "+
			"the front's error must not wait for the deadline it makes moot",
			elapsed, o.ServiceReadyTimeout)
	}
	// The same launch also exercises the pre-spawn stale-artifact unlink, whose
	// error was discarded: here the endpoint path cannot even be statted.
	if !strings.Contains(out, "stale endpoint file") {
		t.Errorf("a pre-spawn unlink that failed for a reason other than ENOENT said "+
			"nothing: %q", out)
	}
}

// TestExternalServiceReportsAServiceThatIgnoresSIGTERM: the teardown escalation. A
// daemon that ignores SIGTERM costs every quit the full grace in wall clock and is
// the shape that leaves forked grandchildren behind — and the branch was a bare
// `_ = syscall.Kill(…, SIGKILL)`, so the one observable was a quit that took five
// seconds longer than it should and said nothing about which of N daemons spent
// them.
func TestExternalServiceReportsAServiceThatIgnoresSIGTERM(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.ServiceTermGrace = 250 * time.Millisecond
	spec := jsonx.NewOrderedMap()
	// No transport: readiness is the socket path EXISTING, so the daemon can be a
	// shell that touches it. `trap "" TERM` makes the shell ignore SIGTERM while the
	// loop keeps the group alive past the grace.
	spec.Set("command", []any{"sh", "-c",
		`trap "" TERM; : > "{socket}"; while :; do sleep 0.2; done`})
	h, ok := o.startExternalService("stubborn", spec, socketsDir, "", "", nil)
	if !ok {
		t.Fatalf("the daemon never became reachable; output: %q", buf.String())
	}
	h.stop()
	out := buf.String()
	for _, want := range []string{"Warning", "stubborn", "ignored SIGTERM for 250ms",
		"killing its process group"} {
		if !strings.Contains(out, want) {
			t.Errorf("SIGTERM-escalation report %q missing %q", out, want)
		}
	}
}

// TestExternalServiceSaysWhenItCannotOpenTheServiceLog: every failure line on this
// path ends in "see <logPath>", so a launch where that advice is FALSE has to say
// so before it gives it. The daemon still starts — its log is diagnostics, not a
// dependency — and that half is asserted too, because a report that changed the
// outcome would be a different (worse) change.
func TestExternalServiceSaysWhenItCannotOpenTheServiceLog(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "yolo-jail"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The log DIR cannot be created: it is a regular file. No privilege needed.
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "yolo-jail", "logs"),
		nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socketsDir := t.TempDir()
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.ServiceTermGrace = 250 * time.Millisecond
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"sh", "-c", `: > "{socket}"; sleep 30`})
	h, ok := o.startExternalService("nolog", spec, socketsDir, "", "", nil)
	if !ok {
		t.Fatalf("an unopenable log stopped the daemon from starting; output: %q", buf.String())
	}
	defer h.stop()
	for _, want := range []string{"Warning", "nolog", "no log"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing-log report %q missing %q", buf.String(), want)
		}
	}
}

// TestCgroupDelegateSaysWhyWhenTheKernelHasNoCgroupV2 and the bind test below are
// the OQ-R3 pair: "yolo could not ask" must stay distinguishable from "yolo asked
// and it failed", and the distinction is structural (two reporters, two registers)
// rather than a wording convention.
//
// Both declines used to be a bare `return nil, false`, which the caller turned into
// a loophole the user switched ON and that then simply was not there —
// packs/cgroup-delegate's manifest meanwhile says the cgroup-v2 question stays in
// this function because it is "the only place that can report it in the terms an
// operator can act on".
func TestCgroupDelegateSaysWhyWhenTheKernelHasNoCgroupV2(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the in-process delegate is Linux-only; off Linux the inert report owns this")
	}
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.IsMacOS = false
	o.PathExists = func(string) bool { return false }
	if _, ok := o.startCgroupDelegate("yolo-ws-loud0000", "podman", t.TempDir()); ok {
		t.Fatal("the delegate started without cgroup v2")
	}
	out := buf.String()
	for _, want := range []string{"cgroup-delegate", "cgroup v2", "Nothing is missing", "yolo-cglimit"} {
		if !strings.Contains(out, want) {
			t.Errorf("no-cgroup-v2 report %q missing %q", out, want)
		}
	}
	// A machine that cannot run the delegate is not a machine with a broken one.
	if strings.Contains(out, "Warning") {
		t.Errorf("a host that could not be asked was reported as a failure: %q", out)
	}
}

func TestCgroupDelegateReportsABindFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the in-process delegate is Linux-only")
	}
	var buf strings.Builder
	o := &Options{Stdout: &buf}
	fillDefaults(o)
	o.IsMacOS = false
	o.PathExists = func(string) bool { return true } // pretend cgroup v2 is present
	missing := filepath.Join(t.TempDir(), "gone")    // the bind's parent dir does not exist
	if _, ok := o.startCgroupDelegate("yolo-ws-loud0000", "podman", missing); ok {
		t.Fatal("the delegate reported success with no socket bound")
	}
	out := buf.String()
	for _, want := range []string{"Warning", "cgroup-delegate", "could not bind",
		filepath.Join(missing, paths.CgdSocketName), "yolo-cglimit"} {
		if !strings.Contains(out, want) {
			t.Errorf("delegate bind-failure report %q missing %q", out, want)
		}
	}
	// The other half of the OQ-R3 split: this machine COULD run the delegate, so the
	// reassuring register would be a lie.
	if strings.Contains(out, "Nothing is missing") {
		t.Errorf("an actionable delegate failure was reported as an unsupported host: %q", out)
	}
}
