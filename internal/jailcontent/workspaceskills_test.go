package jailcontent

// workspaceskills_test.go pins the workspace layer's reader and composition
// (docs/reference/agent-briefings.md). The CALL-SITE half — that a launch actually hands the
// workspace to PrepareSkillsWith, with the shipped packs' declarations — is
// internal/cli/run/workspaceskills_test.go; these fail if the layer itself stops holding.
//
// P5 FIRST, as §9 step 3 orders: the tests at the top put a secret's bytes outside the
// workspace, point every shape of link at them, and assert those bytes are nowhere in any
// staging dir.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

const wsSecret = "SECRET-BYTES-THAT-MUST-NOT-CROSS"

// wsFixture is a temp HOME, a workspace and a directory OUTSIDE it holding a secret. Paths are
// minted through EvalSymlinks, so a test comparing them against the reader's own resolution
// holds on macOS, where t.TempDir() sits behind /var → /private/var.
type wsFixture struct {
	home, ws, outside, secret string
}

func newWSFixture(t *testing.T) wsFixture {
	t.Helper()
	home := realTempDir(t)
	t.Setenv("HOME", home)
	f := wsFixture{home: home, ws: realTempDir(t), outside: realTempDir(t)}
	f.secret = filepath.Join(f.outside, "id_ed25519")
	must(t, os.WriteFile(f.secret, []byte(wsSecret), 0o600))
	SetPackSkillDirs(nil)
	t.Cleanup(func() { SetPackSkillDirs(nil); SetPackSkillTargets(nil) })
	return f
}

func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return d
}

// file writes rel under the workspace.
func (f wsFixture) file(t *testing.T, rel, body string) {
	t.Helper()
	p := filepath.Join(f.ws, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), 0o644))
}

// link makes rel (under the workspace) a symlink to target.
func (f wsFixture) link(t *testing.T, rel, target string) {
	t.Helper()
	p := filepath.Join(f.ws, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.Symlink(target, p))
}

// targets installs skills destinations: name → the ProjectDirs its agent reads natively.
func targets(t *testing.T, dests map[string][]string) {
	t.Helper()
	var out []SkillTarget
	for _, agent := range sortedKeys(dests) {
		out = append(out, SkillTarget{Staging: SkillStagingName(agent), Dest: "." + agent + "/skills",
			Agent: agent, ProjectDirs: dests[agent]})
	}
	SetPackSkillTargets(out)
}

func sortedKeys(m map[string][]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return ks
}

// stage runs the real composition with the workspace layer over dirs.
func (f wsFixture) stage(t *testing.T, dirs []string, mod ...func(*WorkspaceSkills)) (string, *WorkspaceSkillsReport) {
	t.Helper()
	ws := &WorkspaceSkills{Root: f.ws, Dirs: dirs}
	for _, m := range mod {
		m(ws)
	}
	staging, rep, err := PrepareSkillsWith("ws-test", ws)
	if err != nil {
		t.Fatalf("PrepareSkillsWith: %v", err)
	}
	if rep == nil {
		t.Fatal("the report is never nil")
	}
	return staging, rep
}

func skillsOf(t *testing.T, staging, agent string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(staging, SkillStagingName(agent)))
	must(t, err)
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// assertNoSecretAnywhere walks every staged file and fails on the secret's bytes.
func assertNoSecretAnywhere(t *testing.T, staging string) {
	t.Helper()
	_ = filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr == nil && bytes.Contains(data, []byte(wsSecret)) {
			t.Errorf("the host secret outside the workspace was copied into the staging dir at %s", p)
		}
		return nil
	})
}

func refusedPaths(r *WorkspaceSkillsReport) map[string]string {
	out := map[string]string{}
	for _, f := range r.Refused {
		out[f.Path] = f.Reason
	}
	return out
}

