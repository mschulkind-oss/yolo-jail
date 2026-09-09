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

// TestAutoReapOldImagesDebounces is the "WHEN it runs" test the task asks
// for: it fails if the debounce is deleted from AutoReapOldImages, because a
// second call inside the same interval would then re-probe podman (and,
// harmlessly here, re-attempt the same removal) instead of skipping.
func TestAutoReapOldImagesDebounces(t *testing.T) {
	buildDir := t.TempDir()
	const live = "/nix/store/aaaa-live-image"
	if err := os.WriteFile(filepath.Join(buildDir, "last-load-podman"),
		[]byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
		"id2 localhost/yolo-jail:" + image.ImageStoreKey(live) + " 2026-08-01 09:00:00 +0000 UTC\n"

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	var imagesCalls int
	var rmiCalls []string
	run := imagesRunnerCounting(imgOut, &rmiCalls, &imagesCalls)

	// keep=0: retention is entirely keep-by-use here, so only id2 (the live
	// path's own content tag) survives; id1 is a plain superseded image.
	removed, ran, _ := AutoReapOldImages("podman", buildDir, 0, now, run)
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
	removed, ran, _ = AutoReapOldImages("podman", buildDir, 0, now.Add(time.Minute), run)
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
	_, ran, _ = AutoReapOldImages("podman", buildDir, 0, now.Add(AutoReapInterval+time.Second), run)
	if !ran {
		t.Error("a call past the interval must run again")
	}
	if imagesCalls != 4 {
		t.Errorf("imagesCalls = %d after the interval elapsed, want 4 (two unioned probes per pass, "+
			"two passes)", imagesCalls)
	}
}

// TestAutoReapOldImagesDeclinesOnUnreadableLedger is the other half of the
// task's mandate: an unreadable load-sentinel ledger must decline the WHOLE
// pass, exactly like the manual `yolo prune --apply` section already does —
// and, because the pass never ran, it must not stamp the debounce sentinel
// either, so the very next launch (which may be the one whose own
// image.AddLoadedPath call finally makes the ledger readable) retries right
// away instead of waiting out a full day on a machine that was never
// actually protected.
func TestAutoReapOldImagesDeclinesOnUnreadableLedger(t *testing.T) {
	// It DOES list images first, and that reordering is deliberate rather than a
	// relaxation (OQ-LS2). This test used to assert "decline before ever probing
	// podman's images", which saved one read-only subprocess and cost the ability
	// to tell a denied sweep from a fresh machine: a host where yolo has never
	// loaded an image has an unreadable ledger too, and failing `yolo prune`
	// there would report a problem nobody has. Listing first is what lets
	// "declined" mean "prevented work". Nothing is removed before the guards.
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n"
	rmiCalls := []string{}
	run := func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "rmi" {
			rmiCalls = append(rmiCalls, argv[2])
		}
		if len(argv) >= 2 && argv[1] == "ps" {
			t.Error("an unreadable ledger must decline before asking the runtime what is running — " +
				"the liveness guard is cheaper than the probe it gates")
		}
		return ProbeResult{Stdout: imgOut, Ran: true}
	}
	now := time.Now()
	buildDir := t.TempDir() // no last-load-<runtime> file anywhere: liveKnown=false

	removed, ran, _ := AutoReapOldImages("podman", buildDir, 0, now, run)
	if ran {
		t.Error("an unreadable liveness ledger must decline (ran=false), never sweep")
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

// TestAutoReapOldImagesNeverRemovesTheLiveImage is the liveness-veto test:
// even on the reap's very first (undebounced) call, the image the load
// sentinel vouches for must never appear in what was removed, regardless of
// where it sorts by CreatedAt.
func TestAutoReapOldImagesNeverRemovesTheLiveImage(t *testing.T) {
	buildDir := t.TempDir()
	const live = "/nix/store/cccc-live-image"
	if err := os.WriteFile(filepath.Join(buildDir, "last-load-podman"),
		[]byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The live image is the OLDEST by CreatedAt (a week-old jail still
	// running, minimal-disk-footprint.md §3.3's exact scenario) — a
	// count-only rule would select it first were the veto not applied.
	imgOut := "id-live localhost/yolo-jail:" + image.ImageStoreKey(live) + " 2026-06-01 09:00:00 +0000 UTC\n" +
		"id-newer localhost/yolo-jail:2222222222222222 2026-08-01 09:00:00 +0000 UTC\n"
	var rmiCalls []string
	var imagesCalls int
	run := imagesRunnerCounting(imgOut, &rmiCalls, &imagesCalls)

	removed, ran, _ := AutoReapOldImages("podman", buildDir, 0, time.Now(), run)
	if !ran {
		t.Fatal("a readable ledger must run")
	}
	for _, id := range removed {
		if id == "id-live" {
			t.Fatalf("the live jail's image was removed: %v", removed)
		}
	}
	for _, id := range rmiCalls {
		if id == "id-live" {
			t.Fatalf("rmi was called against the live jail's image: %v", rmiCalls)
		}
	}
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

	// Declined: no ledger, but candidates exist — so work WAS prevented.
	_, ran, declined := AutoReapOldImages("podman", t.TempDir(), 0, now, run)
	if ran || declined == "" {
		t.Fatalf("unreadable ledger: ran=%v declined=%q, want ran=false and a stated reason", ran, declined)
	}

	// Debounced: the stamp is fresh. Also not ran — and it must NOT carry a
	// reason, or every launch would warn about a healthy machine.
	buildDir := t.TempDir()
	sentinel := filepath.Join(buildDir, autoReapSentinelName)
	RecordAutoImageReap(sentinel, now)
	_, ran, declined = AutoReapOldImages("podman", buildDir, 0, now.Add(time.Minute), run)
	if ran {
		t.Error("a fresh stamp must debounce")
	}
	if declined != "" {
		t.Errorf("a debounced pass reported %q — nothing is wrong when the interval has not "+
			"elapsed, and a warning there would fire on every launch", declined)
	}
}

// TestNothingOfOursIsNotADecline: a machine where yolo has never loaded an
// image has an unreadable ledger AND no candidates. Failing there would report
// a problem nobody has, which is why the candidate listing comes first.
func TestNothingOfOursIsNotADecline(t *testing.T) {
	run := func(argv []string, _ time.Duration) ProbeResult {
		return ProbeResult{Stdout: "", Ran: true} // no yolo-jail images at all
	}
	removed, declined := PruneOldImages("podman", 2, map[string]struct{}{}, false /*liveKnown*/, true, run)
	if len(removed) != 0 || declined != "" {
		t.Fatalf("fresh machine: removed=%v declined=%q, want empty and NO decline — an empty "+
			"ledger with nothing to reap denied the user nothing", removed, declined)
	}
}
