package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// housekeepingmacosuser_test.go pins the macos-user arm's housekeeping slot and its offer
// (runMacosUserHousekeeping, startMacosUserHousekeeping): the arm used to run neither, so a
// Mac running only that backend grew the retired loophole-state archive and the shared cache
// with nothing ever reclaiming them, and no launch offered the cache.

// macosUserCall is what a fake macos-user backend is handed, the arguments the tests in this
// package's B26 files read.
type macosUserCall struct {
	cfg     *jsonx.OrderedMap
	overlay macosuser.HomeOverlay
	dryRun  bool
	packEnv *jsonx.OrderedMap
}

// fakeMacosUserRun is the ONE place these files spell the run.Options.MacosUserRun seam's
// signature, so a change to it is one edit here rather than one per test.
func fakeMacosUserRun(fn func(macosUserCall) int) func(*jsonx.OrderedMap, string, []string, []string,
	string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap,
	[]packload.BlockedTool, macosuser.JailDaemons) int {
	return func(cfg *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, overlay macosuser.HomeOverlay,
		_ macosuser.HostContext, dryRun bool, packEnv *jsonx.OrderedMap, _ []packload.BlockedTool,
		_ macosuser.JailDaemons) int {
		return fn(macosUserCall{cfg: cfg, overlay: overlay, dryRun: dryRun, packEnv: packEnv})
	}
}

// seedRetiredLoopholeGenerations plants n retired loophole-state generations in the machine
// store under the current HOME, oldest first, and returns their names.
func seedRetiredLoopholeGenerations(t *testing.T, n int) []string {
	t.Helper()
	return seedRetiredLoopholeGenerationsFrom(t, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), n)
}

// seedRetiredLoopholeGenerationsFrom is seedRetiredLoopholeGenerations with the first
// generation's stamp given, an hour apart from there, for a test that seeds twice.
func seedRetiredLoopholeGenerationsFrom(t *testing.T, base time.Time, n int) []string {
	t.Helper()
	archive := filepath.Join(paths.GlobalStorage(), "state", prune.RetiredLoopholeStateDir)
	var names []string
	for i := 0; i < n; i++ {
		name := base.Add(time.Duration(i) * time.Hour).Format("20060102-150405")
		if err := os.MkdirAll(filepath.Join(archive, name, "some-loophole"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(archive, name, "some-loophole", "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// remainingRetiredGenerations lists what is left in the archive, sorted.
func remainingRetiredGenerations(t *testing.T) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(paths.GlobalStorage(), "state", prune.RetiredLoopholeStateDir))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// reapStamps lists the debounce stamps under the build dir (classDebounce's last-<class>-reap and
// the image reap's own).
func reapStamps(t *testing.T) []string {
	t.Helper()
	entries, _ := os.ReadDir(paths.BuildDir())
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "last-") && strings.HasSuffix(e.Name(), "-reap") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// inMacosUserSlot reports whether the calling goroutine is running the macos-user slot.
func inMacosUserSlot() bool {
	buf := make([]byte, 64<<10)
	return strings.Contains(string(buf[:goruntime.Stack(buf, false)]), ".runMacosUserHousekeeping(")
}

// THE CALL SITE: a macos-user launch runs its slot, which reaps the retired loophole-state
// generations past the keep and says so in housekeeping.log. Fails if the arm's
// startMacosUserHousekeeping call is deleted (nothing else on this arm reaps the archive).
func TestMacosUserLaunchRunsItsHousekeepingSlot(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	seeded := seedRetiredLoopholeGenerations(t, 5)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	// The slot's goroutine reports the runtime calls it makes (it should make none).
	var mu sync.Mutex
	var slotArgv [][]string
	exec := o.Exec
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if inMacosUserSlot() {
			mu.Lock()
			slotArgv = append(slotArgv, argv)
			mu.Unlock()
		}
		return exec(argv, dir, env, timeout)
	}
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	housekeepingSlots.Wait()

	if got, want := remainingRetiredGenerations(t), seeded[2:]; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after a macos-user launch the retired generations are %v, want the newest %d %v: "+
			"the arm ran no housekeeping slot", got, hostArchiveKeepInSlot, want)
	}
	note, err := os.ReadFile(filepath.Join(ws, ".yolo", housekeepingLogName))
	if err != nil || !strings.Contains(string(note), "loophole state: reclaimed 2 retired generation(s)") {
		t.Errorf("housekeeping.log does not record the reap (err %v):\n%s", err, note)
	}
	// THE PASS ASKS NO RUNTIME: none of the container slot's classes ran, so nothing was asked of
	// podman, Apple Container or a binary named after this backend, and nothing declined.
	mu.Lock()
	defer mu.Unlock()
	if len(slotArgv) != 0 {
		t.Errorf("the macos-user slot ran commands, want none: %v", slotArgv)
	}
	if strings.Contains(strings.ToLower(string(note)), "declined") {
		t.Errorf("the macos-user slot noted a declined class, want none:\n%s", note)
	}
	// AND STAMPS ONLY ITS OWN CLASSES: a stamp for a container class (image tars, flake-bundle
	// generations, store outputs, the small classes, the image reap) would delay that class's pass
	// on a container launch of the same Mac by a day.
	if got, want := strings.Join(reapStamps(t), ","), "last-cache-reap,last-loophole-state-reap"; got != want {
		t.Errorf("the macos-user slot left the stamps %q, want %q", got, want)
	}
}

// THE CLASS'S OWN DEBOUNCE (BF-D3 (1)): a second macos-user launch inside the interval leaves the
// retired generations seeded since the first alone. Fails if reapRetiredLoopholeState stops
// consulting its stamp, which would walk and reap the archive on every launch.
func TestMacosUserRetiredLoopholeStateReapIsDebounced(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	first := seedRetiredLoopholeGenerations(t, 5)

	launch := func() {
		t.Helper()
		var stdout, stderr bytes.Buffer
		// The same clock on both launches (dispatchOptions' fixed o.Now), so the second falls
		// inside the interval the first launch's stamp opened.
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
		}
		housekeepingSlots.Wait()
	}
	launch()
	if got := remainingRetiredGenerations(t); strings.Join(got, ",") != strings.Join(first[2:], ",") {
		t.Fatalf("the first launch left %v, want the newest %d %v", got, hostArchiveKeepInSlot, first[2:])
	}
	if !slices.Contains(reapStamps(t), "last-loophole-state-reap") {
		t.Fatalf("the first launch wrote no last-loophole-state-reap stamp: %v", reapStamps(t))
	}

	second := seedRetiredLoopholeGenerationsFrom(t, time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), 5)
	launch()
	got := remainingRetiredGenerations(t)
	for _, name := range second {
		if !slices.Contains(got, name) {
			t.Errorf("a second launch inside the debounce interval reaped %s (left %v): the class "+
				"ran again on a stamp that says it is not due", name, got)
		}
	}
}

