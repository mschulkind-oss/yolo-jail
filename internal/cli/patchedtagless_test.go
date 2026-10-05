package cli

// patchedtagless_test.go drives a PATCHED fork's advance over an upstream branch that carries no
// version tag the default release rule reads (docs/design/patched-forks.md PF-D60): the series' base
// is built, as for a branch whose versions all predate it, and the launch says once that the series
// stays there until a tag appears or `follow` changes.

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE FIRST ADVANCE BUILDS THE SERIES' BASE, and its build line says why and until when; a later
// launch's check finds the same and says nothing again, and the fork's line carries no held suffix.
func TestATaglessUpstreamBuildsTheSeriesBaseAndSaysWhyOnce(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
	fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key == "" || len(fx.builds) != 1 {
		t.Fatalf("a tagless upstream handed %+v after %d builds\n%s", r.delivery, len(fx.builds), out)
	}
	for _, w := range []string{"built fork forkpack/tool: " + shortSHA(fx.base) + " + 2 patches; this jail runs it — ?ref=main of ",
		"carries no version tag that `follow: \"release\"` reads",
		"so what runs stays at the series' base until a tag appears or `follow` changes — `follow: \"head\"` follows the branch's commits"} {
		if !strings.Contains(out, w) {
			t.Errorf("the base's build line lacks %q:\n%s", w, out)
		}
	}
	rec := fx.record(t)
	if rec.Check.Problem != "" || rec.Check.NoVersion == "" || rec.Good == nil || rec.Good.Commit != fx.base {
		t.Fatalf("record = %+v, good %+v; want the note, no problem, and the base built", rec.Check, rec.Good)
	}
	f := fx.fork(t)
	series := mustSeries(t, fx)
	in, _, _, _ := f.CheckWant(series).Inputs()
	if suffix := run.HeldSuffix(f, rec, in, series.Digest, fx.recipe(t)); suffix != "" {
		t.Errorf("the fork's line carries %q", suffix)
	}
	fx.later(2 * time.Hour)
	r2, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 1 || r2.delivery.Key != r.delivery.Key {
		t.Errorf("the next launch built again or handed %+v\n%s", r2.delivery, out)
	}
	if strings.Contains(out, "carries no version tag") {
		t.Errorf("a later check said the note again:\n%s", out)
	}
}

// WITH A GOOD BUILD SERVING, a release rule that finds no version tag keeps it, and the first check
// to find that says so once, naming the good build: here a fork that followed the head turns to the
// default rule over the same tagless branch.
func TestATaglessUpstreamKeepsTheGoodBuildAndSaysSoOnce(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "head")
	upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
	tip := fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	if r, out, _ := fx.launch(t, "podman"); r.delivery.Key == "" || len(fx.builds) != 1 {
		t.Fatalf("the head's first advance handed %+v after %d builds\n%s", r.delivery, len(fx.builds), out)
	}
	fx.writeManifest(t, "main", "")
	r, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 1 || r.delivery.Key == "" {
		t.Fatalf("the turn to release built again or handed nothing (%+v)\n%s", r.delivery, out)
	}
	want := "fork forkpack/tool: ?ref=main of "
	stays := "so what runs stays at the good build " + shortSHA(tip) + " + 2 patches until a tag appears or `follow` changes"
	if !strings.Contains(out, want) || !strings.Contains(out, stays) {
		t.Errorf("the turn to release does not say where the fork stays (%q):\n%s", stays, out)
	}
	fx.later(2 * time.Hour)
	if _, out, _ = fx.launch(t, "podman"); strings.Contains(out, "carries no version tag") || len(fx.builds) != 1 {
		t.Errorf("a later check said the note again, or built (%d builds):\n%s", len(fx.builds), out)
	}
}

// WITH A GOOD BUILD THAT DOES NOT SERVE — its store entry gone — the note names the upstream commit
// the good build is of, which the advance builds again.
func TestATaglessUpstreamNamesTheGoodBuildsCommitWhenItsEntryIsGone(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "head")
	upstreamGit(t, fx.repo, "tag", "-d", "v1.0.0")
	tip := fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen"})
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key == "" {
		t.Fatalf("the head's first advance handed nothing\n%s", out)
	}
	if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(r.delivery.Key); err != nil {
		t.Fatal(err)
	}
	fx.writeManifest(t, "main", "")
	_, out, _ = fx.launch(t, "podman")
	if stays := "so what runs stays at upstream " + shortSHA(tip) + " until a tag appears"; !strings.Contains(out, stays) {
		t.Errorf("the turn to release with the good build gone does not say %q:\n%s", stays, out)
	}
	if len(fx.builds) != 2 {
		t.Errorf("the good build's commit was built %d times, want again once its entry is gone", len(fx.builds))
	}
}