// P5, THE MOTIVATING CASE: a committed `SKILL.md → ~/.ssh/<key>`. The skill's other files still
// arrive; the link does not, and it is named.
func TestWorkspaceLayerNeverReadsASymlinkOutOfTheWorkspace(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/notes.md", "fine")
	f.link(t, ".agents/skills/x/SKILL.md", f.secret)
	targets(t, map[string][]string{"codex": {".codex/skills"}})

	staging, rep := f.stage(t, []string{".agents/skills"})

	assertNoSecretAnywhere(t, staging)
	if _, err := os.Lstat(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md")); err == nil {
		t.Error("the escaping SKILL.md link was staged")
	}
	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "notes.md")); string(data) != "fine" {
		t.Errorf("the skill's in-workspace file should still arrive, got %q", data)
	}
	if why := refusedPaths(rep)[".agents/skills/x/SKILL.md"]; !strings.Contains(why, "outside the workspace") {
		t.Errorf("the refused link must be NAMED, with why; refusals: %+v", rep.Refused)
	}
}

// A skill NOTHING of which could be staged is no skill: it is not delivered as an empty
// directory, the mirror line does not claim it, and a later source's skill of that name wins.
func TestWorkspaceLayerDropsASkillWhoseEveryEntryWasRefused(t *testing.T) {
	f := newWSFixture(t)
	f.link(t, ".claude/skills/x/SKILL.md", f.secret)
	f.file(t, ".agents/skills/x/SKILL.md", "the real x")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".claude/skills", ".agents/skills"})
	data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "SKILL.md"))
	if string(data) != "the real x" {
		t.Errorf("the later source's x should win over an x that staged nothing, got %q", data)
	}
	for _, m := range rep.Mirrored {
		if m.Source == ".claude/skills" {
			t.Errorf("the mirror line claims a skill nothing of which was staged: %+v", m)
		}
	}
	if len(rep.Collisions) != 0 {
		t.Errorf("an x that staged nothing collides with nothing: %+v", rep.Collisions)
	}
	if _, ok := refusedPaths(rep)[".claude/skills/x/SKILL.md"]; !ok {
		t.Errorf("the refusal must still be named: %+v", rep.Refused)
	}
}

// Every other shape an escape can take: a chain, a relative climb, a directory link, a source
// dir that is itself a link out, a link whose parent component is the escaping one.
func TestWorkspaceLayerRefusesEveryShapeOfEscape(t *testing.T) {
	f := newWSFixture(t)
	must(t, os.MkdirAll(filepath.Join(f.outside, "skills", "leak"), 0o755))
	must(t, os.WriteFile(filepath.Join(f.outside, "skills", "leak", "SKILL.md"), []byte(wsSecret), 0o644))

	f.file(t, ".agents/skills/chain/SKILL.md", "ok")
	f.link(t, ".agents/skills/chain/hop1", "hop2")
	f.link(t, ".agents/skills/chain/hop2", f.secret)
	f.file(t, ".agents/skills/climb/SKILL.md", "ok")
	rel, err := filepath.Rel(filepath.Join(f.ws, ".agents/skills/climb"), f.secret)
	must(t, err)
	f.link(t, ".agents/skills/climb/up.md", rel)
	f.file(t, ".agents/skills/dirlink/SKILL.md", "ok")
	f.link(t, ".agents/skills/dirlink/sub", filepath.Join(f.outside, "skills"))
	f.link(t, ".agents/skills/whole", filepath.Join(f.outside, "skills", "leak"))
	f.link(t, ".claude", filepath.Join(f.outside))
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills", ".claude/skills"})

	assertNoSecretAnywhere(t, staging)
	got := refusedPaths(rep)
	for _, p := range []string{
		".agents/skills/chain/hop1", ".agents/skills/chain/hop2", ".agents/skills/climb/up.md",
		".agents/skills/dirlink/sub", ".agents/skills/whole", ".claude/skills",
	} {
		if !strings.Contains(got[p], "outside the workspace") {
			t.Errorf("%s: want a named escape refusal, got %q (all: %+v)", p, got[p], rep.Refused)
		}
	}
	skills := skillsOf(t, staging, "codex")
	for _, name := range []string{"chain", "climb", "dirlink"} {
		if !skills[name] {
			t.Errorf("skill %s lost more than its escaping entry: %v", name, skills)
		}
	}
	if skills["whole"] || skills["leak"] {
		t.Errorf("a skill dir that is itself a link out of the workspace was staged: %v", skills)
	}
}

