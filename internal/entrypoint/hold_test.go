package entrypoint

// hold_test.go pins the hold that keeps a refused boot's container alive.
//
// ⚠ THE FEATURE BLOCKS, so nothing here may simply call it and wait. The shape used
// instead is the one hold.go was built for: beginHold is two phases, and the notice
// (phase one) is assertable without entering the wait (phase two). The wait itself
// is exercised with its release path relocated into a t.TempDir(), so the real
// block-and-release runs with no /tmp side effect and no cross-test race.
//
// The pin that matters most is the LAST one. Everything above it asserts that the
// hold works; only that one asserts that Main USES it, which is the
// callee-pinned/call-site-unpinned shape AGENTS.md names.

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// holdEnv builds an Env with the given hold-related variables and a buffer standing
// in for the boot's stderr — which is where the notice has to land, because that
// writer is also boot.log's (bootlog.go), and a held boot's instructions belong in
// the log that outlives the container.
func holdEnv(t *testing.T, vars map[string]string) (*Env, *bytes.Buffer) {
	t.Helper()
	e := NewEnv(vars)
	var buf bytes.Buffer
	e.Stderr = &buf
	return e, &buf
}

// relocateHold points the release file into a temp dir and shortens the poll, so
// the tests below exercise the REAL wait rather than a stand-in for it.
func relocateHold(t *testing.T) string {
	t.Helper()
	release := filepath.Join(t.TempDir(), "release")
	origFile, origPoll := holdReleaseFile, holdPollInterval
	holdReleaseFile, holdPollInterval = release, time.Millisecond
	t.Cleanup(func() { holdReleaseFile, holdPollInterval = origFile, origPoll })
	return release
}

// THE DEFAULT, and it is the one that must never regress: a jail that hangs instead
// of failing is worse than one that fails, and CI would acquire a hung container on
// its first red boot. So an absent variable holds nothing — the wait is not a wait.
//
// What it DOES get is the offer, which is this feature's whole discoverability: the
// incident it was built for cost three failed attempts by someone who owns the code,
// and by the third the container had been removed twice. A dial nobody knows about
// is a dial nobody uses.
func TestBeginHoldWithoutTheOptInOffersItAndDoesNotBlock(t *testing.T) {
	relocateHold(t)
	e, buf := holdEnv(t, map[string]string{})

	done := make(chan struct{})
	go func() {
		beginHold(e)()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("beginHold BLOCKED with the opt-in unset — every boot that refuses " +
			"would hang, which is the one outcome this feature may never cause")
	}

	got := buf.String()
	if !strings.Contains(got, paths.HoldOnRefusalEnv) {
		t.Errorf("a refusal that did not opt in must name the way to catch the next one; "+
			"got %q", got)
	}
	if strings.Contains(got, "HOLDING THE CONTAINER OPEN") {
		t.Errorf("a launch that did not opt in was told the container is being held: %q", got)
	}
}

// And the offer is not repeated at someone who already took it: the held boot's
// notice says everything the offer would, and a launch stream that repeats itself
// trains the reader to skim the lines that exist to be read.
func TestTheOfferIsNotMadeToAHoldThatIsAlreadyOn(t *testing.T) {
	release := relocateHold(t)
	e, buf := holdEnv(t, map[string]string{paths.HoldOnRefusalEnv: "1"})

	wait := beginHold(e)
	if strings.Contains(buf.String(), "Re-run with") {
		t.Errorf("the held boot was offered the variable it is already running under: %q",
			buf.String())
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wait()
}

// THE NOTICE. A hold nobody knows how to use is a hang, so this asserts the two
// things that make it a diagnostic instead: how to GET IN (the container, named, in
// a command that can be typed) and how to GET OUT (without the launcher).
func TestBeginHoldSaysHowToGetInAndHowToGetOut(t *testing.T) {
	release := relocateHold(t)
	e, buf := holdEnv(t, map[string]string{
		paths.HoldOnRefusalEnv: "1",
		paths.HoldExecEnv:      "podman exec -it yolo-ws-abcd1234 bash",
	})

	wait := beginHold(e)
	got := buf.String()
	for _, want := range []string{
		// The container, by the name the launch banner used, inside a runnable command.
		"podman exec -it yolo-ws-abcd1234 bash",
		// The way out, which must not need the launcher.
		"touch " + release,
		"SIGINT",
		// And what it is, so the line is not read as a crash.
		paths.HoldOnRefusalEnv,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the hold notice does not contain %q; a hold that does not say how "+
				"to use it is indistinguishable from a hang.\ngot:\n%s", want, got)
		}
	}

	// Release it so the test does not leave a goroutine parked on the wait.
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wait()
}

// THE BLOCK AND THE RELEASE, end to end: with the opt-in set the wait really does
// hold, and a `touch` of the file the notice named really does end it. This is the
// "reached when the variable is set" half of the pair.
func TestBeginHoldBlocksUntilTheReleaseFileAppears(t *testing.T) {
	release := relocateHold(t)
	e, _ := holdEnv(t, map[string]string{paths.HoldOnRefusalEnv: "1"})

	wait := beginHold(e)
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the wait returned before anything released it — the container would be " +
			"torn down with the evidence in it, which is the defect this exists to fix")
	case <-time.After(50 * time.Millisecond):
	}

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("touching the release file did not end the hold; the notice tells a human " +
			"to type exactly that, and it must not need the launcher")
	}
}

