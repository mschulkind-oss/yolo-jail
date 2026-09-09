package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// rootFor creates BUILD_DIR/roots/<sha16> → storePath so the path counts as
// durably §1-rooted for UnrootedProtectedPaths.
func rootFor(t *testing.T, rootsDir, storePath string) {
	t.Helper()
	if err := os.MkdirAll(rootsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootsDir, image.ImageStoreKey(storePath))
	if err := os.Symlink(storePath, link); err != nil {
		t.Fatal(err)
	}
}

func TestUnrootedProtectedPaths(t *testing.T) {
	rootsDir := filepath.Join(t.TempDir(), "roots")
	rooted := "/nix/store/aaaa-stream-yolo-jail"
	unrooted := "/nix/store/bbbb-stream-yolo-jail"
	rootFor(t, rootsDir, rooted)

	// A stale root pointing at the WRONG path must not count as protecting `wrong`.
	wrong := "/nix/store/cccc-stream-yolo-jail"
	staleLink := filepath.Join(rootsDir, image.ImageStoreKey(wrong))
	if err := os.Symlink("/nix/store/dddd-other", staleLink); err != nil {
		t.Fatal(err)
	}

	got := UnrootedProtectedPaths(rootsDir, map[string]struct{}{
		rooted: {}, unrooted: {}, wrong: {},
	})
	want := []string{unrooted, wrong} // sorted; bbbb < cccc
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unrooted = %v, want %v", got, want)
	}
}

func TestUnrootedProtectedPathsAllRooted(t *testing.T) {
	rootsDir := filepath.Join(t.TempDir(), "roots")
	a := "/nix/store/aaaa-stream-yolo-jail"
	b := "/nix/store/bbbb-stream-yolo-jail"
	rootFor(t, rootsDir, a)
	rootFor(t, rootsDir, b)
	if got := UnrootedProtectedPaths(rootsDir, map[string]struct{}{a: {}, b: {}}); len(got) != 0 {
		t.Errorf("all rooted → want empty, got %v", got)
	}
}

func TestUnrootedProtectedPathsNoRootsDir(t *testing.T) {
	// No roots dir at all → every protected path is unrooted (the pre-§1 state).
	rootsDir := filepath.Join(t.TempDir(), "does-not-exist")
	a := "/nix/store/aaaa-stream-yolo-jail"
	if got := UnrootedProtectedPaths(rootsDir, map[string]struct{}{a: {}}); !reflect.DeepEqual(got, []string{a}) {
		t.Errorf("no roots dir → want [%q], got %v", a, got)
	}
}

// fakeGCExec returns a RunFunc that asserts the argv shape (dry-run vs --max) and
// returns canned stdout.
func fakeGCExec(t *testing.T, wantApply bool, stdout string, ran bool, rc int) RunFunc {
	t.Helper()
	return func(argv []string, _ time.Duration) ProbeResult {
		joined := ""
		for _, a := range argv {
			joined += a + " "
		}
		hasMax := containsStr(argv, "--max")
		hasDry := containsStr(argv, "--dry-run")
		if wantApply && (!hasMax || hasDry) {
			t.Errorf("apply must pass --max and NOT --dry-run; argv=%q", joined)
		}
		if !wantApply && (hasMax || !hasDry) {
			t.Errorf("dry-run must pass --dry-run and NOT --max; argv=%q", joined)
		}
		return ProbeResult{Stdout: stdout, Ran: ran, RC: rc}
	}
}

func containsStr(argv []string, s string) bool {
	for _, a := range argv {
		if a == s {
			return true
		}
	}
	return false
}

func TestRunNixStoreGCDryRun(t *testing.T) {
	out := RunNixStoreGC(fakeGCExec(t, false, "finding roots...\n2147 store paths would be deleted\n", true, 0), 50<<30, false)
	if !out.Ran || out.Paths != 2147 {
		t.Fatalf("dry-run outcome = %+v", out)
	}
	if out.HaveBytes {
		t.Errorf("dry-run must not report a byte figure, got %d", out.Bytes)
	}
}

func TestRunNixStoreGCApplyWithFreed(t *testing.T) {
	out := RunNixStoreGC(fakeGCExec(t, true, "1234 store paths deleted, 12.5 GiB freed\n", true, 0), 50<<30, true)
	if !out.Ran || out.Paths != 1234 {
		t.Fatalf("apply outcome = %+v", out)
	}
	if !out.HaveBytes || out.Bytes != int64(12.5*float64(1<<30)) {
		t.Errorf("freed bytes = %d (have=%v), want ~13421772800", out.Bytes, out.HaveBytes)
	}
}

func TestRunNixStoreGCSingularAndNoFreed(t *testing.T) {
	// Singular "1 store path deleted" phrasing, no freed clause.
	out := RunNixStoreGC(fakeGCExec(t, true, "1 store path deleted\n", true, 0), 1<<30, true)
	if !out.Ran || out.Paths != 1 || out.HaveBytes {
		t.Errorf("singular outcome = %+v", out)
	}
}