// A link that STAYS inside the workspace is followed, relative or absolute, and an absolute one
// spelled with the jail's mount path (an alias) is read as the root — never as the host's own
// path of that name.
func TestWorkspaceLayerFollowsLinksThatStayInside(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, "docs/shared.md", "shared")
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/rel.md", "../../../docs/shared.md")
	f.link(t, ".agents/skills/x/abs.md", filepath.Join(f.ws, "docs/shared.md"))
	f.link(t, ".agents/skills/x/alias.md", "/jail-mount/docs/shared.md")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"}, func(ws *WorkspaceSkills) {
		ws.Aliases = []string{"/jail-mount"}
	})

	for _, name := range []string{"rel.md", "abs.md", "alias.md"} {
		data, err := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", name))
		if err != nil || string(data) != "shared" {
			t.Errorf("%s: an in-workspace link should be dereferenced, got %q, %v", name, data, err)
		}
		if fi, err := os.Lstat(filepath.Join(staging, SkillStagingName("codex"), "x", name)); err == nil &&
			fi.Mode()&fs.ModeSymlink != 0 {
			t.Errorf("%s was staged as a link; the staged tree must be plain files", name)
		}
	}
	if len(rep.Refused) != 0 {
		t.Errorf("nothing here leaves the workspace, yet: %+v", rep.Refused)
	}
}

// THE DARWIN PATH CLASS, reproduced on purpose: the workspace root is handed over through a
// symlink (macOS's /var → /private/var), and an absolute link inside it may be spelled either
// way. Both spellings name the root; neither is an escape.
func TestWorkspaceLayerAcceptsEitherSpellingOfARootBehindALink(t *testing.T) {
	f := newWSFixture(t)
	spelled := filepath.Join(realTempDir(t), "ws-link")
	must(t, os.Symlink(f.ws, spelled))
	f.file(t, "docs/shared.md", "shared")
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/real.md", filepath.Join(f.ws, "docs/shared.md"))
	f.link(t, ".agents/skills/x/spelled.md", filepath.Join(spelled, "docs/shared.md"))
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"}, func(ws *WorkspaceSkills) { ws.Root = spelled })
	for _, name := range []string{"real.md", "spelled.md"} {
		if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", name)); string(data) != "shared" {
			t.Errorf("%s: want the in-workspace target's bytes, got %q", name, data)
		}
	}
	if len(rep.Refused) != 0 {
		t.Errorf("both spellings are the workspace: %+v", rep.Refused)
	}
}

