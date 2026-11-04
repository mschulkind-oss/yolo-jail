package cli

// packtreestaging_test.go pins what a PACK-TREE PROGRAM's build reads against the tree a launch
// stages (docs/design/pack-buildable-programs.md §6.1): a `from: "."` source, which holds the
// staged pack's root and so a fetched pack's store marker; `yolo pack build`, which must key the
// build a launch looks up for a tree that staging changes; and a `run` that is a symlink, which
// must resolve inside the program's directory (§3).

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// packTreeStagingHome is a temp HOME for a build act run in this package, with config written
// from packs (a raw JSON list body).
func packTreeStagingHome(t *testing.T, packs string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("YOLO_SETUP", "podman-self")
	t.Chdir(floortest.ResolvedTemp(t))
	previousProbe := probeForkBuildContainer
	probeForkBuildContainer = func(string, string, time.Duration) (bool, bool) { return false, true }
	t.Cleanup(func() { probeForkBuildContainer = previousProbe })
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+packs+`]}`)
}

// launchStagedPack stages the configured entry named name exactly as a launch stages it
// (run.Options.stagePackEntry: config.ResolvePack into a destination of its own) and returns the
// staged pack.
func launchStagedPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("LoadPackEntries: %v %v", err, problems)
	}
	for _, e := range entries {
		if e.Name != name {
			continue
		}
		res, err := config.ResolvePack(e, config.ResolvePackSpec{Dest: filepath.Join(t.TempDir(), e.Slug())})
		if err != nil || res.Pack == nil || len(res.Problems) > 0 {
			t.Fatalf("staging %s: %v %v", name, err, res.Problems)
		}
		return res.Pack
	}
	t.Fatalf("no configured pack %s", name)
	return nil
}

// FROM ".", LOCAL AND FETCHED: the program's source is the staged pack's whole root. A fetched
// pack's checkout keeps the store's completion marker there, and the launch's staging copies it, so
// the digest and the build's copy of the source must agree about it — otherwise every build of a
// fetched `from: "."` program is refused as a source that "changed while it was staged". Red
// before the marker was left out of the digest as it was of the copy.
func TestAFromRootPackTreeProgramBuildsFromALaunchsStagedTree(t *testing.T) {
	manifest := `{"name":"%s","contributes":[{"kind":"program","bin":"x","via":"pack","from":".","run":"x"}]}`
	for _, tc := range []struct {
		name, packs string
		fetched     bool
	}{
		{name: "local"},
		{name: "fetched", fetched: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"pack.json": strings.Replace(manifest, "%s", "fx", 1),
				"x":         "#!/bin/sh\necho root\n",
			}
			var src string
			if tc.fetched {
				repo := gitPackRepoWith(t, files)
				src = "git+file://" + repo + "?ref=main"
			} else {
				dir := floortest.ResolvedTemp(t)
				for rel, body := range files {
					writeFile(t, filepath.Join(dir, rel), body)
				}
				src = "file://" + dir
			}
			packTreeStagingHome(t, `{"source":"`+src+`","name":"fx"}`)
			if tc.fetched {
				installGitPack(t)
			}
			p := launchStagedPack(t, "fx")
			_, markerErr := os.Stat(filepath.Join(p.Root, packsrc.TreeCompleteMarker))
			if tc.fetched && markerErr != nil {
				t.Fatalf("the fixture is not the case: the staged fetched pack holds no %s", packsrc.TreeCompleteMarker)
			}
			f := packload.PackTreePrograms([]*packload.Pack{p})[0]
			digest, err := f.Digest()
			if err != nil || digest == "" {
				t.Fatalf("digest: %q %v", digest, err)
			}
			withFakeCaptureJail(t, func(run.Options) int {
				t.Error("a pack-tree program with no build line started a build jail")
				return 1
			})
			entry, err := buildFork(forkBuild{Fork: f, Commit: digest, Platform: "linux/amd64"},
				buildMode{lock: pidlock.NoWait}, io.Discard, io.Discard, false)
			if err != nil || entry == nil {
				t.Fatalf("buildFork of a from: \".\" program = %v, %v", entry, err)
			}
			got := manifestPaths(t, entry.Root)
			for _, p := range got {
				if strings.HasSuffix(p, packsrc.TreeCompleteMarker) {
					t.Errorf("the copy carries the pack store's marker: %s", p)
				}
			}
			if !containsString(got, ".local/lib/yolo-programs/x/pack.json") {
				t.Errorf("the copy is not the pack's root: %v", got)
			}
		})
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ONE KEY FOR `yolo pack build` AND A LAUNCH, for a tree staging changes: an empty directory, a
// symlink (a local pack's is followed into a file), a file with a partial exec bit (0744) and a
// nested `.git`. The verb builds; the store must then hold the build under the digest of the copy
// a launch stages, which is the key the launch looks up. Red when the verb digests the pack in
// place: it builds a key no launch reads, and the next launch builds again.
func TestPackBuildKeysTheBuildALaunchOfAStagedTreeLooksUp(t *testing.T) {
	fx := packBuildHome(t, packBuildFixtureBuild, "")
	src := filepath.Join(fx, "tools", "x")
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("VERSION", filepath.Join(src, "VERSION.link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "script.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(src, "script.sh"), 0o744); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "sub", ".git", "HEAD"), "ref: refs/heads/main\n")
	runs := 0
	withFakeCaptureJail(t, fakePackBuildJail(t, &runs, 0))

	rc, out, errw := packBuildRun(t, "fx")
	if rc != 0 || runs != 1 || !strings.Contains(out, "built") {
		t.Fatalf("yolo pack build: rc %d, %d jail runs\nout:\n%s\nerr:\n%s", rc, runs, out, errw)
	}

	staged := packload.PackTreePrograms([]*packload.Pack{launchStagedPack(t, "fx")})[0]
	digest, err := staged.Digest()
	if err != nil {
		t.Fatal(err)
	}
	inPlace, err := packload.PackTreeDigest(src)
	if err != nil {
		t.Fatal(err)
	}
	if inPlace == digest {
		t.Fatalf("the fixture is not the case: staging left the tree's digest unchanged (%s)", digest)
	}
	b := forkBuild{Fork: staged, Commit: digest, Platform: forkBuildPlatform("")}
	store := &capture.Store{Dir: paths.CapturesDir()}
	if _, _, err := resolveForkBuild(store, "x", b.Platform, b.selectSource(), digest, b.recipe()); err != nil {
		t.Errorf("the launch's key (the staged tree's digest %s) finds no build yolo pack build made: %v\nout:\n%s",
			shortSHA(digest), err, out)
	}
	if !strings.Contains(out, shortSHA(digest)) {
		t.Errorf("yolo pack build's line does not name the staged tree's digest %s:\n%s", shortSHA(digest), out)
	}
}