// A STALE RELEASE FILE MAY NOT DEFEAT THE HOLD. /tmp is a per-container tmpfs so
// one should not exist, but a hold that ends on its first tick reads as the feature
// not working at all — a failure mode indistinguishable from the bug.
func TestBeginHoldClearsAStaleReleaseFile(t *testing.T) {
	release := relocateHold(t)
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := holdEnv(t, map[string]string{paths.HoldOnRefusalEnv: "1"})

	wait := beginHold(e)
	if _, err := os.Stat(release); !os.IsNotExist(err) {
		t.Fatalf("beginHold left a pre-existing release file in place (stat err: %v)", err)
	}

	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("a stale release file ended the hold immediately")
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	<-done
}

// THE DEADLOCK PIN. ⚠ The defect cannot be reproduced by calling the bad code: a
// receive on a nil channel with no other goroutine is a runtime-fatal deadlock and
// would take the whole test binary down. So what is asserted is the PROPERTY that
// makes holding sound — the same pin, for the same reason, as
// internal/wirebridged's TestDaemonContextCanBeDone, which exists because that
// exact mistake crashed every credential-less boot.
func TestHoldContextCanBeDone(t *testing.T) {
	ctx, stop := holdContext()
	defer stop()
	if ctx.Done() == nil {
		t.Fatal("the hold's context has a nil Done channel, so holdUntilReleased would " +
			"deadlock the process instead of holding — which is what context.Background() " +
			"does, and `select {}` does worse")
	}
}

// And the behaviour that context buys: a `podman stop` arrives as SIGTERM, and a
// hold that ignored it would sit there until the runtime escalated to SIGKILL.
func TestHoldUntilReleasedReturnsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		holdUntilReleased(ctx, filepath.Join(t.TempDir(), "never"), time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled context did not end the hold")
	}
}

// THE FALLBACK. The launcher composes the exec line because the jail cannot (see
// paths.HoldExecEnv), but the opt-in can also be set from inside an already-running
// jail, with no launcher to have composed anything. The line must still be usable
// and must not silently guess a runtime — a container's hostname is its own id,
// which every runtime's `exec` accepts.
func TestHoldExecLineFallsBackToTheContainerIdentity(t *testing.T) {
	e := NewEnv(map[string]string{paths.HoldOnRefusalEnv: "1"})
	got := holdExecLine(e)
	if !strings.Contains(got, "exec -it") {
		t.Errorf("the fallback exec line is not a command: %q", got)
	}
	host, err := os.Hostname()
	if err == nil && host != "" && !strings.Contains(got, host) {
		t.Errorf("the fallback exec line %q does not name this container (%q)", got, host)
	}
	if strings.HasPrefix(got, "podman ") {
		t.Errorf("the fallback guessed a runtime (%q); YOLO_RUNTIME is a hard-coded "+
			"`podman` in every jail, so guessing from in here prints a binary that does "+
			"not exist on an Apple Container host", got)
	}
}

// THE CALL-SITE PIN, and it is the one that matters. Everything above asserts the
// hold is sound; none of it fails if Main never calls it — which is precisely the
// shape AGENTS.md names, and this repo has shipped it repeatedly. Main runs the
// whole boot for real (it generates, spawns and execs), so an AST check over its
// source is what is left, the same shape internal/wirebridged's
// TestMainIdlesOnTheDaemonContext uses.
//
// It pins the PLACEMENT, not just the call: the hold has to sit inside the
// genFailuresError branch — the refusal it is for — and that branch has to still
// `return err`, because the hold changes WHEN the jail dies and never whether the
// launch failed. A hold on a boot that then exited 0 would be strictly worse than
// no hold at all.
func TestMainHoldsInsideTheGeneratorRefusalAndStillReturnsTheError(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "boot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var branch *ast.IfStmt
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			ifs, ok := m.(*ast.IfStmt)
			if !ok || ifs.Init == nil {
				return true
			}
			if callsFunc(ifs.Init, "genFailuresError") {
				branch = ifs
				return false
			}
			return true
		})
		return false
	})
	if branch == nil {
		t.Fatal("Main has no `if err := genFailuresError(e); err != nil` branch — that is " +
			"the refusal the hold sits on, and this test cannot see it any more")
	}

	if !callsFunc(branch.Body, "beginHold") {
		t.Error("Main's generator-refusal branch does not call beginHold(). Without it the " +
			"container is removed the instant the boot refuses (--rm), taking the process " +
			"table, the listeners and the daemon state with it — which is the whole defect " +
			"internal/entrypoint/hold.go exists for.")
	}

	var returnsErr bool
	ast.Inspect(branch.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "err" {
			returnsErr = true
		}
		return true
	})
	if !returnsErr {
		t.Error("Main's generator-refusal branch no longer returns the error. The hold may " +
			"only change WHEN the jail dies; the failure must still be reported and the " +
			"exit code must still be non-zero once the hold ends.")
	}
}

// callsFunc reports whether n contains a call to the named plain function.
func callsFunc(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		call, ok := m.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return true
	})
	return found
}
