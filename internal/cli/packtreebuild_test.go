package cli

// packtreebuild_test.go drives a PACK-TREE PROGRAM's build (docs/design/pack-buildable-programs.md
// §6, BG-D2 to BG-D5) through the fork route's one build act, buildFork, with the run pipeline
// substituted as forkbuild_test.go substitutes it: the placement script the build jail runs, the
// staged source, the trim to the program's two paths, the no-build copy the host admits itself,
// and the launch slot's call site (addForkKeys) that reaches all of it.

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The placement script is pinned whole, then RUN through bash in a temp HOME with a fake staged
// source, and the link it leaves must resolve to the built, executable program.
func TestThePackTreePlacementScriptBuildsInPlaceAndLinks(t *testing.T) {
	want := `d="$HOME"/.local/lib/yolo-programs/board && mkdir -p "$d" && cp -a /workspace/src/. "$d"/ && cd "$d" && {
make
} && test -f "$d"/dist/board && chmod +x "$d"/dist/board && mkdir -p "$HOME"/.local/bin && ln -sfn ../lib/yolo-programs/board/dist/board "$HOME"/.local/bin/board`
	if got := packTreePlacementScript("board", "dist/board", "make"); got != want {
		t.Fatalf("placement script:\n got %s\nwant %s", got, want)
	}

	home, src := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "VERSION"), "7\n")
	// A trailing comment on the build line must not swallow the placement after it.
	build := `mkdir -p dist && printf '#!/bin/sh\necho board-%s\n' "$(cat VERSION)" > dist/board # make it`
	script := strings.ReplaceAll(packTreePlacementScript("board", "dist/board", build), "/workspace/src", shquote.Quote(src))
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the placement script failed: %v\n%s", err, out)
	}
	link := filepath.Join(home, ".local", "bin", "board")
	if target, err := os.Readlink(link); err != nil || target != "../lib/yolo-programs/board/dist/board" {
		t.Fatalf("the link is %q (%v), want the relative target into the program's directory", target, err)
	}
	out, err := exec.Command(link).Output()
	if err != nil || strings.TrimSpace(string(out)) != "board-7" {
		t.Fatalf("running the link: %q %v, want board-7 from the built tree", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "lib", "yolo-programs", "board", "VERSION")); err != nil {
		t.Errorf("the source was not copied into the program's directory: %v", err)
	}
}

// packTreeBuildFixture is a temp HOME and a staged pack fx with tools/x (a VERSION file and a .git
// the digest and the staging both leave out), and the pack-tree program x it declares with build.
func packTreeBuildFixture(t *testing.T, build string) (packload.Fork, string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	root := floortest.ResolvedTemp(t)
	writeFile(t, filepath.Join(root, "tools", "x", "VERSION"), "1\n")
	writeFile(t, filepath.Join(root, "tools", "x", ".git", "HEAD"), "ref: refs/heads/main\n")
	p := &packload.Pack{Name: "fx", Root: root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "x", Via: packdecl.ViaPack, From: "tools/x", Build: build, Run: "x",
	}}}}
	f := packload.PackTreePrograms([]*packload.Pack{p})[0]
	digest, err := f.Digest()
	if err != nil || digest == "" {
		t.Fatalf("digest: %q %v", digest, err)
	}
	return f, digest
}

