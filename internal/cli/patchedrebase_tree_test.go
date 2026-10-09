package cli

// patchedrebase_tree_test.go pins `yolo pack rebase` for a PATCHED EXTENSION
// (docs/design/patched-extensions.md §9: PF-D13 "unchanged, addressed `yolo pack rebase
// <pack>/<name>`"). Every conflict line an extension's check prints names the verb with the
// extension's owner key, so the verb must take that key, clone the extension's upstream, stop at the
// conflict and export into the contributing pack's own patch directory.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// THE CONFLICT'S OWN REPAIR COMMAND, run as printed: `yolo pack update` fails on the extension's
// conflict with PF-D81's error, whose Repair line names `yolo pack rebase treepack/tool-ext --onto
// <the conflicting commit>`, and that command stops at the same conflict in a clone of the
// extension's upstream, prints the export into the contributing pack's patches, and writes nothing
// in the pack.
func TestPackRebaseRebasesAPatchedExtensionsSeries(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	packBefore := treeDigest(t, fx.treeDir)
	rc, upd, updErr := packVerb(t, "update")
	const step = "\n  Repair: "
	i := strings.Index(updErr, step)
	if rc != 1 || i < 0 || !strings.Contains(updErr, "ERROR: "+treeKeyCLI+": patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("update rc=%d names no PF-D81 error and repair for the extension's conflict:\n%s\n%s", rc, upd, updErr)
	}
	printed := strings.TrimSpace(strings.SplitN(updErr[i+len(step):], "\n", 2)[0])
	if printed != "yolo pack rebase "+treeKeyCLI+" --onto "+v12 {
		t.Fatalf("the repair line names %q, want the extension's key onto the conflicting commit", printed)
	}
	dir := filepath.Join(t.TempDir(), "ext clone")
	args := append(strings.Fields(printed)[3:], "--into", dir)
	rc, out, errw := rebaseVerb(t, args...)
	if rc != 1 {
		t.Fatalf("rebase rc=%d, want 1 for a rebase left to resolve\n%s\n%s", rc, out, errw)
	}
	q := shquote.QuoteDisplay
	patches := filepath.Join(fx.treeDir, "patches")
	for _, w := range []string{
		"checking extension " + treeKeyCLI + "'s upstream",
		// --onto a commit names the target by its commit, as any --onto commit does.
		"extension " + treeKeyCLI + ": upstream " + shortSHA(v12) + " does not take the patch series — " +
			"the rebase stopped in " + dir,
		"  0001-ten.patch conflicts in f.txt",
		"    git -C " + q(dir) + " rebase --continue\n",
		"-o " + q(patches+".new") + " " + v12 + "..refs/heads/yolo-rebase && mv " + q(patches) + " " + q(patches+".old"),
	} {
		if !strings.Contains(out, w) {
			t.Errorf("rebase lacks %q:\n%s", w, out)
		}
	}
	if m := mustMarker(t, dir); m.Owner != treeKeyCLI {
		t.Errorf("the clone's marker names %q, want the extension's key", m.Owner)
	}
	if got := treeDigest(t, fx.treeDir); got != packBefore {
		t.Error("the rebase wrote into the contributing pack")
	}
}

// IT NAMES THE PATCHED EXTENSIONS TOO: with no key, the refusal lists what the verb takes, and an
// extension is one of them.
func TestPackRebaseNamesThePatchedExtensions(t *testing.T) {
	newTreeFixture(t, `"f.txt"`)
	t.Chdir(t.TempDir())
	rc, out, errw := rebaseVerb(t)
	want := "name the patched fork or extension to rebase, as <pack>/<bin> or <pack>/<name> — the selected " +
		"patched extension is " + treeKeyCLI
	if rc != 2 || !strings.Contains(errw, want) {
		t.Errorf("rebase with no key: rc=%d, want 2 and %q\n%s\n%s", rc, want, out, errw)
	}
}
