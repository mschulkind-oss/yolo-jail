package cli

// hostworkspaceskills_test.go pins the workspace skills layer's host half
// (docs/design/workspace-skills.md WS-D19 to WS-D23) through `yolo host -- codex`, the real
// front door: the one link it puts into the repository, the .gitignore line it adds once, every
// path it must leave alone, and the cases that write nothing. Delete the hostWorkspaceSkills call
// in hostLaunch and the first test fails: no link appears.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// wsSkillsFixture is one host home selecting the codex pack (its floor entry off, so a stub on
// PATH is what runs) and one workspace, kept across launches so the record carries over.
type wsSkillsFixture struct {
	home, ws string
	execed   bool
}

func newWSSkillsFixture(t *testing.T) *wsSkillsFixture {
	t.Helper()
	f := &wsSkillsFixture{}
	f.home = hostGateHome(t, `{"packs": ["codex"], "host_floor": {"codex": false}}`, nil)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	bin := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { f.execed = true; return nil }
	t.Cleanup(func() { hostSyscallExec = orig })
	return f
}

// launch runs `yolo host -- codex` in the workspace and returns its exit code and stderr.
func (f *wsSkillsFixture) launch(t *testing.T, args ...string) (int, string) {
	t.Helper()
	f.execed = false
	var out, errw bytes.Buffer
	rc := hostMain(append(append([]string{}, args...), "--", "codex"), &out, &errw, false, nil)
	return rc, errw.String()
}

func (f *wsSkillsFixture) path(rel string) string { return filepath.Join(f.ws, rel) }

// skill writes a skill directory under rel.
func (f *wsSkillsFixture) skill(t *testing.T, rel string) {
	t.Helper()
	writeFile(t, f.path(filepath.Join(rel, "SKILL.md")), "---\nname: x\ndescription: d\n---\nbody\n")
}