// fakePackTreeJail stands in for the build jail's run: it checks the staged source is the pack's
// tools/x minus .git, then leaves what the placement script leaves — the program's directory and
// its link — plus a stray dependency cache under ~/go, which the trim must drop. runs counts it.
func fakePackTreeJail(t *testing.T, seen *run.Options, runs *int) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*runs++
		*seen = o
		src := filepath.Join(o.Workspace, forkSourceLeaf)
		if _, err := os.Stat(filepath.Join(src, "VERSION")); err != nil {
			t.Errorf("the pack's source is not staged in the build workspace: %v", err)
		}
		if _, err := os.Stat(filepath.Join(src, ".git")); err == nil {
			t.Error("the staged source carries the pack tree's .git, which its digest leaves out")
		}
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity-abc\n")
		out := filepath.Join(o.Workspace, captureOutLeaf)
		tree := capture.TreeDir(out)
		dir := filepath.Join(tree, ".local", "lib", "yolo-programs", "x")
		writeFile(t, filepath.Join(dir, "VERSION"), "1\n")
		writeFile(t, filepath.Join(dir, "x"), "#!/bin/sh\necho x-1\n")
		if err := os.Chmod(filepath.Join(dir, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../lib/yolo-programs/x/x", filepath.Join(tree, ".local", "bin", "x")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(tree, "go", "pkg", "mod", "cache", "junk"), "dependency\n")
		m, err := capture.ManifestForTree(tree, "/home/agent", "linux/amd64")
		if err != nil {
			t.Fatal(err)
		}
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

func manifestPaths(t *testing.T, root string) []string {
	t.Helper()
	m, err := capture.ReadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range m.Entries {
		out = append(out, e.Path)
	}
	return out
}

// THE BUILD: the fork route's act, sealed to the one pack, running the placement script over the
// staged tree, admitting the program's two paths alone under a receipt keyed on the tree digest —
// and a second build of the same key is a hit that runs no jail.
func TestTheBuildActBuildsAPackTreeProgramFromItsStagedTree(t *testing.T) {
	const build = `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	f, digest := packTreeBuildFixture(t, build)
	var seen run.Options
	runs := 0
	withFakeCaptureJail(t, fakePackTreeJail(t, &seen, &runs))
	b := forkBuild{Fork: f, Commit: digest, Platform: "linux/amd64"}
	mode := buildMode{lock: pidlock.NoWait, runJail: func(staging string, b forkBuild, s captureStreams) int {
		return forkBuildRunJail(staging, b, s, false)
	}}
	var out bytes.Buffer
	entry, err := buildFork(b, mode, &out, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("buildFork = %v, %v\n%s", entry, err, out.String())
	}
	if !seen.Sealed || !slices.Equal(seen.OnlyPacks, []string{"fx"}) {
		t.Errorf("the build jail ran with Sealed=%v OnlyPacks=%v, want the seal and the pack alone", seen.Sealed, seen.OnlyPacks)
	}
	if !slices.Equal(seen.Args, forkBuildJailArgv(packTreePlacementScript("x", "x", build))) {
		t.Errorf("build jail argv = %q, want the placement script around the pack's build", seen.Args)
	}
	for _, p := range manifestPaths(t, entry.Root) {
		if !packTreeKeeps("x", p) {
			t.Errorf("the admitted entry carries %s, outside the program's two paths", p)
		}
	}
	if _, err := os.Stat(filepath.Join(capture.TreeDir(entry.Root), "go")); err == nil {
		t.Error("the stray ~/go cache was admitted with the program")
	}
	if !strings.Contains(out.String(), "dropped 1 path") || !strings.Contains(out.String(), "go") {
		t.Errorf("the dropped path is not named:\n%s", out.String())
	}
	receipts, _ := os.ReadFile(capture.ReceiptsPath(entry.Root))
	for _, want := range []string{`"declared":"pack:fx/tools/x"`, digest} {
		if !strings.Contains(string(receipts), want) {
			t.Errorf("the receipt lacks %s:\n%s", want, receipts)
		}
	}
	store := &capture.Store{Dir: paths.CapturesDir()}
	if hit, _, err := resolveForkBuild(store, "x", "linux/amd64", "pack:fx/tools/x", digest, b.recipe()); err != nil ||
		hit.Key != entry.Key {
		t.Fatalf("the build is not selectable by its key: %v %v", hit, err)
	}
	again, err := buildFork(b, mode, io.Discard, io.Discard, false)
	if err != nil || again.Key != entry.Key || runs != 1 {
		t.Errorf("the second build of one key: %v %v, %d jail runs, want the hit and one run", again, err, runs)
	}
}

// NO BUILD LINE, NO JAIL (BG-D5): the host lays out the copy itself, the exec bit set on `run`, and
// admits it under the same key and receipt kind, recording that no build ran.
func TestAPackTreeProgramWithNoBuildLineIsCopiedWithNoJail(t *testing.T) {
	f, _ := packTreeBuildFixture(t, "")
	writeFile(t, filepath.Join(f.SourceDir(), "x"), "#!/bin/sh\necho plain\n") // shipped without its exec bit
	digest, err := f.Digest()
	if err != nil {
		t.Fatal(err)
	}
	withFakeCaptureJail(t, func(run.Options) int {
		t.Error("a pack-tree program with no build line started a build jail")
		return 1
	})
	b := forkBuild{Fork: f, Commit: digest, Platform: "linux/amd64"}
	var out bytes.Buffer
	entry, err := buildFork(b, buildMode{lock: pidlock.NoWait}, &out, io.Discard, false)
	if err != nil || entry == nil {
		t.Fatalf("buildFork = %v, %v\n%s", entry, err, out.String())
	}
	got := manifestPaths(t, entry.Root)
	for _, want := range []string{".local/bin/x", ".local/lib/yolo-programs/x", ".local/lib/yolo-programs/x/x"} {
		if !slices.Contains(got, want) {
			t.Errorf("the copy lacks %s: %v", want, got)
		}
	}
	for _, p := range got {
		if !packTreeKeeps("x", p) || strings.Contains(p, ".git") {
			t.Errorf("the copy carries %s", p)
		}
	}
	tree := capture.TreeDir(entry.Root)
	if target, err := os.Readlink(filepath.Join(tree, ".local", "bin", "x")); err != nil || target != "../lib/yolo-programs/x/x" {
		t.Errorf("the link is %q (%v)", target, err)
	}
	if fi, err := os.Stat(filepath.Join(tree, ".local", "lib", "yolo-programs", "x", "x")); err != nil || fi.Mode()&0o100 == 0 {
		t.Errorf("run is not executable in the copy: %v %v", fi, err)
	}
	receipts, _ := os.ReadFile(capture.ReceiptsPath(entry.Root))
	for _, want := range []string{`"declared":"pack:fx/tools/x"`, digest, packTreeNoBuildToolchain} {
		if !strings.Contains(string(receipts), want) {
			t.Errorf("the receipt lacks %s:\n%s", want, receipts)
		}
	}
	if !strings.Contains(out.String(), "copied") {
		t.Errorf("no result line:\n%s", out.String())
	}
}

// A staged source that no longer matches the digest it was keyed on is refused, never admitted
// under a revision it does not hold.
func TestAPackTreeSourceThatMovedIsRefused(t *testing.T) {
	f, digest := packTreeBuildFixture(t, "")
	writeFile(t, filepath.Join(f.SourceDir(), "x"), "#!/bin/sh\n")
	err := stagePackTreeSource(f, digest, filepath.Join(t.TempDir(), "src"))
	if err == nil || !strings.Contains(err.Error(), "changed while it was staged") {
		t.Fatalf("stagePackTreeSource = %v, want the moved-source refusal", err)
	}
}

// THE CALL SITE: a launch's slot (runBuildSlot → addForkKeys) hands a pack-tree pin to the build
// act, which builds it once in the build-jail child and answers its key; the next launch with the
// same tree is a hit that runs no jail and prints no build line. Red if addForkKeys stops routing a
// pack-tree pin to the act, or selects the hit by the fork's (empty) source address.
func TestTheLaunchSlotBuildsAPackTreeProgramOnceThenHits(t *testing.T) {
	const build = `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	f, digest := packTreeBuildFixture(t, build)
	var seen run.Options
	runs := 0
	withFakeCaptureJail(t, fakePackTreeJail(t, &seen, &runs))
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	ws := floortest.ResolvedTemp(t)
	slot := func() (string, string) {
		var stream bytes.Buffer
		req := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: digest}}, Platform: "linux/amd64",
			Runtime: "podman", Workspace: ws, Interrupt: &run.ActInterrupt{}}
		forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
		if forks["x"].Key == "" {
			t.Fatalf("the slot answered %+v\n%s", forks["x"], stream.String())
		}
		return forks["x"].Key, stream.String()
	}
	key1, out1 := slot()
	if runs != 1 || !strings.Contains(out1, "program builds") || !strings.Contains(out1, "built program fx/x") {
		t.Fatalf("the first launch: %d jail runs, want one build with its lines:\n%s", runs, out1)
	}
	// WHICH BYTES (§4): the result line names the tree digest in place of a commit.
	if !strings.Contains(out1, "built program fx/x (built from its pack's tree) at tree digest "+shortSHA(digest)) {
		t.Errorf("the result line does not name the tree digest it built:\n%s", out1)
	}
	key2, out2 := slot()
	if key2 != key1 || runs != 1 {
		t.Errorf("the second launch: key %s (first %s), %d jail runs, want the hit and no build", key2, key1, runs)
	}
	if strings.Contains(out2, "build") {
		t.Errorf("a hit printed a build line:\n%s", out2)
	}
}

