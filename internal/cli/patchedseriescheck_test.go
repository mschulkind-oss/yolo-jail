package cli

// patchedseriescheck_test.go pins `yolo pack series check` (patchedseriescheck.go;
// docs/design/patched-forks.md PF-D60) through the verb itself, against a real local upstream: in a
// jail, with no pack store or check record of the host's, it reaches the verdict `yolo pack status`
// reads on the host for the same series — the newest version that does not take it, the member and
// the paths that stop it, and the newest fit below — and it writes nothing in the pack, nothing in
// the pack store, and leaves no scratch copy behind.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// seriesVerb runs `yolo pack series args...` and returns its exit status and what it printed.
func seriesVerb(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := packMain(append([]string{"series"}, args...), &out, &errw, false)
	return rc, out.String(), errw.String()
}

// inAJail makes this test's process a jail's whose workspace is ws, with a temporary directory of
// its own, which it returns, so a test can see that a scratch copy is left behind nowhere.
func inAJail(t *testing.T, ws string) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "1.0.0")
	t.Setenv("YOLO_WORKSPACE", ws)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

// noScratchLeft fails when a scratch copy of the upstream is left in tmp.
func noScratchLeft(t *testing.T, tmp string) {
	t.Helper()
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "yolo-series-scratch-") {
			t.Errorf("a scratch copy of the upstream is left in %s: %s", tmp, e.Name())
		}
	}
}

// THE VERDICT `yolo pack status` READS: the host's update records the replays and its status reads
// them back; in a jail, with nothing of the host's, the check reaches the same one — v1.2.0 does not
// take 0001-ten.patch, which conflicts in f.txt, and v1.1.0 below it does — and names the rebase.
// It writes nothing in the pack nor the host's pack store, and moves no check record.
func TestSeriesCheckInAJailGivesTheVerdictPackStatusGivesOnTheHost(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	if rc, out, errw := packVerb(t, "update"); rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	_, status, _ := packVerb(t, "status")
	for _, w := range []string{"candidate: v1.2.0 (" + shortSHA(v12) + ")",
		"does not take 0001-ten.patch (conflicts in f.txt)", "below it: v1.1.0 (" + shortSHA(v11) + ")", ": applies"} {
		if !strings.Contains(status, w) {
			t.Fatalf("the host's status lacks %q:\n%s", w, status)
		}
	}
	seq := patchedRecord(t).Seq
	storeBefore, packBefore := treeDigest(t, paths.PacksDir()), treeDigest(t, f.forkDir)

	tmp := inAJail(t, f.packs)
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 {
		t.Fatalf("check rc=%d, want 1 for a newest version that does not take the series\n%s\n%s", rc, out, errw)
	}
	for _, w := range []string{
		"checking fork forkpack/tool's upstream git+file://" + f.repo + "?ref=main in a scratch copy",
		"fork forkpack/tool: upstream v1.2.0 (" + shortSHA(v12) + ") does not take the patch series —",
		"  0001-ten.patch conflicts in f.txt\n",
		"  rebase the series: yolo pack rebase forkpack/tool --pack " + shquote.QuoteDisplay(f.forkDir) + " --onto v1.2.0\n",
		"fork forkpack/tool: the newest fit, upstream v1.1.0 (" + shortSHA(v11) + "), takes the series (2 patches",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the check in a jail lacks %q:\n%s", w, out)
		}
	}
	if got := patchedRecord(t).Seq; got != seq {
		t.Errorf("the check in a jail moved the host's check record (seq %d after %d)", got, seq)
	}
	if treeDigest(t, paths.PacksDir()) != storeBefore {
		t.Error("the check in a jail wrote into the host's pack store")
	}
	if treeDigest(t, f.forkDir) != packBefore {
		t.Error("the check wrote into the pack")
	}
	noScratchLeft(t, tmp)
}

