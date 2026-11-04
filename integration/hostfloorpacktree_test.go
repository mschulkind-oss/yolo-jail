package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloorpacktree_test.go is the REAL-HOST tier of a PACK-TREE PROGRAM on the host floor
// (docs/design/pack-buildable-programs.md §9's `yolo host`, Linux row; the plan's Task 9): `yolo host
// -- <bin>` on the machine the suite runs on builds the program from its pack's own tree in a real
// sealed build jail, materializes it into yolo's floor, follows an edit to the tree once, builds
// nothing when the tree has not changed, builds a dev override's checkout in its place, and refuses a
// launch whose build fails, naming the build's log and the retry.
//
// HERMETIC: packtreeprogram_test.go's fixture shape — a `file://` pack whose tools/x holds a VERSION
// file, and a build line writing a script that prints it — so no network is reached. A private floor
// and capture store (privateStateEntries) keep the run's shared ones untouched.
//
// ⚠ IN A NESTED JAIL this runs the build jail podman-in-podman, which verifies the floor's logic and
// the sealed build; it is NOT the real-host check the plan names for Task 9 (a user's rootless podman,
// their real home and its floor), which a human runs on a Linux host.

const hostPackTreeBin = "hostpacktreefx-x"

func TestHostFloorBuildsAPackTreeProgramFromItsTree(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" {
		t.Skip("a Mac's `yolo host` does not build a pack's own tree yet (the plan's Task 10)")
	}
	pack := t.TempDir()
	src := filepath.Join(pack, "tools", "x")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	setVersion := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest := func(build string) {
		t.Helper()
		buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
		if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"hostpacktreefx","contributes":[`+
			`{"kind":"program","bin":"`+hostPackTreeBin+`","via":"pack","from":"tools/x","build":"`+buildJSON+
			`","run":"x"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setVersion("1")
	writeManifest(`printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`)
	packEntry := `"packs": [{"source": "file://` + pack + `", "name": "hostpacktreefx"}], "host_management": "own"`
	packHome(t, `{`+packEntry+`}`)
	home := os.Getenv("HOME")
	floor := paths.HostFloorDirUnder(home)
	store := paths.CapturesDirUnder(home)
	privateStateEntries(t, home, filepath.Base(floor), filepath.Base(store))
	dir := t.TempDir()

	launch := func(what string, args ...string) result {
		t.Helper()
		r := runCommand(t, dir, append([]string{"host", "--"}, args...), hostLaunchEnv())
		t.Logf("%s: rc %d\nstdout:\n%s\nstderr:\n%s", what, r.rc, r.stdout, r.stderr)
		return r
	}

	// 1. THE FIRST LAUNCH BUILDS IT in a sealed jail and runs the floor's copy of the built tree.
	r := launch("the first launch", hostPackTreeBin)
	if r.rc != 0 || strings.TrimSpace(r.stdout) != "x-1" ||
		!strings.Contains(r.stderr, "no build of "+hostPackTreeBin+" from pack hostpacktreefx's tools/x") ||
		!strings.Contains(r.stderr, "starting "+hostPackTreeBin+" (yolo's floor copy") {
		t.Fatalf("the first launch did not build and run the floor's copy (want x-1)")
	}
	if rec := readFloorRecord(t, floor, hostPackTreeBin); rec.Via != "pack" || !strings.HasPrefix(rec.Version, "tree ") {
		t.Errorf("the floor's record is %+v, want a pack-tree record keyed by its tree digest", rec)
	}

	// 2. AN EDIT TO THE TREE rebuilds once and serves the new bytes.
	setVersion("2")
	if r = launch("the launch after the edit", hostPackTreeBin); r.rc != 0 || strings.TrimSpace(r.stdout) != "x-2" ||
		!strings.Contains(r.stderr, "changed") {
		t.Fatalf("the launch after the edit did not rebuild and serve x-2")
	}

	// 3. NO CHANGE: nothing built or installed.
	if r = launch("the launch with no change", hostPackTreeBin); r.rc != 0 || strings.TrimSpace(r.stdout) != "x-2" ||
		strings.Contains(r.stderr, "no build of") || strings.Contains(r.stderr, "installing "+hostPackTreeBin) {
		t.Errorf("the launch with no change built or installed again")
	}

	// 4. A DEV OVERRIDE builds the checkout in place of the pack's tree, with the pack's own build
	// line, says so, and builds an edit there at the next launch (OQ-BG3's ruling).
	dev, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setDev := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dev, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setDev("dev1")
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.WriteFile(cfg, []byte(`{`+packEntry+`, "programs": {"overrides": {"`+hostPackTreeBin+
		`": {"path": "`+dev+`"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	named := hostPackTreeBin + ": built from your checkout " + dev
	if r = launch("the launch with an override", hostPackTreeBin); r.rc != 0 || strings.TrimSpace(r.stdout) != "x-dev1" ||
		!strings.Contains(r.stderr, named) {
		t.Fatalf("the launch with an override did not build the checkout and say so (want x-dev1 and %q)", named)
	}
	if r = launch("the launch with an unchanged checkout", hostPackTreeBin); r.rc != 0 || strings.TrimSpace(r.stdout) != "x-dev1" ||
		strings.Contains(r.stderr, "no build of") {
		t.Errorf("the launch with an unchanged checkout built again")
	}
	setDev("dev2")
	if r = launch("the launch after an edit in the checkout", hostPackTreeBin); r.rc != 0 || strings.TrimSpace(r.stdout) != "x-dev2" {
		t.Errorf("the override did not build the checkout's edit at the next launch")
	}

	// 5. A BROKEN BUILD refuses a launch of another command through the readiness act, naming the
	// program, the build's log and the retry; the bypass starts it.
	if err := os.WriteFile(cfg, []byte(`{`+packEntry+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest("exit 3")
	if r = runCommand(t, dir, []string{"host", "--", "true"}, hostLaunchEnv(paths.AllowMissingProgramsEnv+"=")); r.rc == 0 {
		t.Fatalf("a launch with a broken pack-tree build started:\n%s", r.stderr)
	}
	for _, want := range []string{"program " + hostPackTreeBin + " (pack hostpacktreefx)", "its log: ",
		"yolo pack build hostpacktreefx --setup host-self", paths.AllowMissingProgramsEnv + "=1"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
		}
	}
	r = runCommand(t, dir, []string{"host", "--", "true"}, hostLaunchEnv(paths.AllowMissingProgramsEnv+"=1"))
	if r.rc != 0 {
		t.Errorf("with the bypass the launch did not start: rc %d\n%s", r.rc, r.stderr)
	}
}