// THE BUILD'S COPY FOLLOWS NO SYMLINK (copySourceTreeSkipping, over internal/nofollow): a link in a
// checkout an agent can write — to a directory or a file outside it, ~/.ssh say — is copied AS A
// LINK, with its target, and none of the bytes behind it reach the build's staged tree. A regular
// file keeps its bytes and its exec bit.
func TestTheBuildsCopyCopiesASymlinkAsALinkAndNeverFollowsIt(t *testing.T) {
	base := t.TempDir()
	src, outside, dst := filepath.Join(base, "checkout"), filepath.Join(base, "ssh"), filepath.Join(base, "staged")
	for _, d := range []string{src, outside, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(outside, "id_ed25519")
	if err := os.WriteFile(key, []byte("SECRET-HOST-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(key, filepath.Join(src, "keylink")); err != nil {
		t.Fatal(err)
	}
	if err := copySourceTreeSkipping(src, dst, packload.PackTreeDigestSkip(), base); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"dirlink": outside, "keylink": key} {
		fi, err := os.Lstat(filepath.Join(dst, name))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s in the staged tree = %v (%v), want a symlink", name, fi, err)
		}
		if got, _ := os.Readlink(filepath.Join(dst, name)); got != want {
			t.Errorf("%s points at %q, want %q", name, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(dst, "build.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("build.sh = %v (%v), want it copied with its exec bit", fi, err)
	}
	_ = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "SECRET-HOST-BYTES") {
				t.Errorf("the staged tree holds the host's bytes at %s", p)
			}
		}
		return nil
	})
}

