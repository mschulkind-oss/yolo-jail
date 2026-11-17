package run

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestFreshSourcedPayloadTreeAndRecordedAttachIgnoreDeletedCache(t *testing.T) {
	home := packHome(t)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	for p, data := range map[string]string{
		"skills/chosen/SKILL.md":         "---\nname: original\n---\nchosen\n",
		"skills/chosen/scripts/probe.sh": "#!/bin/sh\nset -eu\nhere=$(dirname \"$0\")\nprintf '%s\\n' \"$(cat \"$here/../../../VERSION\")\" \"$(cat \"$here/../../../shared/data\")\"\n",
		"VERSION":                        "payload-v1\n", "shared/data": "sibling-v1\n",
		"skills/hidden/SKILL.md": "unselected\n", "pack.toml": "[[skills]]\nagent = \"unwanted\"\ninto = \".unwanted/skills\"\n",
	} {
		file := filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(repo, "skills/chosen/scripts/probe.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared", filepath.Join(repo, "safe")); err != nil {
		t.Fatal(err)
	}
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	dir := t.TempDir()
	source := "git+file://" + repo + "//skills/chosen?ref=main"
	writePack(t, dir, fmt.Sprintf(`{"contributes":[{"kind":"skills","source":%q,"as":"probe"},{"kind":"skills","agent":"fixture","into":".fixture/skills"}]}`, source))
	writeUserPacks(t, home, fmt.Sprintf(`[{"source":%q,"name":"selected"}]`, "file://"+dir))
	var dryOut, dryErr bytes.Buffer
	dry := dispatchOptions(t, t.TempDir(), "macos-user", &dryOut, &dryErr, nil)
	dry.DryRun = true
	dry.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	if rc := Run(*dry); rc != 0 {
		t.Fatalf("sourced dry run: %d %s", rc, dryErr.String())
	}
	if !strings.Contains(dryErr.String(), "NOT FETCHED") || strings.Contains(dryErr.String(), "delivers registration") {
		t.Fatalf("unfetched dry-run claims: %s", dryErr.String())
	}
	if _, err := os.Lstat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Fatalf("dry run fetched: %v", err)
	}
	var report bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &report, Stdout: &report}
	cname := "yolo-test-sourced-record"
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		jailcontent.SetPackSkillDirs(nil)
		jailcontent.SetPackSkillTargets(nil)
		_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
	if _, err := os.Lstat(packsrc.SkillsLockPath(paths.UserConfigPath())); !os.IsNotExist(err) {
		t.Fatalf("early reader wrote record: %v", err)
	}
	var selected *packload.Pack
	for _, p := range packs {
		if p.Name == "selected" {
			selected = p
		}
	}
	if selected == nil || selected.SourcedSkillRows()[0].Outcome.State != packsrc.SkillNotFetched {
		t.Fatalf("early projection: %+v", packs)
	}
	staged := stagedPacks{root: root, packs: packs, briefings: briefings}
	if !o.prepareFreshSkillPayloads(cname, "podman", staged) {
		t.Fatal(report.String())
	}
	row := selected.SourcedSkillRows()[0]
	if !strings.HasPrefix(row.PayloadRoot, paths.ContextPacksDir+"/"+packload.SkillPayloadTreeDir+"/") {
		t.Fatalf("podman target %q", row.PayloadRoot)
	}
	for _, rt := range []string{"apple-container", "macos-user"} {
		target := skillPayloadReadingRoot(cname, rt, root)
		if rt == "apple-container" && target != acPackRootInJail {
			t.Fatalf("Apple reading root %q, want %q", target, acPackRootInJail)
		}
		if strings.Contains(target, paths.PacksDir()) {
			t.Fatalf("%s leaks host cache: %s", rt, target)
		}
	}
	// Simulate the reading namespace locally, exercising the production plan,
	// private writer and planned-link sync; this is not a native/runtime claim.
	packload.MapSkillPayloadTargets(packs, root)
	adoptPackRecords(packs)
	jailcontent.SetPackSkillTargets(packSkillTargets(packs))
	staging, err := jailcontent.PrepareSkills(cname, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	targets := packSkillTargets(packs)
	if len(targets) != 1 {
		t.Fatalf("targets %+v", targets)
	}
	exposed := filepath.Join(staging, targets[0].Staging, "probe/scripts/probe.sh")
	check := func(path string) {
		t.Helper()
		out, err := exec.Command(path).CombinedOutput()
		if err != nil || string(out) != "payload-v1\nsibling-v1\n" {
			t.Fatalf("script %q: %q %v", path, out, err)
		}
	}
	check(exposed)
	if _, err := os.Lstat(filepath.Join(staging, targets[0].Staging, "hidden")); !os.IsNotExist(err) {
		t.Fatalf("unselected exposed: %v", err)
	}
	// Cache/mirrors and the declaring repository all disappear; only the
	// launch's recorded bytes are authority for this attach-side refresh.
	_ = filepath.Walk(paths.PacksDir(), func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode()&os.ModeSymlink == 0 {
			_ = os.Chmod(p, fi.Mode().Perm()|0700)
		}
		return nil
	})
	if err := os.RemoveAll(paths.PacksDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if err := writeLivePackTree(cname, root); err != nil {
		t.Fatal(err)
	}
	futureDir := t.TempDir()
	writePack(t, futureDir, fmt.Sprintf(`{"contributes":[{"kind":"skills","source":%q,"as":"probe"},{"kind":"skills","agent":"fixture","into":".fixture/skills"}]}`, "git+file://"+filepath.Join(home, "absent-current-repository")+"//skills/chosen?ref=main"))
	futurePack, problems := packload.LoadDir(futureDir, "selected")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	current := append([]*packload.Pack(nil), packs...)
	for i, p := range current {
		if p.Name == "selected" {
			current[i] = futurePack
		}
	}
	view, skew := o.runningJailPackView(cname, "podman", jsonx.NewOrderedMap(), stagedPacks{packs: current}, nil, "true")
	if skew != nil || view.staged.root != root {
		t.Fatalf("recorded attach view: %+v %+v", view, skew)
	}
	loaded := view.staged.packs
	if _, err := os.Lstat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Fatalf("attach recreated cache: %v", err)
	}
	packload.MapSkillPayloadTargets(loaded, root)
	guest, err := entrypoint.LoadJailPacks(&entrypoint.Env{Home: home, Workspace: o.Workspace, Vars: map[string]string{"YOLO_PACK_ROOT": root}})
	if err != nil || len(guest) != len(loaded) {
		t.Fatalf("entrypoint recorded load: %d %v", len(guest), err)
	}
	for _, p := range guest {
		if p.Name == "selected" {
			if rows := p.SourcedSkillRows(); len(rows) != 1 || rows[0].Outcome.State != packsrc.SkillReady {
				t.Fatalf("entrypoint registration state: %+v", rows)
			}
		}
	}
	if len(loaded) != len(packs) {
		t.Fatalf("payload discovered as pack: %d", len(loaded))
	}
	adoptPackRecords(loaded)
	jailcontent.SetPackSkillTargets(packSkillTargets(loaded))
	if _, err := jailcontent.PrepareSkills(cname, home, nil); err != nil {
		t.Fatal(err)
	}
	check(exposed)
	// The actual Apple pack copier preserves safe payload links without giving
	// ordinary copies the same exception. Its copied payload also runs the script.
	state := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(state, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
	if err := acMaterializePackTree(root, acPackRootRel, state); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(state, acPackRootRel, filepath.FromSlash(row.PayloadLocation))
	if got, err := os.Readlink(filepath.Join(copied, "safe")); err != nil || got != "shared" {
		t.Fatalf("Apple safe link: %q %v", got, err)
	}
	check(filepath.Join(copied, "skills/chosen/scripts/probe.sh"))
	if err := acMaterializePackTree(root, acPackRootRel, state); err != nil {
		t.Fatalf("replacement of sealed old Apple payload: %v", err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(state, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
}
