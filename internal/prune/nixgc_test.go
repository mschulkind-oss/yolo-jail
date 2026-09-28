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
// durably §1-rooted.
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

// TestUnrootedRunningImagesSeesWhatTheSentinelCannot: the refusal asks the
// runtime, not the load sentinel, so a jail that has been up for days (and aged
// out of the sentinel's LRU) is still seen. Its root is gone — a registration
// that failed, or one that aged out before OQ-LS4 held running roots — so the
// GC must refuse: liveness can hold a root, never create one.
func TestUnrootedRunningImagesSeesWhatTheSentinelCannot(t *testing.T) {
	bd := t.TempDir()
	roots := filepath.Join(bd, "roots")
	if err := os.MkdirAll(roots, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/still-rooted", filepath.Join(roots, "aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}

	refs := []string{"localhost/yolo-jail:bbbbbbbbbbbbbbbb"}
	got := UnrootedRunningImages(bd, refs)
	if len(got) != 1 || !strings.HasPrefix(got[0], refs[0]+" (no durable GC root") {
		t.Fatalf("UnrootedRunningImages(%v) = %v, want exactly that ref — a running container "+
			"whose closure has no root MUST refuse the store GC", refs, got)
	}
	if got := UnrootedRunningImages(bd, []string{"localhost/yolo-jail:aaaaaaaaaaaaaaaa"}); len(got) != 0 {
		t.Errorf("a rooted running image reported %v, want none", got)
	}
}

// testStockIdentity is a well-formed image identity for the stock-ref cases.
const testStockIdentity = "sha256:816a3ee9bbc3b73bef97cdadb88702eadc4ee53bdcefd1c24e0d349b9304fb20"

// TestUnrootedRunningImagesMapsAStockRefThroughItsRecord is the defect found
// 2026-09-28: a normal launch runs `stock-<hex>`, the old guard looked for
// roots/stock-<hex>, found nothing, and refused the GC while ANY normally
// launched jail ran. The stock ref now maps through the record its load wrote.
func TestUnrootedRunningImagesMapsAStockRefThroughItsRecord(t *testing.T) {
	bd := t.TempDir()
	sp := "/nix/store/aaaa-yolo-jail-image.json"
	ref := image.StockImageRef("podman", testStockIdentity)

	// No record: unmappable, refused, and the reason says so.
	got := UnrootedRunningImages(bd, []string{ref})
	if len(got) != 1 || !strings.Contains(got[0], "no build record") {
		t.Fatalf("an unrecorded stock ref reported %v, want one refusal naming the missing record", got)
	}

	// Recorded but unrooted: refused.
	if err := image.RecordStockStorePath(bd, testStockIdentity, sp); err != nil {
		t.Fatal(err)
	}
	got = UnrootedRunningImages(bd, []string{ref})
	if len(got) != 1 || !strings.Contains(got[0], "build/roots/"+image.ImageStoreKey(sp)) {
		t.Fatalf("a recorded but unrooted stock ref reported %v, want one refusal naming its root", got)
	}

	// Recorded and rooted: accepted.
	rootFor(t, filepath.Join(bd, "roots"), sp)
	if got := UnrootedRunningImages(bd, []string{ref}); len(got) != 0 {
		t.Fatalf("a rooted stock jail still refuses the GC: %v", got)
	}

	// A root pointing at a DIFFERENT path than the record names is not this
	// image's root.
	bd2 := t.TempDir()
	if err := image.RecordStockStorePath(bd2, testStockIdentity, sp); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bd2, "roots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/zzzz-other", filepath.Join(bd2, "roots", image.ImageStoreKey(sp))); err != nil {
		t.Fatal(err)
	}
	if got := UnrootedRunningImages(bd2, []string{ref}); len(got) != 1 {
		t.Errorf("a root naming another store path was accepted: %v", got)
	}
}

// TestUnrootedRunningImagesRefusesWhatItCannotMap: unknown is not permission.
// A ref with no content tag — a bare ID, the legacy `latest`, an image loaded
// outside yolo — maps to no closure, so it cannot be confirmed rooted and must
// read as a reason to decline rather than as a pass. This is the half of the
// refusal OQ-LS4 kept.
func TestUnrootedRunningImagesRefusesWhatItCannotMap(t *testing.T) {
	bd := t.TempDir()
	for _, ref := range []string{"localhost/yolo-jail:latest", "somerandomimage", "abc123def456",
		"docker.io/library/postgres:16", "registry:5000/thing"} {
		if got := UnrootedRunningImages(bd, []string{ref}); len(got) != 1 {
			t.Errorf("ref %q reported %v, want one unmappable entry — treating an unmappable "+
				"running container as rooted is the fail-open this gate exists to avoid", ref, got)
		}
	}
}

