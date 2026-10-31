package cli

// packsourcerename_test.go drives `yolo pack source rename` through packMain, the CLI's own
// dispatcher, over a synthetic user config (docs/design/pack-naming.md §3.6).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
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

// In a jail the verb prints the host-run line, and that line answers the y/N question with
// --yes, since a host-run has no terminal to answer it; a --dry-run line asks nothing and gets
// none. Fails if the --yes is dropped from the printed line.
func TestPackSourceRenameInJailPrintsAHostRunLineThatAnswersTheQuestion(t *testing.T) {
	workspace, userPath := sourceRenameFixture(t)
	before, _ := os.ReadFile(userPath)
	t.Setenv("YOLO_VERSION", "test-jail")
	rc, out, errw := runSourceRename(t, workspace, "", "acme", "tools")
	if rc == 0 || out != "" || !strings.Contains(errw, "yolo host-run -C ") ||
		!strings.Contains(errw, "-- yolo pack source rename acme tools --yes\n") {
		t.Errorf("in-jail rename rc=%d stdout=%q stderr=%q, want a host-run line ending in --yes", rc, out, errw)
	}
	_, _, errw = runSourceRename(t, workspace, "", "acme", "tools", "--dry-run")
	if !strings.Contains(errw, "-- yolo pack source rename acme tools --dry-run\n") {
		t.Errorf("the in-jail --dry-run line gained a flag: %q", errw)
	}
	if after, _ := os.ReadFile(userPath); string(after) != string(before) {
		t.Errorf("the in-jail rename wrote the config: %q", after)
	}
}

// A new key a source already has is refused naming the command that removes it.
func TestPackSourceRenameOntoATakenKeyNamesTheUnsetCommand(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "x", "pack.json"), `{}`)
	path := filepath.ToSlash(src)
	user := testconfig.User(t, "packs = [\"acme/x\"]\n\n[pack_sources]\nacme = { path = '"+path+"' }\n"+
		"tools = { path = '"+path+"' }\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, _, errw := runSourceRename(t, testconfig.Workspace(t, "").Root, "", "acme", "tools", "--yes")
	if rc == 0 || !strings.Contains(errw, "`yolo config unset pack_sources.tools --user`") {
		t.Errorf("rc=%d stderr=%q, want the refusal to name `yolo config unset pack_sources.tools --user`", rc, errw)
	}
}

// Two handle-keyed tables the rename would merge are refused naming the command that removes the
// second, quoted for the shell.
func TestPackSourceRenameOfCollidingTablesNamesTheUnsetCommand(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "x", "pack.json"), `{}`)
	user := testconfig.User(t, "packs = [\"acme/x\"]\n\n[pack_sources]\nacme = { path = '"+filepath.ToSlash(src)+"' }\n\n"+
		"[allow.\"acme/x\"]\nmode = \"fast\"\n\n[allow.\"tools/x\"]\nmode = \"slow\"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, _, errw := runSourceRename(t, testconfig.Workspace(t, "").Root, "", "acme", "tools", "--yes")
	if rc == 0 || !strings.Contains(errw, "`yolo config unset 'allow.\"tools/x\"' --user`") || strings.Contains(errw, "by hand") {
		t.Errorf("rc=%d stderr=%q, want the refusal to name the unset command and no hand edit", rc, errw)
	}
}

// When the verb cannot write its plan, it prints the same edits as `yolo config` commands, each
// key spelled so that `yolo config set` and `unset` read back the path the plan edits.
func TestPackSourceRenameSpellsItsPlanAsConfigCommands(t *testing.T) {
	workspace, _ := sourceRenameFixture(t)
	var errw strings.Builder
	plan, _, rc := planSourceRename(workspace, "acme", "tools", &errw)
	if rc != 0 {
		t.Fatalf("plan rc=%d: %s", rc, errw.String())
	}
	got := renameAsCommands(plan)
	for _, want := range []string{
		"  yolo config set pack_sources.tools '{ path = ",
		"  yolo config set 'packs[0]' '\"tools/x\"' --user\n",
		"  yolo config set 'packs[1].pack' '\"tools/y\"' --user\n",
		"  yolo config unset pack_sources.acme --user\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the commands lack %q:\n%s", want, got)
		}
	}
	for _, m := range append([]configedit.Mutation{plan.first}, plan.then...) {
		steps, err := parseConfigEditKey(configEditKey(m.Path))
		if err != nil || len(steps) != len(m.Path) {
			t.Errorf("%q reads back as %+v, %v; want %+v", configEditKey(m.Path), steps, err, m.Path)
			continue
		}
		for i := range steps {
			if steps[i] != m.Path[i] {
				t.Errorf("%q reads back as %+v, want %+v", configEditKey(m.Path), steps, m.Path)
			}
		}
	}
}
