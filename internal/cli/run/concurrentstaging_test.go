package run

// concurrentstaging_test.go reproduces the race the first macOS run of
// TestMacosUserTwoConcurrentLaunchesOfOneWorkspace found (CI run 36240337031, commit
// 6eb92400): two launches of ONE workspace shared AGENTS_DIR/<cname>/packs, and each launch's
// staging cleared that tree and rebuilt it while the other launch was staging into it and
// reading from it. Launch B died with
//
//	unlinkat <state>/agents/<jail>/packs/_official/claude: directory not empty
//
// (its os.RemoveAll of _official racing A's copy into it), and launch A warned
//
//	loophole module dir …/packs/_official/claude/loopholes/claude-oauth-broker is not a
//	directory, so that loophole is NOT active
//
// (A's host daemon start reading its own staged tree after B had removed it). The staging code
// is backend-agnostic, so both reproduce on Linux, through the call the launch makes:
// stageRunPacks, which Run calls once per launch above the backend dispatch.
//
// The ruled behavior these pin (docs/design/pi-git-extension-caching.md OQ-2 and OQ-3, and
// docs/reference/pack-system.md#concurrent-launches-of-one-workspace): a launch's result never
// depends on another launch's. The first fix made the second launch WAIT at staging; ONE PACK
// TREE PER LAUNCH (packtree.go, OQ-PK2 (c)) removed the need to, since no launch writes a tree
// another launch staged, and these tests now pin that directly. The same file pins the attach
// half: an attach reads the running jail's tree and writes nothing into it.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// claudeBrokerModuleDir is the staged module dir A's host-daemon start reads — the path of the
// CI warning — found through the pack set staging returned, the way the launch finds it.
func claudeBrokerModuleDir(t *testing.T, loaded []*packload.Pack) string {
	t.Helper()
	for _, p := range loaded {
		if p.Name == "claude" {
			return filepath.Join(p.Root, "loopholes", "claude-oauth-broker")
		}
	}
	t.Fatalf("the claude pack was not staged; staged: %d packs", len(loaded))
	return ""
}

// TestASecondLaunchNeverTouchesTheFirstLaunchsTree is the deterministic half: launch A has
// staged and is still reading what it staged (its host daemons start, its skills and context
// trees are built, and on macos-user the orchestrator copies the tree for the sandbox — all
// AFTER staging returns). Launch B of the same workspace starts then, with a config that has
// changed since (claude dropped).
//
// Before per-launch trees B restaged the shared tree, and its clear of _official took A's
// claude tree out from under A: the "is not a directory" warning, on demand. The first fix made
// B WAIT for A. Now B stages a tree of its own: it neither waits nor touches A's, and ITS config
// change lands in ITS tree only.
func TestASecondLaunchNeverTouchesTheFirstLaunchsTree(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)

	var outA bytes.Buffer
	a := &Options{Workspace: ws, Stdout: &outA, Stderr: &outA}
	fillDefaults(a)
	stagedA, ok := a.stageRunPacks(cname)
	if !ok {
		t.Fatalf("launch A's staging failed:\n%s", outA.String())
	}
	moduleDir := claudeBrokerModuleDir(t, stagedA.packs)
	if !isDir(moduleDir) {
		t.Fatalf("launch A staged no claude-oauth-broker module dir at %s", moduleDir)
	}
	beforeA := snapshotTree(t, stagedA.root, nil)

	// Launch B, the same workspace, a config that no longer selects claude, run to completion
	// while A still reads its tree.
	writeUserPacks(t, home, `[]`)
	var outB syncBuffer
	b := &Options{Workspace: ws, Stdout: &outB, Stderr: &outB}
	fillDefaults(b)
	done := make(chan stagedPacks, 1)
	go func() {
		stagedB, okB := b.stageRunPacks(cname)
		if !okB {
			stagedB = stagedPacks{}
		}
		done <- stagedB
	}()
	var stagedB stagedPacks
	select {
	case stagedB = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("launch B's staging did not finish while launch A held its tree; staging a tree " +
			"of its own needs no other launch to be done")
	}
	if stagedB.root == "" {
		t.Fatalf("launch B's staging failed:\n%s", outB.String())
	}
	if stagedB.root == stagedA.root {
		t.Fatalf("both launches staged into %s", stagedA.root)
	}
	if !isDir(moduleDir) {
		t.Errorf("launch B's staging removed %s from launch A's tree: the CI warning \"loophole "+
			"module dir … is not a directory, so that loophole is NOT active\" — A's result "+
			"depended on B's", moduleDir)
	}
	if d := diffSnapshots(beforeA, snapshotTree(t, stagedA.root, nil)); len(d) != 0 {
		t.Errorf("launch B's staging changed launch A's tree:\n  %s", strings.Join(d, "\n  "))
	}
	if isDir(filepath.Join(stagedB.root, officialStagingDir, "claude")) {
		t.Error("launch B's tree holds claude, which B's config does not select")
	}
	if strings.Contains(outB.String(), "Waiting for concurrent jail launch") {
		t.Errorf("launch B waited to stage; staging takes no lock any more:\n%s", outB.String())
	}
}

