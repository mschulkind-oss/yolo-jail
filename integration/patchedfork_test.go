package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// patchedfork_test.go is the container-level cell for a PATCHED fork
// (docs/design/patched-forks.md §14 step 2's "done looks like"): a fork pack that names its
// upstream and carries a `git format-patch --base` series, delivered by fresh launches with nobody
// pinning anything.
//
//  1. The first launch checks the upstream, replays the series onto the newest version it fits,
//     builds that in a sealed capture jail and runs the patched program.
//  2. A new upstream version, checked now by `yolo pack update` (a launch checks hourly), is built
//     by the next launch — as a child build jail, since a good build serves — and the jail runs it.
//  3. A version the series does not fit is held: the next launch's jail runs the previous build,
//     and its fork line names the member that stopped it.
//
// HERMETIC, like forkbuild_test.go: the upstream is a local git repository (git+file://), and its
// build writes a script naming the upstream version and the line the series patches. ⚠ THE PACK
// AND CAPTURE STORES ARE SHARED with the machine (packHomeSharedStores), so the test removes the
// capture entries it added and its fork's check record.

const (
	patchFixtureBin      = "patchfixture"
	patchFixtureBasePack = "patchfixture-base"
	patchFixtureForkPack = "patchfixture-fork"
	patchFixtureBaseRan  = "PATCHFIXTURE_BASE_INSTALLER_RAN"
	patchFixtureMarker   = "PATCHFIXTURE"
)

// patchFixtureUpstream is the upstream: ver.txt names the version and f.txt is thirty numbered
// lines, the tenth of which the series patches.
type patchFixtureUpstream struct {
	t   *testing.T
	dir string
}

func (u patchFixtureUpstream) git(args ...string) string {
	u.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = u.dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		u.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// release commits ver.txt and f.txt with line 10 set to ten, and tags it when tag is set.
func (u patchFixtureUpstream) release(ver, ten, tag string) string {
	u.t.Helper()
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		if i == 10 {
			b.WriteString(ten + "\n")
			continue
		}
		b.WriteString(strings.Repeat("x", i) + "\n")
	}
	for name, body := range map[string]string{"ver.txt": ver + "\n", "f.txt": b.String()} {
		if err := os.WriteFile(filepath.Join(u.dir, name), []byte(body), 0o644); err != nil {
			u.t.Fatal(err)
		}
	}
	u.git("add", "-A")
	u.git("commit", "-qm", "release "+ver)
	if tag != "" {
		u.git("tag", tag)
	}
	return u.git("rev-parse", "HEAD")
}

