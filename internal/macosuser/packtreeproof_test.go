package macosuser

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// shortSettle makes a finishing launch's wait for its guest to exit a few milliseconds long.
func shortSettle(t *testing.T) {
	t.Helper()
	bound, poll := packTreeSettleBound, packTreeSettlePoll
	packTreeSettleBound, packTreeSettlePoll = 30*time.Millisecond, time.Millisecond
	t.Cleanup(func() { packTreeSettleBound, packTreeSettlePoll = bound, poll })
}

// hostTable is a process table holding only processes that are not the sandbox account's: this
// test process and its parent, and launchd.
func hostTable() []GuestProc {
	return []GuestProc{
		{PID: 1, PPID: 0, Start: time.Now().Add(-24 * time.Hour)},
		{PID: os.Getppid(), PPID: 1, Start: time.Now().Add(-time.Hour)},
		{PID: os.Getpid(), PPID: os.Getppid(), Start: time.Now().Add(-time.Minute)},
	}
}

func TestPackTreeHoldersAttributesEachSandboxProcess(t *testing.T) {
	anchor := time.Now()
	before, after := anchor.Add(-time.Hour), anchor.Add(time.Second)
	const self, other = 100, 200
	procs := []GuestProc{
		{PID: 1, PPID: 0},
		{PID: self, PPID: 1, Start: before},
		{PID: other, PPID: 1, Start: before},
		// sudo (root, the host user's real uid) under this launch, its guest child under it.
		{PID: 101, PPID: self, Start: after},
		{PID: 102, PPID: 101, Sandbox: true, Start: after},
		// A guest of this launch that started before the anchor cannot exist, but if the
		// table says one is under this launch it is this launch's, whatever its start.
		{PID: 103, PPID: 101, Sandbox: true, Start: before},
		// Another running launch's guest, started after the anchor.
		{PID: 201, PPID: other, Start: after},
		{PID: 202, PPID: 201, Sandbox: true, Start: after},
		// An escaped guest (double-forked onto launchd) started after the anchor: may be ours.
		{PID: 300, PPID: 1, Sandbox: true, Start: after},
		// One started before anything could have handed it this tree.
		{PID: 301, PPID: 1, Sandbox: true, Start: before},
		// A host process under launchd is never a holder.
		{PID: 400, PPID: 1, Start: after},
		// A parent the table does not hold: unattributed.
		{PID: 500, PPID: 499, Sandbox: true, Start: after},
		// A loop in the table must not hang the walk.
		{PID: 600, PPID: 601, Sandbox: true, Start: before},
		{PID: 601, PPID: 600, Sandbox: true, Start: before},
	}
	got := map[int]bool{}
	for _, h := range packTreeHolders(procs, self, map[int]bool{other: true}, anchor) {
		got[h.PID] = true
	}
	want := map[int]bool{102: true, 103: true, 300: true, 500: true}
	if len(got) != len(want) {
		t.Errorf("holders = %v, want %v", got, want)
	}
	for pid := range want {
		if !got[pid] {
			t.Errorf("process %d is not counted as a holder; holders = %v", pid, got)
		}
	}
	// Prune has no launch of its own: the same table, self 0, still counts 102 and 103 —
	// nothing running claims them — as unattributed, by start time.
	got = map[int]bool{}
	for _, h := range packTreeHolders(procs, 0, map[int]bool{other: true}, anchor) {
		got[h.PID] = true
	}
	if !got[102] || got[103] || got[202] || got[301] {
		t.Errorf("prune's holders = %v; want 102 (after the anchor), not 103, 202 or 301", got)
	}
}