// A FIFO where a SKILL.md should be must not hang the launcher, and is named. Measured: an
// os.Root open without O_NONBLOCK blocks forever on one.
func TestWorkspaceLayerDoesNotBlockOnAFIFO(t *testing.T) {
	f := newWSFixture(t)
	must(t, os.MkdirAll(filepath.Join(f.ws, ".agents/skills/x"), 0o755))
	must(t, syscall.Mkfifo(filepath.Join(f.ws, ".agents/skills/x/SKILL.md"), 0o644))
	f.file(t, ".agents/skills/x/other.md", "ok")
	targets(t, map[string][]string{"codex": nil})

	done := make(chan *WorkspaceSkillsReport, 1)
	go func() {
		_, rep := f.stage(t, []string{".agents/skills"})
		done <- rep
	}()
	select {
	case rep := <-done:
		if why := refusedPaths(rep)[".agents/skills/x/SKILL.md"]; !strings.Contains(why, "not a regular file") {
			t.Errorf("the FIFO should be refused and named, got %q", why)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("staging blocked on a FIFO in the workspace")
	}
}

// THE RACE HALF of the FIFO case: an entry classified as a regular file (or a directory) and
// swapped for a FIFO before it is opened. The walk's Lstat catches a FIFO that is already there;
// only the open's own flags catch one that arrives after, so the openers are driven directly.
func TestConfinedOpensDoNotBlockOnAFIFOSwappedIn(t *testing.T) {
	f := newWSFixture(t)
	must(t, syscall.Mkfifo(filepath.Join(f.ws, "swapped"), 0o644))
	tree, err := openConfinedTree(&WorkspaceSkills{Root: f.ws})
	must(t, err)
	defer tree.close()

	done := make(chan [2]string, 1)
	go func() {
		reason := tree.copyReal("swapped", filepath.Join(t.TempDir(), "out"))
		_, derr := tree.listDir("swapped")
		msg := ""
		if derr != nil {
			msg = derr.Error()
		}
		done <- [2]string{reason, msg}
	}()
	select {
	case got := <-done:
		if !strings.Contains(got[0], "not a regular file") {
			t.Errorf("copyReal on a FIFO should refuse it, got %q", got[0])
		}
		if got[1] == "" {
			t.Error("listDir on a FIFO should fail rather than list it")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a confined open blocked on a FIFO")
	}
}

// A link cycle terminates and is named; two links to one directory copy it once.
func TestWorkspaceLayerBoundsLinkCyclesAndFanOut(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/loop", "..")
	f.file(t, "shared/big.md", "big")
	f.link(t, ".agents/skills/x/a", "../../../shared")
	f.link(t, ".agents/skills/x/b", "../../../shared")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})
	got := refusedPaths(rep)
	if !strings.Contains(got[".agents/skills/x/loop"], "cycle") {
		t.Errorf("the cycle should be refused as one: %+v", rep.Refused)
	}
	if !strings.Contains(got[".agents/skills/x/b"], "second symlink") {
		t.Errorf("the second link to one directory should be refused: %+v", rep.Refused)
	}
	if data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), "x", "a", "big.md")); string(data) != "big" {
		t.Errorf("the first link's directory should arrive, got %q", data)
	}
}

// A cycle that closes THROUGH a directory reached by a link is still a cycle, named at the link
// that closes it, and the skill is not copied into itself.
func TestWorkspaceLayerSeesACycleThroughALinkedDirectory(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.file(t, "shared/doc.md", "d")
	f.link(t, ".agents/skills/x/a", "../../../shared")
	f.link(t, "shared/back", "../.agents/skills/x")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})
	if why := refusedPaths(rep)[".agents/skills/x/a/back"]; !strings.Contains(why, "cycle") {
		t.Errorf("the link back to the skill should be refused as a cycle: %+v", rep.Refused)
	}
	if _, err := os.Stat(filepath.Join(staging, SkillStagingName("codex"), "x", "a", "back")); err == nil {
		t.Error("the skill was copied into itself")
	}
}

// .git, .yolo and per-side paths are never read through a link, and are named.
func TestWorkspaceLayerNeverReadsGitYoloOrPerSidePaths(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".git/config", "[core]")
	f.file(t, ".yolo/home/token", "t")
	f.file(t, "node_modules/pkg/index.js", "host-built")
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/git", "../../../.git")
	f.link(t, ".agents/skills/x/state", "../../../.yolo/home")
	f.link(t, ".agents/skills/x/lib", "../../../node_modules/pkg")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"}, func(ws *WorkspaceSkills) {
		ws.PerSide = []string{"node_modules", ".venv"}
	})
	got := refusedPaths(rep)
	for p, want := range map[string]string{
		".agents/skills/x/git":   ".git",
		".agents/skills/x/state": ".yolo",
		".agents/skills/x/lib":   "per-side",
	} {
		if !strings.Contains(got[p], want) {
			t.Errorf("%s: want a refusal naming %s, got %q", p, want, got[p])
		}
	}
	for _, name := range []string{"git", "state", "lib"} {
		if _, err := os.Lstat(filepath.Join(staging, SkillStagingName("codex"), "x", name)); err == nil {
			t.Errorf("%s was staged", name)
		}
	}
}

