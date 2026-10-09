package cli

// patchedrebase_export_test.go pins what keeps `yolo pack rebase`'s printed export from replacing
// the user's series with a partial or an empty one (docs/design/patched-forks.md §8.4: "never an
// empty or a partial series"), by running the printed lines as a person pasting the block would:
// mid-rebase, after a format-patch that failed, after an aborted rebase, and on a clone a run left
// before its replay ended. The series is the user's source of truth; each case asserts it is
// byte-identical afterwards.

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// pasteBlock runs the printed lines as ONE pasted block: each line in turn, a failing one not
// stopping the next, as an interactive shell runs a paste. It returns what the block printed.
func pasteBlock(t *testing.T, cmds []string, env ...string) string {
	t.Helper()
	cmd := printedCmd(strings.Join(cmds, "\n"))
	cmd.Env = append(cmd.Env, env...)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// resolveFirstStop resolves the rebase's first stop (the first member onto v1.2.0) and stages it,
// leaving the continue to run.
func resolveFirstStop(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "f.txt"), lines30(map[int]string{10: "ten", 11: "eleven", 14: "fourteen"}))
	upstreamGit(t, dir, "add", "f.txt")
}

// failingFormatPatch is a directory holding a `git` that fails every format-patch as git does when
// its base is not an ancestor of the range (exit 128), and runs the real git for anything else.
func failingFormatPatch(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\ncase \" $* \" in *\" format-patch \"*)\n"+
		"  echo 'fatal: base commit should be the ancestor of revision list' >&2; exit 128;;\nesac\n"+
		"exec "+shquote.Quote(real)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A PASTED BLOCK MID-REBASE CHANGES NOTHING: the user resolves the first stop and pastes the
// continue and the export together; the continue stops again at the second member, and the export,
// which reads the rebased branch only once the rebase moves it, refuses — the series is untouched,
// no <patches>.new or .old is made, and the clone is still mid-rebase.
//
// A FORMAT-PATCH THAT FAILS ends the line there: with the rebase finished and a git whose
// format-patch fails, the export leaves the series as it was, renaming nothing. The <patches>.new it
// made is then named to remove first by the verb's next run, and the export refuses while it is
// there; removed, the export replaces the series.
func TestAPastedExportChangesNothingUntilTheRebaseIsFinished(t *testing.T) {
	fx, _, _, first, out, _ := firstAdvance(t)
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	if first.delivery.Key == "" {
		t.Fatalf("the first advance did not build:\n%s", out)
	}
	patches := filepath.Join(fx.forkDir, "patches")
	before := treeDigest(t, patches)
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	cmds := printedCommands(out)
	if rc != 1 || len(cmds) != 2 {
		t.Fatalf("rebase rc=%d with commands %q, want the conflict's continue and export\n%s\n%s", rc, cmds, out, errw)
	}
	resolveFirstStop(t, dir)
	pasted := pasteBlock(t, cmds)
	if !packsrc.RebaseInProgress(dir) {
		t.Fatalf("the fixture's continue did not stop at the second member:\n%s", pasted)
	}
	if got := treeDigest(t, patches); got != before {
		t.Errorf("the pasted export replaced the series while the rebase was stopped:\n%s", pasted)
	}
	for _, left := range []string{patches + ".new", patches + ".old"} {
		if _, err := os.Lstat(left); err == nil {
			t.Errorf("the refused export left %s", left)
		}
	}

	// The rebase finished, a format-patch that fails.
	resolveRebase(t, dir, v12, cmds[0])
	failed := pasteBlock(t, cmds[1:], "PATH="+failingFormatPatch(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := treeDigest(t, patches); got != before {
		t.Errorf("a failed format-patch's export replaced the series:\n%s", failed)
	}
	if _, err := os.Lstat(patches + ".old"); err == nil {
		t.Errorf("a failed format-patch's export renamed the series aside:\n%s", failed)
	}

	// The <patches>.new it left: named first, and refused while it is there.
	rc, again, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if w := "  first remove " + shquote.QuoteDisplay(patches+".new") + ", which an earlier export left — the export " +
		"refuses while it is there:\n    test -n "; rc != 1 || !strings.Contains(again, w) {
		t.Errorf("the next run does not name the leftover first (rc=%d), want %q\n%s\n%s", rc, w, again, errw)
	}
	pasteBlock(t, printedCommands(again))
	if got := treeDigest(t, patches); got != before {
		t.Error("the export ran over a leftover <patches>.new")
	}
	if err := os.RemoveAll(patches + ".new"); err != nil {
		t.Fatal(err)
	}
	runPrinted(t, cmds[1])
	if s := mustSeries(t, fx); s.Base != v12 || s.Len() != 2 {
		t.Errorf("the export, run once the leftover went, gave %d members at %s, want 2 at v1.2.0", s.Len(), s.Base)
	}
}

// AN ABORTED REBASE IS NO FINISHED ONE: after `git rebase --abort` in the clone, a second run says
// it holds nothing to export and names the restart, printing no export — and the export the first
// run printed, pasted anyway, refuses and leaves the series as it was.
func TestASecondRunOnAnAbortedRebaseNamesTheRestart(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	patches := filepath.Join(f.forkDir, "patches")
	before := treeDigest(t, patches)
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	cmds := printedCommands(out)
	if rc != 1 || len(cmds) != 2 {
		t.Fatalf("rebase rc=%d with commands %q\n%s\n%s", rc, cmds, out, errw)
	}
	upstreamGit(t, dir, "rebase", "--abort")
	rc, again, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(again, "it holds no finished rebase to export: the rebase there was aborted") ||
		!strings.Contains(again, "`yolo pack rebase forkpack/tool --into "+shquote.QuoteDisplay(dir)+" --restart` removes it") ||
		strings.Contains(again, "the rebase there is done") || len(printedCommands(again)) != 0 {
		t.Errorf("the run on an aborted rebase: rc=%d, want no export and the restart\n%s\n%s", rc, again, errw)
	}
	pasted := pasteBlock(t, cmds[1:])
	if got := treeDigest(t, patches); got != before {
		t.Errorf("the export pasted after an abort replaced the series:\n%s", pasted)
	}
}

// A CLONE A RUN LEFT BEFORE ITS REPLAY ENDED — a SIGKILL, an OOM kill, after the marker and before
// the rebase — holds the upstream's default branch and no applied series: a second run says it
// holds nothing to export and names the restart, rather than exporting the upstream's own commits
// as the series.
func TestASecondRunOnACloneWithNoReplayNamesTheRestart(t *testing.T) {
	f := newPatchedFixture(t, "")
	v12 := f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	f.commitMsg(t, "upstream later work", "", map[int]string{11: "eleven", 25: "later"})
	dir := filepath.Join(t.TempDir(), "clone")
	upstreamGit(t, filepath.Dir(dir), "clone", "-q", "--no-checkout", "--template=", "file://"+f.repo, dir)
	writeFile(t, filepath.Join(dir, ".git", packsrc.RebaseMarkerName), `{"schema":1,"owner":"forkpack/tool",`+
		`"repo":"file://`+f.repo+`","base":"`+f.base+`","target":{"commit":"`+v12+`","tag":"v1.2.0"},"at":1}`)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(out, "it holds no finished rebase to export") || strings.Contains(out, "format-patch") ||
		!strings.Contains(out, "--restart` removes it") {
		t.Errorf("the run on a clone with no replay: rc=%d, want no export and the restart\n%s\n%s", rc, out, errw)
	}
}

// THE INTERRUPT CONTEXT ENDS ON A HANGUP, the terminal or the ssh session closing, as on a Ctrl-C,
// so the clone's git is ended and the clone removed rather than left with a marker and no replay.
func TestTheRebaseInterruptEndsOnAHangup(t *testing.T) {
	// The test's own handler, so a hangup the context does not take cannot end the test binary.
	held := make(chan os.Signal, 1)
	signal.Notify(held, syscall.SIGHUP)
	defer signal.Stop(held)
	ctx, stop := rebaseInterrupt()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Error("a SIGHUP did not end the rebase's interrupt context")
	}
}