// runPackTreeLaunch runs RunMacosUser on the fixture with a pack tree, the agent returning
// agentRC, the process table from table (nil wires no reader), and returns the fixture path of
// the tree, the claim dir, and what the launch printed.
func runPackTreeLaunch(t *testing.T, agentRC int, table func() ([]GuestProc, error),
	configure func(*Deps, *Options)) (tree, claims string, out *bytes.Buffer, rc int) {
	t.Helper()
	shortSettle(t)
	workspace := filepath.Join(t.TempDir(), "project")
	cname := cnameFor(workspace, "")
	state := t.TempDir()
	unsealOnCleanup(t, state)
	host := writeFixturePackTree(t, "#!/bin/sh\necho launch\n", strconv.Itoa(agentRC))
	tree = StagedPackTreeRoot(cname, host, state)
	claims = t.TempDir()
	d := fixtureStateDeps(t, state, nil, nil)
	d.PackTreeClaimDir = func() string { return claims }
	if table != nil {
		d.GuestProcesses = func(Mode) ([]GuestProc, error) { return table() }
	}
	production := StagedPackTreeRoot(cname, host, "")
	d.RunWithProxy = func([]string) int {
		// The tree is claimed for the whole launch, by this process.
		b, err := os.ReadFile(filepath.Join(claims, filepath.Base(production)+sessionRecordSuffix))
		if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
			t.Errorf("the running launch holds no claim naming its pid on its tree: %q, %v", b, err)
		}
		return agentRC
	}
	out = &bytes.Buffer{}
	d.Out = out
	opts := newOpts(workspace)
	opts.HostPackRoot = host
	if configure != nil {
		configure(&d, &opts)
	}
	rc = RunMacosUser(d, opts)
	return tree, claims, out, rc
}

func assertNoClaims(t *testing.T, claims string) {
	t.Helper()
	entries, _ := os.ReadDir(claims)
	for _, e := range entries {
		t.Errorf("the launch left its claim behind: %s", e.Name())
	}
}

// THE BUG: every macos-user launch kept its guest pack tree, on a normal exit too, and printed a
// paragraph about it. A launch whose guest has exited now removes its own tree, quietly.
func TestANormalExitRemovesItsGuestPackTree(t *testing.T) {
	for _, agentRC := range []int{0, 42} {
		t.Run("agent "+strconv.Itoa(agentRC), func(t *testing.T) {
			tree, claims, out, rc := runPackTreeLaunch(t, agentRC,
				func() ([]GuestProc, error) { return hostTable(), nil }, nil)
			if rc != agentRC {
				t.Fatalf("RunMacosUser = %d, want %d\n%s", rc, agentRC, out)
			}
			if _, err := os.Lstat(tree); !os.IsNotExist(err) {
				t.Errorf("the launch kept its guest pack tree %s (%v):\n%s", tree, err, out)
			}
			if strings.Contains(out.String(), "pack tree") {
				t.Errorf("a normal exit said something about its pack tree:\n%s", out)
			}
			assertNoClaims(t, claims)
		})
	}
}

// A guest that is still exiting when the launch finishes (a supervisor just told to stop) is
// waited for, and then the tree goes.
func TestTheLaunchWaitsForItsGuestToExitBeforeRemovingTheTree(t *testing.T) {
	calls := 0
	tree, _, out, _ := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
		calls++
		table := hostTable()
		if calls == 1 {
			table = append(table, GuestProc{PID: 9001, PPID: os.Getpid(), Sandbox: true, Start: time.Now()})
		}
		return table, nil
	}, nil)
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("the tree was kept after its last guest exited (%v):\n%s", err, out)
	}
	if calls < 2 {
		t.Errorf("the process table was read %d time(s); the launch did not wait", calls)
	}
}

// A guest that is still alive keeps the tree, in ONE line naming it and the command that removes
// it later.
func TestALiveGuestKeepsTheTreeInOneLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		holder GuestProc
	}{
		{"under the launch", GuestProc{PID: 9002, PPID: os.Getpid(), Sandbox: true, Start: time.Now()}},
		{"escaped onto launchd", GuestProc{PID: 9002, PPID: 1, Sandbox: true, Start: time.Now()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, claims, out, _ := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
				return append(hostTable(), tc.holder), nil
			}, nil)
			if _, err := os.Stat(filepath.Join(tree, "local", "loopholes", "hello", "bin", "hello")); err != nil {
				t.Errorf("a tree a live guest may read was removed: %v", err)
			}
			assertOneKeptLine(t, out.String(), "sandbox process 9002")
			assertNoClaims(t, claims)
		})
	}
	t.Run("no process reader", func(t *testing.T) {
		tree, _, out, _ := runPackTreeLaunch(t, 0, nil, nil)
		if _, err := os.Stat(tree); err != nil {
			t.Errorf("a tree was removed with no proof its guest ended: %v", err)
		}
		assertOneKeptLine(t, out.String(), "could not list the sandbox's processes")
	})
}