// THE OPT-OUT: YOLO_NO_AUTO_IMAGE_REAP turns the automatic classes off on this arm too (storage.md
// says so for every launch), so the archive is left whole and the class leaves no stamp that would
// delay the first pass after the opt-out is lifted. Fails if reapRetiredLoopholeState stops
// honoring it.
func TestMacosUserRetiredLoopholeStateReapHonorsTheOptOut(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	seeded := seedRetiredLoopholeGenerations(t, 5)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	getenv := o.Getenv
	o.Getenv = func(k string) string {
		if k == autoReapOptOutEnv {
			return "1"
		}
		return getenv(k)
	}
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	housekeepingSlots.Wait()

	if got := remainingRetiredGenerations(t); strings.Join(got, ",") != strings.Join(seeded, ",") {
		t.Errorf("with %s=1 a macos-user launch left %v of %v: the opt-out did not stop the reap",
			autoReapOptOutEnv, got, seeded)
	}
	if slices.Contains(reapStamps(t), "last-loophole-state-reap") {
		t.Errorf("with %s=1 a macos-user launch stamped the loophole-state class: %v",
			autoReapOptOutEnv, reapStamps(t))
	}
}

// A dry run starts no session, so it offers nothing and runs no slot.
func TestMacosUserDryRunOffersAndHousekeepsNothing(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	seeded := seedRetiredLoopholeGenerations(t, 5)
	RecordOfferMeasurement(cachePurgeClass, offerMeasurement{Bytes: 5 << 30, Detail: "9 files", When: time.Now()})

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	housekeepingSlots.Wait()

	if got := remainingRetiredGenerations(t); len(got) != len(seeded) {
		t.Errorf("a macos-user --dry-run reaped retired generations: %v left of %v", got, seeded)
	}
	if got := reapStamps(t); len(got) != 0 {
		t.Errorf("a macos-user --dry-run stamped %v", got)
	}
	if strings.Contains(stderr.String(), "is reclaimable") || strings.Contains(stdout.String(), "is reclaimable") {
		t.Errorf("a macos-user --dry-run offered the cache:\n%s%s", stdout.String(), stderr.String())
	}
}

