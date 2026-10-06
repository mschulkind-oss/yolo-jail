package cli

// patchedtaglessverbs_test.go pins what the explicit acts say of a patched fork over an upstream
// branch that carries no version tag the default release rule reads (docs/design/patched-forks.md
// PF-D60): `yolo pack rebase`, keyed and scratch, and `yolo pack series check` each say why the
// list is empty and name `follow: "head"`, as `yolo pack update` and `status` do, and the note a
// launch says once is said again when what the check reads changes.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `yolo pack rebase` ON A TAGLESS UPSTREAM says why nothing is newer: a rebase with no --onto must
// not say "nothing upstream is newer" of a branch whose newer commits the release rule cannot see.
func TestPackRebaseOnATaglessUpstreamSaysWhy(t *testing.T) {
	t.Run("no good build", func(t *testing.T) {
		f := newPatchedFixture(t, "")
		upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
		f.commitMsg(t, "untagged one", "", map[int]string{14: "fourteen"})
		dir := filepath.Join(t.TempDir(), "clone")
		rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", dir)
		for _, w := range []string{"carries no version tag that `follow: \"release\"` reads",
			"so what runs stays at the series' base " + shortSHA(f.base) + " until a tag appears", "`follow: \"head\"`",
			"`--onto <ref>` rebases the series onto an upstream commit"} {
			if rc != 0 || !strings.Contains(out, w) {
				t.Errorf("rebase on a tagless upstream: rc=%d, lacks %q:\n%s\n%s", rc, w, out, errw)
			}
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the rebase made its clone (%v)", err)
		}
	})
	t.Run("a good build", func(t *testing.T) {
		fx := newPatchedAdvanceFixture(t, "")
		upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
		fx.commitMsg(t, "untagged one", "", map[int]string{14: "fourteen"})
		if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
			t.Fatalf("the base was not built:\n%s", out)
		}
		fx.commitMsg(t, "untagged two", "", map[int]string{14: "fourteen", 20: "twenty"})
		rc, out, errw := rebaseVerb(t, "forkpack/tool", "--into", filepath.Join(t.TempDir(), "clone"))
		for _, w := range []string{"carries no version tag that `follow: \"release\"` reads",
			"so what runs stays at the good build " + shortSHA(fx.base), "`follow: \"head\"`"} {
			if rc != 0 || !strings.Contains(out, w) {
				t.Errorf("rebase on a tagless upstream with a good build: rc=%d, lacks %q:\n%s\n%s", rc, w, out, errw)
			}
		}
		if strings.Contains(out, "nothing upstream is newer") {
			t.Errorf("the rebase says nothing upstream is newer of a branch it cannot read:\n%s", out)
		}
	})
}

// THE SCRATCH FORMS ON A TAGLESS UPSTREAM say it too: `yolo pack rebase --pack` clones nothing and
// says why, and `yolo pack series check` gives the base's verdict with the note. With no check
// record behind them, both name this machine's good build or the series' base.
func TestTheScratchFormsOnATaglessUpstreamSayWhy(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	f.commitMsg(t, "untagged one", "", map[int]string{14: "fourteen"})
	tmp := inAJail(t, f.packs)
	stays := "so what runs stays at this machine's good build, or with none the series' base " + shortSHA(f.base) +
		", until a tag appears or `follow` changes — `follow: \"head\"` follows the branch's commits"
	dir := filepath.Join(t.TempDir(), "clone")
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--pack", f.forkDir, "--into", dir)
	if rc != 0 || !strings.Contains(out, "carries no version tag that `follow: \"release\"` reads") ||
		!strings.Contains(out, stays) || strings.Contains(out, "no version of the branch is newer") {
		t.Errorf("the scratch rebase on a tagless upstream: rc=%d\n%s\n%s", rc, out, errw)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the scratch rebase made its clone (%v)", err)
	}
	rc, out, errw = seriesVerb(t, "check", f.forkDir)
	if rc != 0 || !strings.Contains(out, "the series' base "+shortSHA(f.base)+" (no version of the branch contains it) "+
		"takes the series") || !strings.Contains(out, stays) {
		t.Errorf("the series check on a tagless upstream: rc=%d\n%s\n%s", rc, out, errw)
	}
	noScratchLeft(t, tmp)
}

// THE NOTE IS SAID AGAIN WHEN `follow` CHANGES ("the first launch whose check finds it, what it
// reads unchanged since, says it once"): a turn from `release` to `release:agent@` over the same
// tagless branch is a new rule that finds nothing, and its note names the new rule.
func TestTheTaglessNoteIsSaidAgainWhenFollowChanges(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "head")
	upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
	fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the head's first advance handed nothing\n%s", out)
	}
	fx.writeManifest(t, "main", "")
	if _, out, _ := fx.launch(t, "podman"); !strings.Contains(out, "`follow: \"release\"` reads") {
		t.Fatalf("the turn to release did not say the note:\n%s", out)
	}
	fx.later(10 * time.Minute)
	fx.writeManifest(t, "main", "release:agent@")
	if _, out, _ := fx.launch(t, "podman"); !strings.Contains(out, "`follow: \"release:agent@\"` reads") {
		t.Errorf("the turn to release:agent@ did not say its note:\n%s", out)
	}
}

// `yolo pack status` AND `update` WITH A GOOD BUILD on a tagless upstream name the good build as
// where the fork stays (stayAt), not the series' base, which is not what runs.
func TestStatusOnATaglessUpstreamNamesTheGoodBuild(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "head")
	upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
	tip := fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" {
		t.Fatalf("the head's first advance handed nothing\n%s", out)
	}
	fx.writeManifest(t, "main", "")
	for _, verb := range []string{"update", "status"} {
		_, out, errw := packVerb(t, verb)
		if w := "so what runs stays at the good build " + shortSHA(tip) + " until a tag appears"; !strings.Contains(out, w) {
			t.Errorf("%s lacks %q:\n%s\n%s", verb, w, out, errw)
		}
	}
}