func TestRunNixStoreGCDegrade(t *testing.T) {
	// nix absent / daemon unreachable → Ran=false, not a bogus zero.
	if out := RunNixStoreGC(fakeGCExec(t, false, "", false, 0), 1<<30, false); out.Ran {
		t.Errorf("degrade must yield Ran=false, got %+v", out)
	}
	// Ran but non-zero exit → also a degrade.
	if out := RunNixStoreGC(fakeGCExec(t, false, "error: ...", true, 1), 1<<30, false); out.Ran {
		t.Errorf("non-zero exit must yield Ran=false, got %+v", out)
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := []struct {
		num, unit string
		want      int64
		ok        bool
	}{
		{"512", "B", 512, true},
		{"512", "bytes", 512, true},
		{"2", "KiB", 2048, true},
		{"1.5", "MiB", int64(1.5 * float64(1<<20)), true},
		{"3", "GiB", 3 << 30, true},
		{"1", "TiB", 1 << 40, true},
		{"x", "GiB", 0, false},
		{"1", "PiB", 0, false},
	}
	for _, c := range cases {
		got, ok := parseHumanBytes(c.num, c.unit)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseHumanBytes(%q,%q) = (%d,%v), want (%d,%v)", c.num, c.unit, got, ok, c.want, c.ok)
		}
	}
}

// TestUnrootedRunningImagesSeesWhatTheSentinelCannot is the hole OQ-LS1 opens
// and this gate closes, stated as the scenario rather than as a unit shape: a
// jail that has been up for days is not in the load sentinel's LRU-10, and
// since LS1 its GC root is reaped on age like any other — so before this gate,
// `--nix-gc --apply` would delete the closure its /bin/* resolve through.
func TestUnrootedRunningImagesSeesWhatTheSentinelCannot(t *testing.T) {
	roots := t.TempDir()
	// One root exists (a recently-used image); the long-running jail's does not.
	if err := os.Symlink("/nix/store/still-rooted", filepath.Join(roots, "aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}

	// The sentinel-based check is blind to the running jail: its path is not in
	// the LRU, so `protected` is empty and it reports nothing wrong.
	if got := UnrootedProtectedPaths(roots, map[string]struct{}{}); len(got) != 0 {
		t.Fatalf("sentinel check reported %v for an empty LRU — the premise of this test is that it says nothing", got)
	}

	// The runtime-based check sees it, because the container is there to be asked.
	refs := []string{"localhost/yolo-jail:bbbbbbbbbbbbbbbb"}
	got := UnrootedRunningImages(roots, refs)
	if len(got) != 1 || got[0] != refs[0] {
		t.Fatalf("UnrootedRunningImages(%v) = %v, want exactly that ref — a running container "+
			"whose closure has no root MUST refuse the store GC (LS1 removed the root reaper's "+
			"liveness veto on the grounds that unrooting costs only a rebuild; DELETING the "+
			"closure does not)", refs, got)
	}

	// A running image that IS rooted raises nothing.
	if got := UnrootedRunningImages(roots, []string{"localhost/yolo-jail:aaaaaaaaaaaaaaaa"}); len(got) != 0 {
		t.Errorf("a rooted running image reported %v, want none", got)
	}
}

// TestUnrootedRunningImagesRefusesWhatItCannotMap: unknown is not permission.
// A ref with no content tag — a bare ID, the legacy `latest`, an image loaded
// outside yolo — maps to no closure, so it cannot be confirmed rooted and must
// read as a reason to decline rather than as a pass.
func TestUnrootedRunningImagesRefusesWhatItCannotMap(t *testing.T) {
	roots := t.TempDir()
	for _, ref := range []string{"localhost/yolo-jail:latest", "somerandomimage", "abc123def456"} {
		if got := UnrootedRunningImages(roots, []string{ref}); len(got) != 1 {
			t.Errorf("ref %q reported %v, want one unmappable entry — treating an unmappable "+
				"running container as rooted is the fail-open this gate exists to avoid", ref, got)
		}
	}
}

// TestStoreGCConsultsTheRuntime is the CALL-SITE pin. The two tests above prove
// UnrootedRunningImages works; neither would notice it vanishing from the store
// GC section, and the regression that follows is silent — a `--nix-gc --apply`
// that deletes a long-running jail's closure, which is the incident this whole
// design exists to prevent, reintroduced from the other end.
//
// Source-reading is this repo's answer for a call site a unit test cannot reach
// (the methodDecl pattern in configapproval_test.go). It pins the CALL, not the
// wording around it.
func TestStoreGCConsultsTheRuntime(t *testing.T) {
	src, err := os.ReadFile("prunecmd.go")
	if err != nil {
		t.Fatalf("read prunecmd.go: %v", err)
	}
	for _, want := range []string{"RunningImageRefs(", "UnrootedRunningImages("} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("prunecmd.go no longer calls %s — the store GC is back to confirming rooting "+
				"from the load sentinel alone, which cannot see a jail that has aged out of the "+
				"LRU-10. Since OQ-LS1 that jail's root is also reaped on age, so the two gaps "+
				"compose into deleting a live jail's closure. If the call moved, move this pin "+
				"with it rather than deleting it.", want)
		}
	}
}
