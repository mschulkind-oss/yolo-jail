package prune

// guard_test.go pins each reaper's RECHECK under a Guard (guard.go, OQ-PR2 of
// docs/design/podman-reboot-readiness.md): the housekeeping slot lets go of its lock between
// deletions, so each class asks again, under the lock and right before each deletion,
// whether the item is still unused. Every test drives a guard that changes the world the way
// a launch holding the lock would — just before the recheck — and asserts the item survives;
// and the same pass with nothing changed still deletes, so the recheck is not a blanket no.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// changingGuard runs change (what a launch did while holding the lock) before each
// recheck, then deletes only if the recheck still allows it. It counts the deletions it ran.
func changingGuard(change func()) (Guard, *int) {
	ran := 0
	return func(recheck func() bool, del func()) bool {
		if change != nil {
			change()
		}
		if recheck != nil && !recheck() {
			return false
		}
		del()
		ran++
		return true
	}, &ran
}

func TestAGuardedImageReapKeepsWhatALaunchRecordedDuringThePass(t *testing.T) {
	const stale = "/nix/store/bbbb-stale-image"
	cases := []struct {
		name   string
		change func(buildDir string)
		want   bool // removed
	}{
		{"nothing changed", nil, true},
		{"a launch recorded it in the load sentinel", func(buildDir string) {
			_ = image.AddLoadedPath(image.LoadSentinelPath(buildDir, "podman"), stale)
		}, false},
		{"a workspace made it its current image", func(buildDir string) {
			if err := RecordCurrentImage(buildDir, "yolo-ws-feedface", t.TempDir(), stale); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buildDir := t.TempDir()
			tag := pointAt(t, buildDir, "/nix/store/aaaa-current-image")
			// The load sentinel already names the current image: the pass's snapshot.
			_ = image.AddLoadedPath(image.LoadSentinelPath(buildDir, "podman"), "/nix/store/aaaa-current-image")
			rows := "id-cur localhost/yolo-jail:" + tag + " 2026-08-01 09:00:00 +0000 UTC\n" +
				"id-stale localhost/yolo-jail:" + image.ImageStoreKey(stale) + " 2026-07-01 09:00:00 +0000 UTC\n"
			var rmi []string
			var n int
			var change func()
			if tc.change != nil {
				change = func() { tc.change(buildDir) }
			}
			guard, _ := changingGuard(change)
			removed, ran, declined := AutoReapOldImagesGuarded("podman", buildDir, time.Now(),
				imagesRunnerCounting(rows, &rmi, &n), guard)
			if !ran || declined != "" {
				t.Fatalf("ran=%v declined=%q", ran, declined)
			}
			got := len(rmi) == 1 && rmi[0] == "id-stale"
			if got != tc.want || (len(removed) == 1) != tc.want {
				t.Errorf("rmi=%v removed=%v, want the stale image removed=%v", rmi, removed, tc.want)
			}
			for _, id := range rmi {
				if id == "id-cur" {
					t.Fatal("the current image was removed")
				}
			}
		})
	}
}

// loadRecordsSince, row by row: new paths, moved paths, and the last path whenever the file
// changed at all (the safe direction).
func TestLoadRecordsSince(t *testing.T) {
	t0 := time.Unix(1000, 0)
	snap := func(mt time.Time, ps ...string) loadSentinelSnapshot {
		return loadSentinelSnapshot{paths: ps, mtime: mt, ok: true}
	}
	key := image.ImageStoreKey
	for _, tc := range []struct {
		name          string
		before, after loadSentinelSnapshot
		want          []string
	}{
		{"unchanged", snap(t0, "a", "b"), snap(t0, "a", "b"), nil},
		{"new path", snap(t0, "a", "b"), snap(t0.Add(time.Second), "a", "b", "c"), []string{"c"}},
		{"moved to the end", snap(t0, "a", "b", "c"), snap(t0.Add(time.Second), "a", "c", "b"), []string{"b"}},
		{"re-recorded the last", snap(t0, "a", "b"), snap(t0.Add(time.Second), "a", "b"), []string{"b"}},
		{"no sentinel before", loadSentinelSnapshot{}, snap(t0, "a"), []string{"a"}},
		{"gone", snap(t0, "a"), loadSentinelSnapshot{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := loadRecordsSince(tc.before, tc.after)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, p := range tc.want {
				if !got[key(p)] {
					t.Errorf("%s not counted as recorded: %v", p, got)
				}
			}
		})
	}
}

func TestAGuardedStoreOutputDeleteKeepsAPathRootedDuringThePass(t *testing.T) {
	for _, rootIt := range []bool{false, true} {
		roots := t.TempDir()
		const p = "/nix/store/xxxx-yolo-jail-install-prefix"
		var deletes int
		run := func(argv []string, _ time.Duration) ProbeResult {
			deletes++
			return ProbeResult{Ran: true}
		}
		guard, _ := changingGuard(func() {
			if rootIt {
				_ = os.Symlink(p, filepath.Join(roots, "root"))
			}
		})
		done := DeleteSupersededStoreOutputsGuarded([]string{p}, true, run, guard, []string{roots})
		if (deletes == 1) == rootIt || (len(done) == 1) == rootIt {
			t.Errorf("rooted during the pass=%v: deletes=%d done=%v", rootIt, deletes, done)
		}
	}
}

