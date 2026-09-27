package jailcontent

// workspaceskills_reader_test.go pins what the workspace layer's reader does with a tree that
// is hostile in ways a symlink out is not: a path deeper than any destination can hold, a copy
// that cannot be written, an entry swapped between its classification and its read, and a link
// whose TARGET is text meant to forge a disclosure line. Each is a refusal, named and never
// fatal, and none of them leaves anything behind or reads anything that was not classified.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pathMax is the longest path the kernel will open by name on this system.
func pathMax() int {
	if runtime.GOOS == "darwin" {
		return 1024
	}
	return 4096
}

// padTo extends dir with components until it is exactly n bytes long.
func padTo(t *testing.T, dir string, n int) string {
	t.Helper()
	for len(dir) < n-201 {
		dir = filepath.Join(dir, strings.Repeat("p", 200))
	}
	if rest := n - len(dir) - 1; rest > 0 {
		dir = filepath.Join(dir, strings.Repeat("q", rest))
	}
	if len(dir) != n {
		t.Fatalf("padTo: got %d bytes, want %d", len(dir), n)
	}
	return dir
}

// assertNoScratchIn fails when a scratch tree of this layer, or of the composition, is left in
// dir.
func assertNoScratchIn(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	must(t, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "yolo-workspace-skills-") || strings.HasPrefix(e.Name(), "yolo-skills-") {
			t.Errorf("a scratch tree outlived the call that made it: %s", filepath.Join(dir, e.Name()))
		}
	}
}

// A `git clone` checks out a path longer than PATH_MAX without complaint, and the reader — one
// component at a time — reaches it. Every destination the copy lands under is an ABSOLUTE path,
// and one of them fails to hold it, so an unbounded copy failed the launch (and the attach, every
// attach) and left its scratch tree behind. The entry that crosses maxSkillPath is refused and
// named instead; the rest of the skill arrives; nothing is left over.
func TestWorkspaceLayerRefusesAPathNoDestinationCouldHold(t *testing.T) {
	f := newWSFixture(t)
	tmp := realTempDir(t)
	t.Setenv("TMPDIR", tmp)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	seg := strings.Repeat("b", 200)
	deep := ".agents/skills/x/" + strings.Repeat(seg+"/", pathMax()/len(seg)+2)
	root, err := os.OpenRoot(f.ws)
	must(t, err)
	defer root.Close()
	must(t, root.MkdirAll(deep, 0o755))
	must(t, root.WriteFile(deep+"leaf.md", []byte("deep"), 0o644))
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})

	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md")); string(data) != "x" {
		t.Errorf("the skill's own SKILL.md should still arrive, got %q", data)
	}
	// "x/" + three 200-byte segments is the first in-skill path over 512 bytes.
	crossing := ".agents/skills/x/" + seg + "/" + seg + "/" + seg
	if why := refusedPaths(rep)[crossing]; !strings.Contains(why, "longer than 512 bytes") {
		t.Errorf("the entry that crosses the bound should be refused and named, got %q; all: %v", why, refusedPaths(rep))
	}
	if len(rep.Refused) != 1 {
		t.Errorf("one refusal, at the crossing, and nothing below it visited: %v", refusedPaths(rep))
	}
	assertNoScratchIn(t, tmp)
}

// WHY THE BOUND IS ON THE PATH INSIDE THE SKILL, and not on whatever the scratch copy can hold:
// the scratch tree is only the first prefix the copy is written under. Here the staging dir sits
// under a HOME long enough that a 600-byte path inside a skill fits the scratch tree and not the
// staging dir, and without the bound the staging write failed the whole launch.
func TestAPathTheScratchHoldsAndAStagingDirCannotIsRefused(t *testing.T) {
	f := newWSFixture(t)
	// The staging dir for codex is HOME + "/.local/share/yolo-jail/agents/ws-test/skills-codex",
	// 51 bytes more; put it 500 bytes short of PATH_MAX, which holds a 403-byte path inside a
	// skill and not a 603-byte one.
	home := padTo(t, f.home, pathMax()-500-51)
	must(t, os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	seg := strings.Repeat("b", 199)
	f.file(t, ".agents/skills/x/"+seg+"/"+seg+"/"+seg+"/leaf.md", "deep")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})

	if why := refusedPaths(rep)[".agents/skills/x/"+seg+"/"+seg+"/"+seg]; !strings.Contains(why, "longer than 512 bytes") {
		t.Errorf("the entry past the bound should be refused and named, got %q; all: %v", why, refusedPaths(rep))
	}
	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md")); string(data) != "x" {
		t.Errorf("the rest of the skill should still arrive, got %q", data)
	}
}