// A SERIES THE NEWEST VERSION TAKES: exit 0, said with what a launch then does, which no record
// here can narrow — and on a host whose pack store has never been made, it makes none.
func TestSeriesCheckOfASeriesTheNewestVersionTakes(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 0 {
		t.Fatalf("check rc=%d\n%s\n%s", rc, out, errw)
	}
	if !strings.Contains(out, "fork forkpack/tool: upstream v1.1.0 ("+shortSHA(v11)+") takes the series (2 patches, "+
		"series ") || !strings.Contains(out, "applies — "+scratchBuilds) {
		t.Errorf("the check does not say v1.1.0 takes the series:\n%s", out)
	}
	if strings.Contains(out, "rebase the series") {
		t.Errorf("a clean check names a rebase:\n%s", out)
	}
	if _, err := os.Stat(paths.PacksDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the check made the pack store %s (%v)", paths.PacksDir(), err)
	}
	// From the pack's own directory, the default.
	t.Chdir(f.forkDir)
	if rc, out, errw := seriesVerb(t, "check"); rc != 0 || !strings.Contains(out, "takes the series") {
		t.Errorf("check in the pack's directory: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// --onto CHECKS ONE COMMIT, whatever the follow rule would take: v1.1.0 takes the series and v1.2.0
// does not, and its rebase step names v1.2.0.
func TestSeriesCheckOntoChecksThatVersionAlone(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	rc, out, errw := seriesVerb(t, "check", f.forkDir, "--onto", "v1.1.0")
	if rc != 0 || !strings.Contains(out, "upstream v1.1.0 ("+shortSHA(v11)+") takes the series") ||
		strings.Contains(out, "v1.2.0") || strings.Contains(out, scratchBuilds) {
		t.Errorf("--onto v1.1.0: rc=%d\n%s\n%s", rc, out, errw)
	}
	rc, out, errw = seriesVerb(t, "check", f.forkDir, "--onto="+v12)
	if rc != 1 || !strings.Contains(out, "upstream "+shortSHA(v12)+" does not take the patch series") ||
		!strings.Contains(out, "--onto "+v12) || strings.Contains(out, "no upstream version on the list") {
		t.Errorf("--onto v1.2.0's commit: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// NOTHING ON THE LIST TAKES IT: every version conflicts, said with what a launch then runs — with
// no good build, the series' base, which the followed branch contains.
func TestSeriesCheckWithNoFitSaysWhatALaunchRuns(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	f.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(out, "no upstream version on the list takes the series — a launch keeps its "+
		"good build running; with none, it builds the series' base "+shortSHA(f.base)) {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
}

// A SERIES THAT CANNOT BE READ is said with the series reader's own fix, and nothing is fetched.
func TestSeriesCheckOfAnUnreadableSeriesNamesTheFix(t *testing.T) {
	f := newPatchedFixture(t, "")
	if err := os.RemoveAll(filepath.Join(f.forkDir, "patches")); err != nil {
		t.Fatal(err)
	}
	tmp := inAJail(t, f.packs)
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(out, "⚠ fork forkpack/tool: patch series patches") || strings.Contains(out, "checking") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("a series that cannot be read made %v", ents)
	}
}

// AN UPSTREAM THAT CANNOT BE FETCHED: a scratch copy starts empty, so nothing is checked, and the
// line names running the check again.
func TestSeriesCheckOfAnUpstreamItCannotFetchSaysSo(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.repo = filepath.Join(t.TempDir(), "gone")
	f.writeManifest(t, "main", "")
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(errw, "could not fetch git+file://"+f.repo+"?ref=main into a scratch copy") ||
		!strings.Contains(errw, "then run `yolo pack series check "+shquote.QuoteDisplay(f.forkDir)+"` again") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
}

// THE COMMAND LINE: a verb is required, one directory at most, a known flag, and a pack that
// declares a series.
func TestSeriesCheckRefusesWhatItCannotCheck(t *testing.T) {
	f := newPatchedFixture(t, "")
	plain := filepath.Join(t.TempDir(), "plain")
	writeFile(t, filepath.Join(plain, "pack.json"), `{"name":"plain","contributes":[]}`)
	bad := filepath.Join(t.TempDir(), "bad")
	writeFile(t, filepath.Join(bad, "pack.json"), `{"name":"bad","contributes":[{"kind":"no-such-kind"}]}`)
	for _, tc := range []struct {
		args []string
		rc   int
		want string
	}{
		{nil, 2, "yolo pack series: name what to do — `yolo pack series check [<pack-dir>] [--onto <ref>]`"},
		{[]string{"bogus"}, 2, `yolo pack series: unknown verb "bogus"`},
		{[]string{"check", f.forkDir, "other"}, 2, `unexpected argument "other" — it checks one pack directory`},
		{[]string{"check", "--bogus"}, 2, `unknown flag "--bogus"`},
		{[]string{"check", f.forkDir, "--onto"}, 2, "--onto needs a value"},
		{[]string{"check", plain}, 1, "the pack in " + plain + " declares no patch series"},
		{[]string{"check", filepath.Join(plain, "missing")}, 1, "is not a directory — name the directory holding the pack's pack.json"},
		{[]string{"check", bad}, 1, "— `yolo pack lint " + bad + "` names every problem"},
	} {
		rc, out, errw := seriesVerb(t, tc.args...)
		if rc != tc.rc || !strings.Contains(errw, tc.want) {
			t.Errorf("series %q: rc=%d, want %d and %q\n%s\n%s", tc.args, rc, tc.rc, tc.want, out, errw)
		}
	}
}

// A JAIL'S CHECK OF A PACK OUTSIDE ITS WORKSPACE names the rebase on the host, which is where that
// pack's series can be exported, since the scratch rebase there would refuse it.
func TestSeriesCheckInAJailOfAPackOutsideItsWorkspaceNamesTheHostsRebase(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	inAJail(t, t.TempDir())
	_, out, _ := seriesVerb(t, "check", f.forkDir)
	if !strings.Contains(out, "  rebase the series on the host: yolo pack rebase forkpack/tool --onto v1.2.0\n") {
		t.Errorf("the check does not name the host's rebase:\n%s", out)
	}
}

// THE SCRATCH COPY IS REFUSED INSIDE THE PACK, where a TMPDIR inside it would put it.
func TestSeriesCheckRefusesAScratchCopyInsideThePack(t *testing.T) {
	f := newPatchedFixture(t, "")
	tmp := filepath.Join(f.forkDir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(errw, "would be inside the pack's own directory") || !strings.Contains(errw, "set TMPDIR") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("the refused scratch copy is left in the pack: %v", ents)
	}
}

// A GIT TOO OLD FOR THE REPLAY names updating git as the step (PF-D58), not a retry that meets the
// same git.
func TestSeriesCheckUnderAnOldGitNamesUpdatingGit(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"git version 2.39.5\"; exit 0; fi\n"+
		"exec "+shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := patchedScratchStore
	patchedScratchStore = func(dir string) *packsrc.Store { s := prev(dir); s.Git = filepath.Join(bin, "git"); return s }
	t.Cleanup(func() { patchedScratchStore = prev })
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 1 || !strings.Contains(out, "needs git 2.40 or newer (`git merge-tree --merge-base`), and this git is 2.39.5 — "+
		"update git, then run `yolo pack series check "+shquote.QuoteDisplay(f.forkDir)+"` again") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
}

// A CTRL-C ends the check's git, and the scratch copy is removed.
func TestAnInterruptedSeriesCheckRemovesItsScratchCopy(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prev := rebaseInterrupt
	rebaseInterrupt = func() (context.Context, func()) { return ctx, cancel }
	t.Cleanup(func() { rebaseInterrupt = prev })
	rc, out, errw := seriesVerb(t, "check", f.forkDir)
	if rc != 130 || !strings.Contains(errw, "interrupted — the scratch copy of the upstream is removed") {
		t.Errorf("rc=%d, want 130 and the interruption said\n%s\n%s", rc, out, errw)
	}
	noScratchLeft(t, tmp)
}