// TestLiveImageRootKeys is the liveness set the GC-root reaper spares: content
// refs by their tag, stock refs through their record, foreign containers
// ignored, and an unmappable JAIL or an unanswerable runtime reaping nothing.
func TestLiveImageRootKeys(t *testing.T) {
	bd := t.TempDir()
	sp := "/nix/store/aaaa-yolo-jail-image.json"
	if err := image.RecordStockStorePath(bd, testStockIdentity, sp); err != nil {
		t.Fatal(err)
	}
	stock := image.StockImageRef("podman", testStockIdentity)
	ps := func(stdout string, ran bool) RunFunc {
		return func(argv []string, _ time.Duration) ProbeResult {
			if strings.Join(argv, " ") != "podman ps --format {{.Image}}" {
				t.Errorf("unexpected argv %q", argv)
			}
			return ProbeResult{Stdout: stdout, Ran: ran}
		}
	}

	keys, known, why := LiveImageRootKeys("podman", bd,
		ps("localhost/yolo-jail:0123456789abcdef\n"+stock+"\ndocker.io/library/postgres:16\n", true))
	want := map[string]bool{"0123456789abcdef": true, image.ImageStoreKey(sp): true}
	if !known || !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys=%v known=%v why=%q, want %v", keys, known, why, want)
	}

	for _, tc := range []struct{ name, stdout string }{
		{"legacy latest", "localhost/yolo-jail:latest\n"},
		{"unrecorded stock", image.StockImageRef("podman", "sha256:"+strings.Repeat("0", 64)) + "\n"},
		{"bare id", "abc123def456\n"},
	} {
		if _, known, why := LiveImageRootKeys("podman", bd, ps(tc.stdout, true)); known || why == "" {
			t.Errorf("%s: known=%v why=%q — a running jail yolo cannot map could be on any root", tc.name, known, why)
		}
	}
	if _, known, why := LiveImageRootKeys("podman", bd, ps("", false)); known || !strings.Contains(why, "could not list") {
		t.Errorf("unanswerable runtime: known=%v why=%q", known, why)
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
	for _, want := range []string{"RunningImageRefs(", "UnrootedRunningImages(opts.BuildDir(), runningRefs)"} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("prunecmd.go no longer calls %s — the store GC is back to confirming rooting "+
				"from the load sentinel alone, which cannot see a jail that has aged out of the "+
				"LRU-10. Since OQ-LS1 that jail's root is also reaped on age, so the two gaps "+
				"compose into deleting a live jail's closure. If the call moved, move this pin "+
				"with it rather than deleting it.", want)
		}
	}
}

// TestTheRootReaperIsHandedTheRuntimesLivenessSet is the CALL-SITE pin for
// OQ-LS4. The unit tests prove PruneOrphanImageRoots spares a running image's
// root when it is TOLD which roots are live; nothing there fails if prunecmd.go
// stops asking the runtime and passes an empty set, which silently reinstates
// the age-only reaper that let a running jail's closure be collected.
func TestTheRootReaperIsHandedTheRuntimesLivenessSet(t *testing.T) {
	src, err := os.ReadFile("prunecmd.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"liveKeys, keysKnown, why := LiveImageRootKeys(rt, opts.BuildDir(), opts.Exec)",
		"PruneOrphanImageRoots(joinPath(opts.BuildDir(), \"roots\"),\n\t\t\tliveKeys, keysKnown,",
	} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("prunecmd.go no longer contains %q — the GC-root reaper is no longer "+
				"handed the runtime's liveness set (OQ-LS4). If the call moved, move this pin with it.", want)
		}
	}
}

// TestRunningImageRefsAsksAppleContainerItsOwnWay: `container` has no `ps
// --format`, so without its own argv the GC-root reaper would decline on every
// Apple Container host and never reap an image root there again.
func TestRunningImageRefsAsksAppleContainerItsOwnWay(t *testing.T) {
	var argv []string
	refs, known := RunningImageRefs("container", func(a []string, _ time.Duration) ProbeResult {
		argv = a
		return ProbeResult{Ran: true, Stdout: "ID IMAGE OS ARCH STATE ADDR\n" +
			"yolo-mac-1 yolo-jail:0123456789abcdef linux arm64 running 192.168.64.9/24\n"}
	})
	if !known || !reflect.DeepEqual(refs, []string{"yolo-jail:0123456789abcdef"}) {
		t.Fatalf("refs=%v known=%v", refs, known)
	}
	if strings.Join(argv, " ") != "container ls" {
		t.Errorf("argv = %q, want `container ls`", argv)
	}
	keys, known, why := LiveImageRootKeys("container", t.TempDir(), func([]string, time.Duration) ProbeResult {
		return ProbeResult{Ran: true, Stdout: "ID IMAGE STATE\n"}
	})
	if !known || len(keys) != 0 {
		t.Errorf("an idle Apple Container host: keys=%v known=%v why=%q", keys, known, why)
	}
}