// THE SKIP RULE at the directory grain: an agent that reads a source dir natively gets no copy of
// it, and still gets every other source's skills.
func TestWorkspaceLayerSkipsWhatAnAgentReadsNatively(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".claude/skills/review/SKILL.md", "review")
	f.file(t, ".agents/skills/lint/SKILL.md", "lint")
	targets(t, map[string][]string{
		"claude": {".claude/skills"},
		"pi":     {".pi/skills", ".agents/skills"},
		"codex":  {".codex/skills"},
	})

	staging, rep := f.stage(t, []string{".claude/skills", ".agents/skills"})

	for agent, want := range map[string]map[string]bool{
		"claude": {"review": false, "lint": true},
		"pi":     {"review": true, "lint": false},
		"codex":  {"review": true, "lint": true},
	} {
		got := skillsOf(t, staging, agent)
		for name, present := range want {
			if got[name] != present {
				t.Errorf("%s: %s present=%v, want %v", agent, name, got[name], present)
			}
		}
	}
	if len(rep.Mirrored) != 2 {
		t.Fatalf("one mirror line per source dir that delivered: %+v", rep.Mirrored)
	}
}

// THE SKIP RULE at the skill grain, and a committed alias of a whole dir: neither is a
// collision, and pi — which reads .agents/skills — never gets its own skill twice.
func TestWorkspaceLayerSkipsASkillAnAgentReadsThroughAnotherSource(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".claude/skills/x", "../../.agents/skills/x")
	f.link(t, ".github/skills", "../.agents/skills")
	targets(t, map[string][]string{"pi": {".agents/skills"}, "codex": nil})

	staging, rep := f.stage(t, []string{".claude/skills", ".github/skills", ".agents/skills"})

	if skillsOf(t, staging, "pi")["x"] {
		t.Error("pi reads .agents/skills/x natively; a copy through .claude/skills would load as a second x")
	}
	if !skillsOf(t, staging, "codex")["x"] {
		t.Error("codex reads none of these natively and should receive x")
	}
	if len(rep.Collisions) != 0 {
		t.Errorf("one real directory reached by two spellings is not a collision: %+v", rep.Collisions)
	}
}

// Two source dirs carrying DIFFERENT skills of one name: the first in the source order wins,
// everywhere, and the collision is said once.
func TestWorkspaceLayerResolvesASameNamedSkillByOrderAndSaysSo(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".claude/skills/x/SKILL.md", "from-claude")
	f.file(t, ".agents/skills/x/SKILL.md", "from-agents")
	targets(t, map[string][]string{"codex": nil, "omp": nil})

	staging, rep := f.stage(t, []string{".claude/skills", ".agents/skills"})
	for _, agent := range []string{"codex", "omp"} {
		data, _ := os.ReadFile(filepath.Join(staging, SkillStagingName(agent), "x", "SKILL.md"))
		if string(data) != "from-claude" {
			t.Errorf("%s: the first source dir's x should win, got %q", agent, data)
		}
	}
	if len(rep.Collisions) != 1 || rep.Collisions[0].Winner != ".claude/skills" ||
		len(rep.Collisions[0].Losers) != 1 || rep.Collisions[0].Losers[0] != ".agents/skills" {
		t.Errorf("want one collision naming both dirs: %+v", rep.Collisions)
	}
}