// A source whose ROOT is a symlink is refused before anything is copied (stagePackTreeSource's Lstat;
// PackTreeDigest refuses one the same way).
func TestASymlinkedSourceRootIsRefused(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := packload.PackTreeDigest(link); err == nil {
		t.Errorf("PackTreeDigest of a symlinked root succeeded, want a refusal")
	}
	f := packload.Fork{Pack: "board", Bin: "board", Run: "make", Checkout: link}
	if err := stagePackTreeSource(f, "digest", filepath.Join(base, "dst")); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Errorf("staging a symlinked checkout = %v, want a not-a-directory refusal", err)
	}
}

// THE BUILD'S COPY FOLLOWS NO LINK ABOVE ITS ROOT THAT ROOT DID NOT PLACE (BG-D22): the checkout is
// proj/vendor/tool, and vendor is a user's link — swapped in after the override was checked — to a
// host directory that also holds a tool/ with a key in it. The copy refuses and the staging holds
// none of the host's bytes. Red if copySourceTreeSkipping goes back to a path-based walk.
func TestTheBuildsCopyRefusesAnUntrustedLinkAboveItsRoot(t *testing.T) {
	base := t.TempDir()
	proj, host, dst := filepath.Join(base, "proj"), filepath.Join(base, "host"), filepath.Join(base, "staged")
	for _, d := range []string{proj, filepath.Join(host, "tool")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(host, "tool", "id_ed25519"), []byte("SECRET-HOST-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(proj, "vendor")
	if err := os.Symlink(host, link); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Lchown(link, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	if err := copySourceTreeSkipping(filepath.Join(link, "tool"), dst, nil, ""); err == nil {
		t.Errorf("the copy read through a user's link above its root")
	}
	if _, err := os.Stat(filepath.Join(dst, "id_ed25519")); err == nil {
		t.Errorf("the staging holds the host's key")
	}
}

// THE COPY WRITES NOTHING THROUGH A LINK: a destination entry that already exists — here a link
// standing where the source's directory goes, as `Lib` meets `lib` on a case-insensitive volume —
// fails the copy, and nothing is written where the link points.
func TestTheBuildsCopyNeverWritesThroughAnExistingEntry(t *testing.T) {
	base := t.TempDir()
	src, dst, elsewhere := filepath.Join(base, "src"), filepath.Join(base, "dst"), filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(src, "lib"), dst, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dst, "lib")); err != nil {
		t.Fatal(err)
	}
	if err := copySourceTreeSkipping(src, dst, nil, base); err == nil {
		t.Errorf("the copy went on past an existing destination entry")
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "x")); err == nil {
		t.Errorf("the copy wrote through the link")
	}
	if err := os.WriteFile(filepath.Join(dst, "f"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dst, "lib")); err != nil {
		t.Fatal(err)
	}
	if err := copySourceTreeSkipping(src, dst, nil, base); err == nil {
		t.Errorf("the copy overwrote an existing destination file")
	}
}

