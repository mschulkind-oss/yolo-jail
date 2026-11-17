package hostskills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestSourcedRegistrationsThroughCommonWriter(t *testing.T) {
	repo := t.TempDir()
	write := func(p, text string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	raw := "---\nname: upstream\ndescription: fixture\n---\nraw skill\n"
	write("skills/chosen/SKILL.md", raw, 0644)
	write("skills/chosen/scripts/probe.sh", "#!/bin/sh\nset -eu\nhere=$(dirname \"$0\")\nprintf '%s\\n' \"$(cat \"$here/../../../VERSION\")\" \"$(cat \"$here/../../../shared/data\")\"\n", 0755)
	write("VERSION", "payload-v1\n", 0644)
	write("shared/data", "sibling-v1\n", 0644)
	write("LICENSE", "fixture terms\n", 0644)
	write("skills/unselected/SKILL.md", "not selected", 0644)
	write(".claude-plugin/plugin.json", `{"name":"inert"}`, 0644)
	write("hooks/start", "not executed", 0755)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	git("init", "-b", "main")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	source := "git+file://" + repo + "//skills/chosen?ref=main"
	p := &packload.Pack{Name: "selected", Root: t.TempDir(), Namespace: "demo", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Source: source, As: "first"}, {Kind: packdecl.KindSkills, Source: source, As: "second"}}}}
	agent := &packload.Pack{Name: "reader", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}}}}
	store := &packsrc.Store{Dir: t.TempDir()}
	t.Cleanup(func() {
		_ = filepath.Walk(store.Dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(p, fi.Mode().Perm()|0700)
			}
			return nil
		})
	})
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{p, agent}, store, packsrc.SkillResolveOptions{LockPath: filepath.Join(t.TempDir(), packsrc.SkillsLockName)}, true)
	if len(results) != 1 || results[0].State != packsrc.SkillReady {
		t.Fatalf("resolve: %+v", results)
	}
	p = p.ResolveDestinations([]*packload.Pack{p, agent}).Pack
	home := t.TempDir()
	dests := ComposeHostSkillsFor([]*packload.Pack{p, agent}, home, func(string, packdecl.Kind) bool { return true })
	manifest := &Manifest{}
	req := ComposeRequest{Composed: manifest, ArchiveRoot: ArchiveRoot(t.TempDir()), Stamp: "fixture", PackSetComplete: true, Configured: map[string]bool{"selected": true, "reader": true}}
	if _, err := RenderHostSkills(dests, req, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".fixture/skills")
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 2 {
		t.Fatalf("registered entries: %v %v", entries, err)
	}
	for _, name := range []string{"demo-first", "demo-second"} {
		dir := filepath.Join(dest, name)
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			t.Fatalf("real registration: %v %v", fi, err)
		}
		marker := filepath.Join(dir, "SKILL.md")
		fi, err = os.Lstat(marker)
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("private marker: %v %v", fi, err)
		}
		data, _ := os.ReadFile(marker)
		if !strings.Contains(string(data), "name: "+name+"\n") {
			t.Fatalf("name: %q", data)
		}
		if target, err := os.Readlink(filepath.Join(dir, "scripts")); err != nil || target != filepath.Join(results[0].Payload, "skills/chosen/scripts") {
			t.Fatalf("planned link %q %v", target, err)
		}
		cmd := exec.Command(filepath.Join(dir, "scripts/probe.sh"))
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "payload-v1\nsibling-v1\n" {
			t.Fatalf("exposed script: %q %v", out, err)
		}
	}
	original, _ := os.ReadFile(filepath.Join(results[0].Payload, "skills/chosen/SKILL.md"))
	if string(original) != raw {
		t.Fatal("payload marker rewritten")
	}
	for _, name := range []string{"unselected", "inert", "hooks"} {
		if _, err := os.Lstat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Fatal("upstream activated", name)
		}
	}
	if _, err := RenderHostSkills(dests, req, false); err != nil {
		t.Fatal(err)
	}
	// The same writer composes the staged notch; typed planned links can dangle
	// on the host without being followed by digest or the writer.
	staged := t.TempDir()
	if _, err := ComposeInto(dests[0], staged); err != nil {
		t.Fatal(err)
	}
}

func TestSourcedClaimsRefuseSamePackEvenUnfetched(t *testing.T) {
	source := "git+https://example.invalid/repo//skills/chosen?ref=main"
	p := &packload.Pack{Name: "selected", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Source: source, As: "same", Into: ".fixture/skills"}, {Kind: packdecl.KindSkills, Source: source, As: "same", Into: ".fixture/skills"}}}}
	home := t.TempDir()
	dests := ComposeHostSkillsFor([]*packload.Pack{p}, home, nil)
	if cols := Collisions(dests); len(cols) != 1 {
		t.Fatalf("declaration claims collapsed: %+v", cols)
	}
	if _, err := RenderHostSkills(dests, ComposeRequest{}, false); err == nil || !strings.Contains(err.Error(), "pack skill name collision") {
		t.Fatalf("collision: %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("collision wrote before refusal")
	}
}

func TestSourcedRegistrationArchiveFallbackPreservesRecordedLinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "chosen")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unreachable-jail-path")
	if err := os.Symlink(target, filepath.Join(src, "scripts")); err != nil {
		t.Fatal(err)
	}
	old := archiveRename
	archiveRename = func(string, string) error { return syscall.EXDEV }
	t.Cleanup(func() { archiveRename = old })
	manifest := &Manifest{RegistrationLinks: map[string]map[string]string{src: {"scripts": target}}}
	archived, err := archiveComposed(ArchiveRoot(t.TempDir()), "fixture", "selected", src, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(archived, "scripts")); err != nil || got != target {
		t.Fatalf("link dereferenced: %q %v", got, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatal("original remained after successful archive")
	}
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/unplanned", filepath.Join(src, "scripts")); err != nil {
		t.Fatal(err)
	}
	if _, err := archiveComposed(ArchiveRoot(t.TempDir()), "fixture", "selected", src, manifest); err == nil {
		t.Fatal("archive followed or authorized an unplanned link")
	}
	if _, err := os.Lstat(src); err != nil {
		t.Fatal("failed archive removed original")
	}
}
