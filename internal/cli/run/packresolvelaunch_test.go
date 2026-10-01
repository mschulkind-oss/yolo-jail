package run

// packresolvelaunch_test.go pins the LAUNCH's call site of the one pack resolver
// (config.ResolvePack, docs/plans/notch-convergence.md item 5): every entry stagePacks stages goes
// through it, the embedded ones included, and the embedded packs come from the one
// materialization every notch reads (packload.Embedded). The resolver's own tests are in
// internal/config; these fail when stagePacks stops calling it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// withEmbeddedFS swaps the embedded pack filesystem for one test, releasing the loaded packs on
// both sides so the next Embedded() reads the swapped one.
func withEmbeddedFS(t *testing.T, f fstest.MapFS) {
	t.Helper()
	packload.ReleaseEmbedded()
	packload.SetEmbeddedFS(f)
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(packs.FS)
	})
}

// AN EMBEDDED ENTRY'S `exclude` REACHES THE JAIL'S TREE. It used to be accepted by config and then
// ignored: the launch copied an embedded pack whole (treesync), whatever its entry said.
func TestStagePacksAppliesAnEmbeddedEntrysFilters(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[{"source":"hello-daemon","exclude":["README.md"]}]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-embedded-filter")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staged := filepath.Join(tree, officialStagingDir, "hello-daemon")
	if _, err := os.Stat(filepath.Join(staged, "README.md")); !os.IsNotExist(err) {
		t.Errorf("the entry excludes README.md and the jail's tree still has it (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(staged, "pack.json")); err != nil {
		t.Errorf("control: the rest of the pack must stage: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Root != staged {
		t.Errorf("the loaded pack must be the staged copy: %+v", loaded)
	}
}

// A LOCAL PACK DEPLOYED BY A DOTFILE MANAGER STAGES, its links followed into the dotfiles repo
// (docs/plans/notch-convergence.md OQ-NC9, ruled A: a local pack's links are followed at every
// notch). rcm links each FILE and makes the directories real; stow links a whole DIRECTORY. Both
// shapes are here, one in the conventional local pack and one in a configured local pack whose
// entry carries a filter, because a filtered entry is followed too. The launch used to refuse
// both with packstage's no-escape rule while `yolo host apply` delivered them
// (TestApplyHostConvergesOverASymlinkedPack). The jail's tree holds plain files: the jail cannot
// see the dotfiles repo, so a copied link would point at nothing there.
func TestStagePacksFollowsADotfileManagersLinksInALocalPack(t *testing.T) {
	home := packHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	dotfiles := filepath.Join(t.TempDir(), "dotfiles")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	// rcm: ~/.config/yolo-jail/local/<file> -> ~/.dotfiles/config/yolo-jail/local/<file>.
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	rcmSrc := filepath.Join(dotfiles, "config", "yolo-jail", "local")
	write(filepath.Join(rcmSrc, "pack.json"), `{"name":"local"}`)
	write(filepath.Join(rcmSrc, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: d\n---\nFROM RCM\n")
	link(filepath.Join(rcmSrc, "pack.json"), filepath.Join(local, "pack.json"))
	link(filepath.Join(rcmSrc, "skills", "review", "SKILL.md"), filepath.Join(local, "skills", "review", "SKILL.md"))
	// stow: <pack>/skills/tidy -> ~/dotfiles/stow/tidy, a directory link.
	stowed := localPackDir(t, "stowed")
	write(filepath.Join(dotfiles, "stow", "tidy", "SKILL.md"), "---\nname: tidy\ndescription: d\n---\nFROM STOW\n")
	link(filepath.Join(dotfiles, "stow", "tidy"), filepath.Join(stowed, "skills", "tidy"))
	writeUserPacks(t, home, `[{"source":"file://`+stowed+`","exclude":["marker.txt"]}]`)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-dotfiles")
	if err != nil {
		t.Fatalf("a local pack a dotfile manager deployed must launch: %v", err)
	}
	if got := packNames(loaded); !slices.Equal(got, []string{"stowed", "local"}) {
		t.Fatalf("loaded = %v, want both local packs", got)
	}
	for rel, want := range map[string]string{
		filepath.Join("local", "skills", "review", "SKILL.md"): "FROM RCM",
		filepath.Join("stowed", "skills", "tidy", "SKILL.md"):  "FROM STOW",
	} {
		path := filepath.Join(tree, rel)
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s in the jail's tree must be a plain file (%v, %v)", rel, fi, err)
			continue
		}
		if body, _ := os.ReadFile(path); !strings.Contains(string(body), want) {
			t.Errorf("%s = %q, want the dotfiles repo's content", rel, body)
		}
	}
	if _, err := os.Stat(filepath.Join(tree, "stowed", "marker.txt")); !os.IsNotExist(err) {
		t.Errorf("the entry's exclude still applies to a followed pack (%v)", err)
	}
}

// A FETCHED PACK'S ESCAPING SYMLINK STILL REFUSES THE LAUNCH (OQ-NC9 keeps the no-escape rule for
// the case it was written for): someone else's repository must not stage a host file into a jail.
func TestStagePacksRefusesAFetchedPacksEscapingSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := packHome(t)
	t.Setenv("YOLO_PACK_ROOT", "")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "pack.json"), []byte(`{"name":"acme"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "skills", "leak"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(repo, "skills", "leak", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "pack"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	src := "git+file://" + repo + "?ref=main"
	writeUserPacks(t, home, `[{"name": "acme", "source": "`+src+`"}]`)
	syncPackStore(t, src)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	_, _, _, err := o.stagePacks("yolo-test-fetched-escape")
	if err == nil || !strings.Contains(err.Error(), "outside the pack") || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("a fetched pack's escaping symlink must refuse the launch, naming the pack: %v", err)
	}
}

// THE LAUNCH READS THE ONE EMBEDDED MATERIALIZATION. A pack only the registered embedded FS
// carries stages, and a broken one refuses the launch as a yolo bug. Both fail if stagePacks goes
// back to materializing packs.FS into a scratch dir of its own (row B7).
func TestStagePacksReadsTheOneEmbeddedMaterialization(t *testing.T) {
	home := packHome(t)
	withEmbeddedFS(t, fstest.MapFS{"onlyinfs/pack.json": {Data: []byte(`{"name":"onlyinfs"}`)}})
	writeUserPacks(t, home, `["onlyinfs"]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	_, loaded, _, err := o.stagePacks("yolo-test-onlyinfs")
	if err != nil {
		t.Fatalf("a pack the registered embedded FS carries must stage: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "onlyinfs" {
		t.Fatalf("loaded = %+v, want the one embedded pack", loaded)
	}

	withEmbeddedFS(t, fstest.MapFS{
		"onlyinfs/pack.json": {Data: []byte(`{"name":"onlyinfs"}`)},
		"broken/pack.json":   {Data: []byte("{not json")},
	})
	_, _, _, err = o.stagePacks("yolo-test-broken-embedded")
	if err == nil || !strings.Contains(err.Error(), "official packs:") {
		t.Fatalf("a broken embedded materialization must refuse the launch as a yolo bug: %v", err)
	}
}

// THE LAZY LOOPHOLE RESOLVER READS AN UNFILTERED LOCAL PACK IN PLACE AND FOLLOWS ITS LINKS, as it
// did before there was one resolver. In place because a loophole module it hands out is where a
// host-scope daemon spawned by `yolo host-daemon start` resolves {loophole_dir}, and that daemon
// outlives the verb, whose process pack tree is deleted when it exits. Following because the host
// verbs and the launch deliver such a pack (OQ-NC9), and `yolo loopholes list` must not
// then leave out a loophole it ships.
func TestResolveConfiguredPacksReadsAnUnfilteredLocalPackInPlace(t *testing.T) {
	home := packHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	p := writePackManifest(t, "stow", `{"name":"stow"}`)
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("---\nname: s\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Root, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Root, "skills", "s", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+p.Root+`"]`)
	var got *packload.Pack
	for _, r := range resolveConfiguredPacks() {
		if r.Name == "stow" {
			got = r
		}
	}
	if got == nil {
		t.Fatal("the lazy resolver left out a local pack the host verbs deliver")
	}
	if got.Root != p.Root {
		t.Errorf("the lazy resolver read pack stow at %s, want its own directory %s", got.Root, p.Root)
	}
}