func TestPatchedForkFollowsItsUpstreamAndHoldsAtAConflict(t *testing.T) {
	requireJail(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := patchFixtureUpstream{t: t, dir: t.TempDir()}
	up.git("init", "-q", "-b", "main")
	base := up.release("1.0.0", "ten", "v1.0.0")

	// THE SERIES: one member, exported from a branch of the upstream with its base named.
	fork := t.TempDir()
	up.git("checkout", "-q", "-b", "series")
	body, err := os.ReadFile(filepath.Join(up.dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up.dir, "f.txt"), []byte(strings.Replace(string(body), "ten\n", "patched\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	up.git("commit", "-qam", "patch line ten")
	up.git("format-patch", "-q", "--base="+base, "-o", filepath.Join(fork, "patches"), "main..series")
	up.git("checkout", "-q", "main")
	up.git("branch", "-q", "-D", "series")
	v11 := up.release("1.1.0", "ten", "v1.1.0")

	basePack := t.TempDir()
	if err := os.WriteFile(filepath.Join(basePack, "install.sh"), []byte("#!/bin/bash\n"+
		"mkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/bash\\necho "+patchFixtureBaseRan+"\\n' > "+
		"\"$HOME/.local/bin/"+patchFixtureBin+"\"\nchmod +x \"$HOME/.local/bin/"+patchFixtureBin+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest(basePack, `{"name":"`+patchFixtureBasePack+`","contributes":[{"kind":"program","bin":"`+
		patchFixtureBin+`","via":"installer","url":"file:///ctx/packs/`+patchFixtureBasePack+`/install.sh"}]}`)
	build := `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho ` + patchFixtureMarker + `_%s_%s\n' "$(cat ver.txt)" ` +
		`"$(head -n 10 f.txt | tail -n 1)" > "$HOME/.local/bin/` + patchFixtureBin + `" && chmod +x "$HOME/.local/bin/` +
		patchFixtureBin + `"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	writeManifest(fork, `{"name":"`+patchFixtureForkPack+`","contributes":[{"kind":"program","bin":"`+patchFixtureBin+
		`","via":"source","fork_of":"`+patchFixtureBasePack+`","source":"git+file://`+up.dir+`?ref=main",`+
		`"patches":"patches","build":"`+buildJSON+`","produces":[".local/bin/`+patchFixtureBin+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+basePack+`", "name": "`+patchFixtureBasePack+`"}, `+
		`{"source": "file://`+fork+`", "name": "`+patchFixtureForkPack+`"}]}`)
	withPrivateFixtureYoloStore(t)

	state := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	store := filepath.Join(state, "captures")
	before := captureEntryNames(t, store)
	owner := patchFixtureForkPack + "/" + patchFixtureBin
	record := (&packsrc.Store{Dir: filepath.Join(state, "packs")}).CheckRecordPath(owner)
	_ = os.Remove(record)
	t.Cleanup(func() {
		removeNewCaptureEntries(t, store, before, patchFixtureBin)
		_ = os.Remove(record)
	})
	userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")

	var lastWorkspace string
	launch := func(what string) string {
		t.Helper()
		lastWorkspace = t.TempDir()
		r := runCommand(t, lastWorkspace, append(jailRunArgs(), "--", patchFixtureBin), withHostSemantics())
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s: rc %d\n%s", what, r.rc, out)
		}
		if strings.Contains(out, patchFixtureBaseRan) {
			t.Fatalf("%s ran the BASE's program under the fork's name:\n%s", what, out)
		}
		return out
	}
	// jailGone waits for the last launch's jail to be known gone: its keeper removes the jail's pack
	// tree, and the delivery record beside it, once the container is (docs/design/patched-forks.md
	// §6.3). Until then a move keeps the build that jail was handed.
	jailGone := func() {
		t.Helper()
		root := filepath.Join(state, "agents", naming.FromWorkspace(lastWorkspace), "pack-trees")
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if left, _ := filepath.Glob(filepath.Join(root, "*.forks.json")); len(left) == 0 {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("the last launch's jail left its delivery record in %s for 90s after it exited", root)
	}
	runs := func(ver string) string { return patchFixtureMarker + "_" + ver + "_patched" }

	// 1. THE FIRST LAUNCH: no pin, no `yolo pack install` — the newest version the series fits,
	// built and run with the series applied.
	out := launch("the first launch")
	if !strings.Contains(out, runs("1.1.0")) {
		t.Fatalf("the first launch does not run v1.1.0 with the series applied:\n%s", out)
	}
	if !strings.Contains(out, "built fork "+owner+": v1.1.0 ("+v11[:8]+") + 1 patch; this jail runs it") {
		t.Errorf("the first launch does not say what it built:\n%s", out)
	}
	if live := liveCaptureEntries(t, store, newCaptureEntries(t, store, before, patchFixtureBin)); len(live) != 1 {
		t.Errorf("the first launch added %d live entries, want 1: %v", len(live), live)
	}
	if data, err := os.ReadFile(packsrc.ForkLockPath(userConfig)); err == nil && strings.Contains(string(data), owner) {
		t.Errorf("a patched fork was pinned in the fork lock:\n%s", data)
	}

	// 2. A NEW VERSION: checked now by `yolo pack update`, built by the next launch, and the build it
	// replaced reaped (no running jail holds it).
	jailGone()
	v12 := up.release("1.2.0", "ten", "v1.2.0")
	if r := runCommand(t, t.TempDir(), []string{"pack", "update"}, withHostSemantics()); !strings.Contains(r.combined(), "takes the series") {
		t.Fatalf("yolo pack update did not replay the series at v1.2.0:\n%s", r.combined())
	}
	out = launch("the launch after v1.2.0")
	if !strings.Contains(out, runs("1.2.0")) {
		t.Fatalf("the launch after v1.2.0 does not run it:\n%s", out)
	}
	if !strings.Contains(out, "updated fork "+owner+": v1.1.0 ("+v11[:8]+") → v1.2.0 ("+v12[:8]+"), 1 patch") {
		t.Errorf("the moving launch does not disclose the move:\n%s", out)
	}
	// A reaped entry keeps its metadata and loses its completion marker and its tree (capture/gc.go).
	if live := liveCaptureEntries(t, store, newCaptureEntries(t, store, before, patchFixtureBin)); len(live) != 1 {
		t.Errorf("after the move %d new entries are live, want the good build's alone: %v\n%s", len(live), live, out)
	}

	// 3. A VERSION THE SERIES DOES NOT FIT is held: the next launch's jail runs v1.2.0's build, and
	// the fork's line names what stopped v1.3.0.
	// Since PF-D81 (docs/design/patched-forks.md §8) the update FAILS on the conflict, naming the
	// version, the patch, its rebase onto the conflicting commit and the bypass, and builds no older
	// fit or base.
	v13 := up.release("1.3.0", "upstream-ten", "v1.3.0")
	if r := runCommand(t, t.TempDir(), []string{"pack", "update"}, withHostSemantics()); r.rc == 0 ||
		!strings.Contains(r.combined(), patchFailureBlock(owner, "v1.3.0", v13, "f.txt")) {
		t.Fatalf("yolo pack update did not fail on the conflict at v1.3.0: rc %d\n%s", r.rc, r.combined())
	}
	out = launch("the launch after v1.3.0")
	if !strings.Contains(out, runs("1.2.0")) || strings.Contains(out, patchFixtureMarker+"_1.3.0") {
		t.Fatalf("the held launch does not run the previous build:\n%s", out)
	}
	if !strings.Contains(out, "held at v1.2.0 ("+v12[:8]+"): upstream v1.3.0 ("+v13[:8]+") does not take 0001-patch-line-ten.patch") {
		t.Errorf("the held launch's fork line does not name what holds it:\n%s", out)
	}
}

// patchFailureBlock is PF-D81's error for a conflict of the fixtures' one patch at upstream tag
// (commit), from its ERROR line through its Bypass line (internal/cli/patchfailure.go).
func patchFailureBlock(owner, tag, commit, paths string) string {
	return "ERROR: " + owner + ": patch application failed at upstream " + tag + " (" + commit + ")\n" +
		"  Patch: 0001-patch-line-ten.patch\n" +
		"  Conflict: " + paths + "\n" +
		"  Operation stopped; no older fit or base will be built.\n" +
		"  Repair: yolo pack rebase " + owner + " --onto " + commit + "\n" +
		"  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo\n"
}

// liveCaptureEntries is the keys among keys whose entry is complete: its marker is there.
func liveCaptureEntries(t *testing.T, store string, keys []string) []string {
	t.Helper()
	var live []string
	for _, k := range keys {
		if _, err := os.Stat(filepath.Join(store, "entries", k, ".yolo-capture-complete")); err == nil {
			live = append(live, k)
		}
	}
	return live
}
