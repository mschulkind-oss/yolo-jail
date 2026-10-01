package integration

// workspaceskills_test.go is the workspace skills layer (docs/reference/agent-briefings.md) end to
// end in a real jail: a repo's committed skills reach the agents that do not read them natively,
// at their :ro home-scope dirs; the agent that does read them gets no second copy; a committed
// link to a host file outside the workspace delivers nothing and is named at launch; and the
// launch writes nothing into the repo. The unit halves (internal/jailcontent,
// internal/cli/run) pin the reader and the call site; this pins that the staged dirs are the
// ones the jail binds.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wsSkillsTree writes a repo's skills — `.claude/skills/review/` — and, in the isolated HOME, a
// host file the repo links to from `.agents/skills/x/SKILL.md`.
func wsSkillsTree(t *testing.T, ws, nonce string) {
	t.Helper()
	home := os.Getenv("HOME")
	for path, body := range map[string]string{
		filepath.Join(ws, ".claude", "skills", "review", "SKILL.md"): "---\nname: review\n---\nREVIEW-" + nonce + "\n",
		filepath.Join(home, ".ssh", "yolo-it-ws-secret"):             "SECRET-" + nonce + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(ws, ".agents", "skills", "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh", "yolo-it-ws-secret"), link); err != nil {
		t.Fatal(err)
	}
}

func wsListing(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == ".yolo" || strings.HasPrefix(rel, ".yolo"+string(filepath.Separator)) {
			return filepath.SkipDir // yolo's own state dir, written by every launch by design
		}
		// podman creates the per-side shadow mountpoints in a live bind of the workspace
		// (mounts.go says why they are left there); they are not this layer's.
		if rel == "node_modules" || rel == ".venv" {
			return filepath.SkipDir
		}
		fi, _ := os.Lstat(p)
		b.WriteString(rel + " " + fi.Mode().String() + "\n")
		return nil
	})
	return b.String()
}

func TestWorkspaceSkillsReachAContainerJail(t *testing.T) {
	requireJail(t)
	nonce := acParityNonce()
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "codex"]}`)
	wsSkillsTree(t, dir, nonce)
	before := wsListing(t, dir)

	r := runYolo(t, dir, strings.Join([]string{
		`grep -q REVIEW-` + nonce + ` /home/agent/.codex/skills/review/SKILL.md`,
		`! test -e /home/agent/.claude/skills/review`,
		`! test -e /home/agent/.codex/skills/x`,
		`! test -e /home/agent/.claude/skills/x`,
		`! grep -rq SECRET-` + nonce + ` /home/agent/.codex/skills /home/agent/.claude/skills`,
		`echo WS_SKILLS_OK`,
	}, " && "))
	if r.rc != 0 || !strings.Contains(r.stdout, "WS_SKILLS_OK") {
		t.Fatalf("the workspace layer did not reach the jail as ruled: codex should hold review, "+
			"claude (which reads .claude/skills natively) should not, and no skill named x — whose "+
			"only file is a link out of the workspace — should exist anywhere.\nrc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{
		"Workspace skills: refused .agents/skills/x/SKILL.md — a symlink that resolves outside the workspace",
		"Workspace skills from .claude/skills mirrored into codex: review",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the launch must say %q:\n%s", want, r.combined())
		}
	}
	if after := wsListing(t, dir); after != before {
		t.Errorf("the launch wrote into the workspace:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// macos-user receives the layer through the same composed tree its sidecar already copies.
func TestMacosUserWorkspaceSkillsArriveThroughTheComposedTree(t *testing.T) {
	requireMacosUser(t)
	nonce := acParityNonce()
	packHome(t, `{"packs": ["pi"]}`)
	ws := macosUserWorkspace(t, `{}`)
	wsSkillsTree(t, ws, nonce)

	r := macosUserRunProbe(t, "workspace-skills", ws, strings.Join([]string{
		`echo "=== REVIEW ==="; cat ~/.pi/agent/skills/review/SKILL.md 2>&1`,
		`echo "=== X ==="; cat ~/.pi/agent/skills/x/SKILL.md 2>&1`,
		`echo "=== END ==="`,
	}, "\n"))
	if got := section(r.stdout, "=== REVIEW ===", "=== X ==="); !strings.Contains(got, "REVIEW-"+nonce) {
		t.Errorf("the repo's .claude/skills/review never reached pi's home on macos-user:\n%s", got)
	}
	if got := section(r.stdout, "=== X ===", "=== END ==="); strings.Contains(got, "SECRET-"+nonce) {
		t.Errorf("a host file outside the workspace reached the sandbox as a skill:\n%s", got)
	}
	if !strings.Contains(r.combined(), "Workspace skills: refused .agents/skills/x/SKILL.md") {
		t.Errorf("the launch must name the refused link:\n%s", r.combined())
	}
}