// syncBuffer is a bytes.Buffer safe to write from the goroutine a launch runs on and read from
// the test's own.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// stagingHelperWorkspaceEnv names the workspace a helper process launches in. Set only by
// TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed, for its children.
const stagingHelperWorkspaceEnv = "YOLO_TEST_STAGING_HELPER_WORKSPACE"

// TestStagingHelperProcess is not a test: it is ONE LAUNCH's staging, run as its own process by
// TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed, because two launches are two processes,
// which share nothing but the filesystem. It stages the way Run does,
// then reads what it staged over a short window — the reads a launch makes after staging —
// and fails if any of it went missing or never arrived.
func TestStagingHelperProcess(t *testing.T) {
	ws := os.Getenv(stagingHelperWorkspaceEnv)
	if ws == "" {
		t.Skip("the child half of TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed")
	}
	var out bytes.Buffer
	o := &Options{Workspace: ws, Stdout: &out, Stderr: &out}
	fillDefaults(o)
	staged, ok := o.stageRunPacks(yoloruntime.FromWorkspace(ws))
	if !ok {
		t.Fatalf("staging failed:\n%s", out.String())
	}
	moduleDir := claudeBrokerModuleDir(t, staged.packs)
	for i := 0; i < 10; i++ {
		for _, p := range staged.packs {
			if _, err := os.ReadFile(filepath.Join(p.Root, "pack.json")); err != nil {
				t.Fatalf("read %d: pack %s's staged manifest went missing under this launch: %v",
					i, p.Name, err)
			}
		}
		if !isDir(moduleDir) {
			t.Fatalf("read %d: loophole module dir %s is not a directory under this launch", i, moduleDir)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed is the CI shape itself: two launch
// processes of one workspace, started at the same moment with the same config, stage and then
// read. Before the fix each process's clear of _official raced the other's copy into it, and
// one died with `unlinkat …/_official/claude: directory not empty` or read a tree the other had
// just removed. Both must succeed, every time, and with per-launch trees they do without any
// lock between them.
func TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed(t *testing.T) {
	if os.Getenv(stagingHelperWorkspaceEnv) != "" {
		t.Skip("running as a helper child")
	}
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	const rounds = 8
	for round := 0; round < rounds; round++ {
		type result struct {
			out []byte
			err error
		}
		results := make([]result, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range results {
			cmd := exec.Command(os.Args[0], "-test.run=^TestStagingHelperProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "HOME="+home, stagingHelperWorkspaceEnv+"="+ws)
			wg.Add(1)
			go func(i int, cmd *exec.Cmd) {
				defer wg.Done()
				<-start
				out, err := cmd.CombinedOutput()
				results[i] = result{out: out, err: err}
			}(i, cmd)
		}
		close(start)
		wg.Wait()
		for i, r := range results {
			if r.err != nil {
				t.Fatalf("round %d: launch %c of two concurrent launches of one workspace failed "+
					"(%v):\n%s", round, 'A'+i, r.err, lastLinesOf(string(r.out), 30))
			}
		}
	}
}

// stagingEntry is one path under AGENTS_DIR/<cname> as a live bind would see it: the inode a
// bind of it captured, and — for a file — the mtime that says whether it was rewritten.
type stagingEntry struct {
	ino   uint64
	mtime time.Time
	dir   bool
}

// snapshotStaging records every path under root, the jail's per-workspace staging, except the
// home skeletons, which have their own never-edited rule (homeskeleton.go) and their own test.
func snapshotStaging(t *testing.T, root string) map[string]stagingEntry {
	t.Helper()
	out := map[string]stagingEntry{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "home" && fi.IsDir() {
			return filepath.SkipDir
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			t.Skip("no syscall.Stat_t on this platform")
		}
		out[rel] = stagingEntry{ino: st.Ino, mtime: fi.ModTime(), dir: fi.IsDir()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// attachStagingFixture sets up the state an ATTACH meets: a jail launched from this workspace
// (a fresh launch's staging, briefing refresh and live-tree record, the three things its fresh
// path leaves on the host) with a configured pack carrying a `files` tree, a single `files` file
// and a skill, beside claude (a loophole module dir, a skills destination, a briefing) and pi
// (single-file `files`). It returns the jail's tree, the treepack source (so a test can change
// it) and the workspace.
func attachStagingFixture(t *testing.T) (home, ws, tree, local string) {
	t.Helper()
	home = packHome(t)
	emptyLoopholeDirs(t)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	local = filepath.Join(t.TempDir(), "treepack")
	for rel, body := range map[string]string{
		"tree/a.txt":              "a",
		"tree/sub/b.txt":          "b",
		"one.json":                "{}",
		"skills/extra/SKILL.md":   "---\nname: extra\ndescription: x\n---\nbody\n",
		"skills/extra/ref/doc.md": "doc",
		"briefing/rules.md":       "BOOTED-RULE\n",
	} {
		p := filepath.Join(local, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePack(t, local, `{"name":"treepack","contributes":[
		{"kind":"files","from":"tree","into":".treepack/tree"},
		{"kind":"files","from":"one.json","into":".treepack-one.json"}
	]}`)
	writeUserPacks(t, home, `["claude", "pi", "file://`+local+`"]`)

	ws = t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &out, &out, nil)
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatalf("the fixture's config does not validate:\n%s", out.String())
	}
	o.stagingCfg = cfg
	staged, ok := o.stageRunPacks(cname)
	if !ok {
		t.Fatalf("the fresh launch's staging failed:\n%s", out.String())
	}
	if _, err := o.refreshJailBriefings(cname, cfg, "podman", staged); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatal(err)
	}
	return home, ws, staged.root, local
}

// attachRun is what attachThroughRun observed.
type attachRun struct {
	stdout, stderr string
	// execArgv is the argv the attach handed the runtime, as the fake runtime received it.
	execArgv string
	// treesAtExec is the workspace's pack-tree root, listed by the fake runtime at the moment
	// of the exec, which is the moment the attach's session starts.
	treesAtExec []string
}

// attachThroughRun drives a real attach through Run: the runtime reports this workspace's jail
// running and its environment carrying this build's contract tags. The one runtime command Run
// really executes, the attach's exec, reaches a fake `podman` on PATH that records its argv and
// what the pack-tree root holds at that moment, and exits 0.
func attachThroughRun(t *testing.T, ws string, mutate func(*Options)) attachRun {
	t.Helper()
	cname := yoloruntime.FromWorkspace(ws)
	bin := t.TempDir()
	rec := t.TempDir()
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > '" + filepath.Join(rec, "argv") + "'\n" +
		"ls '" + paths.PackTreeRoot(cname) + "' > '" + filepath.Join(rec, "trees") + "' 2>/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	var out, errOut bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &out, &errOut, nil)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		joined := strings.Join(argv, " ")
		switch {
		case len(argv) >= 2 && argv[1] == "info":
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
			return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
		case len(argv) >= 2 && argv[1] == "inspect":
			return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" +
				entrypointContractTagsLine() + "\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	if mutate != nil {
		mutate(o)
	}
	_ = Run(*o)
	if !strings.Contains(out.String(), "Attaching to existing jail") {
		t.Fatalf("the launch did not take the attach arm, so this test says nothing about it\n"+
			"stdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	r := attachRun{stdout: out.String(), stderr: errOut.String()}
	argv, err := os.ReadFile(filepath.Join(rec, "argv"))
	if err != nil {
		t.Fatalf("the attach never exec'd the runtime:\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}
	r.execArgv = strings.TrimSpace(string(argv))
	trees, _ := os.ReadFile(filepath.Join(rec, "trees"))
	for _, l := range strings.Split(strings.TrimSpace(string(trees)), "\n") {
		if l != "" {
			r.treesAtExec = append(r.treesAtExec, l)
		}
	}
	return r
}

// entrypointContractTagsLine is the contract-tags entry a jail this build launched carries, so the
// fixture's jail can receive whatever the attach delivers and the contract gate stays out of it.
func entrypointContractTagsLine() string {
	return entrypoint.ContractTagsEnv + "=" + launchContractTagsValue()
}

// TestAnAttachWithAnUnchangedConfigLeavesTheLiveJailsStagingUntouched is the other half of the
// same defect, the one that needs no second launch at all. A podman jail has its staging BOUND —
// /ctx/packs itself, each pack's `files` tree and single files, the loophole module dirs a jail
// daemon runs from, every skills destination and every briefing file — and every attach used to
// re-stage it: first by clearing and copying (a bind of a directory that is removed and recreated
// shows the removed one, which is empty), then by sync. Now an attach stages nothing into it at
// all (packtree.go), and refreshes the skills and briefing staging from the jail's own tree.
//
// With nothing changed, an attach must change nothing under AGENTS_DIR/<cname>: every path keeps
// its inode, no file is rewritten, none appears or goes — the attach's own staging of the config,
// which it needs only to compare, included — and it says nothing about packs.
func TestAnAttachWithAnUnchangedConfigLeavesTheLiveJailsStagingUntouched(t *testing.T) {
	_, ws, tree, _ := attachStagingFixture(t)
	cname := yoloruntime.FromWorkspace(ws)
	root := filepath.Join(paths.AgentsDir(), cname)
	before := snapshotStaging(t, root)
	// The fixture must really contain what the assertion is about, or it proves nothing.
	rel := func(p string) string { r, _ := filepath.Rel(root, p); return r }
	for _, want := range []string{
		filepath.Join(tree, "_official", "claude", "loopholes", "claude-oauth-broker"),
		filepath.Join(tree, "_official", "pi", "extensions", "yolo-footer.js"),
		filepath.Join(tree, "treepack", "tree", "sub", "b.txt"),
		filepath.Join(tree, "treepack", "one.json"),
	} {
		if _, ok := before[rel(want)]; !ok {
			t.Fatalf("the fresh launch staged no %s; the fixture no longer exercises it", rel(want))
		}
	}
	var skillFiles, briefingFiles int
	for rel, e := range before {
		if !e.dir && strings.HasPrefix(rel, "skills-") {
			skillFiles++
		}
		if !e.dir && !strings.Contains(rel, string(filepath.Separator)) {
			briefingFiles++
		}
	}
	if skillFiles == 0 || briefingFiles == 0 {
		t.Fatalf("the fresh launch staged %d skill files and %d briefing files; the fixture no "+
			"longer exercises the skills and briefing staging", skillFiles, briefingFiles)
	}

	// Let a coarse filesystem clock tick, so a rewrite cannot hide behind an equal mtime.
	time.Sleep(20 * time.Millisecond)
	stderr := attachThroughRun(t, ws, nil).stderr
	after := snapshotStaging(t, root)

	for rel, b := range before {
		a, ok := after[rel]
		switch {
		case !ok:
			t.Errorf("%s is gone after an attach with an unchanged config", rel)
		case a.ino != b.ino:
			t.Errorf("%s was replaced by an attach with an unchanged config (inode %d -> %d): a "+
				"live bind of it now shows the removed copy", rel, b.ino, a.ino)
		case !b.dir && !a.mtime.Equal(b.mtime):
			t.Errorf("%s was rewritten by an attach with an unchanged config; a live reader "+
				"could have seen it mid-write", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("%s appeared under the staging after an attach with an unchanged config", rel)
		}
	}
	if strings.Contains(stderr, "configured packs differ") || strings.Contains(stderr, "could not find the pack tree") {
		t.Errorf("an attach whose config matches the jail's packs said they differ:\n%s", stderr)
	}
}

// TestAnAttachWritesNothingIntoTheRunningJailsPackTree is OQ-PK2 (c)'s guard, and the pin the
// release-decode allowlist cites (packs/releasedecode_test.go): with the config CHANGED since the
// jail booted — a pack dropped, a pack's content changed — an attach still writes nothing into
// the tree the jail binds, so a jail an older yolo launched never reads a newer yolo's packs. It
// discards the tree it staged to compare, and it tells the user what differs and that a restart
// picks it up.
func TestAnAttachWritesNothingIntoTheRunningJailsPackTree(t *testing.T) {
	home, ws, tree, local := attachStagingFixture(t)
	cname := yoloruntime.FromWorkspace(ws)
	before := snapshotTree(t, tree, nil)
	agents := filepath.Join(paths.AgentsDir(), cname)

	// pi dropped; treepack's `files`, skill and briefing prose all changed.
	for rel, body := range map[string]string{
		"tree/a.txt":            "CHANGED",
		"skills/extra/SKILL.md": "---\nname: extra\ndescription: x\n---\nCONFIGURED-SKILL\n",
		"briefing/rules.md":     "CONFIGURED-RULE\n",
	} {
		if err := os.WriteFile(filepath.Join(local, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, `["claude", "file://`+local+`"]`)
	r := attachThroughRun(t, ws, nil)

	if d := diffSnapshots(before, snapshotTree(t, tree, nil)); len(d) != 0 {
		t.Errorf("an attach wrote into the pack tree the running jail binds:\n  %s", strings.Join(d, "\n  "))
	}
	// Its own staging is discarded BEFORE the session starts, not when the session ends: an attach
	// session can last all day, and nothing ever binds that tree.
	if len(r.treesAtExec) != 1 || r.treesAtExec[0] != filepath.Base(tree) {
		t.Errorf("when the attach exec'd, the pack-tree root held %v, want only the jail's %s",
			r.treesAtExec, filepath.Base(tree))
	}
	if trees := packTreesUnder(t, cname); len(trees) != 1 || trees[0] != tree {
		t.Errorf("after the attach the pack-tree root holds %v, want only the jail's %s", trees, tree)
	}
	for _, want := range []string{"configured packs differ", "removed pi", "changed treepack", "yolo stop"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the attach's notice does not say %q:\n%s", want, r.stderr)
		}
	}
	// The skills and briefing staging the jail binds are refreshed FROM THE JAIL'S TREE: the
	// configured skill and prose are not delivered, the booted ones stay.
	var skillBodies, briefingBodies []string
	_ = filepath.Walk(agents, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.Contains(p, "pack-trees") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.HasSuffix(p, filepath.Join("extra", "SKILL.md")) {
			skillBodies = append(skillBodies, string(b))
		}
		if strings.Contains(string(b), "RULE") {
			briefingBodies = append(briefingBodies, string(b))
		}
		return nil
	})
	if len(skillBodies) == 0 || len(briefingBodies) == 0 {
		t.Fatalf("the fixture's skill (%d copies) or briefing prose (%d files) never reached the staging, "+
			"so this proves nothing about what the attach refreshed", len(skillBodies), len(briefingBodies))
	}
	for _, b := range skillBodies {
		if strings.Contains(b, "CONFIGURED-SKILL") {
			t.Errorf("the attach delivered the configured pack's skill into the running jail's skills staging:\n%s", b)
		}
	}
	for _, b := range briefingBodies {
		if strings.Contains(b, "CONFIGURED-RULE") || !strings.Contains(b, "BOOTED-RULE") {
			t.Errorf("the attach's briefing refresh did not compose from the jail's own packs:\n%s", b)
		}
	}
}

// TestAnAttachExecsWithTheJailsOwnLaunchFlags: the command an attach execs carries the launch
// flags of the packs the jail booted with, not the configured ones — here the config dropped
// copilot, and a command exec'd into a jail that still has it gets copilot's flag, disclosed.
func TestAnAttachExecsWithTheJailsOwnLaunchFlags(t *testing.T) {
	home := packHome(t)
	emptyLoopholeDirs(t)
	writeUserPacks(t, home, `["copilot"]`)
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

	writeUserPacks(t, home, `[]`)
	r := attachThroughRun(t, ws, func(o *Options) { o.Args = []string{"copilot", "chat"} })
	if !strings.Contains(r.execArgv, "copilot --yolo chat") {
		t.Errorf("the attach exec'd %q; the jail still has copilot, so its launch flag applies", r.execArgv)
	}
	if !strings.Contains(r.stderr, "yolo CHANGED the command you asked for") {
		t.Errorf("the attach injected a flag without disclosing it:\n%s", r.stderr)
	}
}

// TestAnAttachToAJailLaunchedBeforePerLaunchTreesLeavesItsSharedTreeAlone: a jail an older yolo
// launched binds the ONE shared staging tree, AGENTS_DIR/<cname>/packs, which every launch used to
// re-stage. An attach by this yolo must leave it byte-identical — it is the tree an older jail's
// boot re-reads on the attach, and handing it this tree's packs is the boot failure the
// release-decode allowlist names — and read it as the jail's packs.
func TestAnAttachToAJailLaunchedBeforePerLaunchTreesLeavesItsSharedTreeAlone(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	// What an older launch left: a shared tree holding an embedded pack that this config no
	// longer selects, and no record and no per-launch tree.
	legacy := paths.LegacyPackStagingDir(cname)
	if err := os.MkdirAll(filepath.Join(legacy, officialStagingDir, "journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, filepath.Join(legacy, officialStagingDir, "journal"), `{"name":"journal"}`)
	before := snapshotTree(t, legacy, nil)

	r := attachThroughRun(t, ws, nil)
	stdout, stderr := r.stdout, r.stderr

	if d := diffSnapshots(before, snapshotTree(t, legacy, nil)); len(d) != 0 {
		t.Errorf("an attach changed the shared tree an older jail binds:\n  %s", strings.Join(d, "\n  "))
	}
	if trees := packTreesUnder(t, cname); len(trees) != 0 {
		t.Errorf("the attach left its own staging behind: %v", trees)
	}
	for _, want := range []string{"added aws-auth, claude,", "removed journal"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the attach did not read the older jail's shared tree as its packs (no %q):\nstderr:\n%s\nstdout:\n%s",
				want, stderr, stdout)
		}
	}
}

func lastLinesOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
