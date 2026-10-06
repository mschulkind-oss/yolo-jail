package cli

// patchedrebase_lines_test.go pins each of `yolo pack rebase`'s lines at its call site (patchedrebase.go;
// docs/design/patched-forks.md §8.4, PF-D47–PF-D49), so a line deleted or pointed at the wrong
// version fails here: the `--onto` a conflict below the newest carries in the launch's walk and in
// `yolo pack status`; a fetched fork pack at a tag, and one rebased twice, whose clone the second
// steps update; a series based past every version; a fetch that failed; the good build that already
// runs a target; a second run on the same directory; and the next step of an unreadable config and
// of a restart that cannot remove its clone.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// THE LAUNCH'S WALK NAMES THE REBASE ONTO THE ENTRY IT IS ABOUT: bare for the newest candidate, which
// the verb takes with no --onto, and `--onto v1.2.0` for the conflict below it.
func TestALaunchsWalkNamesTheRebaseOntoAConflictBelowTheNewest(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 11: "eleven", 20: "twenty"})
	_, out, _ := fx.launch(t, "podman")
	for _, w := range []string{"upstream v1.3.0", "  rebase the series: yolo pack rebase forkpack/tool\n",
		"upstream v1.2.0", "  rebase the series: yolo pack rebase forkpack/tool --onto v1.2.0\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("the launch's walk lacks %q:\n%s", w, out)
		}
	}
}

