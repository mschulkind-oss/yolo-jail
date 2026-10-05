package cli

// patchedrebasescratch_test.go pins the SCRATCH REBASE, `yolo pack rebase <key> --pack <dir>`
// (patchedrebasescratch.go; docs/design/patched-forks.md PF-D61), through the verb itself against
// a real local upstream: in a jail, for a local pack inside the jail's workspace, it checks the
// upstream in a scratch copy with no check record, stops at the member the host's own rebase stops
// at, prints the continue and the export into that directory, and writes nothing in the pack or the
// host's pack store; the printed steps, run as printed, give the rebased series; a pack outside the
// workspace is refused with the host's command; and a target the series takes removes the clone.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// A JAIL REBASES A LOCAL PACK INSIDE ITS WORKSPACE: the default target is the scratch check's newest
// version, the rebase stops at the member and paths the host's keyed rebase stops at, the steps
// export into the pack's own directory with no signature and offer no jail, nothing of the host's is
// read or written, and the printed steps, run as printed, replace the series with one at v1.2.0.
func TestPackRebaseInAJailRebasesALocalPackInsideTheWorkspace(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	packBefore := treeDigest(t, f.forkDir)
	tmp := inAJail(t, f.packs)
	dir := filepath.Join(t.TempDir(), "jail clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", dir)
	if rc != 1 {
		t.Fatalf("the scratch rebase rc=%d, want 1 for a rebase left to resolve\n%s\n%s", rc, out, errw)
	}
	q := shquote.QuoteDisplay
	patches := filepath.Join(f.forkDir, "patches")
	for _, w := range []string{
		"checking fork forkpack/tool's upstream git+file://" + f.repo + "?ref=main in a scratch copy",
		"fork forkpack/tool: upstream v1.2.0 (" + shortSHA(v12) + ") does not take the patch series — the rebase stopped in " + dir,
		"  0001-ten.patch conflicts in f.txt\n",
		"    git -C " + q(dir) + " rebase --continue\n",
		"then replace the series in " + q(f.forkDir) + " with the rebased one",
		"mkdir " + q(patches+".new") + " && git -C " + q(dir) + " -c format.noprefix=false format-patch --no-signature " +
			"--suffix=.patch --base=" + v12 + " -o " + q(patches+".new"),
		"the next fresh launch builds the new series wherever your `packs` selects this pack",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the scratch rebase lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "&& yolo") {
		t.Errorf("the scratch rebase in a jail offers to start a jail:\n%s", out)
	}
	if !packsrc.RebaseInProgress(dir) {
		t.Fatal("the clone is not mid-rebase")
	}
	if treeDigest(t, f.forkDir) != packBefore {
		t.Error("the scratch rebase wrote into the pack")
	}
	if _, err := os.Stat(paths.PacksDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the scratch rebase made the pack store %s (%v)", paths.PacksDir(), err)
	}
	noScratchLeft(t, tmp)

	// THE SAME STOP ON THE HOST: the keyed rebase of the selected pack stops at the same member.
	t.Setenv("YOLO_VERSION", "")
	hostDir := filepath.Join(t.TempDir(), "host clone")
	if rc, host, errw := rebaseVerb(t, "forkpack/tool", "--into", hostDir); rc != 1 ||
		!strings.Contains(host, "upstream v1.2.0 ("+shortSHA(v12)+") does not take the patch series") ||
		!strings.Contains(host, "  0001-ten.patch conflicts in f.txt\n") {
		t.Errorf("the host's rebase does not stop where the jail's did: rc=%d\n%s\n%s", rc, host, errw)
	}

	// RUN AS PRINTED, in the jail.
	t.Setenv("YOLO_VERSION", "1.0.0")
	cmds := printedCommands(out)
	if len(cmds) != 2 {
		t.Fatalf("the printed commands are %q, want the continue and the export", cmds)
	}
	resolveRebase(t, dir, v12, cmds[0])
	runPrinted(t, cmds[1])
	series, err := packsrc.ReadSeries(f.forkDir, "patches")
	if err != nil || series.Base != v12 || series.Len() != 2 {
		t.Fatalf("the exported series: %v, want 2 members at v1.2.0 (%+v)", err, series)
	}
}

// --onto, and --restart on its own clone, work as the keyed verb's; a second run on its own clone
// prints its steps again with no check.
func TestPackRebaseInAJailOntoAndItsOwnClone(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	f.commit(t, "v1.2.0", map[int]string{14: "fourteen"})
	inAJail(t, f.packs)
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--onto", "v1.1.0", "--into", dir)
	if rc != 1 || !strings.Contains(out, "upstream v1.1.0 ("+shortSHA(v11)+") does not take the patch series") {
		t.Fatalf("--onto v1.1.0: rc=%d\n%s\n%s", rc, out, errw)
	}
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--onto", "v1.1.0", "--into", dir)
	if rc != 1 || !strings.Contains(out, dir+" is its rebase clone") || strings.Contains(out, "checking") ||
		!strings.Contains(out, "`yolo pack rebase forkpack/tool --pack "+shquote.QuoteDisplay(f.forkDir)+" --onto v1.1.0 --into "+
			shquote.QuoteDisplay(dir)+" --restart` removes it") {
		t.Errorf("the second run on its own clone: rc=%d\n%s\n%s", rc, out, errw)
	}
	rc, out, errw = rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", dir, "--restart")
	if rc != 0 || !strings.Contains(out, "removed the old rebase clone") ||
		!strings.Contains(out, "upstream v1.2.0") || !strings.Contains(out, "takes the series as it stands") ||
		!strings.Contains(out, scratchBuilds) {
		t.Errorf("--restart onto the newest, which takes the series: rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a clean rebase left its clone (%v)", err)
	}
}

