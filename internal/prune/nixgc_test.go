package prune

import (
	"os"
	"path/filepath"
	"reflect"
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
