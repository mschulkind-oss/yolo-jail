package cli

// patchedfetchretry_test.go pins what a FAILED FETCH leaves behind for the next launch
// (patchedadvance.go, packsrc/patchcheck.go; docs/reference/patched-forks.md §4.2, §8.1): a check that
// could not reach the upstream — the machine's network not up yet at boot — is a transient fault,
// never the upstream's answer. A later fresh launch with nothing built to run fetches again itself,
// inside the hour the earlier attempt's stamp would otherwise hold, and builds; and the refusal the
// earlier launch printed names the network and the next step. Reported 2026-10-16: a launch at
// boot, before the network was up, failed its fetch, and every launch for the next hour refused
// with "the next fresh launch tries again" without fetching, until `yolo pack update`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// reexportOntoNewBase re-exports fx's series onto a new upstream commit past every version, as the
// maintainer's rebase onto upstream main does: the good build's recipe no longer matches, so nothing
// serves, and the new base is a commit this machine's mirror has not fetched yet.
func reexportOntoNewBase(t *testing.T, fx *patchedAdvanceFixture) string {
	t.Helper()
	newBase := fx.commitMsg(t, "upstream later work", "", map[int]string{14: "fourteen", 25: "later"})
	upstreamGit(t, fx.repo, "checkout", "-q", "-b", "fork2")
	fx.commitMsg(t, "ten", "", map[int]string{14: "fourteen", 25: "later", 10: "ten"})
	patches := filepath.Join(fx.forkDir, "patches")
	if err := os.RemoveAll(patches); err != nil {
		t.Fatal(err)
	}
	upstreamGit(t, fx.repo, "format-patch", "-q", "--base="+newBase, "-o", patches, "main..fork2")
	upstreamGit(t, fx.repo, "checkout", "-q", "main")
	return newBase
}

// offlineFetches makes every `git fetch` the advance's store runs fail as a machine with no DNS yet
// does, while the file it returns exists.
func offlineFetches(t *testing.T) string {
	t.Helper()
	offline := filepath.Join(t.TempDir(), "offline")
	writeFile(t, offline, "")
	patchedGitWrapper(t, "if [ -e "+shquote.Quote(offline)+" ]; then for a in \"$@\"; do if [ \"$a\" = fetch ]; then "+
		"echo \"fatal: unable to access 'https://github.com/example/tool/': Could not resolve host: github.com\" >&2; "+
		"exit 128; fi; done; fi")
	return offline
}

// THE INCIDENT, THROUGH THE PRODUCTION BUILD SLOT: a launch with no network refuses, naming the
// network; once the network is up, the next fresh launch — ten minutes later, inside the hour the
// failed attempt stamped — fetches the base itself and builds.
func TestALaunchAfterAFailedFetchFetchesAgainAndBuilds(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	if first.delivery.Key == "" {
		t.Fatal("the fixture's first advance built nothing")
	}
	reexportOntoNewBase(t, fx)
	offline := offlineFetches(t)
	fx.later(2 * time.Hour)

	var out syncBuffer
	req := forkRequest(t, fx, "podman")
	got, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, &out, false)
	refused := got["tool"]
	if refused.Key != "" || len(fx.builds) != 1 {
		t.Fatalf("with no network the launch was handed %+v (builds %d)\n%s", refused, len(fx.builds), out.String())
	}
	for _, want := range []string{"Could not resolve host", "network", "`yolo pack update`",
		"the next fresh launch tries again"} {
		if !strings.Contains(refused.Reason, want) {
			t.Errorf("the offline refusal lacks %q: %s", want, refused.Reason)
		}
	}
	if strings.Contains(refused.Reason, "names nothing to build") || strings.Contains(refused.Reason, "in an hour") {
		t.Errorf("the offline refusal reads as the upstream's verdict, or promises an hour's wait: %s", refused.Reason)
	}

	if err := os.Remove(offline); err != nil {
		t.Fatal(err)
	}
	fx.later(10 * time.Minute)
	out = syncBuffer{}
	req = forkRequest(t, fx, "podman")
	got, _ = runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, &out, false)
	if got["tool"].Key == "" || len(fx.builds) != 2 {
		t.Fatalf("once the network was up the next fresh launch did not fetch and build: %+v (builds %d)\n%s",
			got["tool"], len(fx.builds), out.String())
	}
	if g := fx.record(t).Good; g == nil || g.Patches != 1 || g.Entry != got["tool"].Key {
		t.Errorf("the good build = %+v, want the re-exported one-patch series' build, handed", g)
	}
	if rec := fx.record(t); rec.Check == nil || rec.Check.FetchErr != "" || rec.Check.Problem != "" {
		t.Errorf("the retried check = %+v, want a fetch that reached the upstream", rec.Check)
	}
}

// A LAUNCH THAT HAS A GOOD BUILD TO RUN checks again too (LPC-D6 retired "an attempt counts"): after a
// failed fetch it runs the good build, and the next launch, with the network back, fetches and builds.
func TestAServingLaunchAfterAFailedFetchChecksAgain(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	offline := offlineFetches(t)
	fx.later(2 * time.Hour)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != first.delivery.Key {
		t.Fatalf("offline, the launch was handed %+v, want the good build\n%s", r.delivery, out)
	}
	if err := os.Remove(offline); err != nil {
		t.Fatal(err)
	}
	log := patchedGitWrapper(t, "")
	fx.later(10 * time.Minute)
	r, out, _ = fx.launch(t, "podman")
	if r.delivery.Key == "" || r.delivery.Key == first.delivery.Key || len(fx.builds) != 2 {
		t.Fatalf("with the network back the next launch was handed %+v after %d builds, want the new version built\n%s",
			r.delivery, len(fx.builds), out)
	}
	if data, _ := os.ReadFile(log); !strings.Contains(string(data), "fetch") {
		t.Errorf("the launch after a failed attempt did not fetch:\n%s", data)
	}
}
