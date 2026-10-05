package cli

// patchedrebase_test.go pins `yolo pack rebase` (patchedrebase.go; docs/design/patched-forks.md
// §8.4, PF-D13, PF-D26, PF-D47) through the verb itself, against a real local upstream: it forces
// the check, clones the upstream where it is told, stops at the conflict and prints the continue and
// the export with the commits filled in, and writes nothing in the pack; the printed steps, run as
// printed, give a series the next fresh launch builds and moves the good build to (§14's done
// condition); a fetched fork pack's steps publish through a clone of the pack's repository; it
// refuses a directory it must not touch, says so on its own clone and starts over with --restart;
// and it runs on the host only.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// rebaseVerb runs `yolo pack rebase args...` and returns its exit status and what it printed.
func rebaseVerb(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := packMain(append([]string{"rebase"}, args...), &out, &errw, false)
	return rc, out.String(), errw.String()
}

// treeDigest is a digest of every file under dir, by path and bytes, so a test can say a tree was
// not written.
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel + "\x00"))
		if d.Type().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// printedCommands are the command lines the verb printed for a person to paste: its four-space
// indented lines, in order.
func printedCommands(out string) []string {
	var cmds []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "    ") {
			cmds = append(cmds, strings.TrimPrefix(line, "    "))
		}
	}
	return cmds
}

// runPrinted runs one printed command line in bash, as a person pasting it would, with a hermetic
// git and no editor to wait on.
func runPrinted(t *testing.T, line string) {
	t.Helper()
	if out, err := printedCmd(line).CombinedOutput(); err != nil {
		t.Fatalf("the printed command %q failed: %v\n%s", line, err, out)
	}
}

func printedCmd(line string) *exec.Cmd {
	cmd := exec.Command("bash", "-c", line)
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_EDITOR=true",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	return cmd
}

// mustMarker is the rebase clone at dir's marker.
func mustMarker(t *testing.T, dir string) *packsrc.RebaseMarker {
	t.Helper()
	state, m, err := packsrc.InspectRebaseDir(dir)
	if err != nil || state != packsrc.RebaseDirClone {
		t.Fatalf("%s is no rebase clone (%v, %v)", dir, state, err)
	}
	return m
}

// resolveRebase resolves every stop of the rebase in dir onto v12 by writing f.txt as the member
// being picked leaves it, then runs the printed continue.
func resolveRebase(t *testing.T, dir, onto string, cont string) {
	t.Helper()
	states := []map[int]string{
		{10: "ten", 11: "eleven", 14: "fourteen"},
		{10: "ten", 11: "eleven", 12: "twelve", 14: "fourteen"},
	}
	for i := 0; packsrc.RebaseInProgress(dir); i++ {
		if i >= len(states) {
			t.Fatalf("the rebase in %s stopped more often than the series has members", dir)
		}
		done, _ := strconv.Atoi(upstreamGit(t, dir, "rev-list", "--count", onto+"..HEAD"))
		writeFile(t, filepath.Join(dir, "f.txt"), lines30(states[done]))
		upstreamGit(t, dir, "add", "f.txt")
		// The continue stops again, exiting 1, at the next member that conflicts: the loop resolves it.
		if out, err := printedCmd(cont).CombinedOutput(); err != nil && !packsrc.RebaseInProgress(dir) {
			t.Fatalf("the printed continue %q failed: %v\n%s", cont, err, out)
		}
	}
}