// OQ-WS2: the workspace adds but never shadows. A built-in's name keeps the built-in's bytes in
// every destination, and the loss is ONE entry however many destinations it happened in.
func TestWorkspaceLayerNeverShadowsABuiltinOrAPackSkill(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/configuring-the-jail/SKILL.md", "an agent's own version")
	f.file(t, ".agents/skills/house-rule/SKILL.md", "workspace's")
	f.file(t, ".agents/skills/"+LSPPluginDir+"/.claude-plugin/plugin.json", "{}")
	packDir := filepath.Join(f.home, "pack", "skills")
	must(t, os.MkdirAll(filepath.Join(packDir, "house-rule"), 0o755))
	must(t, os.WriteFile(filepath.Join(packDir, "house-rule", "SKILL.md"), []byte("pack's"), 0o644))
	SetPackSkillDirs([]PackSkillSource{{Dir: packDir, Pack: "house"}})
	targets(t, map[string][]string{"codex": nil, "omp": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})

	want, _ := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	for _, agent := range []string{"codex", "omp"} {
		dir := filepath.Join(staging, SkillStagingName(agent))
		if data, _ := os.ReadFile(filepath.Join(dir, "configuring-the-jail", "SKILL.md")); !bytes.Equal(data, want) {
			t.Errorf("%s: the workspace replaced yolo's built-in", agent)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, "house-rule", "SKILL.md")); string(data) != "pack's" {
			t.Errorf("%s: the workspace replaced a pack skill: %q", agent, data)
		}
		if _, err := os.Stat(filepath.Join(dir, LSPPluginDir)); err == nil {
			t.Errorf("%s: the workspace planted a directory under yolo's LSP plugin name", agent)
		}
	}
	byName := map[string]WorkspaceSkillShadow{}
	for _, s := range rep.Shadowed {
		if _, dup := byName[s.Name]; dup {
			t.Errorf("%s shadowed twice in the report — the ruling is one line per name", s.Name)
		}
		byName[s.Name] = s
	}
	if s := byName["configuring-the-jail"]; len(s.In) != 2 || !strings.Contains(strings.Join(s.By, ""), "built-in") {
		t.Errorf("configuring-the-jail: want one shadow naming the built-in and both destinations, got %+v", s)
	}
	if s := byName["house-rule"]; !strings.Contains(strings.Join(s.By, ""), "pack house") {
		t.Errorf("house-rule: the shadow must name the pack that took the name, got %+v", s)
	}
	if _, ok := byName[LSPPluginDir]; !ok {
		t.Errorf("the LSP plugin's name is taken whether or not a plugin is written: %+v", rep.Shadowed)
	}
	if len(rep.Mirrored) != 0 {
		t.Errorf("nothing was delivered, so nothing is mirrored: %+v", rep.Mirrored)
	}
}

// The degenerate cases: absent and empty dirs are SILENT; a source that is a file is named; a
// skill with no SKILL.md is copied as it is; a top-level file is not a skill; a source linked
// to the workspace root is refused.
func TestWorkspaceLayerDegenerateCases(t *testing.T) {
	f := newWSFixture(t)
	must(t, os.MkdirAll(filepath.Join(f.ws, ".opencode/skills"), 0o755)) // empty
	f.file(t, ".codex/skills", "a file where a directory belongs")
	f.file(t, ".agents/skills/README.md", "not a skill")
	f.file(t, ".agents/skills/bare/notes.txt", "no SKILL.md here")
	f.link(t, ".pi/skills", "..")
	f.link(t, ".github/skills", "nowhere")
	targets(t, map[string][]string{"omp": nil})

	staging, rep := f.stage(t, []string{".claude/skills", ".opencode/skills", ".codex/skills",
		".agents/skills", ".pi/skills", ".github/skills"})

	got := refusedPaths(rep)
	if len(got) != 3 || !strings.Contains(got[".codex/skills"], "not a directory") ||
		!strings.Contains(got[".pi/skills"], "workspace root") || !strings.Contains(got[".github/skills"], "dangling") {
		t.Errorf("want exactly the file, the root link and the dangling link named: %+v", rep.Refused)
	}
	skills := skillsOf(t, staging, "omp")
	if !skills["bare"] {
		t.Error("a skill dir with no SKILL.md is copied as-is — what counts as a skill is the agent's business")
	}
	if _, err := os.Stat(filepath.Join(staging, SkillStagingName("omp"), "README.md")); err == nil {
		t.Error("a top-level file is not a skill directory")
	}
}