// A process of the sandbox account that started before the launch staged its tree is some other
// session's, and does not keep this one.
func TestAnOlderSandboxProcessDoesNotKeepTheTree(t *testing.T) {
	tree, _, out, _ := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
		return append(hostTable(), GuestProc{PID: 7, PPID: 1, Sandbox: true,
			Start: time.Now().Add(-time.Hour)}), nil
	}, nil)
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("an hour-old sandbox process kept this launch's tree (%v):\n%s", err, out)
	}
}

func assertOneKeptLine(t *testing.T, out, why string) {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Kept guest pack tree") {
			kept = append(kept, line)
		}
	}
	if len(kept) != 1 {
		t.Fatalf("want one Kept line, got %d:\n%s", len(kept), out)
	}
	for _, want := range []string{why, "`yolo prune --apply`"} {
		if !strings.Contains(kept[0], want) {
			t.Errorf("the Kept line does not say %q:\n%s", want, kept[0])
		}
	}
	if strings.Contains(out, "Retaining guest pack tree") || strings.Contains(out, "restart capability") {
		t.Errorf("the old retention paragraph is still printed:\n%s", out)
	}
}

// THE OTHER REPORTED LAUNCH: provisioning refused it (exit 79 from the stage, 1 from yolo), after
// the bootstrap had read the tree. Its guest has exited, so the tree goes.
func TestARefusedProvisioningRemovesItsGuestPackTree(t *testing.T) {
	var stageRan bool
	tree, claims, out, rc := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) { return hostTable(), nil },
		func(d *Deps, o *Options) {
			o.Config = provisionCfg()
			run := d.Run
			d.Run = func(argv []string) int {
				if containsArg(argv, "/usr/bin/sandbox-exec") && !strings.Contains(strings.Join(argv, " "), "darwin-bootstrap") {
					stageRan = true
					return provision.StageRefusedStatus
				}
				return run(argv)
			}
		})
	if !stageRan || rc == 0 {
		t.Fatalf("the provisioning stage did not refuse the launch (ran %v, rc %d):\n%s", stageRan, rc, out)
	}
	if !strings.Contains(out.String(), "Provisioning refused the launch") {
		t.Fatalf("not the refusal path:\n%s", out)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("a refused launch kept its guest pack tree (%v):\n%s", err, out)
	}
	assertNoClaims(t, claims)
}

// A writer whose Run returned has stopped (Deps.Run waits), and no guest has seen the tree, so a
// failed copy removes it.
func TestAFailedPackCopyRemovesTheReservedTree(t *testing.T) {
	shortSettle(t)
	workspace := filepath.Join(t.TempDir(), "project")
	state := t.TempDir()
	unsealOnCleanup(t, state)
	host := writeFixturePackTree(t, "#!/bin/sh\necho copy\n")
	tree := StagedPackTreeRoot(cnameFor(workspace, ""), host, state)
	sibling := StagedPackTreeRoot(cnameFor(workspace, ""), writeFixturePackTree(t, "#!/bin/sh\necho sibling\n"), state)
	legacy := StagedPackRoot(cnameFor(workspace, ""), state)
	for _, dir := range []string{sibling, legacy} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := fixtureStateDeps(t, state, nil, func(argv []string) bool {
		return len(argv) == 4 && argv[0] == cpBin && argv[1] == "-R" && strings.Contains(argv[2], host)
	})
	var out bytes.Buffer
	d.Out = &out
	opts := newOpts(workspace)
	opts.HostPackRoot = host
	if rc := RunMacosUser(d, opts); rc == 0 {
		t.Fatal("a failed pack copy returned success")
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("the reserved tree of a failed copy was kept (%v):\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Could not stage entrypoint") {
		t.Errorf("the failed copy was not reported:\n%s", out.String())
	}
	for _, dir := range []string{sibling, legacy} {
		if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
			t.Errorf("cleanup changed %s: %v", dir, err)
		}
	}
}