// A `run` THAT IS A SYMLINK MUST RESOLVE INSIDE THE PROGRAM'S DIRECTORY (§3), in a build's output
// and in the no-build copy: one pointing outside is refused, naming the field and the fix, and the
// copy never sets an exec bit through it on a file outside the copy. A link inside is accepted.
func TestAPackTreeRunSymlinkMustResolveInsideTheProgramDirectory(t *testing.T) {
	const dir = ".local/lib/yolo-programs/x"
	entries := func(target string) map[string]capture.ManifestEntry {
		return map[string]capture.ManifestEntry{
			dir:                {Path: dir, Kind: capture.KindDir},
			dir + "/real":      {Path: dir + "/real", Kind: capture.KindFile},
			dir + "/x":         {Path: dir + "/x", Kind: capture.KindSymlink, Target: target},
			".local/bin/other": {Path: ".local/bin/other", Kind: capture.KindFile},
		}
	}
	for target, ok := range map[string]bool{
		"real":                         true,
		"/home/agent/" + dir + "/real": true,
		"../../../bin/other":           false,
		"/home/agent/.local/bin/other": false,
		"/etc/passwd":                  false,
		"missing":                      false,
	} {
		why := packTreeRunLinkProblem(entries(target), "/home/agent", dir, dir+"/x")
		if ok != (why == "") {
			t.Errorf("run -> %s: problem %q, want accepted=%v", target, why, ok)
		}
	}
	m := &capture.Manifest{Home: "/home/agent"}
	for _, e := range entries("/etc/passwd") {
		m.Entries = append(m.Entries, e)
	}
	m.Entries = append(m.Entries, capture.ManifestEntry{Path: ".local/bin/x", Kind: capture.KindSymlink,
		Target: packdecl.PackTreeLinkTarget("x", "x")})
	f := packload.Fork{Pack: "fx", Bin: "x", From: "tools/x", Run: "x"}
	if why := packTreeAdmitProblem(m, f, containerWorkspace); !strings.Contains(why, "`run`") ||
		!strings.Contains(why, "outside") || !strings.Contains(why, "point `run` at the file itself") {
		t.Errorf("a build's run linking outside its directory: %q, want a refusal naming `run` and the fix", why)
	}

	// The no-build copy: an absolute link out of a staged tree (a pack read in place could carry
	// one) is refused before any chmod, and the outside file keeps its mode.
	f2, _ := packTreeBuildFixture(t, "")
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, outside, "#!/bin/sh\n")
	if err := os.Chmod(outside, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f2.SourceDir(), "x")); err != nil {
		t.Fatal(err)
	}
	digest, err := f2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	withFakeCaptureJail(t, func(run.Options) int { return 1 })
	_, err = buildFork(forkBuild{Fork: f2, Commit: digest, Platform: "linux/amd64"}, buildMode{lock: pidlock.NoWait},
		io.Discard, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "`run`") || !strings.Contains(err.Error(), "outside") {
		t.Errorf("the copy of a run linking outside: %v, want the refusal", err)
	}
	if fi, err := os.Stat(outside); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("the file outside the copy changed mode: %v %v", fi, err)
	}
}