// gitRepo makes the workspace a git work tree the way a check reads one: a .git directory.
func (f *wsSkillsFixture) gitRepo(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(f.path(".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readlinkOr(t *testing.T, p string) string {
	t.Helper()
	target, err := os.Readlink(p)
	if err != nil {
		return ""
	}
	return target
}

func readOr(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// (a) and (b): the link and the ignore line, once; a second launch keeps both and adds nothing.
func TestHostLaunchLinksTheRepositorysSkillsIntoTheAgentsPathAndIgnoresItOnce(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	writeFile(t, f.path(".gitignore"), "node_modules/")

	rc, errs := f.launch(t)
	if rc != 0 || !f.execed {
		t.Fatalf("launch rc=%d execed=%v:\n%s", rc, f.execed, errs)
	}
	if got := readlinkOr(t, f.path(".codex/skills")); got != "../.claude/skills" {
		t.Fatalf(".codex/skills -> %q, want ../.claude/skills:\n%s", got, errs)
	}
	if got := readOr(f.path(".gitignore")); got != "node_modules/\n/.codex/skills\n" {
		t.Errorf(".gitignore = %q, want the line appended once on a line of its own", got)
	}
	for _, want := range []string{"yolo host: workspace skills: placed the link .codex/skills -> ../.claude/skills",
		"review", "added `/.codex/skills` to .gitignore"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch did not say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "codex reads") || strings.Contains(errs, "codex loads") {
		t.Errorf("a line claims codex reads the link, which is unmeasured:\n%s", errs)
	}

	rc, errs = f.launch(t)
	if rc != 0 {
		t.Fatalf("second launch rc=%d:\n%s", rc, errs)
	}
	if got := readOr(f.path(".gitignore")); strings.Count(got, "/.codex/skills") != 1 {
		t.Errorf("the second launch added the line again: %q", got)
	}
	if !strings.Contains(errs, "kept the link .codex/skills -> ../.claude/skills") {
		t.Errorf("the second launch did not say it kept the link:\n%s", errs)
	}
}

// (c) A line yolo added and the user removed is not added back, and the launch says git will list
// the link.
func TestHostLaunchDoesNotAddBackAnIgnoreLineTheUserRemoved(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	if rc, errs := f.launch(t); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	writeFile(t, f.path(".gitignore"), "# mine\n")
	_, errs := f.launch(t)
	if got := readOr(f.path(".gitignore")); got != "# mine\n" {
		t.Errorf("the removed line was added back: %q", got)
	}
	if !strings.Contains(errs, "has been removed, so yolo leaves it out and `git status` will list the link") {
		t.Errorf("the launch did not say it left the line out:\n%s", errs)
	}
}

// (d) The repository's own directory for the agent, or another path the agent reads, is left alone
// and nothing is written.
func TestHostLaunchLeavesARepositorysOwnAgentPathAlone(t *testing.T) {
	for _, own := range []string{".codex/skills", ".agents/skills"} {
		t.Run(own, func(t *testing.T) {
			f := newWSSkillsFixture(t)
			f.gitRepo(t)
			f.skill(t, ".claude/skills/review")
			f.skill(t, filepath.Join(own, "mine"))
			_, errs := f.launch(t)
			if fi, err := os.Lstat(f.path(".codex/skills")); own == ".agents/skills" && err == nil {
				t.Errorf("a link was written although the repo has %s: %v", own, fi.Mode())
			}
			if fi, err := os.Lstat(f.path(".codex/skills")); own == ".codex/skills" &&
				(err != nil || fi.Mode()&os.ModeSymlink != 0) {
				t.Errorf("the repository's own .codex/skills was replaced (%v)", err)
			}
			if _, err := os.Stat(f.path(".gitignore")); err == nil {
				t.Errorf("a .gitignore was written although nothing was linked")
			}
			if strings.Contains(errs, "workspace skills") {
				t.Errorf("a launch that wrote nothing said something about it:\n%s", errs)
			}
		})
	}
}

// (e) A source holding an escaping link is refused by the jail's reader: no link, the entry named
// and never its target, and a link yolo wrote before is removed.
func TestHostLaunchRefusesASourceWithAnEscapingLinkAndRemovesItsOwnLink(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	if rc, errs := f.launch(t); rc != 0 || readlinkOr(t, f.path(".codex/skills")) == "" {
		t.Fatalf("setup: rc=%d, no link\n%s", rc, errs)
	}
	secret := filepath.Join(t.TempDir(), "secret-target-dir")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, f.path(".claude/skills/evil")); err != nil {
		t.Fatal(err)
	}
	_, errs := f.launch(t)
	if _, err := os.Lstat(f.path(".codex/skills")); err == nil {
		t.Errorf("the link survived a source holding an escaping link:\n%s", errs)
	}
	if !strings.Contains(errs, "refused .claude/skills/evil") ||
		!strings.Contains(errs, "removed the link yolo had put at .codex/skills") {
		t.Errorf("the refusal or the removal was not said:\n%s", errs)
	}
	if strings.Contains(errs, secret) {
		t.Errorf("a line names the escaping link's target:\n%s", errs)
	}
	if _, err := os.Stat(f.path(".codex")); err == nil {
		t.Errorf("the .codex directory yolo made for the link was left behind")
	}
}

// (f) A parent that is a link, or a .gitignore that is one, is never written through.
func TestHostLaunchWritesThroughNoLink(t *testing.T) {
	t.Run("a linked .codex", func(t *testing.T) {
		f := newWSSkillsFixture(t)
		f.gitRepo(t)
		f.skill(t, ".claude/skills/review")
		elsewhere := f.path("elsewhere")
		if err := os.MkdirAll(elsewhere, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("elsewhere", f.path(".codex")); err != nil {
			t.Fatal(err)
		}
		_, errs := f.launch(t)
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("yolo wrote through the linked .codex into its target: %v", entries)
		}
		if !strings.Contains(errs, ".codex is a symbolic link or not a directory") {
			t.Errorf("the refusal of the linked parent was not said:\n%s", errs)
		}
	})
	t.Run("a linked .gitignore", func(t *testing.T) {
		f := newWSSkillsFixture(t)
		f.gitRepo(t)
		f.skill(t, ".claude/skills/review")
		target := filepath.Join(t.TempDir(), "not-the-repos")
		writeFile(t, target, "keep\n")
		if err := os.Symlink(target, f.path(".gitignore")); err != nil {
			t.Fatal(err)
		}
		_, errs := f.launch(t)
		if got := readOr(target); got != "keep\n" {
			t.Errorf("the .gitignore link's target was written: %q", got)
		}
		if !strings.Contains(errs, ".gitignore is not a regular file yolo writes") {
			t.Errorf("the skipped ignore line was not said:\n%s", errs)
		}
	})
}

// (g) and (h): the home itself is never a workspace here, and in a jail the mirror alone serves.
func TestHostWorkspaceSkillsWritesNothingInTheHomeOrInAJail(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.skill(t, ".claude/skills/review")
	packs := composeHostLaunch("codex", "", nil, func(string) {}).packs

	t.Setenv("YOLO_VERSION", "test")
	var errw bytes.Buffer
	hostWorkspaceSkills(packs, "codex", &errw)
	if _, err := os.Lstat(f.path(".codex/skills")); err == nil || errw.Len() > 0 {
		t.Errorf("in a jail the host link was written (%v):\n%s", err, errw.String())
	}
	t.Setenv("YOLO_VERSION", "")

	homeSkill := filepath.Join(f.home, ".claude", "skills", "review", "SKILL.md")
	writeFile(t, homeSkill, "x")
	t.Chdir(f.home)
	hostWorkspaceSkills(packs, "codex", &errw)
	if _, err := os.Lstat(filepath.Join(f.home, ".codex", "skills")); err == nil || errw.Len() > 0 {
		t.Errorf("a launch in the home linked its skills (%v):\n%s", err, errw.String())
	}
}

// (i) A source that vanished takes the recorded link with it; an unrecorded link of the same name
// is the repository's and is left alone.
func TestHostLaunchRemovesItsLinkWhenTheSourceVanishesAndLeavesOthersAlone(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	if rc, errs := f.launch(t); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if err := os.RemoveAll(f.path(".claude")); err != nil {
		t.Fatal(err)
	}
	_, errs := f.launch(t)
	if _, err := os.Lstat(f.path(".codex/skills")); err == nil {
		t.Errorf("the dangling link yolo wrote was left:\n%s", errs)
	}
	if !strings.Contains(errs, "no skills directory it could point at is left") {
		t.Errorf("the removal was not said:\n%s", errs)
	}

	// The repository commits its own link of that name: nothing records it, so it is not yolo's.
	g := newWSSkillsFixture(t)
	g.gitRepo(t)
	if err := os.MkdirAll(g.path(".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.claude/skills", g.path(".codex/skills")); err != nil {
		t.Fatal(err)
	}
	_, errs = g.launch(t)
	if readlinkOr(t, g.path(".codex/skills")) != "../.claude/skills" {
		t.Errorf("the repository's own link was touched:\n%s", errs)
	}
}

// (j) A refused launch writes nothing into the workspace.
func TestARefusedHostLaunchWritesNoWorkspaceSkillsLink(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	rc, errs := f.launch(t, "-p", "no-such-profile")
	if rc == 0 || f.execed {
		t.Fatalf("setup: the launch was not refused (rc=%d):\n%s", rc, errs)
	}
	if _, err := os.Lstat(f.path(".codex")); err == nil {
		t.Errorf("a refused launch wrote .codex:\n%s", errs)
	}
	if _, err := os.Stat(f.path(".gitignore")); err == nil {
		t.Errorf("a refused launch wrote a .gitignore")
	}
}

// (k) `yolo host env` launches nothing, and writes nothing into the workspace.
func TestHostEnvWritesNoWorkspaceSkillsLink(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "codex"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("host env rc=%d:\n%s", rc, errw.String())
	}
	if _, err := os.Lstat(f.path(".codex")); err == nil {
		t.Errorf("`yolo host env` wrote .codex")
	}
}

// The record is host-side, under yolo's own state dir, and never in the workspace's .yolo/.
func TestHostWorkspaceSkillsRecordLivesInTheStateDir(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	if rc, errs := f.launch(t); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	records, _ := filepath.Glob(filepath.Join(paths.HostWorkspaceSkillsDir(), "*.json"))
	if len(records) != 1 {
		t.Fatalf("want one record under %s, got %v", paths.HostWorkspaceSkillsDir(), records)
	}
	if _, err := os.Stat(f.path(".yolo")); err == nil {
		t.Errorf("the launch made a .yolo in the workspace for the record")
	}
}
