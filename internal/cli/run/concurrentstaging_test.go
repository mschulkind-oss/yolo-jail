package run

// concurrentstaging_test.go reproduces the race the first macOS run of
// TestMacosUserTwoConcurrentLaunchesOfOneWorkspace found (CI run 36240337031, commit
// 6eb92400): two launches of ONE workspace share AGENTS_DIR/<cname>/packs, and each launch's
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
// depends on another launch's; it WAITS for what it needs rather than failing or reading a
// half-built tree.

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

// TestASecondLaunchWaitsForTheFirstLaunchsStagedTree is the deterministic half: launch A has
// staged and is still reading what it staged (its host daemons start, its skills and context
// trees are built, and on macos-user the orchestrator copies the tree for the sandbox — all
// AFTER staging returns). Launch B of the same workspace starts then, with a config that has
// changed since (claude dropped).
//
// Before the fix B ran straight through, and its clear of _official took A's claude tree out
// from under A: the "is not a directory" warning, on demand. B must instead WAIT until A's
// launch no longer reads the staged tree — then do its own staging, which is the moment its
// config change is allowed to land.
func TestASecondLaunchWaitsForTheFirstLaunchsStagedTree(t *testing.T) {
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
	t.Cleanup(a.releaseLaunchLock)
	moduleDir := claudeBrokerModuleDir(t, stagedA.packs)
	if !isDir(moduleDir) {
		t.Fatalf("launch A staged no claude-oauth-broker module dir at %s", moduleDir)
	}

	// Launch B, the same workspace, a config that no longer selects claude.
	writeUserPacks(t, home, `[]`)
	var outB syncBuffer
	b := &Options{Workspace: ws, Stdout: &outB, Stderr: &outB}
	fillDefaults(b)
	done := make(chan bool, 1)
	go func() {
		_, okB := b.stageRunPacks(cname)
		b.releaseLaunchLock()
		done <- okB
	}()

	// Give B every chance to run to completion; before the fix it does, in milliseconds.
	bFinishedEarly := false
	select {
	case <-done:
		bFinishedEarly = true
	case <-time.After(2 * time.Second):
	}
	if !isDir(moduleDir) {
		t.Errorf("launch B re-staged the workspace's tree while launch A was still reading it: "+
			"%s is gone, which is the CI warning \"loophole module dir … is not a directory, so "+
			"that loophole is NOT active\" — A's result depended on B's", moduleDir)
	}
	if bFinishedEarly {
		t.Errorf("launch B finished staging while launch A still held its staged tree; it must " +
			"wait for A rather than restage under it")
	}

	// A's launch no longer reads the staged tree (its container started, or its sandbox
	// bootstrap copied the tree): B proceeds, and ITS config — no claude — takes effect.
	a.releaseLaunchLock()
	if bFinishedEarly {
		return
	}
	select {
	case okB := <-done:
		if !okB {
			t.Errorf("launch B's staging failed once A released:\n%s", outB.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("launch B never finished staging after launch A released the workspace")
	}
	if !strings.Contains(outB.String(), "Waiting for concurrent jail launch in workspace") {
		t.Errorf("launch B waited without saying so; a silent wait reads as a hang:\n%s", outB.String())
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
// TestTwoProcessesStagingOneWorkspaceAtOnceBothSucceed, because two launches are two processes
// and the lock that serializes them is a kernel lock between them. It stages the way Run does,
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
	defer o.releaseLaunchLock()
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
// just removed. Both must succeed, every time.
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

// TestAnAttachWithAnUnchangedConfigLeavesTheLiveJailsStagingUntouched is the other half of the
// same defect, the one that needs no second launch at all: every invocation re-stages the
// workspace's tree, an ATTACH to a running podman jail included, and the jail has that tree
// BOUND — /ctx/packs itself, each pack's `files` tree and single files, the loophole module dirs
// a jail daemon runs from, every skills destination and every briefing file. The re-stage used
// to clear _official, clear each configured pack's dir and each skills dir, and copy again: a
// bind of a directory that is removed and recreated shows the removed one, which is empty, and a
// reader walking one mid-copy saw half of it. So an attach with a config identical to the one the
// jail booted with emptied the jail's own binds.
//
// With nothing changed, an attach must change nothing: every path keeps its inode, no file is
// rewritten, and none appears or goes.
func TestAnAttachWithAnUnchangedConfigLeavesTheLiveJailsStagingUntouched(t *testing.T) {
	home := packHome(t)
	emptyLoopholeDirs(t)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	// A configured pack with a `files` tree, a single `files` file and a skill, beside claude
	// (a loophole module dir, a skills destination, a briefing) and pi (single-file `files`).
	local := filepath.Join(t.TempDir(), "treepack")
	for rel, body := range map[string]string{
		"tree/a.txt":              "a",
		"tree/sub/b.txt":          "b",
		"one.json":                "{}",
		"skills/extra/SKILL.md":   "---\nname: extra\ndescription: x\n---\nbody\n",
		"skills/extra/ref/doc.md": "doc",
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

	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	o := goldenOptions(ws, home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	cfg := newConfig()

	// The fresh launch.
	staged, ok := o.stageRunPacks(cname)
	if !ok {
		t.Fatal("stageRunPacks failed")
	}
	if _, err := o.refreshJailBriefings(cname, cfg, "podman", staged); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	o.releaseLaunchLock()
	root := filepath.Join(paths.AgentsDir(), cname)
	before := snapshotStaging(t, root)
	// The fixture must really contain what the assertion is about, or it proves nothing.
	for _, rel := range []string{
		"packs/_official/claude/loopholes/claude-oauth-broker",
		"packs/_official/pi/extensions/yolo-footer.js",
		"packs/treepack/tree/sub/b.txt",
		"packs/treepack/one.json",
	} {
		if _, ok := before[filepath.FromSlash(rel)]; !ok {
			t.Fatalf("the fresh launch staged no %s; the fixture no longer exercises it", rel)
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

	// The attach: the same config, re-staged and refreshed, as runContainer does before its
	// attach decision.
	staged, ok = o.stageRunPacks(cname)
	if !ok {
		t.Fatal("stageRunPacks failed on the attach")
	}
	if _, err := o.refreshJailBriefings(cname, cfg, "podman", staged); err != nil {
		t.Fatalf("refreshJailBriefings on the attach: %v", err)
	}
	o.releaseLaunchLock()
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
}

func lastLinesOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