// oldCacheFile plants a file in the shared cache that the 30-day purge would take, and returns it.
func oldCacheFile(t *testing.T) string {
	t.Helper()
	f := filepath.Join(paths.GlobalStorage(), "cache", "pip", "old-wheel.whl")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	return f
}

// THE OFFER ON A NON-TTY LAUNCH: one line naming the reclaimable size and `yolo prune --apply`,
// no prompt, and nothing deleted — never an implicit yes.
func TestMacosUserOfferPrintsOneLineAndDeletesNothingWithoutATerminal(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cached := oldCacheFile(t)
	RecordOfferMeasurement(cachePurgeClass, offerMeasurement{Bytes: 5 << 30, Detail: "9 files", When: time.Now()})

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Now = time.Now
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	housekeepingSlots.Wait()

	if n := strings.Count(stderr.String(), "is reclaimable"); n != 1 {
		t.Errorf("a non-TTY macos-user launch printed the offer %d times, want once:\n%s", n, stderr.String())
	}
	if !strings.Contains(stderr.String(), "yolo prune --apply") {
		t.Errorf("the offer line names no next step:\n%s", stderr.String())
	}
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("a non-TTY offer deleted %s (%v): no answer is never consent", cached, err)
	}
}

// THE OFFER'S ANSWER REACHES THE SLOT: a class the user already promoted to automatic is purged by
// this launch's slot. Fails if the arm hands the slot anything but the offer's result.
func TestMacosUserOfferConsentReachesTheSlot(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cached := oldCacheFile(t)
	RecordOfferAnswer(cachePurgeClass, OfferYes, time.Now())

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Now = time.Now
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	housekeepingSlots.Wait()

	if _, err := os.Stat(cached); err == nil {
		t.Errorf("%s survived a macos-user launch whose cache class is automatic: the offer's "+
			"consent did not reach the slot", cached)
	}
	note, _ := os.ReadFile(filepath.Join(ws, ".yolo", housekeepingLogName))
	if !strings.Contains(string(note), "cache: reclaimed") {
		t.Errorf("housekeeping.log does not record the purge:\n%s", note)
	}
}

// THE OFFER'S PLACE ON THE ARM: after the config-change approval (both prompts together, before
// any setup) and before the launch lock, which a prompt must never hold while a human reads it.
func TestMacosUserOfferSitsBetweenTheApprovalAndTheLaunchLock(t *testing.T) {
	arm := macosUserArm(t)
	pos := func(name string) token.Pos {
		var at token.Pos
		ast.Inspect(arm, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && at == token.NoPos {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
					at = call.Pos()
				}
			}
			return true
		})
		if at == token.NoPos {
			t.Fatalf("the macos-user arm no longer calls %s", name)
		}
		return at
	}
	approval, offer, lock := pos("checkConfigChanges"), pos("maybeOfferReclaim"), pos("holdLaunchLock")
	if !(approval < offer && offer < lock) {
		t.Errorf("the macos-user arm offers the cache at %d, want after the approval (%d) and "+
			"before the launch lock (%d)", offer, approval, lock)
	}
	slot := pos("startMacosUserHousekeeping")
	backend := pos("MacosUserRun")
	if !(slot < backend) {
		t.Errorf("the macos-user slot starts at %d, after the backend dispatch at %d: run there "+
			"it would hold the prompt after the agent exits", slot, backend)
	}
}

// macosUserArm is Run's `if rt == "macos-user"` block, parsed from run.go.
func macosUserArm(t *testing.T) *ast.BlockStmt {
	t.Helper()
	var arm *ast.BlockStmt
	ast.Inspect(funcDecl(t, "run.go", "Run"), func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || arm != nil {
			return true
		}
		if be, ok := ifs.Cond.(*ast.BinaryExpr); ok && be.Op == token.EQL {
			if id, ok := be.X.(*ast.Ident); ok && id.Name == "rt" {
				if lit, ok := be.Y.(*ast.BasicLit); ok && lit.Value == `"macos-user"` {
					arm = ifs.Body
				}
			}
		}
		return true
	})
	if arm == nil {
		t.Fatal(`run.go's Run has no if rt == "macos-user" block`)
	}
	return arm
}
