package cli

// packverbhelp_test.go pins that a `yolo pack` verb answers `--help` and `-h` with the usage and
// does nothing else. The dispatcher read the verb alone and handed the rest to verbs that ignore
// it, so `yolo pack update --help` ran the update: every git pack re-fetched, every fork pin moved,
// every pack-declared program refreshed, and, on a host whose `host_management` is on, a
// `yolo host apply --assert` writing into the real home. `yolo pack init --help` scaffolded a pack
// in a directory named `--help`.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// packActsFixture is a host (not a jail) whose user config turns host management on, so a
// `yolo pack update` that ran would reach `yolo host apply --assert`, with both of update's acts
// replaced by counters. It returns the counters and the empty working directory `init` would
// scaffold into.
func packActsFixture(t *testing.T) (refreshes, applies *int, cwd string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[],"host_management":"assert"}`)
	cwd = t.TempDir()
	t.Chdir(cwd)
	refreshes, applies = new(int), new(int)
	origRefresh, origApply := programRefresh, hostApplyFromPackUpdate
	programRefresh = func(richtext.Printer, io.Writer) int { *refreshes++; return 0 }
	hostApplyFromPackUpdate = func([]string, io.Writer, io.Writer, bool, io.Reader) int { *applies++; return 0 }
	t.Cleanup(func() { programRefresh, hostApplyFromPackUpdate = origRefresh, origApply })
	return refreshes, applies, cwd
}

// Every verb, either spelling, anywhere after the verb: the usage on stdout, nothing on stderr,
// exit 0, and no act — no refresh, no host apply, no scaffold.
func TestAPackVerbsHelpFlagPrintsTheUsageAndRunsNothing(t *testing.T) {
	refreshes, applies, cwd := packActsFixture(t)
	verbs := []string{"init", "lint", "ls", "explain", "footprint", "install", "update", "status"}
	for _, verb := range verbs {
		for _, args := range [][]string{{verb, "--help"}, {verb, "-h"}, {verb, "x", "--help"}} {
			var out, errw bytes.Buffer
			rc := packMain(args, &out, &errw, false)
			if rc != 0 || out.String() != packUsage+"\n" || errw.Len() != 0 {
				t.Errorf("`yolo pack %s`: rc=%d, want 0 and the usage alone; stdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), rc, out.String(), errw.String())
			}
		}
	}
	if *refreshes != 0 || *applies != 0 {
		t.Errorf("a help flag ran the update: %d program refresh(es), %d host apply(s)", *refreshes, *applies)
	}
	if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
		t.Errorf("a help flag scaffolded something in the working directory (%v): %v", err, entries)
	}
}

// A verb that takes no argument refuses one it was given, rather than running its act over every
// configured pack as though the argument were not there: `yolo pack update claude` updated every
// pack. The refusal names the argument and where the usage is, and runs nothing.
func TestAPackVerbTakingNoArgumentRefusesOneAndRunsNothing(t *testing.T) {
	refreshes, applies, _ := packActsFixture(t)
	for _, verb := range []string{"ls", "install", "update", "status"} {
		var out, errw bytes.Buffer
		rc := packMain([]string{verb, "claude"}, &out, &errw, false)
		if rc != 2 || out.Len() != 0 {
			t.Errorf("`yolo pack %s claude`: rc=%d, want 2 and nothing on stdout:\n%s", verb, rc, out.String())
		}
		for _, want := range []string{"yolo pack " + verb + ":", `"claude"`, "takes no argument", "`yolo pack --help`"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("`yolo pack %s claude`'s refusal lacks %q:\n%s", verb, want, errw.String())
			}
		}
	}
	if *refreshes != 0 || *applies != 0 {
		t.Errorf("a refused argument ran the update: %d program refresh(es), %d host apply(s)", *refreshes, *applies)
	}
}

// The control: the bare verb still runs the act this file's fixture counts, so the two tests above
// fail if the counters stop counting.
func TestPackUpdateWithNoArgumentStillRunsBothActs(t *testing.T) {
	refreshes, applies, _ := packActsFixture(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"update"}, &out, &errw, false); rc != 0 {
		t.Fatalf("`yolo pack update` rc=%d: %s", rc, errw.String())
	}
	if *refreshes != 1 || *applies != 1 {
		t.Errorf("`yolo pack update` ran %d refresh(es) and %d host apply(s), want 1 and 1", *refreshes, *applies)
	}
}
