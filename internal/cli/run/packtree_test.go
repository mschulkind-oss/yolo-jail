package run

// packtree_test.go pins ONE IMMUTABLE PACK TREE PER LAUNCH (packtree.go; the maintainer's OQ-PK2
// ruling, option (c), docs/reference/pack-system.md#oq-pk2): how a launch hands its tree to its
// container, when the tree goes, how an attach finds the tree a running jail booted from, and
// what the attach composes from it. The attach's end-to-end behavior through Run — it writes
// nothing into the jail's tree, discards its own staging, and says what differs — is in
// concurrentstaging_test.go.

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// newTreeForTest creates a pack tree under cname's root holding one embedded-style pack.
func newTreeForTest(t *testing.T, cname, pack string) string {
	t.Helper()
	tree, err := newPackTree(cname)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tree, officialStagingDir, pack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"`+pack+`"}`)
	return tree
}

// TestAGoneContainersPackTreeGoesWithItsTracking: on the known-gone evidence that drops the
// tracking file, the launch that handed its tree to the container removes the tree and the
// live-tree record naming it. Driven through the real normal-exit teardown. An older tree beside
// it, and another jail's, are not this launch's and stay.
func TestAGoneContainersPackTreeGoesWithItsTracking(t *testing.T) {
	const cname = "yolo-tree-gone"
	o, _, _ := trackingFixture(t, cname, ExecResult{Ran: true, RC: 0})
	older := newTreeForTest(t, cname, "claude")
	mine := newTreeForTest(t, cname, "claude")
	other := newTreeForTest(t, "yolo-tree-other", "claude")
	if err := writeLivePackTree(cname, mine); err != nil {
		t.Fatal(err)
	}
	o.packTree, o.packTreeHeld = mine, true

	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)

	if _, err := os.Lstat(mine); !os.IsNotExist(err) {
		t.Errorf("the launch's own pack tree %s survived a known-gone exit (err %v)", mine, err)
	}
	if _, err := os.Lstat(paths.LivePackTreeRecord(cname)); !os.IsNotExist(err) {
		t.Errorf("the live-tree record survived its tree (err %v)", err)
	}
	for _, keep := range []string{older, other} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("a pack tree this launch did not hand to its container was removed: %s (%v)", keep, err)
		}
	}
}

// TestAPackTreeStaysUnlessItsContainerIsKnownGone: "could not ask", "still there", and a tree
// no container was ever handed (packTreeHeld false, which Run's deferred discard owns) all leave
// the tree, as they leave the tracking file. A host-side rmdir under a live bind detaches it.
func TestAPackTreeStaysUnlessItsContainerIsKnownGone(t *testing.T) {
	const cname = "yolo-tree-kept"
	for _, c := range []struct {
		name   string
		answer ExecResult
		held   bool
	}{
		{"the probe did not run", ExecResult{Ran: false}, true},
		{"the probe failed", ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"}, true},
		{"the probe timed out", ExecResult{Ran: true, Timeout: true}, true},
		{"the container is still there", ExecResult{Ran: true, RC: 0, Stdout: "abcd1234\n"}, true},
		{"no container was handed the tree", ExecResult{Ran: true, RC: 0}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, _, _ := trackingFixture(t, cname, c.answer)
			tree := newTreeForTest(t, cname, "claude")
			if err := writeLivePackTree(cname, tree); err != nil {
				t.Fatal(err)
			}
			o.packTree, o.packTreeHeld = tree, c.held
			o.forgetGoneContainer(cname, "podman", "")
			if _, err := os.Stat(tree); err != nil {
				t.Errorf("the pack tree went on %q: %v", c.name, err)
			}
			if _, err := os.Stat(paths.LivePackTreeRecord(cname)); err != nil {
				t.Errorf("the live-tree record went on %q: %v", c.name, err)
			}
		})
	}
}

// TestALateTeardownLeavesALaterLaunchsRecord: a launch that restarted the jail has written the
// record for ITS tree; the earlier launch's teardown, seeing its own container gone, removes its
// own tree and must leave the later launch's record alone.
func TestALateTeardownLeavesALaterLaunchsRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const cname = "yolo-tree-record"
	earlier := newTreeForTest(t, cname, "claude")
	later := newTreeForTest(t, cname, "claude")
	if err := writeLivePackTree(cname, later); err != nil {
		t.Fatal(err)
	}
	forgetLivePackTree(cname, earlier)
	if dir, _ := runningJailPackTree(cname, "podman"); dir != later {
		t.Errorf("after the earlier launch's teardown the running jail's tree reads as %q, want %q", dir, later)
	}
}

// TestDiscardPackTreeReachesOnlyThisWorkspacesTrees: the removal takes only a direct child of the
// cname's pack-tree root, so a wrong argument cannot reach another jail's tree or anything else.
func TestDiscardPackTreeReachesOnlyThisWorkspacesTrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mine := newTreeForTest(t, "yolo-tree-a", "claude")
	other := newTreeForTest(t, "yolo-tree-b", "claude")
	for _, wrong := range []string{other, paths.PackTreeRoot("yolo-tree-a"),
		filepath.Join(mine, officialStagingDir), ""} {
		discardPackTree("yolo-tree-a", wrong)
	}
	for _, keep := range []string{mine, other, filepath.Join(mine, officialStagingDir, "claude")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("discardPackTree removed %s through an argument that is not one of this cname's trees: %v", keep, err)
		}
	}
	discardPackTree("yolo-tree-a", mine)
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Errorf("discardPackTree left this workspace's own tree %s (%v)", mine, err)
	}
}

// TestRunningJailPackTreeFindsTheBootedTree: the record's tree when it is there; the shared tree an
// older launch left when the record is missing or names a tree that is gone; nothing when neither,
// with why. On Apple Container the shared tree is never the answer: that backend copied it at the
// jail's launch and older attaches re-staged it since, so it need not be what the jail has.
func TestRunningJailPackTreeFindsTheBootedTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const cname = "yolo-tree-find"
	if dir, why := runningJailPackTree(cname, "podman"); dir != "" || !strings.Contains(why, "no record") {
		t.Fatalf("with nothing on disk the running jail's tree reads as %q (%q)", dir, why)
	}
	legacy := paths.LegacyPackStagingDir(cname)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, why := runningJailPackTree(cname, "podman"); dir != legacy || why != "" {
		t.Errorf("with only the shared tree it reads as %q (%q), want %q", dir, why, legacy)
	}
	if dir, why := runningJailPackTree(cname, "container"); dir != "" || !strings.Contains(why, "Apple Container") {
		t.Errorf("on Apple Container the shared tree reads as the booted one: %q (%q)", dir, why)
	}
	tree := newTreeForTest(t, cname, "claude")
	if err := writeLivePackTree(cname, tree); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"podman", "container"} {
		if dir, why := runningJailPackTree(cname, rt); dir != tree || why != "" {
			t.Errorf("%s: with a record it reads as %q (%q), want the recorded %q", rt, dir, why, tree)
		}
	}
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if dir, _ := runningJailPackTree(cname, "podman"); dir != legacy {
		t.Errorf("with a record naming a gone tree it reads as %q, want the shared %q", dir, legacy)
	}
	// A record can never name a path outside the root.
	if err := os.WriteFile(paths.LivePackTreeRecord(cname), []byte("../../elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, _ := runningJailPackTree(cname, "podman"); dir != legacy {
		t.Errorf("a record naming %q was followed: %q", "../../elsewhere", dir)
	}
}

// TestAnUnrecordedTreeNamesConfiguredPacksByTheirConfigName: the shared tree an older launch left
// carries no record, so a configured pack's directory is its SLUG; the attach names it the way the
// config does, as that launch did, and walks the tree in the jail loader's order.
func TestAnUnrecordedTreeNamesConfiguredPacksByTheirConfigName(t *testing.T) {
	home := packHome(t)
	local := localPackDir(t, "mine")
	writeUserPacks(t, home, `[{"source":"file://`+local+`","name":"renamed"}]`)
	root := t.TempDir()
	for _, dir := range []string{filepath.Join(root, officialStagingDir, "claude"), filepath.Join(root, "renamed")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, `{"name":"`+filepath.Base(dir)+`"}`)
	}
	packs, err := loadPackTree(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "claude,renamed" {
		t.Errorf("the unrecorded tree loads as %v, want [claude renamed]", names)
	}
}

// TestDiffPackSetsSaysWhatARestartWouldChange: added, removed and changed, by name and content.
func TestDiffPackSetsSaysWhatARestartWouldChange(t *testing.T) {
	mk := func(name, body string) *packload.Pack {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, `{"name":"`+name+`"}`)
		if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		p, probs := packload.LoadDir(dir, name)
		if len(probs) > 0 {
			t.Fatal(probs)
		}
		return p
	}
	configured := []*packload.Pack{mk("same", "1"), mk("edited", "new"), mk("fresh", "1")}
	booted := []*packload.Pack{mk("same", "1"), mk("edited", "old"), mk("gone", "1")}
	d := diffPackSets(configured, booted)
	if strings.Join(d.Added, ",") != "fresh" || strings.Join(d.Removed, ",") != "gone" ||
		strings.Join(d.Changed, ",") != "edited" || len(d.Unreadable) != 0 {
		t.Errorf("diff = %+v, want added [fresh], removed [gone], changed [edited]", d)
	}
	if !diffPackSets(booted, booted).empty() {
		t.Error("a pack set compared with itself differs")
	}
	// The execute bit is content: a script that lost it renders a different jail.
	a, b := mk("mode", "1"), mk("mode", "1")
	if err := os.Chmod(filepath.Join(b.Root, "x.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if diffPackSets([]*packload.Pack{a}, []*packload.Pack{b}).empty() {
		t.Error("an execute bit set on one side did not count as a change")
	}
}

// TestRetireLegacyPackStagingNeedsAKnownGoneContainer: the shared tree an older launch left goes
// only when the runtime ANSWERS that no container of the name exists. A jail launched before
// per-launch trees binds it, so "could not ask" and "still there" keep it.
func TestRetireLegacyPackStagingNeedsAKnownGoneContainer(t *testing.T) {
	const cname = "yolo-legacy-retire"
	for _, c := range []struct {
		name   string
		answer ExecResult
		gone   bool
	}{
		{"no container of the name", ExecResult{Ran: true, RC: 0}, true},
		{"the probe did not run", ExecResult{Ran: false}, false},
		{"the probe failed", ExecResult{Ran: true, RC: 125}, false},
		{"a container is there", ExecResult{Ran: true, RC: 0, Stdout: "abcd1234\n"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, _, _ := trackingFixture(t, cname, c.answer)
			legacy := paths.LegacyPackStagingDir(cname)
			if err := os.MkdirAll(filepath.Join(legacy, officialStagingDir, "claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			o.retireLegacyPackStaging(cname, "podman")
			_, err := os.Stat(legacy)
			if c.gone && !os.IsNotExist(err) {
				t.Errorf("the shared tree survived %q (%v)", c.name, err)
			}
			if !c.gone && err != nil {
				t.Errorf("the shared tree went on %q: %v", c.name, err)
			}
		})
	}
}

// TestTheFreshPathHandsItsPackTreeToTheContainer pins runContainer's fresh-path call sites, which
// a unit fixture cannot drive to the container start: the tree is recorded and marked held only
// after every attach site and before the container starts (a launch that attached must never
// claim a tree, and a container must never start without its record), the shared tree an older
// launch left is retired only once no attach site took the launch, and the skills and briefing
// refresh runs only on the fresh path — an attach refreshes from the running jail's tree inside
// attachExisting, and a refresh above the attach decision is the re-stage the ruling removed.
func TestTheFreshPathHandsItsPackTreeToTheContainer(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var lastAttach, record, held, retire, refresh, start, lockPos token.Pos
	refreshes := 0
	ast.Inspect(fd, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.CallExpr:
			switch skelCallee(st) {
			case "attachExisting":
				if st.Pos() > lastAttach {
					lastAttach = st.Pos()
				}
			case "writeLivePackTree":
				record = st.Pos()
			case "retireLegacyPackStaging":
				retire = st.Pos()
			case "refreshJailBriefings":
				refresh = st.Pos()
				refreshes++
			case "startKeeper":
				// The keeper this launch spawns starts the container (keeperspawn.go).
				start = st.Pos()
			case "holdLaunchLock":
				lockPos = st.Pos()
			}
		case *ast.AssignStmt:
			for i, l := range st.Lhs {
				if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "packTreeHeld" {
					if id, ok := st.Rhs[i].(*ast.Ident); ok && id.Name == "true" {
						held = st.Pos()
					}
				}
			}
		}
		return true
	})
	for name, pos := range map[string]token.Pos{"attachExisting": lastAttach, "writeLivePackTree": record,
		"packTreeHeld = true": held, "retireLegacyPackStaging": retire, "refreshJailBriefings": refresh,
		"startKeeper": start, "holdLaunchLock": lockPos} {
		if pos == token.NoPos {
			t.Fatalf("runContainer no longer has %s; re-anchor this pin, do not delete it", name)
		}
	}
	if refreshes != 1 {
		t.Errorf("runContainer calls refreshJailBriefings %d times, want once, on the fresh path", refreshes)
	}
	if !(lockPos < lastAttach) {
		t.Error("the launch lock is taken after an attach decision")
	}
	for name, pos := range map[string]token.Pos{"writeLivePackTree": record, "packTreeHeld = true": held,
		"retireLegacyPackStaging": retire, "refreshJailBriefings": refresh} {
		if !(lastAttach < pos) {
			t.Errorf("%s runs before runContainer's last attach decision, so a launch that ends up "+
				"attaching runs it too", name)
		}
	}
	if !(record < start && held < start) {
		t.Error("the container starts before this launch's pack tree is recorded and marked held")
	}
	if !(retire < refresh) {
		t.Error("the shared tree is retired after the refresh; retire it first, with the other fresh-path housekeeping")
	}
}

// TestAttachExistingComposesFromTheRunningJailsTree pins attachExisting's order: the running
// jail's packs are loaded before the contract gate (what the gate weighs is what this entry
// delivers, composed over those packs), and both the refresh and the delivery read that view,
// never the configured staging the caller passed in.
func TestAttachExistingComposesFromTheRunningJailsTree(t *testing.T) {
	fd := methodDecl(t, "run.go", "attachExisting")
	var view, gate token.Pos
	readsView := map[string]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "runningJailPackView":
			view = call.Pos()
		case "attachContractFor":
			gate = call.Pos()
		case "refreshJailBriefings", "deliverChannelOnAttach":
			for _, a := range call.Args {
				if sel, ok := a.(*ast.SelectorExpr); ok && skelIdent(sel.X) == "view" && sel.Sel.Name == "staged" {
					readsView[skelCallee(call)] = true
				}
			}
		}
		return true
	})
	if view == token.NoPos || gate == token.NoPos {
		t.Fatal("attachExisting no longer calls runningJailPackView or attachContractFor; re-anchor this pin")
	}
	if !(view < gate) {
		t.Error("the contract gate runs before the running jail's packs are read")
	}
	for _, callee := range []string{"refreshJailBriefings", "deliverChannelOnAttach"} {
		if !readsView[callee] {
			t.Errorf("attachExisting's %s does not read view.staged, the running jail's own packs", callee)
		}
	}
}

// TestStagingTakesNoLaunchLock: a launch's staging writes a tree of its own, so it takes no lock —
// the lock that opened here before per-launch trees made a second launch of the workspace wait
// for the first launch's whole prelude.
func TestStagingTakesNoLaunchLock(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	o := &Options{Workspace: ws, Stdout: &out, Stderr: &out}
	fillDefaults(o)
	if _, ok := o.stageRunPacks(cname); !ok {
		t.Fatalf("staging failed:\n%s", out.String())
	}
	if launchLockHeld(t, launchLockPath(cname)) {
		t.Error("staging took the workspace launch lock")
	}
}

// TestTheMacosUserArmTakesTheLockAfterItsHostDaemons pins where the native arm opens the launch
// lock: after startLoopholesDisclosed (the daemons run from this launch's own tree, so nothing
// they read is shared) and before refreshJailBriefings (the per-workspace staging its overlay and
// context trees are built from, which the orchestrator copies under the same hold).
func TestTheMacosUserArmTakesTheLockAfterItsHostDaemons(t *testing.T) {
	fd := funcDecl(t, "run.go", "Run")
	var daemons, lockPos, refresh token.Pos
	ast.Inspect(fd, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch skelCallee(call) {
			case "startLoopholesDisclosed":
				daemons = call.Pos()
			case "holdLaunchLock":
				lockPos = call.Pos()
			case "refreshJailBriefings":
				refresh = call.Pos()
			}
		}
		return true
	})
	if daemons == token.NoPos || lockPos == token.NoPos || refresh == token.NoPos {
		t.Fatal("Run's native arm lost one of startLoopholesDisclosed, holdLaunchLock, refreshJailBriefings; re-anchor this pin")
	}
	if !(daemons < lockPos && lockPos < refresh) {
		t.Error("the native arm's launch lock no longer opens between its host daemons and its content staging")
	}
}

// TestAnAttachRefusesASelectionOnlyTheConfiguredPacksSatisfy: the jail keeps the packs it booted
// with, so a profile only a newly configured pack declares cannot be delivered into it. Without a
// terminal the attach refuses before the contract gate and before any write, names the restart,
// and says what differs (the rest of the disposition: packskew_test.go).
func TestAnAttachRefusesASelectionOnlyTheConfiguredPacksSatisfy(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	fresh := dispatchOptions(t, ws, "podman", &out, &out, nil)
	cfg, ok := fresh.loadAndValidateConfig()
	if !ok {
		t.Fatalf("config:\n%s", out.String())
	}
	fresh.stagingCfg = cfg
	staged, ok := fresh.stageRunPacks(cname)
	if !ok {
		t.Fatalf("staging:\n%s", out.String())
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatal(err)
	}

	writeUserPacks(t, home, `["claude", "zai"]`)
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.ProfileName = "zai"
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch {
		case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(strings.Join(argv, " "), "name=^/"+cname+"$"):
			return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
		case len(argv) >= 2 && argv[1] == "inspect":
			return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	envFile := filepath.Join(paths.WorkspaceHomeState(ws), "yolo-user-env.sh")
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want the attach refused\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "Attaching to existing jail") {
		t.Errorf("the attach went ahead:\n%s", stdout.String())
	}
	for _, want := range []string{"Refusing to attach", "launched without zai", "yolo stop",
		"Added to your config since it launched: zai"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Errorf("a refused attach wrote the live channel file (%v)", err)
	}
	if trees := packTreesUnder(t, cname); len(trees) != 1 || trees[0] != staged.root {
		t.Errorf("after a refused attach the pack-tree root holds %v, want only the jail's %s", trees, staged.root)
	}
}

// TestAnAttachInjectsTheJailsOwnLaunchFlags: when the running jail's packs differ, the command an
// attach execs carries THEIR launch flags, and the rewrite is disclosed only when it differs from
// the one Run already disclosed from the configured packs.
func TestAnAttachInjectsTheJailsOwnLaunchFlags(t *testing.T) {
	embedded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var copilot []*packload.Pack
	for _, p := range embedded {
		if p.Name == "copilot" {
			copilot = append(copilot, p)
		}
	}
	if len(copilot) != 1 {
		t.Fatal("the embedded copilot pack is gone; this test needs a pack that declares a launch flag")
	}
	var stderr bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = discardBuf(), &stderr

	// The jail's packs add a flag the configured ones did not.
	got := o.injectLaunchFlagsForAttach(copilot, []string{"copilot", "chat"}, "copilot chat")
	if got != "copilot --yolo chat" {
		t.Errorf("the attach runs %q, want the jail's own flag injected: copilot --yolo chat", got)
	}
	if !strings.Contains(stderr.String(), "yolo CHANGED the command you asked for") {
		t.Errorf("a rewrite that differs from the disclosed one was not disclosed:\n%s", stderr.String())
	}

	// Identical to what Run disclosed: nothing printed again.
	stderr.Reset()
	if got := o.injectLaunchFlagsForAttach(copilot, []string{"copilot", "chat"}, "copilot --yolo chat"); got != "copilot --yolo chat" {
		t.Errorf("got %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("an unchanged rewrite was disclosed a second time:\n%s", stderr.String())
	}

	// The jail's packs add nothing where the configured ones did: said, since Run promised a flag.
	if got := o.injectLaunchFlagsForAttach(nil, []string{"copilot", "chat"}, "copilot --yolo chat"); got != "copilot chat" {
		t.Errorf("got %q, want the command as typed", got)
	}
	if !strings.Contains(stderr.String(), "as you typed it") {
		t.Errorf("an attach that dropped a disclosed flag said nothing:\n%s", stderr.String())
	}
}
