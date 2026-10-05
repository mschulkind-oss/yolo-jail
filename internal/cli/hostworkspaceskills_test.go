package cli

// hostworkspaceskills_test.go pins the workspace skills layer's host half
// (docs/design/workspace-skills.md WS-D19 to WS-D23) through `yolo host -- codex`, the real
// front door: the one link it puts into the repository, the .gitignore line it adds once, every
// path it must leave alone, and the cases that write nothing. Delete the hostWorkspaceSkills call
// in hostLaunch and the first test fails: no link appears.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
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
	return newWSSkillsFixtureFor(t, `{"packs": ["codex"], "host_floor": {"codex": false}}`, "codex")
}

// newWSSkillsFixtureFor is newWSSkillsFixture with cfg as the user config and a stub on PATH for
// each of agents.
func newWSSkillsFixtureFor(t *testing.T, cfg string, agents ...string) *wsSkillsFixture {
	t.Helper()
	f := &wsSkillsFixture{}
	f.home = hostGateHome(t, cfg, nil)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	bin := filepath.Join(t.TempDir(), "bin")
	for _, agent := range agents {
		writeFile(t, filepath.Join(bin, agent), "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(bin, agent), 0o755); err != nil {
			t.Fatal(err)
		}
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
	return f.launchAgent(t, "codex", args...)
}

// launchAgent is launch for another agent.
func (f *wsSkillsFixture) launchAgent(t *testing.T, agent string, args ...string) (int, string) {
	t.Helper()
	f.execed = false
	var out, errw bytes.Buffer
	rc := hostMain(append(append([]string{}, args...), "--", agent), &out, &errw, false, nil)
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
		if !strings.Contains(errs, ".codex is a symbolic link or not a directory") ||
			!strings.Contains(errs, "make .codex a real directory and the next launch places it, "+
				"or create .codex/skills yourself") {
			t.Errorf("the refusal of the linked parent, and its next step, were not said:\n%s", errs)
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
		if !strings.Contains(errs, ".gitignore is not a regular file yolo writes") ||
			!strings.Contains(errs, "add `/.codex/skills` to your ignore rules yourself to quiet it") {
			t.Errorf("the skipped ignore line, and its next step, were not said:\n%s", errs)
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

// serviceSkillsLaunch runs `yolo host -p codex -- claude` (claude beside the launch-owned wire
// bridge, the resident path) in a git work tree holding only .agents/skills, with start standing
// in for startLaunchService when given, and returns the exit code, stderr and the workspace.
func serviceSkillsLaunch(t *testing.T, start func(*launchservice.Plan, map[string]string) (*launchservice.Running, error)) (int, string, string) {
	t.Helper()
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	hostGateHome(t, codexConfig(upstream.URL), nil)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ws, ".agents", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: d\n---\n")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "claude"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if start != nil {
		orig := startLaunchService
		startLaunchService = start
		t.Cleanup(func() { startLaunchService = orig })
	}
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { t.Error("the resident path exec'd"); return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })
	var out, errw bytes.Buffer
	rc := hostMain([]string{"-p", "codex", "--", "claude"}, &out, &errw, false, nil)
	return rc, errw.String(), ws
}

// (j') A launch refused at the last step before the hand-over — a launch-owned service that does
// not start — writes nothing into the workspace either: the link is the launch's last write.
func TestAHostLaunchRefusedAtServiceStartWritesNoWorkspaceSkillsLink(t *testing.T) {
	rc, errs, ws := serviceSkillsLaunch(t, func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		return nil, errors.New("the bridge could not bind")
	})
	if rc == 0 || !strings.Contains(errs, "refusing to launch: the bridge could not bind") {
		t.Fatalf("setup: the launch was not refused at service start (rc=%d):\n%s", rc, errs)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude")); err == nil {
		t.Errorf("a launch refused at service start wrote .claude:\n%s", errs)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".gitignore")); err == nil {
		t.Errorf("a launch refused at service start wrote a .gitignore:\n%s", errs)
	}
	if strings.Contains(errs, "workspace skills") {
		t.Errorf("a launch that wrote nothing said something about workspace skills:\n%s", errs)
	}
}

// The resident path links too: an agent beside a launch-owned service gets the link once every
// service has started, and before the starting line.
func TestAHostLaunchBesideALaunchOwnedServiceLinksTheSkills(t *testing.T) {
	rc, errs, ws := serviceSkillsLaunch(t, nil)
	if rc != 0 {
		t.Fatalf("rc=%d:\n%s", rc, errs)
	}
	if got := readlinkOr(t, filepath.Join(ws, ".claude", "skills")); got != "../.agents/skills" {
		t.Fatalf(".claude/skills -> %q, want ../.agents/skills:\n%s", got, errs)
	}
	started := strings.Index(errs, "started the \"wire-bridge\" service")
	linked := strings.Index(errs, "workspace skills: placed the link .claude/skills")
	if started < 0 || linked < started {
		t.Errorf("the link was not placed after the services started:\n%s", errs)
	}
}