// Nothing in the workspace, or no workspace at all: the report is empty and the staging is what
// it always was.
func TestWorkspaceLayerIsSilentWhenThereIsNothing(t *testing.T) {
	f := newWSFixture(t)
	targets(t, map[string][]string{"codex": nil})
	_, rep := f.stage(t, []string{".claude/skills", ".agents/skills"})
	if !rep.Empty() {
		t.Errorf("an absent source set must say nothing: %+v", rep)
	}
	staging, rep2, err := PrepareSkillsWith("ws-test", nil)
	must(t, err)
	if !rep2.Empty() || !skillsOf(t, staging, "codex")["configuring-the-jail"] {
		t.Error("a nil workspace stages exactly the other layers")
	}
}

// The execute bit rides through, as it does for a pack's own tree: a skill that tells an agent to
// run scripts/check.sh must be able to ship it runnable.
func TestWorkspaceLayerCarriesTheExecuteBit(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.file(t, ".agents/skills/x/check.sh", "#!/bin/sh\n")
	must(t, os.Chmod(filepath.Join(f.ws, ".agents/skills/x/check.sh"), 0o775))
	targets(t, map[string][]string{"codex": nil})
	staging, _ := f.stage(t, []string{".agents/skills"})
	fi, err := os.Stat(filepath.Join(staging, SkillStagingName("codex"), "x", "check.sh"))
	must(t, err)
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("check.sh staged %v, want 0755", fi.Mode().Perm())
	}
}

// RE-STAGED ON EVERY CALL, into the same inode: an edited workspace skill reaches the next
// invocation, a deleted one leaves it, and the bound directory is never replaced.
func TestWorkspaceLayerIsReStagedOnEveryInvocation(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "v1")
	f.file(t, ".agents/skills/gone/SKILL.md", "g")
	targets(t, map[string][]string{"codex": nil})
	staging, _ := f.stage(t, []string{".agents/skills"})
	dir := filepath.Join(staging, SkillStagingName("codex"))
	ino := inodeOf(t, dir)

	f.file(t, ".agents/skills/x/SKILL.md", "v2")
	must(t, os.RemoveAll(filepath.Join(f.ws, ".agents/skills/gone")))
	f.stage(t, []string{".agents/skills"})

	if data, _ := os.ReadFile(filepath.Join(dir, "x", "SKILL.md")); string(data) != "v2" {
		t.Errorf("the edit did not reach the next staging: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone")); err == nil {
		t.Error("a skill deleted from the workspace survived the next staging")
	}
	if inodeOf(t, dir) != ino {
		t.Error("the staging dir was replaced — a running jail's bind would detach")
	}
}

// The workspace is never written: the whole composition leaves its tree byte-for-byte alone.
func TestWorkspaceLayerWritesNothingIntoTheWorkspace(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, ".agents/skills/x/SKILL.md", "x")
	f.link(t, ".agents/skills/x/bad", f.secret)
	targets(t, map[string][]string{"codex": nil})
	before := treeListing(t, f.ws)
	f.stage(t, []string{".agents/skills", ".claude/skills"})
	if after := treeListing(t, f.ws); after != before {
		t.Errorf("the workspace changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func treeListing(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, _ := os.Lstat(p)
		b.WriteString(p + " " + fi.Mode().String() + "\n")
		return nil
	})
	return b.String()
}