// THE TRUST ROOT AT THE CALL SITE (stagePackTreeSource, BG-D22): a pack's own staged tree sits under
// yolo's state directory, which a user may have symlinked to another disk, and is digested and copied
// through that link; a dev override's checkout behind the same kind of link is refused, by the digest
// and by the copy, because an agent may write any of a checkout's ancestors. Red if the call site
// stops choosing the checkout reach from Fork.Checkout, either way round.
func TestStagingFollowsTheUsersStateLinkButNotACheckouts(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "disk", "packs", "fx", "tools", "x")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(real, "VERSION"), "1\n")
	state := filepath.Join(base, "state")
	if err := os.Symlink(filepath.Join(base, "disk"), state); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Lchown(state, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	own := packload.Fork{Pack: "fx", Bin: "x", Run: "x", Root: filepath.Join(state, "packs", "fx"), From: "tools/x"}
	digest, err := own.Digest()
	if err != nil || digest == "" {
		t.Fatalf("Digest of a pack's tree under the user's state link = %q, %v", digest, err)
	}
	if err := stagePackTreeSource(own, digest, filepath.Join(base, "dst-own")); err != nil {
		t.Errorf("staging a pack's tree under the user's state link: %v", err)
	}
	checkout := own
	checkout.Checkout = filepath.Join(state, "packs", "fx", "tools", "x")
	if d, err := checkout.Digest(); err == nil {
		t.Errorf("Digest of a checkout behind a user's link = %q, want a refusal", d)
	}
	if err := stagePackTreeSource(checkout, digest, filepath.Join(base, "dst-checkout")); err == nil {
		t.Errorf("staging a checkout behind a user's link succeeded, want a refusal")
	}
}

// A LINK INSIDE THE PACK, BETWEEN ITS ROOT AND `from`, IS REFUSED (BG-D23): the trust root of a
// pack's own source is the pack's root, not the source directory's parent, so a pack whose tools/ is
// a link to a host directory holding an x/ with a key in it is neither digested nor copied. Red if
// Fork.SourceBase, or either reader's use of it, goes back to the source directory's parent.
func TestALinkInsideThePackAboveItsSourceIsRefused(t *testing.T) {
	base := t.TempDir()
	root, host := filepath.Join(base, "packs", "fx"), filepath.Join(base, "host")
	for _, d := range []string{root, filepath.Join(host, "x")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(host, "x", "id_ed25519"), "SECRET-HOST-BYTES")
	link := filepath.Join(root, "tools")
	if err := os.Symlink(host, link); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Lchown(link, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	f := packload.Fork{Pack: "fx", Bin: "x", Run: "x", Root: root, From: "tools/x"}
	if d, err := f.Digest(); err == nil {
		t.Errorf("Digest read through a link inside the pack: %s", d)
	}
	dst := filepath.Join(base, "dst")
	if err := stagePackTreeSource(f, "digest", dst); err == nil || strings.Contains(err.Error(), "changed while it was staged") {
		t.Errorf("staging through a link inside the pack = %v, want the link refused", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "id_ed25519")); err == nil {
		t.Errorf("the staging holds the host's key")
	}
}