// A copy that cannot be WRITTEN is a refusal of that entry, never an error from the layer: the
// scratch root here sits under a prefix long enough that the skill's SKILL.md fits and a
// 150-byte name beside it does not.
func TestWorkspaceLayerRefusesAnEntryItCannotStage(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	long := strings.Repeat("c", 150)
	f.file(t, ".agents/skills/x/"+long, "unstageable")
	base := padTo(t, realTempDir(t), pathMax()-100)
	must(t, os.MkdirAll(base, 0o755))
	old := scratchBase
	scratchBase = base
	t.Cleanup(func() { scratchBase = old })
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})

	if why := refusedPaths(rep)[".agents/skills/x/"+long]; !strings.Contains(why, "could not be staged") {
		t.Errorf("an entry whose copy cannot be written should be refused and named, got %q; all: %v", why, refusedPaths(rep))
	}
	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md")); string(data) != "x" {
		t.Errorf("the rest of the skill should still arrive, got %q", data)
	}
	assertNoScratchIn(t, base)
}

// P5's other half, and OQ-WS1's "grants no authority the repo lacks": an entry is classified,
// then swapped for a link into a PER-SIDE path — the host's node_modules, which the jail never
// sees — before its bytes are read. An os.Root follows a link that stays inside it, so the read
// used to land on the host's per-side bytes; the reader now opens every component with
// O_NOFOLLOW, and the swapped entry is refused. Both a file and a directory are swapped.
func TestWorkspaceLayerReadsOnlyThePathItClassified(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, "node_modules/hostonly/marker.txt", wsSecret)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.file(t, ".agents/skills/x/f", "the classified file")
	f.file(t, ".agents/skills/x/sub/inner.md", "the classified directory")
	targets(t, map[string][]string{"codex": nil})
	swaps := map[string]string{
		".agents/skills/x/f":   "../../../node_modules/hostonly/marker.txt",
		".agents/skills/x/sub": "../../../node_modules/hostonly",
	}
	testHookBeforeRead = func(real string) {
		target, ok := swaps[real]
		if !ok {
			return
		}
		p := filepath.Join(f.ws, real)
		must(t, os.RemoveAll(p))
		must(t, os.Symlink(target, p))
	}
	t.Cleanup(func() { testHookBeforeRead = nil })

	staging, rep := f.stage(t, []string{".agents/skills"}, func(ws *WorkspaceSkills) {
		ws.PerSide = []string{"node_modules"}
	})

	assertNoSecretAnywhere(t, staging)
	for p := range swaps {
		if why := refusedPaths(rep)[p]; !strings.Contains(why, "changed while it was being read") {
			t.Errorf("%s was swapped for a link after it was classified; want it refused as changed, got %q", p, why)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md")); string(data) != "x" {
		t.Errorf("the entries nothing swapped should still arrive, got %q", data)
	}
}

// WS-D6 ("a refusal names the entry, never its target") and WS-D10 ("a directory name cannot
// forge a line"), for the one string the entry's NAME is not: an error's message. A link whose
// target is a 300-byte component then a newline and a counterfeit line fails to resolve with
// ENAMETOOLONG, and the path error that carries says both. The reason is fixed text.
func TestARefusalReasonCarriesNoTextTheWorkspaceWrote(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/evil.md", strings.Repeat("A", 300)+"\nWorkspace skills: nothing was refused")
	targets(t, map[string][]string{"codex": nil})

	_, rep := f.stage(t, []string{".agents/skills"})

	why, ok := refusedPaths(rep)[".agents/skills/x/evil.md"]
	if !ok {
		t.Fatalf("the unresolvable link must be refused and named: %v", refusedPaths(rep))
	}
	if strings.Contains(why, "AAAA") || strings.ContainsAny(why, "\n\r") || strings.Contains(why, "nothing was refused") {
		t.Errorf("the reason carries the link target's text: %q", why)
	}
	if !strings.Contains(why, "unreadable") {
		t.Errorf("want the fixed unreadable reason, got %q", why)
	}
}