// A source the jail's reader cannot check (its scratch copy cannot be made) gets no link, and the
// line names where the copy goes and the fix.
func TestHostLaunchThatCannotCheckASourceSaysWhereTheCheckRuns(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	gone := filepath.Join(t.TempDir(), "no-such-tmp")
	t.Setenv("TMPDIR", gone)
	_, errs := f.launch(t)
	if _, err := os.Lstat(f.path(".codex/skills")); err == nil {
		t.Errorf("a source that could not be checked was linked:\n%s", errs)
	}
	if !strings.Contains(errs, "could not check .claude/skills") ||
		!strings.Contains(errs, "yolo checks a copy under "+gone+"; make it writable, or point TMPDIR "+
			"at a folder that is, and the next launch checks again") {
		t.Errorf("the failed check, and its next step, were not said:\n%s", errs)
	}
}

// Outside a git work tree the link is placed and no .gitignore is written: an ignore file in a
// directory git does not track is clutter nobody asked for (WS-D22).
func TestHostLaunchOutsideAGitWorkTreeWritesNoIgnoreFile(t *testing.T) {
	f := newWSSkillsFixture(t)
	if insideGitWorkTree(f.ws) {
		t.Skipf("the temp dir %s is inside a git work tree, so this cell cannot be set up here", f.ws)
	}
	f.skill(t, ".claude/skills/review")
	rc, errs := f.launch(t)
	if rc != 0 || readlinkOr(t, f.path(".codex/skills")) != "../.claude/skills" {
		t.Fatalf("rc=%d, no link:\n%s", rc, errs)
	}
	if _, err := os.Lstat(f.path(".gitignore")); err == nil {
		t.Errorf("a .gitignore was written outside a git work tree:\n%s", errs)
	}
	if strings.Contains(errs, ".gitignore") {
		t.Errorf("a launch outside a git work tree talked about .gitignore:\n%s", errs)
	}
}

// A second source present is named as not handed, since one link points at one directory; and a
// skill of the same name in the agent's home-scope dir is named, the precedence being the agent's.
func TestHostLaunchNamesTheSourcesItDropsAndTheHomeSkillsOfTheSameName(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".claude/skills/review")
	f.skill(t, ".github/skills/deploy")
	writeFile(t, filepath.Join(f.home, ".codex", "skills", "review", "SKILL.md"), "x")
	_, errs := f.launch(t)
	if got := readlinkOr(t, f.path(".codex/skills")); got != "../.claude/skills" {
		t.Fatalf(".codex/skills -> %q, want the first source in the order, ../.claude/skills:\n%s", got, errs)
	}
	for _, want := range []string{
		".github/skills also holds skills, and a link points at one directory, so codex is not handed .github/skills",
		"~/.codex/skills also has a skill named review; which of the two codex uses is codex's own rule",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch did not say %q:\n%s", want, errs)
		}
	}
}

// A link yolo put in for ANOTHER agent is never this agent's source: it is yolo's, not the
// repository's, and pointing at it would chain one link onto another.
func TestHostLaunchNeverTakesAnotherAgentsLinkAsItsSource(t *testing.T) {
	f := newWSSkillsFixtureFor(t, `{"packs": ["codex", "copilot"], "host_floor": {"codex": false, "copilot": false}}`,
		"codex", "copilot")
	f.gitRepo(t)
	f.skill(t, ".opencode/skills/review")
	if rc, errs := f.launchAgent(t, "copilot"); rc != 0 || readlinkOr(t, f.path(".github/skills")) != "../.opencode/skills" {
		t.Fatalf("setup: copilot's link (rc=%d) -> %q:\n%s", rc, readlinkOr(t, f.path(".github/skills")), errs)
	}
	_, errs := f.launch(t)
	if got := readlinkOr(t, f.path(".codex/skills")); got != "../.opencode/skills" {
		t.Errorf(".codex/skills -> %q, want the repository's own ../.opencode/skills, never copilot's link:\n%s",
			got, errs)
	}
}

// When an earlier source in the order appears, yolo's own link is pointed at it, and the launch
// says it refreshed the link.
func TestHostLaunchRefreshesItsLinkWhenTheChosenSourceChanges(t *testing.T) {
	f := newWSSkillsFixture(t)
	f.gitRepo(t)
	f.skill(t, ".opencode/skills/review")
	if rc, errs := f.launch(t); rc != 0 || readlinkOr(t, f.path(".codex/skills")) != "../.opencode/skills" {
		t.Fatalf("setup: rc=%d\n%s", rc, errs)
	}
	f.skill(t, ".claude/skills/review")
	_, errs := f.launch(t)
	if got := readlinkOr(t, f.path(".codex/skills")); got != "../.claude/skills" {
		t.Errorf(".codex/skills -> %q after .claude/skills appeared, want ../.claude/skills:\n%s", got, errs)
	}
	if !strings.Contains(errs, "refreshed the link .codex/skills -> ../.claude/skills") {
		t.Errorf("the launch did not say it refreshed the link:\n%s", errs)
	}
}
