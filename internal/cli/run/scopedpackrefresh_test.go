package run

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestDirectScopedRefreshFailureKeepsAdvancedPinWithoutPublication(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	dir := filepath.Join(repo, "corpus/skills/skill-a")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("first\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("init", "-b", "main")
	runGit("add", "--all")
	runGit("-c", "commit.gpgsign=false", "commit", "-m", "first")
	first := runGit("rev-parse", "HEAD")
	cfg := func(ref, name string) string {
		return fmt.Sprintf("packs = [{ pack = \"demo/corpus\", skills = { only = [%q] } }]\n[pack_sources.demo]\ngit = %q\nref = %q\n", name, "file://"+filepath.ToSlash(repo), ref)
	}
	user := testconfig.User(t, cfg("main", "skill-a"))
	t.Setenv("HOME", user.Root)
	t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(user.Root, paths.PacksDir()) })
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	var report bytes.Buffer
	refresh := func() {
		RefreshConfiguredPacks(func(s string) { fmt.Fprintln(&report, s) }, func(s string) { fmt.Fprintln(&report, s) })
	}
	refresh()
	o := &Options{Workspace: t.TempDir(), Stderr: &report, Stdout: &report}
	const liveName = "yolo-test-scoped-live"
	liveRoot, livePacks, briefings, err := o.stagePacks(liveName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardPackTree(liveName, liveRoot) })
	live := stagedPacks{root: liveRoot, packs: livePacks, briefings: briefings}
	if !o.prepareFreshSkillPayloads(liveName, "podman", live) {
		t.Fatal(report.String())
	}
	if err := writeLivePackTree(liveName, liveRoot); err != nil {
		t.Fatal(err)
	}
	before, err := packContentDigest(liveRoot)
	if err != nil {
		t.Fatal(err)
	}
	runGit("checkout", "-b", "next")
	runGit("mv", "corpus/skills/skill-a", "corpus/skills/skill-b")
	runGit("-c", "commit.gpgsign=false", "commit", "-m", "rename")
	second := runGit("rev-parse", "HEAD")
	if err := os.WriteFile(user.Path, []byte(cfg("next", "skill-a")), 0644); err != nil {
		t.Fatal(err)
	}
	refresh()
	lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := lock.Get("demo/corpus")
	if !ok || pin.Commit != second {
		t.Fatalf("pin did not advance: %+v", pin)
	}
	root, _, _, err := o.stagePacks("yolo-test-scoped-stale")
	if err == nil || root != "" {
		t.Fatalf("invalid scope published: root=%s err=%v", root, err)
	}
	for _, want := range []string{second, "skill-a", "skill-b", "ref"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing recovery detail %q: %v", want, err)
		}
	}
	for range 2 {
		root, _, _, repeat := o.stagePacks("yolo-test-scoped-repeat")
		if repeat == nil || root != "" || !strings.Contains(repeat.Error(), second) {
			t.Fatalf("unchanged stale selection accepted: %s %v", root, repeat)
		}
	}
	store := launchStore()
	if _, err := os.Lstat(store.SkillPayloadPath("file://"+filepath.ToSlash(repo), second)); !os.IsNotExist(err) {
		t.Fatalf("invalid advanced scope published payload: %v", err)
	}
	after, err := packContentDigest(liveRoot)
	if err != nil || before != after {
		t.Fatalf("refusal mutated running tree: %s %s %v", before, after, err)
	}
	view, skew := o.runningJailPackView(liveName, "podman", jsonx.NewOrderedMap(), live, nil, "true")
	if skew != nil || view.staged.root != liveRoot {
		t.Fatalf("refusal broke adopted attach: %+v %+v", view, skew)
	}
	for _, recovery := range []struct{ ref, name string }{{"next", "skill-b"}, {first, "skill-a"}} {
		if err := os.WriteFile(user.Path, []byte(cfg(recovery.ref, recovery.name)), 0644); err != nil {
			t.Fatal(err)
		}
		refresh()
		tree, _, _, err := o.stagePacks("yolo-test-scoped-recovery")
		if err != nil {
			t.Fatalf("recovery %+v: %v", recovery, err)
		}
		discardPackTree("yolo-test-scoped-recovery", tree)
	}
}
