package cli

// patchedbase_test.go drives a PATCHED fork's advance where it falls back to the series' base and
// where a replay fails (patchedadvance.go; docs/design/patched-forks.md §6.4, §8.1, PF-D23, PF-D40,
// PF-D44, PF-D45): with nothing serving, a newest fit whose build fails sends the same advance on to
// the base, and so does a walk that stopped on an apply error; a branch with nothing newer than the
// base builds it with no hold said; a build stopped at the bound is a failed build however far its
// jail got; an apply error is said once and replayed again only by the next check; and the build's
// replay into src/ takes what the walk left of the replay's bound, while the base gets one of its own.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// patchedGitWrapper puts a git in front of the advance's store that logs every run and runs extra
// (shell, before the real git) first, and returns the log's path.
func patchedGitWrapper(t *testing.T, extra string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	wrapper := filepath.Join(dir, "git")
	writeFile(t, wrapper, "#!/bin/sh\necho \"$*\" >> "+shquote.Quote(logPath)+"\n"+extra+"\nexec "+
		shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	prev := patchedAdvanceStore
	patchedAdvanceStore = func(launch bool) *packsrc.Store {
		s := prev(launch)
		s.Git = wrapper
		return s
	}
	t.Cleanup(func() { patchedAdvanceStore = prev })
	return logPath
}

// failReadingFilesAt is a patchedGitWrapper extra under which reading the files of the commit named
// in failFile fails, as a blob that cannot be fetched does: an apply error at that entry (§5.2).
func failReadingFilesAt(failFile string) string {
	return "fail=$(cat " + shquote.Quote(failFile) + " 2>/dev/null)\n" +
		"if [ -n \"$fail\" ]; then for a in \"$@\"; do if [ \"$a\" = \"$fail^{tree}\" ]; then " +
		"echo \"fatal: simulated: cannot read $fail\" >&2; exit 128; fi; done; fi"
}

// failBuildsOf makes the fake build jail fail every build whose patched f.txt holds marker, and
// succeed at the rest.
func (fx *patchedAdvanceFixture) failBuildsOf(t *testing.T, marker string) {
	t.Helper()
	inner := fx.buildJail(t)
	withFakeCaptureJail(t, func(o run.Options) int {
		data, _ := os.ReadFile(filepath.Join(o.Workspace, forkSourceLeaf, "f.txt"))
		fx.rc = 0
		if strings.Contains(string(data), marker) {
			fx.rc = 2
		}
		return inner(o)
	})
}

// WITH NOTHING SERVING, A FIT THAT FAILS TO BUILD SENDS THE SAME ADVANCE TO THE BASE (PF-D23, §8.1):
// a first advance on a migrating machine, and the user's own edit, each end with the base built and
// handed, said as held there; the fit is not built again while its back-off holds.
func TestAFitThatFailsToBuildWithNothingServingBuildsTheBase(t *testing.T) {
	for _, tc := range []string{"first advance", "the user's edit"} {
		t.Run(tc, func(t *testing.T) {
			var fx *patchedAdvanceFixture
			var v11 string
			if tc == "first advance" {
				fx = newPatchedAdvanceFixture(t, "")
				v11 = fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			} else {
				fx, v11, _, _, _, _ = firstAdvance(t)
				writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`,
					`"build":"sh build2.sh"`, 1))
			}
			before := len(fx.builds)
			fx.failBuildsOf(t, "fourteen") // v1.1.0 and newer fail; the base builds
			r, out, handed := fx.launch(t, "podman")
			if len(fx.builds) != before+2 || strings.Contains(fx.builds[len(fx.builds)-1], "fourteen") {
				t.Fatalf("builds = %q, want the fit and then the base, in this one launch\n%s", fx.builds[before:], out)
			}
			g := fx.record(t).Good
			if r.delivery.Key == "" || g == nil || g.Commit != fx.base || g.Entry != r.delivery.Key || g.Tag != "v1.0.0" {
				t.Fatalf("handed %+v with good build %+v, want the base's build\n%s", r.delivery, g, out)
			}
			if len(handed) == 0 || handed[len(handed)-1].Key != r.delivery.Key {
				t.Errorf("the delivery record holds %+v, want the base's build last", handed)
			}
			for _, w := range []string{"the build of v1.1.0 (" + shortSHA(v11) + ") + 2 patches failed",
				"so the series is built at its base v1.0.0 (" + shortSHA(fx.base) + ") instead",
				"held at the series' base, since the newest version the series takes did not build"} {
				if !strings.Contains(out, w) {
					t.Errorf("the launch lacks %q:\n%s", w, out)
				}
			}
			if o := (&advance{rec: fx.record(t), series: mustSeries(t, fx), recipe: fx.recipe(t),
				yolo: patchedYoloVersion()}).buildFailure(v11); o == nil {
				t.Error("the fit's failed build was not recorded, so the next launch would not back off")
			}
			fx.later(2 * time.Hour)
			if again, out, _ := fx.launch(t, "podman"); again.delivery.Key != r.delivery.Key || len(fx.builds) != before+2 {
				t.Errorf("inside the fit's back-off the next launch built again or moved (%d builds):\n%s", len(fx.builds), out)
			}
		})
	}
}

// THE USER'S OWN FAILED EDIT, AT THE BASE TOO (PF-D23): with the base failing as well the program is
// missing, said with the revert; nothing is held, and reverting brings the good build back.
func TestAUsersFailedEditTriesTheBaseThenGoes(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1))
	fx.rc = 2
	edited, out, _ := fx.launch(t, "podman")
	if edited.delivery.Key != "" || edited.delivery.Reason == "" || len(fx.builds) != 3 ||
		fx.builds[2] != lines30(map[int]string{10: "ten", 12: "twelve"}) {
		t.Fatalf("the failed edit handed %+v after builds %q, want the fit and the base tried and no program\n%s",
			edited.delivery, fx.builds, out)
	}
	if !strings.Contains(out, "reverting the edit to the series or the build brings back the good build v1.1.0") {
		t.Errorf("the failed edit does not name the revert:\n%s", out)
	}
	if !storeEntryExists(r.delivery.Key) {
		t.Error("the failed edit reaped the good build it did not replace")
	}
}

// A FIRST ADVANCE WHOSE WALK STOPS ON AN APPLY ERROR still builds the base (§6.4: "On a first
// advance the base is still built meanwhile"), says the error, and the entry it stopped at stays
// pending for the next check.
func TestAFirstAdvanceWhoseWalkHitsAnApplyErrorBuildsTheBase(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	failFile := filepath.Join(t.TempDir(), "fail")
	writeFile(t, failFile, v11)
	patchedGitWrapper(t, failReadingFilesAt(failFile))
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key == "" || len(fx.builds) != 1 || fx.builds[0] != lines30(map[int]string{10: "ten", 12: "twelve"}) {
		t.Fatalf("handed %+v after builds %q, want the base built\n%s", r.delivery, fx.builds, out)
	}
	for _, w := range []string{"could not replay the series", "simulated: cannot read",
		"building it at its base " + shortSHA(fx.base) + " meanwhile",
		"held at the series' base until the series replays at a newer version"} {
		if !strings.Contains(out, w) {
			t.Errorf("the launch lacks %q:\n%s", w, out)
		}
	}
	rec := fx.record(t)
	if rec.ApplyErrAtLastCheck() == nil {
		t.Errorf("the apply error is not on the record: %+v", rec.ApplyErr)
	}
	for _, o := range rec.Outcomes {
		if o.Commit == v11 && o.Kind != packsrc.OutcomeApplies {
			t.Errorf("the entry the apply error stopped at was settled: %+v", o)
		}
	}
}

// AN APPLY ERROR IS SAID ONCE, AND THE NEXT CHECK RETRIES IT (§6.2, PF-D45): a launch inside the hour
// runs no git at all and says nothing, the fork's line carries the held suffix, and the launch after
// the next check builds what then replays.
func TestAnApplyErrorIsRetriedByTheNextCheckNotEveryLaunch(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	failFile := filepath.Join(t.TempDir(), "fail")
	writeFile(t, failFile, v13)
	logPath := patchedGitWrapper(t, failReadingFilesAt(failFile))
	fx.later(2 * time.Hour)
	got, out, _ := fx.launch(t, "podman")
	if got.delivery.Key != r.delivery.Key || len(fx.builds) != 1 || !strings.Contains(out, "could not replay the series") ||
		!strings.Contains(out, "the next check, in an hour, retries it") {
		t.Fatalf("the apply error handed %+v after %d builds\n%s", got.delivery, len(fx.builds), out)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	fx.later(time.Minute)
	again, out, _ := fx.launch(t, "podman")
	if again.delivery.Key != r.delivery.Key || strings.Contains(out, "could not replay") || len(fx.builds) != 1 {
		t.Errorf("a launch inside the hour replayed the apply error again:\n%s", out)
	}
	if logged, err := os.ReadFile(logPath); err == nil && len(logged) > 0 {
		t.Errorf("a launch inside the hour after an apply error ran git:\n%s", logged)
	}
	f := fx.fork(t)
	series := mustSeries(t, fx)
	in, _, _, _ := f.CheckWant(series).Inputs()
	if suffix := run.HeldSuffix(f, fx.record(t), in, series.Digest, fx.recipe(t)); !strings.Contains(suffix,
		"the series could not be replayed at upstream v1.3.0 ("+shortSHA(v13)+")") {
		t.Errorf("the held suffix is %q", suffix)
	}
	writeFile(t, failFile, "")
	fx.later(2 * time.Hour)
	moved, out, _ := fx.launch(t, "podman")
	if moved.delivery.Key == r.delivery.Key || len(fx.builds) != 2 {
		t.Errorf("the next check did not retry the entry the apply error stopped at:\n%s", out)
	}
	if fx.record(t).ApplyErr != nil {
		t.Errorf("a walk that settled left the apply error on the record: %+v", fx.record(t).ApplyErr)
	}
}

// A BUILD STOPPED AT THE BOUND IS A FAILED BUILD (PF-D38, §8.1), recorded with its back-off, however
// far its jail got — one still booting at the bound, before its build line wrote the toolchain
// record, included — so the next launch does not wait the whole bound again.
func TestABuildStoppedAtTheBoundIsAFailedBuild(t *testing.T) {
	for _, booted := range []bool{true, false} {
		t.Run(map[bool]string{true: "its build line ran", false: "still booting"}[booted], func(t *testing.T) {
			fx, _, _, r, _, _ := firstAdvance(t)
			fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
			fx.later(2 * time.Hour)
			prev := forkBuildChild
			forkBuildChild = func(_ context.Context, _ time.Duration, staging string, _ forkBuild, _ captureStreams, _ bool) (int, bool) {
				if booted {
					writeFile(t, filepath.Join(staging, forkToolchainLeaf), "image-identity\n")
				}
				return 130, true
			}
			t.Cleanup(func() { forkBuildChild = prev })
			got, out, _ := fx.launch(t, "podman")
			if got.delivery.Key != r.delivery.Key || !strings.Contains(out, "it ran past the "+forkBuildWaitBound.String()+
				" bound and was stopped") || strings.Contains(out, "did not start") {
				t.Errorf("the bound's stop handed %+v and said:\n%s", got.delivery, out)
			}
			recorded := false
			for _, o := range fx.record(t).Outcomes {
				recorded = recorded || o.Kind == packsrc.OutcomeBuildFailed
			}
			if !recorded {
				t.Fatalf("a build stopped at the bound recorded no failed build:\n%s", out)
			}
			calls := 0
			forkBuildChild = func(context.Context, time.Duration, string, forkBuild, captureStreams, bool) (int, bool) {
				calls++
				return 130, true
			}
			fx.later(time.Hour + time.Minute)
			if _, out, _ = fx.launch(t, "podman"); calls != 0 {
				t.Errorf("a launch inside the back-off waited on the build again:\n%s", out)
			}
		})
	}
}

// A BRANCH WITH NOTHING NEWER THAN THE SERIES' BASE builds the base on a first advance, and holds
// nothing (PF-D27): its line says why it is the base, never that it is held, and the fork's line
// carries no held suffix.
func TestABaseBuiltForAnEmptyListHoldsNothing(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	// The series re-exported over an untagged commit after every version on the branch.
	base := fx.commitMsg(t, "untagged", "", map[int]string{14: "fourteen", 20: "twenty"})
	upstreamGit(t, fx.repo, "checkout", "-q", "-b", "fork2")
	fx.commitMsg(t, "ten", "", map[int]string{10: "ten", 14: "fourteen", 20: "twenty"})
	fx.commitMsg(t, "twelve", "", map[int]string{10: "ten", 12: "twelve", 14: "fourteen", 20: "twenty"})
	if err := os.RemoveAll(filepath.Join(fx.forkDir, "patches")); err != nil {
		t.Fatal(err)
	}
	upstreamGit(t, fx.repo, "format-patch", "-q", "--base="+base, "-o", filepath.Join(fx.forkDir, "patches"), "main..fork2")
	upstreamGit(t, fx.repo, "checkout", "-q", "main")
	upstreamGit(t, fx.repo, "branch", "-q", "-D", "fork2")
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key == "" || len(fx.builds) != 1 {
		t.Fatalf("the empty list handed %+v after %d builds\n%s", r.delivery, len(fx.builds), out)
	}
	if !strings.Contains(out, "built fork forkpack/tool: "+shortSHA(base)+" + 2 patches; this jail runs it — no version "+
		"of the branch is newer than the series' base") || strings.Contains(out, "held") {
		t.Errorf("the base's build line holds what nothing holds:\n%s", out)
	}
	rec := fx.record(t)
	f := fx.fork(t)
	series := mustSeries(t, fx)
	in, _, _, _ := f.CheckWant(series).Inputs()
	if rec.Check.Problem != "" || rec.Good == nil || rec.Good.Commit != base {
		t.Fatalf("record = %+v, good %+v", rec.Check, rec.Good)
	}
	if suffix := run.HeldSuffix(f, rec, in, series.Digest, fx.recipe(t)); suffix != "" {
		t.Errorf("the fork's line carries %q", suffix)
	}
}

// THE BUILD'S REPLAY INTO src/ TAKES WHAT THE WALK LEFT OF THE REPLAY'S BOUND (PF-D44), so git holds a
// launch at most a check and one replay bound before a build; the series' base, built after the fit
// came to nothing, gets a bound of its own.
func TestTheBuildsReplayTakesWhatTheWalkLeft(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	prev := replayElapsed
	calls := 0
	replayElapsed = func(start time.Time) time.Duration {
		calls++
		if calls == 1 {
			return packsrc.ReplayTimeout // the walk of the list took the whole bound
		}
		return prev(start)
	}
	t.Cleanup(func() { replayElapsed = prev })
	r, out, _ := fx.launch(t, "podman")
	if !strings.Contains(out, "the series' replay ran out of its "+packsrc.ReplayTimeout.String()) {
		t.Errorf("the fit's replay into src/ was given a bound of its own:\n%s", out)
	}
	if r.delivery.Key == "" || len(fx.builds) != 1 || fx.record(t).Good.Commit != fx.base {
		t.Errorf("handed %+v after builds %q, want the base built on a bound of its own\n%s", r.delivery, fx.builds, out)
	}
}

// mustSeries is the fixture's series as the advance reads it.
func mustSeries(t *testing.T, fx *patchedAdvanceFixture) *packsrc.Series {
	t.Helper()
	s, err := fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// recipe is the fixture's patched recipe as the manifest asks for it now.
func (fx *patchedAdvanceFixture) recipe(t *testing.T) string {
	t.Helper()
	return forkBuild{Fork: fx.fork(t), Series: mustSeries(t, fx)}.recipe()
}

// A GIT TOO OLD FOR THE REPLAY names updating git as the next step (PF-D58): the series' base is not
// tried, since it would meet the same git, and neither the launch's line nor the jail's reason offers
// a retry that cannot help. With a good build serving, the jail starts on it and the line says when the
// series is replayed again once git is updated.
func TestAnOldGitOnTheHostNamesUpdatingGitAsTheNextStep(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	oldGit := `if [ "$1" = version ]; then echo "git version 2.39.5"; exit 0; fi`
	prev := patchedAdvanceStore
	patchedGitWrapper(t, oldGit)
	r, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 0 || r.delivery.Key != "" {
		t.Fatalf("under git 2.39.5 the launch built %d and handed %+v\n%s", len(fx.builds), r.delivery, out)
	}
	if !strings.Contains(out, "needs git 2.40 or newer (`git merge-tree --merge-base`), and this host's git is 2.39.5 — "+
		"this jail has no tool; update git, and the next fresh launch builds it") {
		t.Errorf("the launch does not name updating git as the next step:\n%s", out)
	}
	for _, stale := range []string{"building it at its base", "retries it", "`yolo pack update` now"} {
		if strings.Contains(out, stale) {
			t.Errorf("the launch offers %q, which meets the same git:\n%s", stale, out)
		}
	}
	if why := r.delivery.Reason; !strings.Contains(why, "update git on the host, and the next fresh launch builds it") ||
		strings.Contains(why, "tries again") {
		t.Errorf("the jail is told %q, want updating git on the host named", why)
	}

	// With a good build serving, the jail starts on it.
	patchedAdvanceStore = prev
	good, _, _ := fx.launch(t, "podman")
	if good.delivery.Key == "" {
		t.Fatal("with a current git the launch built nothing")
	}
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	patchedGitWrapper(t, oldGit)
	r, out, _ = fx.launch(t, "podman")
	if r.delivery.Key != good.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("under git 2.39.5 the serving launch handed %+v after %d builds\n%s", r.delivery, len(fx.builds), out)
	}
	if !strings.Contains(out, "this host's git is 2.39.5 — still running v1.1.0") ||
		!strings.Contains(out, "; update git, then the next check, in an hour, retries it, or `yolo pack update` now") {
		t.Errorf("the serving launch does not name updating git, then the retry:\n%s", out)
	}
}