func TestAGuardedCachePurgeKeepsAFileRewrittenDuringThePass(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		root := t.TempDir()
		file := filepath.Join(root, "uv", "old.whl")
		must(t, os.MkdirAll(filepath.Dir(file), 0o755))
		must(t, os.WriteFile(file, []byte("x"), 0o644))
		old := time.Now().Add(-60 * 24 * time.Hour)
		must(t, os.Chtimes(file, old, old))
		guard, _ := changingGuard(func() {
			if rewrite {
				now := time.Now()
				_ = os.Chtimes(file, now, now)
			}
		})
		_, files := PurgeCacheByAgeGuarded(root, []string{"uv"}, nil, 30, true, time.Now(), guard)
		_, err := os.Stat(file)
		if (err == nil) != rewrite || (files == 1) == rewrite {
			t.Errorf("rewritten during the pass=%v: file kept=%v, counted=%d", rewrite, err == nil, files)
		}
	}
}

func TestAGuardedAgentStagingReapKeepsADirStagedOrTrackedDuringThePass(t *testing.T) {
	for _, tc := range []struct {
		name string
		kept bool
		do   func(agents, containers string)
	}{
		{"nothing changed", false, nil},
		{"a launch staged into it", true, func(agents, _ string) {
			now := time.Now()
			_ = os.Chtimes(filepath.Join(agents, "yolo-gone-1"), now, now)
		}},
		{"its container started", true, func(_, containers string) {
			_ = os.WriteFile(filepath.Join(containers, "yolo-gone-1"), nil, 0o644)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agents, containers := t.TempDir(), t.TempDir()
			dir := filepath.Join(agents, "yolo-gone-1")
			must(t, os.MkdirAll(dir, 0o755))
			old := time.Now().Add(-3 * time.Hour)
			must(t, os.Chtimes(dir, old, old))
			var change func()
			if tc.do != nil {
				change = func() { tc.do(agents, containers) }
			}
			guard, _ := changingGuard(change)
			_, n, _ := PruneOrphanAgentStagingGuarded(agents, map[string]struct{}{}, true, time.Hour, true,
				time.Now(), guard, containers)
			_, err := os.Stat(dir)
			if (err == nil) != tc.kept || (n == 0) != tc.kept {
				t.Errorf("kept=%v (want %v), removed count=%d", err == nil, tc.kept, n)
			}
		})
	}
}

func TestAGuardedImageCacheReapKeepsATarRewrittenDuringThePass(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		dir := t.TempDir()
		tar := filepath.Join(dir, "k.tar")
		must(t, os.WriteFile(tar, []byte("x"), 0o644))
		old := time.Now().Add(-2 * time.Hour)
		must(t, os.Chtimes(tar, old, old))
		guard, _ := changingGuard(func() {
			if rewrite {
				now := time.Now()
				_ = os.Chtimes(tar, now, now)
			}
		})
		_, n := PruneImageCacheGuarded(dir, 0, true, guard)
		_, err := os.Stat(tar)
		if (err == nil) != rewrite || (n == 0) != rewrite {
			t.Errorf("rewritten=%v: kept=%v, removed=%d", rewrite, err == nil, n)
		}
	}
}

func TestAGuardedDeliveryReapKeepsADeliveryWritingDuringThePass(t *testing.T) {
	for _, writing := range []bool{false, true} {
		dir := t.TempDir()
		work := filepath.Join(dir, "k-1"+image.DeliveryWorkSuffix)
		must(t, os.MkdirAll(work, 0o755))
		old := time.Now().Add(-2 * time.Hour)
		must(t, os.Chtimes(work, old, old))
		guard, _ := changingGuard(func() {
			if writing {
				_ = os.WriteFile(filepath.Join(work, "blob"), []byte("x"), 0o644)
			}
		})
		_, n := PruneImageDeliveryGuarded(dir, true, guard)
		_, err := os.Stat(work)
		if (err == nil) != writing || (n == 0) != writing {
			t.Errorf("writing=%v: kept=%v, removed=%d", writing, err == nil, n)
		}
	}
}

func TestAGuardedLoopholeStateReapCountsOnlyWhatWent(t *testing.T) {
	for _, goneFirst := range []bool{false, true} {
		root := t.TempDir()
		archive := filepath.Join(root, RetiredLoopholeStateDir)
		for _, g := range []string{"20260101-000000", "20260201-000000"} {
			must(t, os.MkdirAll(filepath.Join(archive, g, "svc"), 0o755))
		}
		guard, _ := changingGuard(func() {
			if goneFirst {
				_ = os.RemoveAll(filepath.Join(archive, "20260101-000000"))
			}
		})
		_, n, _ := PruneRetiredLoopholeStateGuarded(root, 1, true, guard)
		if (n == 1) == goneFirst {
			t.Errorf("gone before the recheck=%v: counted %d", goneFirst, n)
		}
		if _, err := os.Stat(filepath.Join(archive, "20260201-000000")); err != nil {
			t.Error("the newest generation went")
		}
	}
}

// The manual `yolo prune` passes no guard: every deletion runs directly, as before.
func TestANilGuardDeletesDirectly(t *testing.T) {
	var g Guard
	ran := false
	if !g.Do(func() bool { t.Fatal("a nil guard ran the recheck"); return false }, func() { ran = true }) || !ran {
		t.Error("a nil guard did not delete")
	}
}
