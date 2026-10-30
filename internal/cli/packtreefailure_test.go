package cli

// packtreefailure_test.go pins a PACK-TREE PROGRAM's failed build (docs/design/pack-buildable-programs.md
// §8, BG-D13, BG-D14): the launch slot refuses that one program with the build's log and the
// retry command, in the pack's words and never a fork's; a launch that waited on another launch's
// build of the same key takes its failure rather than repeating it; and a launch that starts after
// the failure builds again.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// failingPackTreeJail stands in for a build jail whose build line ran and exited 3, as the line's
// own status records it. runs counts it.
func failingPackTreeJail(t *testing.T, runs *int) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*runs++
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity-abc\n")
		writeFile(t, filepath.Join(o.Workspace, capture.InstallerStatusLeaf), "3\n")
		return 3
	}
}

// THE CALL SITE: a launch's slot (runBuildSlot → addForkKeys → buildPlainForkForLaunch) whose
// pack-tree build fails answers no key and a reason naming the pack's build, its log under the
// workspace's .yolo and `yolo pack build <pack>` — never a fork, a commit, or that the launch
// continues without it (the readiness act stops it: OQ-BG1's leaning). Red if addForkKeys stops
// routing a pack-tree pin to the act, or the failure is worded as a fork's.
func TestAFailedPackTreeBuildIsRefusedWithItsLogAndRetry(t *testing.T) {
	f, digest := packTreeBuildFixture(t, "exit 3")
	runs := 0
	withFakeCaptureJail(t, failingPackTreeJail(t, &runs))
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	ws := floortest.ResolvedTemp(t)
	slot := func() (string, string) {
		var stream syncBuffer
		req := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: digest}}, Platform: "linux/amd64",
			Runtime: "podman", Workspace: ws, Interrupt: &run.ActInterrupt{}}
		forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
		if forks["x"].Key != "" {
			t.Fatalf("a failed build answered a key: %+v\n%s", forks["x"], stream.String())
		}
		return forks["x"].Reason, stream.String()
	}
	reason, out := slot()
	if runs != 1 {
		t.Fatalf("%d build jail runs, want 1", runs)
	}
	for _, want := range []string{"pack fx's build of x from tools/x (tree digest " + shortSHA(digest) + ")",
		"failed on the host", ".yolo/build-", "`yolo pack build fx`"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not name %q:\n%s", want, reason)
		}
	}
	for _, said := range []string{reason, out} {
		if strings.Contains(said, "fork") || strings.Contains(said, "commit") || strings.Contains(said, "continues without") {
			t.Errorf("a pack-tree program's failure reads as a fork's, or says the launch continues:\n%s", said)
		}
	}
	if !strings.Contains(out, "Retry: `yolo pack build fx`") {
		t.Errorf("the launch's warning does not name the retry:\n%s", out)
	}
	// A launch that STARTS after the failure builds again, with no back-off (BG-D13).
	if _, _ = slot(); runs != 2 {
		t.Errorf("the next launch ran %d build jails in all, want a second build", runs)
	}
}