// fetchedForkPack makes the fixture's fork pack a git repository, served bare, and points the user
// config at it under ref; `yolo pack install` fetches it. It returns the repository's address as
// the pack store normalizes it, and the bare repository.
func fetchedForkPack(t *testing.T, f *patchedFixture, ref string) (repo, bare string) {
	t.Helper()
	upstreamGit(t, f.forkDir, "init", "-q", "-b", "main")
	upstreamGit(t, f.forkDir, "add", "-A")
	upstreamGit(t, f.forkDir, "commit", "-qm", "the fork pack")
	upstreamGit(t, f.forkDir, "tag", "v1")
	bare = filepath.Join(t.TempDir(), "forkpack.git")
	upstreamGit(t, filepath.Dir(bare), "clone", "-q", "--bare", f.forkDir, bare)
	writeFile(t, filepath.Join(f.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(f.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"git+file://`+bare+`?ref=`+ref+`","name":"forkpack"}]}`)
	if rc, out, errw := packVerb(t, "install"); rc != 0 && !strings.Contains(out, "does not take") {
		t.Fatalf("install rc=%d\n%s\n%s", rc, out, errw)
	}
	return "file://" + strings.TrimSuffix(bare, ".git"), bare
}

// A FETCHED FORK PACK AT A TAG: the steps clone the pack's default branch (no -b) and say to point
// the config's ?ref= at the pushed commit, which no refresh moves a tag to.
func TestPackRebaseOfAFetchedForkPackAtATagSaysToMoveItsRef(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	repo, _ := fetchedForkPack(t, f, "v1")
	work := t.TempDir()
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(work, "clone"))
	q := shquote.QuoteDisplay
	if w := "    git clone " + q(repo) + " " + q(filepath.Join(work, "forkpack-pack")) + "\n"; rc != 1 || !strings.Contains(out, w) {
		t.Errorf("rc=%d, want the pack's clone with no -b, %q\n%s\n%s", rc, w, out, errw)
	}
	if w := "your config holds pack forkpack at ?ref=v1: point its ?ref= at the pushed commit, and the next fresh " +
		"launch builds it"; !strings.Contains(out, w) || strings.Contains(out, "refresh at a launch brings") {
		t.Errorf("the steps lack %q, or name a refresh no tag takes:\n%s", w, out)
	}
}

// resolveRebaseOnto resolves every stop of the rebase in dir by writing f.txt as the upstream's
// edits plus the members picked so far leave it — member one's line 10, then member two's line 12.
func resolveRebaseOnto(t *testing.T, dir, onto, cont string, upstream map[int]string) {
	t.Helper()
	members := []map[int]string{{10: "ten"}, {12: "twelve"}}
	for i := 0; packsrc.RebaseInProgress(dir); i++ {
		if i >= len(members) {
			t.Fatalf("the rebase in %s stopped more often than the series has members", dir)
		}
		done, _ := strconv.Atoi(upstreamGit(t, dir, "rev-list", "--count", onto+"..HEAD"))
		edits := map[int]string{}
		for k, v := range upstream {
			edits[k] = v
		}
		for _, m := range members[:done+1] {
			for k, v := range m {
				edits[k] = v
			}
		}
		writeFile(t, filepath.Join(dir, "f.txt"), lines30(edits))
		upstreamGit(t, dir, "add", "f.txt")
		if out, err := printedCmd(cont).CombinedOutput(); err != nil && !packsrc.RebaseInProgress(dir) {
			t.Fatalf("the printed continue %q failed: %v\n%s", cont, err, out)
		}
	}
}

// A FETCHED FORK PACK REBASED TWICE: the first steps clone the pack's repository beside the rebase
// clone — at <pack>-pack-2, since <pack>-pack holds something else — and the second, at the next
// conflict, find that clone and update it rather than clone over it. Run as printed both times, the
// pack's repository holds the series rebased onto v1.3.0.
func TestPackRebaseOfAFetchedForkPackTwiceUpdatesItsClone(t *testing.T) {
	f := newPatchedFixture(t, "")
	v12 := f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	repo, bare := fetchedForkPack(t, f, "main")
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "forkpack-pack", "mine.txt"), "not a clone of the pack\n")
	q := shquote.QuoteDisplay
	clone := filepath.Join(work, "forkpack-pack-2")

	dir := filepath.Join(work, "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	cmds := printedCommands(out)
	if rc != 1 || len(cmds) != 3 || cmds[1] != "git clone -b main "+q(repo)+" "+q(clone) {
		t.Fatalf("the first rebase: rc=%d, commands %q, want the clone at %s\n%s\n%s", rc, cmds, clone, out, errw)
	}
	resolveRebaseOnto(t, dir, v12, cmds[0], map[int]string{11: "eleven"})
	for _, c := range cmds[1:] {
		runPrinted(t, c)
	}
	if got := mustRead(t, filepath.Join(work, "forkpack-pack", "mine.txt")); got != "not a clone of the pack\n" {
		t.Errorf("the steps touched the directory that is not the pack's clone: %q", got)
	}

	// The next conflict: v1.3.0 edits line 11 again. `yolo pack update` brings the pushed series.
	v13 := f.commit(t, "v1.3.0", map[int]string{11: "ELEVEN"})
	packVerb(t, "update")
	dir2 := filepath.Join(work, "clone2")
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--into", dir2)
	cmds = printedCommands(out)
	if w := "git -C " + q(clone) + " checkout -q main && git -C " + q(clone) + " pull -q --ff-only"; rc != 1 ||
		len(cmds) != 3 || cmds[1] != w || strings.Contains(out, "git clone") {
		t.Fatalf("the second rebase: rc=%d, commands %q, want the existing clone updated, %q\n%s\n%s", rc, cmds, w, out, errw)
	}
	resolveRebaseOnto(t, dir2, v13, cmds[0], map[int]string{11: "ELEVEN"})
	for _, c := range cmds[1:] {
		runPrinted(t, c)
	}
	if got := upstreamGit(t, bare, "show", "main:patches/0001-ten.patch"); !strings.Contains(got, "base-commit: "+v13) {
		t.Errorf("the pack's repository does not hold the series rebased onto v1.3.0:\n%s", got)
	}
}

// A SERIES BASED PAST EVERY VERSION, with no good build: the next fresh launch builds its base, so
// no rebase is needed — said with --onto, exit 0, and nothing cloned.
func TestPackRebaseOfASeriesBasedPastEveryVersionNeedsNone(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	newBase := f.commitMsg(t, "upstream later work", "", map[int]string{14: "fourteen", 25: "later"})
	upstreamGit(t, f.repo, "checkout", "-q", "-b", "fork2")
	f.commitMsg(t, "ten", "", map[int]string{14: "fourteen", 25: "later", 10: "ten"})
	patches := filepath.Join(f.forkDir, "patches")
	if err := os.RemoveAll(patches); err != nil {
		t.Fatal(err)
	}
	upstreamGit(t, f.repo, "format-patch", "-q", "--base="+newBase, "-o", patches, "main..fork2")
	upstreamGit(t, f.repo, "checkout", "-q", "main")
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if w := "fork forkpack/tool: no version of the branch is newer than the series' base " + shortSHA(newBase) +
		", which the next fresh launch builds — no rebase is needed; `--onto <ref>` rebases it onto another upstream commit"; rc != 0 ||
		!strings.Contains(out, w) {
		t.Errorf("rc=%d, want 0 and %q\n%s\n%s", rc, w, out, errw)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the rebase cloned with no rebase needed")
	}
}

// A FETCH THAT FAILED is said, and the check answers from this machine's copy of the upstream.
func TestPackRebaseSaysAFetchItCouldNotMake(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	packVerb(t, "update")
	if err := os.Rename(f.repo, f.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(f.repo+".gone", f.repo) })
	_, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(t.TempDir(), "clone"))
	if w := "yolo pack rebase: fork forkpack/tool: could not fetch " + "git+file://" + f.repo + "?ref=main ("; !strings.Contains(errw, w) ||
		!strings.Contains(errw, ") — using this machine's copy") {
		t.Errorf("the rebase lacks the failed fetch %q:\n%s\n%s", w, out, errw)
	}
}

// --onto THE GOOD BUILD'S OWN COMMIT, the series unchanged: the good build already runs it.
func TestPackRebaseOntoTheGoodBuildSaysItRunsIt(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the first advance built nothing:\n%s", out)
	}
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--onto", "v1.1.0", "--into", filepath.Join(t.TempDir(), "clone"))
	if rc != 0 || !strings.Contains(out, "takes the series as it stands (2 patches, series ") ||
		!strings.Contains(out, " — the good build already runs it; nothing to rebase") {
		t.Errorf("rc=%d, want the target the good build runs said\n%s\n%s", rc, out, errw)
	}
}

// A SECOND RUN ON THE SAME DIRECTORY, while another holds it (a second terminal): refused at once,
// naming the command to run again, with nothing cloned and nothing removed.
func TestASecondRebaseOnTheSameDirectoryIsRefused(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	dir := filepath.Join(t.TempDir(), "my clone")
	unlock, held, err := mustRebaseDirLocks(t).TryLockRebaseDir(resolveExistingPrefix(dir))
	if err != nil || held {
		t.Fatalf("taking the lock: held=%v %v", held, err)
	}
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if w := "another `yolo pack rebase` is working in " + dir + " right now — run `yolo pack rebase forkpack/tool --into " +
		shquote.QuoteDisplay(dir) + "` again once it has finished"; rc != 1 || !strings.Contains(errw, w) ||
		strings.Contains(out, "cloning") {
		t.Errorf("rc=%d, want %q\n%s\n%s", rc, w, out, errw)
	}
	if _, err := os.Lstat(dir); err == nil {
		t.Error("the refused run made the directory")
	}
	unlock()
	if rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir); rc != 1 || !packsrc.RebaseInProgress(dir) {
		t.Errorf("once the lock is released: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// AN UNREADABLE CONFIG names the file to fix and the command to run again.
func TestPackRebaseNamesTheFixForAnUnreadableConfig(t *testing.T) {
	f := newPatchedFixture(t, "")
	writeFile(t, filepath.Join(f.home, ".config", "yolo-jail", "config.jsonc"), `{"packs": [`)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--onto", "v1.2.0")
	if w := "  fix what it names in " + paths.UserConfigPath() + ", then run `yolo pack rebase forkpack/tool --onto v1.2.0` again"; rc != 1 ||
		!strings.Contains(errw, w) {
		t.Errorf("rc=%d, want %q\n%s\n%s", rc, w, out, errw)
	}
}

// A RESTART THAT CANNOT REMOVE ITS CLONE names the two ways on.
func TestPackRebaseNamesTheFixWhenTheRestartCannotRemove(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	dir := filepath.Join(t.TempDir(), "clone")
	if rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir); rc != 1 {
		t.Fatalf("rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	prev := removeRebaseClone
	removeRebaseClone = func(string, string) error { return errors.New("device or resource busy") }
	t.Cleanup(func() { removeRebaseClone = prev })
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir, "--restart")
	if w := "removing the old rebase clone: device or resource busy — remove " + dir + " yourself, or name another " +
		"directory with --into <dir>"; rc != 1 || !strings.Contains(errw, w) {
		t.Errorf("rc=%d, want %q\n%s\n%s", rc, w, out, errw)
	}
}