// The installer capture and the fork build stage a tree and dispatch a guest too: the same proof.
func TestCaptureActsRemoveTheirGuestPackTreeOnceTheGuestExited(t *testing.T) {
	shortSettle(t)
	for _, tc := range []struct {
		name string
		run  func(Deps) (string, int)
	}{
		{"installer capture", func(d Deps) (string, int) {
			plan := BuildCapturePlan(testCaptureOptions())
			return plan.PackRoot, RunCapturePlan(d, plan)
		}},
		{"fork build", func(d Deps) (string, int) {
			plan := BuildForkBuildPlan(testForkBuildOptions())
			return plan.CapturePlan.PackRoot, RunForkBuildPlan(d, plan)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &captureDeps{}
			d := c.deps()
			d.GuestProcesses = func(Mode) ([]GuestProc, error) { return hostTable(), nil }
			root, rc := tc.run(d)
			if rc != 0 {
				t.Fatalf("capture act = %d, want 0\n%s", rc, c.out.String())
			}
			if !strings.Contains(strings.Join(c.ran, "\n"), rmBin+" -rf -- "+root) {
				t.Errorf("the capture did not remove its tree %s:\n%s", root, strings.Join(c.ran, "\n"))
			}
			if strings.Contains(c.out.String(), "pack tree") {
				t.Errorf("the capture said something about its pack tree:\n%s", c.out.String())
			}
		})
	}
}

// --- yolo prune's sweep ---