// BG-D13: A WAITER TAKES THE WINNER'S RESULT, FAILURE INCLUDED. A launch blocked on the build's lock
// while another launch's build of the same key fails returns that failure, naming the winner's log,
// and runs no build jail of its own; a launch that took the lock without waiting builds.
func TestAPackTreeWaiterTakesTheWinnersFailure(t *testing.T) {
	f, digest := packTreeBuildFixture(t, "exit 3")
	runs := 0
	withFakeCaptureJail(t, failingPackTreeJail(t, &runs))
	b := forkBuild{Fork: f, Commit: digest, Platform: "linux/amd64"}
	mode := buildMode{lock: pidlock.Mode{Wait: true, Bound: time.Minute}, runJail: func(staging string, b forkBuild,
		s captureStreams) int {
		return forkBuildRunJail(staging, b, s, false)
	}}

	// The winner: this test holds the lock, as another launch's build would.
	winner, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		_, err := buildFork(b, mode, &out, io.Discard, false)
		done <- result{err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "waiting for pid") {
		if time.Now().After(deadline) {
			t.Fatalf("the second build never waited on the lock:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The winner's build fails, recording its result under the lock, then releases it.
	recordPackTreeOutcome(b, buildMode{}, errors.New("the build line ran and failed with exit status 3"))
	winner.Release()
	r := <-done
	var adopted packTreeWaitedFailure
	if !errors.As(r.err, &adopted) || !strings.Contains(r.err.Error(), "exit status 3") {
		t.Fatalf("the waiter returned %v, want the winner's failure", r.err)
	}
	if runs != 0 {
		t.Errorf("the waiter ran %d build jails, want none: it takes the winner's result", runs)
	}

	// A launch that STARTS after the failure takes the lock at once, and builds again.
	if _, err := buildFork(b, mode, io.Discard, io.Discard, false); err == nil || errors.As(err, &adopted) || runs != 1 {
		t.Errorf("a launch after the failure: %v, %d jail runs, want its own build", err, runs)
	}
}

// BG-D21 AT A LAUNCH: where the pack declares a prebuilt for the launch's platform, the slot's
// refusal of a pack-tree build names the explicit opt-in — a build that ran and failed, and a build
// jail that stopped before its build line — and never takes the prebuilt. A dev override's checkout
// names none (its `path` cannot be combined with `prebuilt`). Red if buildPlainForkForLaunch stops
// appending Fork.PrebuiltOptInClause to either reason.
func TestALaunchsPackTreeBuildRefusalNamesThePrebuiltOptIn(t *testing.T) {
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	notStarted := func(t *testing.T, runs *int) func(run.Options) int {
		return func(run.Options) int { *runs++; return 125 }
	}
	for _, tc := range []struct {
		name     string
		jail     func(*testing.T, *int) func(run.Options) int
		checkout bool
		said     string
	}{
		{"failed", failingPackTreeJail, false, "failed on the host"},
		{"not started", notStarted, false, "was not started"},
		{"failed checkout", failingPackTreeJail, true, "failed on the host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, digest := packTreeBuildFixture(t, "exit 3")
			f.Prebuilts = map[string]packdecl.PrebuiltBuild{"linux/amd64": {URL: "https://example.invalid/x",
				SHA256: strings.Repeat("c", 64)}}
			if tc.checkout {
				f.Checkout = f.SourceDir()
			}
			runs := 0
			withFakeCaptureJail(t, tc.jail(t, &runs))
			var stream syncBuffer
			req := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f, Commit: digest}}, Platform: "linux/amd64",
				Runtime: "podman", Workspace: floortest.ResolvedTemp(t), Interrupt: &run.ActInterrupt{}}
			forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
			d := forks["x"]
			if d.Key != "" || runs != 1 || !strings.Contains(d.Reason, tc.said) {
				t.Fatalf("%d runs, delivery %+v, want one run and a reason saying %q\n%s", runs, d, tc.said, stream.String())
			}
			named := strings.Contains(d.Reason, "`yolo config set programs.overrides.x.prebuilt true --user`") && strings.Contains(d.Reason, "linux/amd64")
			if named == tc.checkout {
				t.Errorf("the reason names the opt-in = %v, want %v:\n%s", named, !tc.checkout, d.Reason)
			}
		})
	}
}

// BG-D21 ON THE PRE-FLIGHT SKIP: two pack-tree programs of one pack share a build jail's seal; the
// first's jail refuses its own config, so the second is not started ("its build jail would refuse to
// start"), and that refusal names the prebuilt opt-in too. Red if buildPlainForkForLaunch's
// refusedSeal arm stops appending Fork.PrebuiltOptInClause.
func TestAPackTreeBuildSkippedForARefusedSealNamesThePrebuiltOptIn(t *testing.T) {
	x, _ := packTreeBuildFixture(t, "exit 3")
	writeFile(t, filepath.Join(x.Root, "tools", "y", "VERSION"), "1\n")
	pre := map[string]packdecl.PrebuiltBuild{"linux/amd64": {URL: "https://example.invalid/p", SHA256: strings.Repeat("c", 64)}}
	p := &packload.Pack{Name: "fx", Root: x.Root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindProgram, Bin: "x", Via: packdecl.ViaPack, From: "tools/x", Build: "exit 3", Run: "x", Prebuilt: pre},
		{Kind: packdecl.KindProgram, Bin: "y", Via: packdecl.ViaPack, From: "tools/y", Build: "exit 3", Run: "y", Prebuilt: pre},
	}}}
	var pins []packload.ForkPin
	for _, f := range packload.PackTreePrograms([]*packload.Pack{p}) {
		digest, err := f.Digest()
		if err != nil {
			t.Fatal(err)
		}
		pins = append(pins, packload.ForkPin{Fork: f, Commit: digest})
	}
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
	prev := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, s captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, s, color), false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	var stream syncBuffer
	req := run.ForkBuildRequest{Pins: pins, Platform: "linux/amd64", Runtime: "podman",
		Workspace: floortest.ResolvedTemp(t), Interrupt: &run.ActInterrupt{}}
	forks, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, &stream, false)
	skipped := ""
	for _, d := range forks {
		if strings.Contains(d.Reason, "would refuse to start on the host") {
			skipped = d.Reason
		}
	}
	if runs != 1 || skipped == "" {
		t.Fatalf("%d build jails, deliveries %+v; want one jail and the other build skipped\n%s", runs, forks, stream.String())
	}
	if !strings.Contains(skipped, ".prebuilt true --user`") || !strings.Contains(skipped, "linux/amd64") {
		t.Errorf("the skipped build's refusal does not name the prebuilt opt-in:\n%s", skipped)
	}
}
