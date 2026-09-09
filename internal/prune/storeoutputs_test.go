package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func mkStorePath(t *testing.T, storeDir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(storeDir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	tv := []unix.Timeval{unix.NsecToTimeval(when.UnixNano()), unix.NsecToTimeval(when.UnixNano())}
	if err := unix.Lutimes(p, tv); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSupersededStoreOutputsIsScopedByNameAndRoots pins the two properties that
// make this admissible under P5 at all: it only ever names yolo's own outputs,
// and it never names one a root of ours points at.
func TestSupersededStoreOutputsIsScopedByNameAndRoots(t *testing.T) {
	store := t.TempDir()
	old := 48 * time.Hour

	rooted := mkStorePath(t, store, "aaaa-yolo-jail-install-prefix", old)
	orphanPrefix := mkStorePath(t, store, "bbbb-yolo-jail-install-prefix", old)
	orphanGo := mkStorePath(t, store, "cccc-yolo-jail-go-0-dev", old)
	// Not ours. Two shapes that a careless glob would sweep: somebody else's
	// package, and a name that merely CONTAINS ours.
	stranger := mkStorePath(t, store, "dddd-firefox-140.0", old)
	lookalike := mkStorePath(t, store, "eeee-yolo-jail-install-prefix-backup", old)

	rootsDir := filepath.Join(t.TempDir(), "roots")
	if err := os.MkdirAll(rootsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rooted, filepath.Join(rootsDir, "key")); err != nil {
		t.Fatal(err)
	}

	got := SupersededStoreOutputs(store, []string{rootsDir}, StoreOutputGrace, time.Now())

	want := map[string]bool{orphanPrefix: true, orphanGo: true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("selected %q, which is not an unrooted yolo output", p)
		}
	}
	for _, never := range []string{rooted, stranger, lookalike} {
		for _, p := range got {
			if p == never {
				t.Errorf("selected %q — this pass may only ever name yolo's OWN outputs, and "+
					"never one a root points at; widening the set is what P5 forbids", never)
			}
		}
	}
}

// TestStoreOutputGraceCoversAnUnrootedNewBuild: an output realized moments ago
// belongs to a launch that may not have rooted it yet. Same guard, same reason,
// as PrefixRootGrace.
func TestStoreOutputGraceCoversAnUnrootedNewBuild(t *testing.T) {
	store := t.TempDir()
	fresh := mkStorePath(t, store, "ffff-yolo-jail-install-prefix", time.Minute)
	if got := SupersededStoreOutputs(store, nil, StoreOutputGrace, time.Now()); len(got) != 0 {
		t.Fatalf("selected %v — a path built a minute ago has not reached its rooting step", got)
	}
	_ = fresh
}

// TestStoreDeleteCmdNeverIgnoresLiveness is the pin that keeps nix's own refusal
// as the second veto. A flag that switched it off would turn a named,
// self-scoped deletion into exactly the careless store GC P5 forbids — and it is
// one word, in one place, with nothing else to notice it.
func TestStoreDeleteCmdNeverIgnoresLiveness(t *testing.T) {
	argv := strings.Join(StoreDeleteCmd("/nix/store/x-yolo-jail-install-prefix"), " ")
	for _, forbidden := range []string{"--ignore-liveness", "gc", "--all", "--recursive"} {
		if strings.Contains(argv, forbidden) {
			t.Fatalf("StoreDeleteCmd contains %q (%s) — nix's refusal to delete a live path is "+
				"this design's SECOND veto, behind yolo's own rooting", forbidden, argv)
		}
	}
	if !strings.HasPrefix(argv, "nix store delete ") {
		t.Fatalf("StoreDeleteCmd = %q, want a single named `nix store delete`", argv)
	}
}

// TestDeleteSkipsWhatNixRefuses: a live path nix declines is the veto working,
// not a failure, so it is skipped and not reported as removed.
func TestDeleteSkipsWhatNixRefuses(t *testing.T) {
	refused := "/nix/store/live-yolo-jail-install-prefix"
	ok := "/nix/store/dead-yolo-jail-install-prefix"
	run := func(argv []string, _ time.Duration) ProbeResult {
		if strings.Contains(strings.Join(argv, " "), refused) {
			return ProbeResult{Ran: true, RC: 1} // nix: "path is still alive"
		}
		return ProbeResult{Ran: true, RC: 0}
	}
	got := DeleteSupersededStoreOutputs([]string{refused, ok}, true, run)
	if len(got) != 1 || got[0] != ok {
		t.Fatalf("removed %v, want only %q — a path nix refuses is the liveness veto working", got, ok)
	}
}