// A CONFLICT: the check is forced, the clone stops mid-rebase at the member the launch named, and
// the verb prints the continue and the export with the commits filled in, its paths quoted for the
// shell — and writes nothing in the pack, nor a fork-lock entry.
func TestPackRebaseStopsAtTheConflictAndPrintsItsNextSteps(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	packBefore := treeDigest(t, f.forkDir)
	if rc, out, errw := packVerb(t, "update"); rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	checked := patchedRecord(t).Seq
	dir := filepath.Join(t.TempDir(), "my clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 {
		t.Fatalf("rebase rc=%d, want 1 for a rebase left to resolve\n%s\n%s", rc, out, errw)
	}
	patches := filepath.Join(f.forkDir, "patches")
	q := shquote.QuoteDisplay
	m := mustMarker(t, dir)
	for _, w := range []string{
		"checking fork forkpack/tool's upstream",
		"fork forkpack/tool: upstream v1.2.0 (" + shortSHA(v12) + ") does not take the patch series — the rebase stopped in " + dir,
		"  0001-ten.patch conflicts in f.txt",
		"    git -C " + q(dir) + " rebase --continue\n",
		// THE EXPORT, ONE LINE: the guard, a new directory, the branch exported with `.patch` names
		// and `a/` `b/` prefixes, and the renames — each only once the one before it succeeded.
		"    test -n \"$(git -C " + q(dir) + " rev-list -n 1 refs/heads/yolo-rebase --not " + v12 + " " + m.Applied +
			" --)\" && mkdir " + q(patches+".new") + " && git -C " + q(dir) + " -c format.noprefix=false format-patch " +
			"--suffix=.patch --base=" + v12 + " -o " + q(patches+".new") + " " + v12 + "..refs/heads/yolo-rebase && mv " +
			q(patches) + " " + q(patches+".old") + " && mv " + q(patches+".new") + " " + q(patches) + " && rm -r " +
			q(patches+".old") + "\n",
		"the next fresh launch builds the new series",
		"cd " + q(dir) + " && yolo",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("rebase lacks %q:\n%s", w, out)
		}
	}
	if cmds := printedCommands(out); len(cmds) != 2 {
		t.Errorf("the printed commands are %q, want the continue and the export, one line each", cmds)
	}
	if !packsrc.RebaseInProgress(dir) {
		t.Error("the clone is not mid-rebase")
	}
	if got := treeDigest(t, f.forkDir); got != packBefore {
		t.Error("the rebase wrote into the fork pack")
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("the rebase pinned a patched fork at %s", got)
	}
	if rec := patchedRecord(t); rec.Check == nil || rec.Seq != checked+1 {
		t.Errorf("the rebase did not force the check inside the hour of the last one (seq %d after %d)", rec.Seq, checked)
	}
}

// §14'S DONE CONDITION: "`yolo pack rebase` stops at the same conflict, and after the user resolves
// and exports, the next launch builds and moves the good build" — the printed steps, run exactly as
// printed, replace the series with one the next fresh launch builds at v1.2.0.
func TestARebasedAndExportedSeriesIsBuiltByTheNextLaunch(t *testing.T) {
	fx, v11, v12, first, out, _ := firstAdvance(t)
	if first.delivery.Key == "" || fx.record(t).Good.Commit != v11 {
		t.Fatalf("the first advance did not build v1.1.0:\n%s", out)
	}
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(out, "upstream v1.2.0 ("+shortSHA(v12)+") does not take the patch series") {
		t.Fatalf("rebase rc=%d, want the v1.2.0 conflict the launch held at\n%s\n%s", rc, out, errw)
	}
	cmds := printedCommands(out)
	if len(cmds) != 2 || !strings.HasSuffix(cmds[0], "rebase --continue") {
		t.Fatalf("the printed commands are %q, want the continue and the export", cmds)
	}
	resolveRebase(t, dir, v12, cmds[0])
	// ITS OWN CLONE, THE REBASE DONE: a second run says so and prints the same export again.
	rc, again, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(again, "the rebase there is done") || strings.Contains(again, "rebase --continue") ||
		strings.Join(printedCommands(again), "\n") != strings.Join(cmds[1:], "\n") {
		t.Errorf("the second run on the resolved clone: rc=%d, want the export alone again\n%s\n%s", rc, again, errw)
	}
	runPrinted(t, cmds[1])

	series := mustSeries(t, fx)
	if series.Base != v12 || series.Len() != 2 {
		t.Fatalf("the exported series has %d members at base %s, want 2 at v1.2.0", series.Len(), series.Base)
	}
	fx.later(2 * time.Hour)
	moved, out, _ := fx.launch(t, "podman")
	g := fx.record(t).Good
	if moved.delivery.Key == "" || g.Commit != v12 || g.Series != series.Digest {
		t.Fatalf("the next launch handed %+v with good build %+v, want v1.2.0 with the new series\n%s",
			moved.delivery, g, out)
	}
	if want := lines30(map[int]string{10: "ten", 11: "eleven", 12: "twelve", 14: "fourteen"}); fx.builds[len(fx.builds)-1] != want {
		t.Errorf("the build saw f.txt\n%s\nwant the resolved series at v1.2.0\n%s", fx.builds[len(fx.builds)-1], want)
	}
}