// makeSealedTree makes a sealed per-launch tree named leaf in sd's packs dir, its mtime at mtime.
func makeSealedTree(t *testing.T, sd, leaf string, mtime time.Time) string {
	t.Helper()
	root := filepath.Join(sd, packsLeaf, leaf)
	inner := filepath.Join(root, "local", "bin")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "hello"), []byte("hello"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{inner, filepath.Dir(inner), root} {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(root, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return root
}

func hostRun(t *testing.T) func([]string) int {
	return func(argv []string) int {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Logf("%v: %v\n%s", argv, err, out)
			return 1
		}
		return 0
	}
}

func TestPruneReapsKeptPackTreesOnceNothingMayReadThem(t *testing.T) {
	sd := t.TempDir()
	unsealOnCleanup(t, sd)
	claims := t.TempDir()
	staged := time.Now().Add(-time.Hour)
	free := makeSealedTree(t, sd, "yolo-a.free", staged)
	claimed := makeSealedTree(t, sd, "yolo-c.claimed", staged)
	stale := makeSealedTree(t, sd, "yolo-d.stale", staged)
	legacy := filepath.Join(sd, packsLeaf, "yolo-legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	// A running launch's claim, held by an open file here; its pid is 4242.
	live, err := openPackTreeClaim(claims, claimed, 4242)
	if err != nil {
		t.Fatal(err)
	}
	defer live.close()
	// A claim whose launch died without letting it go: nobody holds it.
	dead, err := openPackTreeClaim(claims, stale, 4343)
	if err != nil {
		t.Fatal(err)
	}
	dead.release()

	table := func() ([]GuestProc, error) {
		return []GuestProc{
			{PID: 1},
			// The running launch, and its guest, started after every tree: attributed to it.
			{PID: 4242, PPID: 1, Start: staged.Add(-time.Hour)},
			{PID: 4250, PPID: 4242, Sandbox: true, Start: time.Now()},
			// A sandbox process older than every tree: nothing could have handed it one.
			{PID: 77, PPID: 1, Sandbox: true, Start: staged.Add(-time.Hour)},
		}, nil
	}

	// The dry run first: removes nothing.
	dry := reapPackTrees(sd, claims, "", table, hostRun(t), os.Geteuid(), false)
	if !dry.Applicable || dry.Declined != "" {
		t.Fatalf("dry run: applicable %v, declined %q", dry.Applicable, dry.Declined)
	}
	if _, err := os.Stat(free); err != nil {
		t.Fatalf("the dry run removed a tree: %v", err)
	}

	r := reapPackTrees(sd, claims, "", table, hostRun(t), os.Geteuid(), true)
	removed := map[string]bool{}
	for _, e := range r.Removed {
		if e.Err != nil {
			t.Errorf("removing %s failed: %v", e.Path, e.Err)
		}
		removed[filepath.Base(e.Path)] = true
	}
	for _, leaf := range []string{"yolo-a.free", "yolo-d.stale"} {
		if !removed[leaf] {
			t.Errorf("%s was not reaped; removed %v, kept %+v", leaf, removed, r.Kept)
		}
		if _, err := os.Lstat(filepath.Join(sd, packsLeaf, leaf)); !os.IsNotExist(err) {
			t.Errorf("%s is still on disk (%v)", leaf, err)
		}
	}
	if _, err := os.Stat(claimed); err != nil {
		t.Errorf("a running launch's claimed tree was removed: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("a legacy workspace tree was touched: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(claims, "yolo-d.stale"+sessionRecordSuffix)); !os.IsNotExist(err) {
		t.Errorf("the dead launch's claim was not removed (%v)", err)
	}
	if r.Bytes() <= 0 {
		t.Errorf("the reap reports %d bytes", r.Bytes())
	}
}

func TestPruneKeepsATreeAnEscapedGuestMayRead(t *testing.T) {
	sd := t.TempDir()
	unsealOnCleanup(t, sd)
	tree := makeSealedTree(t, sd, "yolo-a.read", time.Now().Add(-time.Minute))
	table := func() ([]GuestProc, error) {
		return []GuestProc{{PID: 1}, {PID: 88, PPID: 1, Sandbox: true, Start: time.Now()}}, nil
	}
	r := reapPackTrees(sd, t.TempDir(), "", table, hostRun(t), os.Geteuid(), true)
	if len(r.Removed) != 0 || len(r.Kept) != 1 || !strings.Contains(r.Kept[0].Detail, "sandbox process 88") {
		t.Errorf("removed %+v, kept %+v", r.Removed, r.Kept)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("the tree was removed: %v", err)
	}
}

func TestPruneDeclinesWhenItCannotListTheSandboxProcesses(t *testing.T) {
	sd := t.TempDir()
	unsealOnCleanup(t, sd)
	tree := makeSealedTree(t, sd, "yolo-a.x", time.Now().Add(-time.Hour))
	r := reapPackTrees(sd, t.TempDir(), "", func() ([]GuestProc, error) { return nil, syscall.EPERM },
		hostRun(t), os.Geteuid(), true)
	if r.Declined == "" || len(r.Removed) != 0 {
		t.Errorf("declined %q, removed %+v", r.Declined, r.Removed)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("the tree was removed: %v", err)
	}
	if none := reapPackTrees(t.TempDir(), "", "", nil, nil, 0, true); none.Applicable {
		t.Errorf("a staging root with no packs dir is applicable: %+v", none)
	}
}

// A running session of the tree's workspace keeps it even with no claim and no guest alive: a
// launch whose claim failed, or an older yolo that makes none, between two steps.
func TestPruneKeepsATreeWhoseWorkspaceHasARunningSession(t *testing.T) {
	sd := t.TempDir()
	unsealOnCleanup(t, sd)
	tree := makeSealedTree(t, sd, "yolo-a.unclaimed", time.Now().Add(-time.Hour))
	gone := makeSealedTree(t, sd, "yolo-b.unclaimed", time.Now().Add(-time.Hour))
	sessions := t.TempDir()
	rec, err := openSessionRecord(sessions, SessionKey("yolo-a", "0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.close()
	ended, err := openSessionRecord(sessions, SessionKey("yolo-b", "fedcba9876543210"))
	if err != nil {
		t.Fatal(err)
	}
	ended.release()
	table := func() ([]GuestProc, error) { return []GuestProc{{PID: 1}}, nil }
	r := reapPackTrees(sd, t.TempDir(), sessions, table, hostRun(t), os.Geteuid(), true)
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("a running session's tree was removed: %v", err)
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Errorf("an ended session's tree was kept (%v): %+v", err, r.Kept)
	}
}

// The production wiring: a real launch reads the process table and claims its tree. Without
// either, every launch keeps its tree again, and only a Mac would notice.
func TestRealDepsWireTheGuestProcessReaderAndTheClaims(t *testing.T) {
	d := RealDeps(nil, nil, false)
	if d.GuestProcesses == nil || reflect.ValueOf(d.GuestProcesses).Pointer() != reflect.ValueOf(guestProcessesReal).Pointer() {
		t.Error("RealDeps does not wire guestProcessesReal")
	}
	if d.PackTreeClaimDir == nil || d.PackTreeClaimDir() != PackTreeClaimsDir() {
		t.Error("RealDeps does not wire PackTreeClaimsDir")
	}
	// Without the reaper, every session whose guest outlives the launch's wait keeps its tree.
	if d.StartPackTreeReaper == nil || reflect.ValueOf(d.StartPackTreeReaper).Pointer() != reflect.ValueOf(startPackTreeReaperReal).Pointer() {
		t.Error("RealDeps does not wire startPackTreeReaperReal")
	}
}