// OUTSIDE THE WORKSPACE, in a jail, the scratch rebase refuses and names the host's keyed verb;
// nothing is cloned or fetched.
func TestPackRebaseInAJailRefusesAPackOutsideTheWorkspace(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	tmp := inAJail(t, t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--onto", "v1.2.0")
	if rc != 1 || !strings.Contains(errw, "--pack "+f.forkDir+" is outside this jail's workspace") ||
		!strings.Contains(errw, "run `yolo pack rebase forkpack/tool --onto v1.2.0` in a terminal on the host") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Errorf("the refused rebase made %v", ents)
	}
	noScratchLeft(t, tmp)
}

// WHAT --pack NAMES: a directory, holding the fork the key names; a wrong key is answered with the
// keys the directory holds.
func TestPackRebaseScratchNamesThePacksForks(t *testing.T) {
	f := newPatchedFixture(t, "")
	bad := filepath.Join(f.packs, "bad")
	writeFile(t, filepath.Join(bad, "pack.json"), `{"name":"bad","contributes":[{"kind":"no-such-kind"}]}`)
	inAJail(t, f.packs)
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, tc := range []struct {
		args []string
		rc   int
		want string
	}{
		{[]string{"forkpack/other", "--pack", f.forkDir}, 1, "no fork or extension in " + f.forkDir +
			" is forkpack/other — the patched fork in " + f.forkDir + " is forkpack/tool"},
		{[]string{"--pack", f.forkDir}, 2, "name the patched fork or extension to rebase"},
		{[]string{"forkpack/tool", "--pack", filepath.Join(f.packs, "missing")}, 1, "is not a directory"},
		{[]string{"forkpack/tool", "--pack"}, 2, "--pack needs a value"},
		{[]string{"forkpack/tool", "--pack="}, 2, "--onto, --into and --pack each need a value"},
		{[]string{"basepack/tool", "--pack", filepath.Join(f.packs, "basepack")}, 1,
			"the pack in " + filepath.Join(f.packs, "basepack") + " declares no patched fork or extension"},
		{[]string{"bad/tool", "--pack", bad}, 1, "— `yolo pack lint " + bad + "` names every problem"},
	} {
		rc, out, errw := rebaseVerb(t, tc.args...)
		if rc != tc.rc || !strings.Contains(errw, tc.want) {
			t.Errorf("rebase %q: rc=%d, want %d and %q\n%s\n%s", tc.args, rc, tc.rc, tc.want, out, errw)
		}
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Errorf("a refused rebase made %v", ents)
	}
}

// ON THE HOST, --pack rebases a pack directory no `packs` entry selects, with no check record: the
// pack store is never made, the clone's marker names the key typed, and the steps offer a jail to
// resolve it in.
func TestPackRebaseScratchOnTheHostReadsAnUnselectedPack(t *testing.T) {
	f := newPatchedFixture(t, "")
	v12 := f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	other := filepath.Join(t.TempDir(), "mine")
	if err := os.CopyFS(other, os.DirFS(f.forkDir)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "mine/tool", "--pack", other, "--into", dir)
	if rc != 1 || !strings.Contains(out, "fork mine/tool: upstream v1.2.0 ("+shortSHA(v12)+") does not take the patch series") ||
		!strings.Contains(out, "mkdir "+shquote.QuoteDisplay(filepath.Join(other, "patches.new"))) ||
		!strings.Contains(out, "cd "+shquote.QuoteDisplay(dir)+" && yolo") {
		t.Errorf("rc=%d\n%s\n%s", rc, out, errw)
	}
	if m := mustMarker(t, dir); m.Owner != "mine/tool" {
		t.Errorf("the clone's marker names %s, want mine/tool", m.Owner)
	}
	if _, err := os.Stat(paths.PacksDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the scratch rebase on the host made the pack store %s (%v)", paths.PacksDir(), err)
	}
	noScratchLeft(t, tmp)
}

// A CTRL-C DURING THE SCRATCH CHECK clones nothing and removes the scratch copy.
func TestAnInterruptedScratchRebaseClonesNothing(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	tmp := inAJail(t, f.packs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prev := rebaseInterrupt
	rebaseInterrupt = func() (context.Context, func()) { return ctx, cancel }
	t.Cleanup(func() { rebaseInterrupt = prev })
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", dir)
	if rc != 130 || !strings.Contains(errw, "interrupted — nothing was cloned, and the scratch copy is removed") {
		t.Errorf("rc=%d, want 130\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the interrupted scratch rebase made its clone (%v)", err)
	}
	noScratchLeft(t, tmp)
}
