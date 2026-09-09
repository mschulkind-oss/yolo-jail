package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// TestDueForAutoImageReap pins the debounce clock itself, independent of the
// orchestration below: a fresh/missing/corrupt sentinel is always due, and a
// sentinel younger than the interval is not.
func TestDueForAutoImageReap(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "last-image-reap")
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	if !DueForAutoImageReap(sentinel, AutoReapInterval, now) {
		t.Error("a missing sentinel must be due")
	}

	RecordAutoImageReap(sentinel, now)
	if DueForAutoImageReap(sentinel, AutoReapInterval, now.Add(time.Minute)) {
		t.Error("a minute after stamping must not be due (interval is a day)")
	}
	if !DueForAutoImageReap(sentinel, AutoReapInterval, now.Add(AutoReapInterval+time.Second)) {
		t.Error("a day+1s after stamping must be due again")
	}

	if err := os.WriteFile(sentinel, []byte("not-a-timestamp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !DueForAutoImageReap(sentinel, AutoReapInterval, now) {
		t.Error("a corrupt sentinel must be due, not stuck closed forever")
	}
}

// imagesRunnerCounting is imagesRunner (probes_test.go) plus a call counter on
// the `images` probe, so a test can assert a debounced call never re-probes
// podman at all — not merely that it removed nothing.
func imagesRunnerCounting(rows string, rmiCalls *[]string, imagesCalls *int) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "images" {
			*imagesCalls++
			return ProbeResult{Stdout: rows, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "rmi" {
			// `rmi <id>` — NOT `rmi -f <id>`, which is what this index used to
			// read. Forcing removed the CONTAINERS using an image and killed
			// live jails mid-session (2026-09-08); the plain form fails instead.
			*rmiCalls = append(*rmiCalls, argv[2])
			return ProbeResult{Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "ps" {
			return ProbeResult{Ran: true} // nothing running
		}
		return ProbeResult{Ran: true}
	}
}

// pointAt records a current-image pointer for a workspace that EXISTS, which is
// what CurrentImageTags requires before it will honour one, and returns the
// content tag that pointer protects. Every reap test here needs a live pointer
// or the pass declines, so the fixture is written once.
func pointAt(t *testing.T, buildDir, storePath string) string {
	t.Helper()
	ws := t.TempDir()
	if err := RecordCurrentImage(buildDir, "yolo-ws-deadbeef", ws, storePath); err != nil {
		t.Fatal(err)
	}
	return image.ImageStoreKey(storePath)
}

// TestAutoReapOldImagesDebounces is the "WHEN it runs" test the task asks
// for: it fails if the debounce is deleted from AutoReapOldImages, because a
// second call inside the same interval would then re-probe podman (and,
// harmlessly here, re-attempt the same removal) instead of skipping.
func TestAutoReapOldImagesDebounces(t *testing.T) {
	buildDir := t.TempDir()
	const current = "/nix/store/aaaa-current-image"
	tag := pointAt(t, buildDir, current)
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
		"id2 localhost/yolo-jail:" + tag + " 2026-08-01 09:00:00 +0000 UTC\n"

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	var imagesCalls int
	var rmiCalls []string
	run := imagesRunnerCounting(imgOut, &rmiCalls, &imagesCalls)

	// One workspace, one pointer (OQ-LS3): id2 is that workspace's current image
	// and survives; id1 is a superseded copy and there is no undo buffer for it
	// to sit in.
	removed, ran, _ := AutoReapOldImages("podman", buildDir, now, run)
	if !ran {
		t.Fatal("first call on a fresh machine must run (no debounce sentinel yet)")
	}
	// TWO probes, not one: OQ-DF3's REACH ruling made the candidate listing a
	// UNION of the repo-name query and a label query, because podman refuses
	// `images <repo> --filter ...` outright ("cannot specify an image and a
	// filter(s)"). Both arms are load-bearing — the label finds untagged rows,
	// and the repo name is the only thing that finds tagged rows built before
	// the label shipped — so this count is 2 by construction, not by accident.
	if imagesCalls != 2 {
		t.Fatalf("images probed %d times, want 2 (repo query + label query, unioned)", imagesCalls)
	}
	if len(removed) != 1 || removed[0] != "id1" {
		t.Errorf("removed = %v, want [id1] (id2 is the live image)", removed)
	}
	if len(rmiCalls) != 1 || rmiCalls[0] != "id1" {
		t.Errorf("rmi calls = %v, want [id1]", rmiCalls)
	}

	// A second call moments later, on the same machine clock, must be a
	// no-op — the assertion that fails if the debounce (or its call site) is
	// ever deleted.
	removed, ran, _ = AutoReapOldImages("podman", buildDir, now.Add(time.Minute), run)
	if ran {
		t.Error("a call inside the interval must be debounced (ran=false)")
	}
	if removed != nil {
		t.Errorf("a debounced call must not report removals, got %v", removed)
	}
	if imagesCalls != 2 {
		t.Errorf("a debounced call re-probed podman: imagesCalls=%d, want 2 (the union from the "+
			"first call, unchanged — a debounce must add none)", imagesCalls)
	}

	// Past the interval, it fires again.
	_, ran, _ = AutoReapOldImages("podman", buildDir, now.Add(AutoReapInterval+time.Second), run)
	if !ran {
		t.Error("a call past the interval must run again")
	}
	if imagesCalls != 4 {
		t.Errorf("imagesCalls = %d after the interval elapsed, want 4 (two unioned probes per pass, "+
			"two passes)", imagesCalls)
	}
}

// TestAutoReapOldImagesDeclinesWithNoCurrentPointers is the other half of the
// task's mandate: with no retention evidence the WHOLE pass declines, exactly
// like the manual `yolo prune --apply` section already does — and, because the
// pass never ran, it must not stamp the debounce sentinel either, so the very
// next launch (which may be the one whose own RecordCurrentImage call finally
// gives the pass its evidence) retries right away instead of waiting out a full
// day on a machine that was never actually protected.
//
// THIS IS ALSO THE DAY OQ-LS3 SHIPS, which is why it is worth a test of its own:
// on an upgraded machine the pointer directory does not exist yet and every
// image on disk is unpointed, so a pass that did NOT decline here would reap the
// whole store on the strength of an absence.
func TestAutoReapOldImagesDeclinesWithNoCurrentPointers(t *testing.T) {
	// It DOES list images first, and that reordering is deliberate rather than a
	// relaxation (OQ-LS2). This test used to assert "decline before ever probing
	// podman's images", which saved one read-only subprocess and cost the ability
	// to tell a denied sweep from a fresh machine: a host where yolo has never
	// launched has no pointers either, and failing `yolo prune` there would
	// report a problem nobody has. Listing first is what lets "declined" mean
	// "prevented work". Nothing is removed before the guards.
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n"
	rmiCalls := []string{}
	run := func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "rmi" {
			rmiCalls = append(rmiCalls, argv[2])
		}
		if len(argv) >= 2 && argv[1] == "ps" {
			t.Error("a missing retention set must decline before asking the runtime what is " +
				"running — the cheaper guard gates the probe, not the other way round")
		}
		return ProbeResult{Stdout: imgOut, Ran: true}
	}
	now := time.Now()
	buildDir := t.TempDir() // no current-images/ dir at all: known=false

	removed, ran, _ := AutoReapOldImages("podman", buildDir, now, run)
	if ran {
		t.Error("no honourable current-image pointer must decline (ran=false), never sweep")
	}
	if removed != nil {
		t.Errorf("removed = %v, want nil", removed)
	}
	if !DueForAutoImageReap(filepath.Join(buildDir, autoReapSentinelName), AutoReapInterval, now.Add(time.Second)) {
		t.Error("a declined pass must not stamp the debounce sentinel")
	}
	if len(rmiCalls) != 0 {
		t.Errorf("a declined pass removed %v — listing candidates before the guards must never "+
			"mean acting on them", rmiCalls)
	}
}

// TestAutoReapOldImagesNeverRemovesAPointedImage is the retention test: even on
// the reap's very first (undebounced) call, a workspace's CURRENT image must
// never appear in what was removed — and this fixture makes it the OLDEST row
// by CreatedAt, which is where the rule this replaced went wrong. CreatedAt is
// when the archive was streamed, so a workspace that has been on the same image
// for a week sorts first, and "keep the newest N" selected it first
// (minimal-disk-footprint.md §3.3's exact scenario). Under OQ-LS3 position
// decides nothing.
func TestAutoReapOldImagesNeverRemovesAPointedImage(t *testing.T) {
	buildDir := t.TempDir()
	const current = "/nix/store/cccc-current-image"
	tag := pointAt(t, buildDir, current)
	imgOut := "id-current localhost/yolo-jail:" + tag + " 2026-06-01 09:00:00 +0000 UTC\n" +
		"id-newer localhost/yolo-jail:2222222222222222 2026-08-01 09:00:00 +0000 UTC\n"
	var rmiCalls []string
	var imagesCalls int
	run := imagesRunnerCounting(imgOut, &rmiCalls, &imagesCalls)

	removed, ran, _ := AutoReapOldImages("podman", buildDir, time.Now(), run)
	if !ran {
		t.Fatal("a readable pointer must let the pass run")
	}
	for _, id := range removed {
		if id == "id-current" {
			t.Fatalf("a workspace's current image was removed: %v", removed)
		}
	}
	for _, id := range rmiCalls {
		if id == "id-current" {
			t.Fatalf("rmi was called against a workspace's current image: %v", rmiCalls)
		}
	}
	// AND THE NEWEST GOES, which is the half a keep window could never do: under
	// `keep=2` both rows survived, so this assertion is what fails if the count
	// comes back.
	if len(removed) != 1 || removed[0] != "id-newer" {
		t.Errorf("removed = %v, want [id-newer] only", removed)
	}
}

// TestAutoReapDeclineIsDistinguishableFromDebounce is OQ-LS2's core: before it,
// an unreadable ledger and a debounced interval both returned (nil, false), so
// the caller could not tell "nothing is wrong, come back tomorrow" from "the
// pass could not establish what is safe to touch". A pass that is not running
// and does not say so is the shape of the defect the whole reclamation effort
// began with, one level down.
func TestAutoReapDeclineIsDistinguishableFromDebounce(t *testing.T) {
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n"
	run := func(argv []string, _ time.Duration) ProbeResult {
		return ProbeResult{Stdout: imgOut, Ran: true}
	}
	now := time.Now()

	// Declined: no pointers, but candidates exist — so work WAS prevented.
	_, ran, declined := AutoReapOldImages("podman", t.TempDir(), now, run)
	if ran || declined == "" {
		t.Fatalf("no pointers: ran=%v declined=%q, want ran=false and a stated reason", ran, declined)
	}

	// Debounced: the stamp is fresh. Also not ran — and it must NOT carry a
	// reason, or every launch would warn about a healthy machine.
	buildDir := t.TempDir()
	sentinel := filepath.Join(buildDir, autoReapSentinelName)
	RecordAutoImageReap(sentinel, now)
	_, ran, declined = AutoReapOldImages("podman", buildDir, now.Add(time.Minute), run)
	if ran {
		t.Error("a fresh stamp must debounce")
	}
	if declined != "" {
		t.Errorf("a debounced pass reported %q — nothing is wrong when the interval has not "+
			"elapsed, and a warning there would fire on every launch", declined)
	}
}

// TestNothingOfOursIsNotADecline: a machine where yolo has never launched has
// no current-image pointers AND no candidates. Failing there would report a
// problem nobody has, which is why the candidate listing comes first.
func TestNothingOfOursIsNotADecline(t *testing.T) {
	run := func(argv []string, _ time.Duration) ProbeResult {
		return ProbeResult{Stdout: "", Ran: true} // no yolo-jail images at all
	}
	removed, declined := PruneOldImages("podman", map[string]struct{}{}, false /*known*/, true, run)
	if len(removed) != 0 || declined != "" {
		t.Fatalf("fresh machine: removed=%v declined=%q, want empty and NO decline — an empty "+
			"retention set with nothing to reap denied the user nothing", removed, declined)
	}
}
