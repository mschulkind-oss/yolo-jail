package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// forkbuild_test.go is the container-level cell for the fork route
// (docs/design/forked-programs-as-packs.md, the plan's step 6): a fork pinned by its first launch
// (FP-D18 — no `yolo pack install`), built by that launch in a sealed capture jail of its own, and
// delivered into the jail in place of its base's program.
//
// HERMETIC, like capture_test.go: the fork's "remote" is a local git repository (git+file://), its
// build writes a marker script, and the base's installer is the pack's own file. No real fork, no
// network. ⚠ THE CAPTURE STORE IS THE DEVELOPER'S OWN here (capture_test.go says why), so the test
// removes only the entries it added.

const (
	forkFixtureBin      = "forkfixture"
	forkFixtureBasePack = "forkfixture-base"
	forkFixtureForkPack = "forkfixture-fork"
	forkFixtureBaseRan  = "FORKFIXTURE_BASE_INSTALLER_RAN"
	forkFixtureMarker   = "FORKFIXTURE_FORK_BUILD"
)

// forkFixtureRepo is a local git repository on main whose rev.txt holds rev, with a commit func
// that moves it.
func forkFixtureRepo(t *testing.T) (dir string, commit func(rev string)) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Without git's own state variables: a hook-exported GIT_DIR would point this helper at
		// the repository running the suite (packsrc.CleanGitEnv's reason).
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	commit = func(rev string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "rev.txt"), []byte(rev+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-qm", "rev "+rev)
	}
	commit("1")
	return dir, commit
}

func TestForkBuildDeliversTheForkInPlaceOfItsBase(t *testing.T) {
	requireJail(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, commit := forkFixtureRepo(t)

	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "install.sh"), []byte("#!/bin/bash\n"+
		"mkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/bash\\necho "+forkFixtureBaseRan+"\\n' > "+
		"\"$HOME/.local/bin/"+forkFixtureBin+"\"\nchmod +x \"$HOME/.local/bin/"+forkFixtureBin+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest(base, `{"name":"`+forkFixtureBasePack+`","contributes":[{"kind":"program","bin":"`+
		forkFixtureBin+`","via":"installer","url":"file:///ctx/packs/`+forkFixtureBasePack+`/install.sh"}]}`)
	fork := t.TempDir()
	build := `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho ` + forkFixtureMarker +
		`_%s\n' "$(cat rev.txt)" > "$HOME/.local/bin/` + forkFixtureBin + `" && chmod +x "$HOME/.local/bin/` + forkFixtureBin + `"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	writeManifest(fork, `{"name":"`+forkFixtureForkPack+`","contributes":[{"kind":"program","bin":"`+forkFixtureBin+
		`","via":"source","fork_of":"`+forkFixtureBasePack+`","source":"git+file://`+repo+`?ref=main",`+
		`"build":"`+buildJSON+`","produces":[".local/bin/`+forkFixtureBin+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+base+`", "name": "`+forkFixtureBasePack+`"}, `+
		`{"source": "file://`+fork+`", "name": "`+forkFixtureForkPack+`"}]}`)

	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before) })

	// THE PIN IS THE FIRST LAUNCH'S (FP-D18, applying OQ-PF1): no `yolo pack install` runs here.
	launch := func(what string) string {
		t.Helper()
		r := runYoloDirect(t, t.TempDir(), forkFixtureBin)
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s: rc %d\n%s", what, r.rc, out)
		}
		if strings.Contains(out, forkFixtureBaseRan) {
			t.Fatalf("%s ran the BASE's program under the fork's name:\n%s", what, out)
		}
		return out
	}

	// FIRST LAUNCH: the pin, one build, and the jail runs the fork's build of rev 1.
	out := launch("the first launch")
	if !strings.Contains(out, "pinned fork "+forkFixtureForkPack+"/"+forkFixtureBin+" at ") {
		t.Fatalf("the first launch did not pin the fork:\n%s", out)
	}
	if !strings.Contains(out, forkFixtureMarker+"_1") || !strings.Contains(out, "fork builds") {
		t.Fatalf("the first launch did not build and run the fork:\n%s", out)
	}
	if added := newCaptureEntries(t, store, before); len(added) != 1 {
		t.Fatalf("the first launch added %d entries, want 1: %v", len(added), added)
	}

	// SECOND LAUNCH, after the branch moved: no pin, no build — the standing pin never moves at
	// launch, and a hit builds nothing.
	commit("1b")
	out = launch("the second launch")
	if !strings.Contains(out, forkFixtureMarker+"_1") || strings.Contains(out, "fork builds") ||
		strings.Contains(out, "pinned fork") {
		t.Errorf("the second launch moved the pin, rebuilt, or did not run the fork:\n%s", out)
	}

	// A NEW COMMIT, AND `yolo pack update` MOVES THE PIN: the next launch builds a new entry.
	commit("2")
	if r := runYoloCLI(t, t.TempDir(), "pack", "update"); !strings.Contains(r.combined(), forkFixtureForkPack) {
		t.Fatalf("yolo pack update did not re-resolve the fork:\n%s", r.combined())
	}
	out = launch("the launch after the pin moved")
	if !strings.Contains(out, forkFixtureMarker+"_2") {
		t.Errorf("the launch after the pin moved does not run rev 2:\n%s", out)
	}
	if added := newCaptureEntries(t, store, before); len(added) != 2 {
		t.Errorf("after the pin moved there are %d new entries, want 2: %v", len(added), added)
	}

	// DELETING THE STORE'S ENTRY COSTS A REBUILD AND NOTHING ELSE.
	removeNewCaptureEntries(t, store, before)
	out = launch("the launch after the entry was removed")
	if !strings.Contains(out, "fork builds") || !strings.Contains(out, forkFixtureMarker+"_2") {
		t.Errorf("the launch after the entry was removed did not rebuild rev 2:\n%s", out)
	}
}