// A FETCHED FORK PACK, whose copy in the pack store no act writes: the steps clone the pack's
// repository, export into it, commit and push — and, run as printed, the pack's own repository holds
// the rebased series at v1.2.0, while the pack store's copy is untouched.
func TestPackRebaseOfAFetchedForkPackPublishesThroughItsRepository(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	upstreamGit(t, f.forkDir, "init", "-q", "-b", "main")
	upstreamGit(t, f.forkDir, "add", "-A")
	upstreamGit(t, f.forkDir, "commit", "-qm", "the fork pack")
	bare := filepath.Join(t.TempDir(), "forkpack.git")
	upstreamGit(t, filepath.Dir(bare), "clone", "-q", "--bare", f.forkDir, bare)
	writeFile(t, filepath.Join(f.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(f.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"git+file://`+bare+`?ref=main","name":"forkpack"}]}`)
	if rc, out, errw := packVerb(t, "install"); rc != 0 && !strings.Contains(out, "does not take") {
		t.Fatalf("install rc=%d\n%s\n%s", rc, out, errw)
	}
	storeTrees := filepath.Join(paths.PacksDir(), "trees")
	storeBefore := treeDigest(t, storeTrees)

	work := t.TempDir()
	dir := filepath.Join(work, "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 {
		t.Fatalf("rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	clone := filepath.Join(work, "forkpack-pack")
	repo := "file://" + strings.TrimSuffix(bare, ".git") // the address as the pack store normalizes it
	q := shquote.QuoteDisplay
	for _, w := range []string{"fork pack forkpack is fetched from " + repo,
		"    git clone -b main " + q(repo) + " " + q(clone) + "\n",
		"-o " + q(filepath.Join(clone, "patches")+".new") + " " + v12 + "..refs/heads/yolo-rebase",
		// The commit and the push end the export's own line, so a refused export pushes nothing.
		" && rm -r " + q(filepath.Join(clone, "patches.old")) + " && git -C " + q(clone) + " add -A patches && git -C " +
			q(clone) + " commit -m 'Rebase the patch series onto v1.2.0 (" + shortSHA(v12) + ")' && git -C " + q(clone) +
			" push\n",
		"the pack's refresh at a launch brings the pushed series within the hour, or `yolo pack update` now",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("rebase lacks %q:\n%s", w, out)
		}
	}
	cmds := printedCommands(out)
	if len(cmds) != 3 {
		t.Fatalf("the printed commands are %q, want the continue, the clone, and the export with its push", cmds)
	}
	resolveRebase(t, dir, v12, cmds[0])
	for _, c := range cmds[1:] {
		runPrinted(t, c)
	}
	if got := upstreamGit(t, bare, "show", "main:patches/0001-ten.patch"); !strings.Contains(got, "base-commit: "+v12) {
		t.Errorf("the pack's repository does not hold the rebased series at v1.2.0:\n%s", got)
	}
	if got := treeDigest(t, storeTrees); got != storeBefore {
		t.Error("the rebase wrote into the pack store's copy of the fork pack")
	}
}

// A TARGET THE SERIES FITS: nothing to rebase — said, with what builds it, and the clone removed.
// `--onto` names it; with none, the newest candidate is taken.
func TestPackRebaseOfATargetThatFitsRemovesTheClone(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--onto", "v1.1.0", "--into", dir)
	if rc != 0 {
		t.Fatalf("rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	if w := "fork forkpack/tool: upstream v1.1.0 (" + shortSHA(v11) + ") takes the series as it stands (2 patches"; !strings.Contains(out, w) ||
		!strings.Contains(out, "the next fresh launch builds it unless a newer version on its list takes the series; "+
			"nothing to rebase, so the clone is removed") {
		t.Errorf("rebase lacks %q and what builds it:\n%s", w, out)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a clean rebase left its clone")
	}
	rc, _, errw = rebaseVerb(t, "forkpack/tool", "--onto", "nope", "--into", dir)
	if rc != 1 || !strings.Contains(errw, "--onto nope names no branch, tag or commit") {
		t.Errorf("--onto naming nothing: rc=%d\n%s", rc, errw)
	}
}

// NOTHING NEWER: a good build that runs this very series at the newest version needs no rebase,
// and the verb clones nothing.
func TestPackRebaseWithNothingNewerClonesNothing(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the first advance built nothing:\n%s", out)
	}
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 0 || !strings.Contains(out, "nothing upstream is newer than the good build v1.1.0 ("+shortSHA(v11)+")") ||
		!strings.Contains(out, "`--onto <ref>`") {
		t.Errorf("rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the rebase cloned with nothing to rebase onto")
	}
}

// ITS OWN CLONE: a second rebase says so, prints that clone's next steps again and names
// --restart, touching nothing; --restart removes it and rebases again from the start.
func TestPackRebaseOnItsOwnCloneSaysSoAndRestartStartsOver(t *testing.T) {
	f := newPatchedFixture(t, "")
	v12 := f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	dir := filepath.Join(t.TempDir(), "clone")
	if rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir); rc != 1 {
		t.Fatalf("rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	writeFile(t, filepath.Join(dir, "f.txt"), "half resolved\n")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 {
		t.Fatalf("the second rebase rc=%d\n%s\n%s", rc, out, errw)
	}
	for _, w := range []string{"fork forkpack/tool: " + dir + " is its rebase clone, made under a minute ago onto upstream v1.2.0 (" + shortSHA(v12) + ")",
		"rebase --continue", "format-patch --suffix=.patch --base=" + v12,
		// The restart names the directory the user named: the bare verb would start over elsewhere.
		"`yolo pack rebase forkpack/tool --into " + shquote.QuoteDisplay(dir) + " --restart` removes it"} {
		if !strings.Contains(out, w) {
			t.Errorf("the second rebase lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "checking fork") {
		t.Errorf("the second rebase checked the upstream before saying the clone is there:\n%s", out)
	}
	if got := mustRead(t, filepath.Join(dir, "f.txt")); got != "half resolved\n" {
		t.Errorf("the second rebase touched its clone's work: f.txt is %q", got)
	}
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--into", dir, "--restart")
	if rc != 1 || !strings.Contains(out, "removed the old rebase clone") || !strings.Contains(out, "the rebase stopped in "+dir) {
		t.Fatalf("--restart rc=%d\n%s\n%s", rc, out, errw)
	}
	if got := mustRead(t, filepath.Join(dir, "f.txt")); !strings.Contains(got, "<<<<<<<") {
		t.Errorf("--restart did not rebase again from the start: f.txt is %q", got)
	}
}

// WHAT IT NEVER TOUCHES: a directory it did not make, another fork's clone, the home itself, yolo's
// state directory, and the fork pack's own directory — each refused naming --into, and left as it
// was.
func TestPackRebaseRefusesADirectoryItMustNotTouch(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	mine := t.TempDir()
	writeFile(t, filepath.Join(mine, "notes.txt"), "mine\n")
	other := filepath.Join(t.TempDir(), "other")
	writeFile(t, filepath.Join(other, ".git", packsrc.RebaseMarkerName), `{"schema":1,"owner":"else/tool"}`)
	// A link to yolo's state directory, under which the clone's directory is not made yet: the rule
	// resolves no path that does not exist, so only the resolved prefix finds it.
	if err := os.MkdirAll(paths.GlobalStorage(), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(paths.GlobalStorage(), link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, into, want string }{
		{"a directory it did not make", mine, "exists and is not a rebase clone of fork forkpack/tool"},
		{"another fork's clone", other, "is the rebase clone of fork else/tool"},
		{"the home", f.home, "the rebase clone IS your home directory"},
		{"yolo's state directory", filepath.Join(paths.PacksDir(), "clone"), "INSIDE yolo's own state directory"},
		{"a link to yolo's state directory", filepath.Join(link, "clone"), "INSIDE yolo's own state directory"},
		{"the fork pack's own directory", filepath.Join(f.forkDir, "clone"), "inside fork pack forkpack's own directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ""
			if _, err := os.Stat(tc.into); err == nil {
				before = treeDigest(t, tc.into)
			}
			rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", tc.into)
			if rc != 1 || !strings.Contains(errw, tc.want) || !strings.Contains(errw, "--into <dir>") {
				t.Errorf("rc=%d, want 1 and %q naming --into:\n%s\n%s", rc, tc.want, out, errw)
			}
			if strings.Contains(out, "cloning") {
				t.Errorf("the refused rebase cloned:\n%s", out)
			}
			if before != "" && treeDigest(t, tc.into) != before {
				t.Error("the refused rebase changed the directory")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(f.forkDir, "clone")); err == nil {
		t.Error("a clone was made inside the fork pack")
	}
}

// ITS DEFAULT DIRECTORY is ./<pack>-<bin>-rebase in the current one.
func TestPackRebaseClonesIntoTheCurrentDirectoryByDefault(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	cwd := t.TempDir()
	t.Chdir(cwd)
	rc, out, errw := rebaseVerb(t, "forkpack/tool")
	if rc != 1 || !packsrc.RebaseInProgress(filepath.Join(cwd, "forkpack-tool-rebase")) {
		t.Errorf("rc=%d, want a rebase stopped in ./forkpack-tool-rebase\n%s\n%s", rc, out, errw)
	}
}

// HOST ONLY: in a jail it names the command to run on the host, and clones nothing.
func TestPackRebaseInAJailNamesTheHost(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	t.Setenv("YOLO_VERSION", "1.0.0")
	cwd := t.TempDir()
	t.Chdir(cwd)
	rc, out, errw := rebaseVerb(t, "forkpack/tool")
	if rc != 1 || !strings.Contains(errw, "run `yolo pack rebase forkpack/tool` in a terminal on the host") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	// The command the user typed, a conflict line's --onto included, which the bare verb would not
	// rebase onto; quoted for the shell.
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--onto", "v1.2.0", "--into", "my clone")
	if rc != 1 || !strings.Contains(errw, "run `yolo pack rebase forkpack/tool --onto v1.2.0 --into 'my clone'` in a terminal on the host") {
		t.Errorf("with --onto: rc=%d\n%s\n%s", rc, out, errw)
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Errorf("the rebase in a jail made %v", ents)
	}
}

// IT NAMES ONE PATCHED FORK: with none named, or a name that is not one, it says which there are;
// a plain fork is told apart; a flag it does not know is refused. None of them clones.
func TestPackRebaseNamesThePatchedForks(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, tc := range []struct {
		args []string
		rc   int
		want string
	}{
		{nil, 2, "name the patched fork to rebase, as <pack>/<bin> — the selected patched fork is forkpack/tool"},
		{[]string{"forkpack/other"}, 1, "no selected fork is forkpack/other — the selected patched fork is forkpack/tool"},
		{[]string{"forkpack/tool", "--bogus"}, 2, `unknown flag "--bogus"`},
		{[]string{"forkpack/tool", "--onto"}, 2, "--onto needs a value"},
		{[]string{"forkpack/tool", "extra"}, 2, `unexpected argument "extra"`},
	} {
		rc, out, errw := rebaseVerb(t, tc.args...)
		if rc != tc.rc || !strings.Contains(errw, tc.want) {
			t.Errorf("rebase %q: rc=%d, want %d and %q\n%s\n%s", tc.args, rc, tc.rc, tc.want, out, errw)
		}
	}
	f.writeManifest(t, "main", "")
	writeFile(t, f.manifest, strings.Replace(mustRead(t, f.manifest), `"patches":"patches",`, "", 1))
	if rc, _, errw := rebaseVerb(t, "forkpack/tool"); rc != 1 || !strings.Contains(errw, "is a plain fork") {
		t.Errorf("a plain fork: rc=%d\n%s", rc, errw)
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Errorf("a refused rebase made %v", ents)
	}
}

// A CTRL-C DURING THE CLONE removes what it made: the verb's git runs under an interrupt context,
// ended here while the clone runs.
func TestAnInterruptedRebaseRemovesItsClone(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	ctx, cancel := context.WithCancel(context.Background())
	prev, prevRun := rebaseInterrupt, rebaseCloneRun
	rebaseInterrupt = func() (context.Context, func()) { return ctx, cancel }
	rebaseCloneRun = func(s *packsrc.Store, o packsrc.RebaseOptions) packsrc.RebaseResult {
		cancel()
		return prevRun(s, o)
	}
	t.Cleanup(func() { rebaseInterrupt, rebaseCloneRun = prev, prevRun })
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 130 || !strings.Contains(errw, "interrupted — the rebase clone "+dir+" is removed") {
		t.Errorf("rc=%d, want 130 and the interruption said\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the interrupted rebase left its clone")
	}
}

// EVERY CONFLICT NAMES THE VERB, onto the entry it is about: `yolo pack update`'s walk names it bare
// for the newest candidate, which the verb takes with no --onto, and with --onto for one below it.
func TestAConflictBelowTheNewestNamesItsRebaseOnto(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	f.commit(t, "v1.3.0", map[int]string{14: "fourteen", 11: "eleven", 20: "twenty"})
	_, out, errw := packVerb(t, "update")
	for _, w := range []string{"upstream v1.3.0", "rebase the series: yolo pack rebase forkpack/tool\n",
		"upstream v1.2.0", "rebase the series: yolo pack rebase forkpack/tool --onto v1.2.0\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("update lacks %q:\n%s\n%s", w, out, errw)
		}
	}
	// `yolo pack status` says the same of each recorded conflict.
	_, out, errw = packVerb(t, "status")
	for _, w := range []string{"candidate: v1.3.0", "(conflicts in f.txt) — `yolo pack rebase forkpack/tool` rebases the series",
		"below it: v1.2.0", "(conflicts in f.txt) — `yolo pack rebase forkpack/tool --onto v1.2.0` rebases the series"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s\n%s", w, out, errw)
		}
	}
}

// A HOLD THAT DOES NOT TAKE THE SERIES, with nothing to serve: the launch's conflict and its
// nothing-to-build line both name the verb, which rebases onto the held tag.
func TestAHeldTagThatDoesNotTakeTheSeriesNamesTheRebase(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v12 := fx.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	fx.writeManifest(t, "v1.2.0", "")
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != "" || len(fx.builds) != 0 {
		t.Fatalf("the conflicting hold built or handed %+v\n%s", r.delivery, out)
	}
	for _, w := range []string{"rebase the series: yolo pack rebase forkpack/tool\n",
		"nothing to build — this jail has no tool; `yolo pack rebase forkpack/tool` rebases the series onto the upstream it does not fit"} {
		if !strings.Contains(out, w) {
			t.Errorf("the held launch lacks %q:\n%s", w, out)
		}
	}
	dir := filepath.Join(t.TempDir(), "clone")
	if rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir); rc != 1 ||
		!strings.Contains(out, "upstream v1.2.0 ("+shortSHA(v12)+") does not take the patch series") {
		t.Errorf("the rebase of the held tag: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// A HOLD BY `agent_updates` is said: no launch checks the upstream, so a launch builds a rebased
// series at the good build's commit until the hold lifts. With no good build there is nothing to
// hold at — a launch checks as ever — and it is not said.
func TestPackRebaseSaysAnAgentUpdatesHold(t *testing.T) {
	hold := "`agent_updates` holds pack forkpack: no launch checks its upstream, so a launch builds the series at " +
		"the good build's commit until the hold lifts"
	fx, _, _, first, out, _ := firstAdvance(t)
	if first.delivery.Key == "" {
		t.Fatalf("the first advance built nothing:\n%s", out)
	}
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	_, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(t.TempDir(), "clone"))
	if !strings.Contains(out, hold) {
		t.Errorf("rebase lacks %q:\n%s\n%s", hold, out, errw)
	}
}

func TestPackRebaseWithNoGoodBuildSaysNoHold(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	f.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	_, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(t.TempDir(), "clone"))
	if strings.Contains(out, "holds pack forkpack") || !strings.Contains(out, "does not take the patch series") {
		t.Errorf("with no good build the rebase says a hold, or no conflict:\n%s\n%s", out, errw)
	}
}

// A TARGET THAT FITS UNDER A HOLD: the clean line says a launch builds it once the hold lifts —
// and a launch two hours later does stay on the good build, as said.
func TestPackRebaseOfATargetThatFitsUnderAHoldSaysTheHold(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the first advance built nothing:\n%s", out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(t.TempDir(), "clone"))
	if w := "takes the series as it stands (2 patches, series "; rc != 0 || !strings.Contains(out, w) ||
		!strings.Contains(out, "a launch builds it once the hold lifts (`agent_updates` holds pack forkpack); nothing to rebase") ||
		strings.Contains(out, patchedNotBuilt) {
		t.Errorf("rc=%d, want the clean target and the hold that keeps a launch from it\n%s\n%s", rc, out, errw)
	}
	fx.later(2 * time.Hour)
	fx.launch(t, "podman")
	if g := fx.record(t).Good; g == nil || g.Commit != v11 {
		t.Errorf("the held launch moved the good build to %+v, so the line was wrong", g)
	}
}

// AN EDITED SERIES WITH NOTHING NEWER is rebased onto the good build's own commit, which the next
// fresh launch replays it at first (PF-D40) — not refused as needing no rebase.
func TestPackRebaseOfAnEditedSeriesTakesTheGoodBuildsCommit(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the first advance built nothing:\n%s", out)
	}
	// The user's edit: one member changing line 14, which v1.1.0 changed too.
	upstreamGit(t, fx.repo, "checkout", "-q", "-b", "edit", fx.base)
	fx.commitMsg(t, "fourteen", "", map[int]string{14: "FOURTEEN"})
	patches := filepath.Join(fx.forkDir, "patches")
	if err := os.RemoveAll(patches); err != nil {
		t.Fatal(err)
	}
	upstreamGit(t, fx.repo, "format-patch", "-q", "--base="+fx.base, "-o", patches, fx.base+"..edit")
	upstreamGit(t, fx.repo, "checkout", "-q", "main")
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(out, "upstream v1.1.0 ("+shortSHA(v11)+") does not take the patch series") {
		t.Errorf("rebase rc=%d, want the edited series stopped at the good build's v1.1.0\n%s\n%s", rc, out, errw)
	}
}

// A CHECK PROBLEM STOPS A REBASE WITH NO TARGET, and not one --onto names: a ?ref= that names
// nothing is said with its fix, and --onto still rebases onto the commit it names.
func TestPackRebaseOntoGoesOnPastACheckProblem(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f.writeManifest(t, "nope", "")
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	if rc != 1 || !strings.Contains(errw, "?ref=nope names no branch, tag or commit") || strings.Contains(out, "cloning") ||
		strings.Contains(errw, "anyway") || strings.Contains(errw, "names nothing this series can be rebased onto") {
		t.Errorf("with no --onto: rc=%d, want the check's problem alone\n%s\n%s", rc, out, errw)
	}
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--onto", "v1.1.0", "--into", dir)
	if rc != 0 || !strings.Contains(errw, "rebasing onto --onto v1.1.0 anyway") ||
		!strings.Contains(out, "upstream v1.1.0 ("+shortSHA(v11)+") takes the series as it stands") {
		t.Errorf("with --onto: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// THE NEWEST CANDIDATE FITS: the verb, with no --onto, says the next fresh launch builds it, and
// names a member already upstream to drop.
func TestPackRebaseOfANewestCandidateThatFitsSaysTheLaunchBuildsIt(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{10: "ten", 25: "x"})
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
	for _, w := range []string{"upstream v1.1.0 (" + shortSHA(v11) + ") takes the series as it stands (2 patches",
		patchedNotBuilt + "; nothing to rebase", "0001-ten.patch is already in upstream v1.1.0 (" + shortSHA(v11) + "); drop it from patches"} {
		if rc != 0 || !strings.Contains(out, w) {
			t.Errorf("rc=%d, want 0 and %q\n%s\n%s", rc, w, out, errw)
		}
	}
}
