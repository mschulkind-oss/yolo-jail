package cli

// packsourcerename_test.go drives `yolo pack source rename` through packMain, the CLI's own
// dispatcher, over a synthetic user config (docs/design/pack-naming.md §3.6).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func sourceRenameFixture(t *testing.T) (workspace, userPath string) {
	t.Helper()
	src := t.TempDir()
	for _, pack := range []string{"x", "y"} {
		writeFile(t, filepath.Join(src, pack, "pack.json"),
			`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
		writeFile(t, filepath.Join(src, pack, "skills", "review-"+pack, "SKILL.md"), "---\nname: review-"+pack+"\n---\n")
	}
	body := "packs = [\"acme/x\", { pack = \"acme/y\", bare = true }]\n\n" +
		"[pack_sources]\n# my tools\nacme = { path = '" + filepath.ToSlash(src) + "' }\n"
	user := testconfig.User(t, body)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	return testconfig.Workspace(t, "").Root, user.Path
}

func runSourceRename(t *testing.T, workspace, answer string, args ...string) (int, string, string) {
	t.Helper()
	old := packSourceStdin
	packSourceStdin = strings.NewReader(answer)
	t.Cleanup(func() { packSourceStdin = old })
	return runPackCapture(t, workspace, append([]string{"source", "rename"}, args...))
}

func TestPackSourceRenameRewritesEveryHandleAndListsTheNamesThatChange(t *testing.T) {
	workspace, userPath := sourceRenameFixture(t)
	rc, out, errw := runSourceRename(t, workspace, "y\n", "acme", "tools")
	if rc != 0 {
		t.Fatalf("rename rc=%d\nstdout=%s\nstderr=%s", rc, out, errw)
	}
	got, _ := os.ReadFile(userPath)
	for _, want := range []string{`"tools/x"`, `pack = "tools/y"`, "tools = ", "# my tools"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the config lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "acme") {
		t.Errorf("the config still names acme:\n%s", got)
	}
	// acme/x is namespaced, so its names change; acme/y is bare, so none of its do.
	for _, want := range []string{"skill acme-review-x → tools-review-x", "acme-x → tools-x", "Rename? [y/N]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "review-y") {
		t.Errorf("the plan lists a bare pack's skill as changing:\n%s", out)
	}
}

func TestPackSourceRenameWritesNothingUnlessConfirmed(t *testing.T) {
	workspace, userPath := sourceRenameFixture(t)
	before, _ := os.ReadFile(userPath)
	for _, run := range [][]string{{"n\n", "acme", "tools"}, {"", "acme", "tools", "--dry-run"}} {
		rc, out, errw := runSourceRename(t, workspace, run[0], run[1:]...)
		if after, _ := os.ReadFile(userPath); string(after) != string(before) {
			t.Fatalf("%v wrote the config (rc=%d):\n%s\nstdout=%s\nstderr=%s", run, rc, after, out, errw)
		}
	}
}

func TestPackSourceRenameRefusesATakenOrIllegalKey(t *testing.T) {
	workspace, _ := sourceRenameFixture(t)
	if rc, _, errw := runSourceRename(t, workspace, "y\n", "acme", "Tools"); rc == 0 || !strings.Contains(errw, `write "tools"`) {
		t.Errorf("an illegal key was not refused with its spelling (rc=%d): %s", rc, errw)
	}
	if rc, _, errw := runSourceRename(t, workspace, "y\n", "nope", "tools"); rc == 0 || !strings.Contains(errw, `no source "nope"`) {
		t.Errorf("a missing source was not refused (rc=%d): %s", rc, errw)
	}
}
