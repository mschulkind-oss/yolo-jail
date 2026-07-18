package prunecmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// stubExec builds a prune.RunFunc keyed by the joined argv, returning canned
// stdout (RC=0, Ran=true). rm/rmi calls (mutations) are recorded when a sink is
// passed. An unmapped argv returns Ran=true, RC=0, "".
func stubExec(mapping map[string]string, calls *[]string) prune.RunFunc {
	return func(argv []string, _ time.Duration) prune.ProbeResult {
		if calls != nil && len(argv) >= 2 && (argv[1] == "rm" || argv[1] == "rmi") {
			*calls = append(*calls, strings.Join(argv, " "))
		}
		return prune.ProbeResult{Stdout: mapping[strings.Join(argv, "\x00")], RC: 0, Ran: true}
	}
}

func k(argv ...string) string { return strings.Join(argv, "\x00") }

// baseOpts returns Options wired to a runtime that reports nothing and storage
// roots under a fresh temp dir. The caller populates the tree / mapping.
func baseOpts(t *testing.T) (Options, string) {
	t.Helper()
	gs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gs, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gs, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	relayBase := t.TempDir()
	o := NewDefaultOptions()
	o.DetectRuntime = func() string { return "podman" }
	o.Exec = stubExec(map[string]string{}, nil)
	o.Now = func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }
	o.GlobalStorage = func() string { return gs }
	o.GlobalHome = func() string { return filepath.Join(gs, "home") }
	o.GlobalCache = func() string { return filepath.Join(gs, "cache") }
	o.RelayBase = relayBase
	o.RelayKill = func(string) {}
	return o, gs
}

func lines(buf *bytes.Buffer) []string {
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

func hasLine(buf *bytes.Buffer, want string) bool {
	for _, l := range lines(buf) {
		if l == want {
			return true
		}
	}
	return false
}

// TestDryRunEmptyEnv: the baseline empty-storage dry-run report has every
// section header, ends with the DRY-RUN summary, and touches nothing.
func TestDryRunEmptyEnv(t *testing.T) {
	o, _ := baseOpts(t)
	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, want := range []string{
		"yolo prune (DRY-RUN)",
		"Runtime: podman  Workspaces tracked: 0",
		"No yolo-* containers found — nothing to dedupe across.",
		"Stopped yolo-* containers",
		"Orphaned broker relays",
		"Old yolo-jail images  (keep=2)",
		"Cached image tarballs  (keep=3)",
		"Orphaned build-root generations",
		"Shadowed seed subtrees",
		"  targets: .cache, .npm, .npm-global, .local, go (each overlay-masked at runtime)",
		"Cache purge  (subdirs=uv,pip,npm,go-build,mise,pex,pants,node-gyp,gopls, age > 30d)",
	} {
		if !hasLine(&buf, want) {
			t.Errorf("missing line %q in:\n%s", want, buf.String())
		}
	}
	last := lines(&buf)[len(lines(&buf))-1]
	wantSummary := "DRY-RUN: would reclaim 0 B via 0 hardlinks, remove 0 container(s), 0 image(s), 0 image tar(s), 0 build-root generation(s), 0 shadowed seed path(s), 0 cache file(s).  Re-run with --apply to execute."
	if last != wantSummary {
		t.Errorf("summary =\n%q\nwant\n%q", last, wantSummary)
	}
}

// TestDryRunReportsButDoesNotMutate: a populated tree reports reclaim numbers
// and removed-name lists but leaves every file/container in place.
func TestDryRunReportsButDoesNotMutate(t *testing.T) {
	o, gs := baseOpts(t)

	// Old cache file (past the 30d cutoff) under a purgeable subdir.
	uvCache := filepath.Join(gs, "cache", "uv")
	mustMkdir(t, uvCache)
	oldFile := filepath.Join(uvCache, "wheel.whl")
	mustWrite(t, oldFile, bytes.Repeat([]byte("x"), 2048))
	backdate(t, oldFile, o.Now().Add(-60*24*time.Hour))

	// Two image tarballs (keep=3 → none removed) + an orphan tmp (always swept).
	imagesDir := filepath.Join(gs, "cache", "images")
	mustMkdir(t, imagesDir)
	mustWrite(t, filepath.Join(imagesDir, "a.tar"), bytes.Repeat([]byte("t"), 1024))
	orphanTmp := filepath.Join(imagesDir, "crashed.tmp")
	mustWrite(t, orphanTmp, bytes.Repeat([]byte("t"), 512))

	// Shadowed .cache seed dir with content.
	shadowed := filepath.Join(gs, "home", ".cache", "junk")
	mustMkdir(t, shadowed)
	mustWrite(t, filepath.Join(shadowed, "blob"), bytes.Repeat([]byte("z"), 4096))

	// Stopped + running containers, plus an old image.
	rmCalls := []string{}
	mapping := map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}}"):                                                 "",
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"):                                      "yolo-dead-1 Exited\nyolo-live-2 Running\n",
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): "id1 yolo-jail:latest 2026-07-01 09:00:00 +0000 UTC\nid2 yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\nid3 yolo-jail:latest 2026-07-10 09:00:00 +0000 UTC\n",
	}
	o.Exec = stubExec(mapping, &rmCalls)
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	// Removed-name lists (dry-run verbs).
	if !hasLine(&buf, "  would remove: 1") || !hasLine(&buf, "    • yolo-dead-1") {
		t.Errorf("stopped-container dry-run wrong:\n%s", buf.String())
	}
	if !hasLine(&buf, "    • id1") {
		t.Errorf("old-image dry-run should list id1 (oldest, keep=2):\n%s", buf.String())
	}
	// Image cache: keep=3, 1 tar + 1 orphan tmp → only the tmp (512 B) removed.
	if !hasLine(&buf, "  would remove: 512 B across 1 file(s)") {
		t.Errorf("image-cache dry-run wrong:\n%s", buf.String())
	}
	// Cache purge: the 2048 B wheel.
	if !hasLine(&buf, "  would remove: 2.0 KiB across 1 files") {
		t.Errorf("cache-purge dry-run wrong:\n%s", buf.String())
	}
	// Shadowed: 4096 B across 1 path.
	if !hasLine(&buf, "  would remove: 4.0 KiB across 1 path(s)") {
		t.Errorf("shadowed dry-run wrong:\n%s", buf.String())
	}

	// No mutations.
	if len(rmCalls) != 0 {
		t.Errorf("dry-run made rm/rmi calls: %v", rmCalls)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Error("dry-run deleted the cache file")
	}
	if _, err := os.Stat(orphanTmp); err != nil {
		t.Error("dry-run deleted the orphan tmp")
	}
	if _, err := os.Stat(shadowed); err != nil {
		t.Error("dry-run deleted the shadowed dir")
	}
}

// TestApplyOnTempRoot: --apply actually reclaims — on a temp storage root only.
// The shadowed dir is EMPTIED but PRESERVED (mount-anchor discipline).
func TestApplyOnTempRoot(t *testing.T) {
	o, gs := baseOpts(t)
	o.Apply = true

	uvCache := filepath.Join(gs, "cache", "uv")
	mustMkdir(t, uvCache)
	oldFile := filepath.Join(uvCache, "wheel.whl")
	mustWrite(t, oldFile, bytes.Repeat([]byte("x"), 2048))
	backdate(t, oldFile, o.Now().Add(-60*24*time.Hour))

	imagesDir := filepath.Join(gs, "cache", "images")
	mustMkdir(t, imagesDir)
	orphanTmp := filepath.Join(imagesDir, "crashed.tmp")
	mustWrite(t, orphanTmp, bytes.Repeat([]byte("t"), 512))

	shadowedDir := filepath.Join(gs, "home", ".cache")
	shadowedChild := filepath.Join(shadowedDir, "junk")
	mustMkdir(t, shadowedChild)
	mustWrite(t, filepath.Join(shadowedChild, "blob"), bytes.Repeat([]byte("z"), 4096))

	rmCalls := []string{}
	mapping := map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"):                                      "yolo-dead-1 Exited\n",
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): "id1 yolo-jail:latest 2026-07-01 09:00:00 +0000 UTC\nid2 yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\nid3 yolo-jail:latest 2026-07-10 09:00:00 +0000 UTC\n",
	}
	o.Exec = stubExec(mapping, &rmCalls)
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	// Applied verbs + summary.
	if !hasLine(&buf, "  removed: 1") {
		t.Errorf("apply stopped-container verb wrong:\n%s", buf.String())
	}
	if !strings.HasPrefix(lines(&buf)[len(lines(&buf))-1], "Reclaimed ") {
		t.Errorf("apply summary should start 'Reclaimed':\n%s", buf.String())
	}

	// Mutations happened.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("apply should delete the old cache file")
	}
	if _, err := os.Stat(orphanTmp); !os.IsNotExist(err) {
		t.Error("apply should sweep the orphan tmp")
	}
	// Shadowed dir preserved, contents gone (mount-anchor discipline).
	if _, err := os.Stat(shadowedDir); err != nil {
		t.Error("apply must PRESERVE the shadowed .cache dir (mount anchor)")
	}
	if _, err := os.Stat(shadowedChild); !os.IsNotExist(err) {
		t.Error("apply must delete the shadowed dir's CONTENTS")
	}
	// rm called for the stopped container; rmi -f for the 1 old image (id1).
	if !containsCall(rmCalls, "podman rm yolo-dead-1") {
		t.Errorf("expected 'podman rm yolo-dead-1' in %v", rmCalls)
	}
	if !containsCall(rmCalls, "podman rmi -f id1") {
		t.Errorf("expected 'podman rmi -f id1' in %v", rmCalls)
	}
}

// TestFlagGating: each --no-* flag suppresses its section; --cache-age 0 skips
// the cache purge entirely.
func TestFlagGating(t *testing.T) {
	o, _ := baseOpts(t)
	o.NoContainers = true
	o.NoImages = true
	o.NoImageCache = true
	o.NoBuildRoots = true
	o.NoShadowedHome = true
	o.NoHardlink = true
	o.CacheAge = 0
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	for _, gone := range []string{
		"Stopped yolo-* containers",
		"Old yolo-jail images",
		"Cached image tarballs",
		"Orphaned build-root generations",
		"Shadowed seed subtrees",
		"Hardlink dedup",
		"Cache purge",
	} {
		for _, l := range lines(&buf) {
			if strings.HasPrefix(l, gone) {
				t.Errorf("section %q should be gated off:\n%s", gone, buf.String())
			}
		}
	}
	// Orphaned broker relays is UNGATED (no flag) — always present.
	if !hasLine(&buf, "Orphaned broker relays") {
		t.Errorf("relay section should always run:\n%s", buf.String())
	}
}

// TestBuildRootDeclineWhenUnknown: when the runtime can't be enumerated
// (Ran=false), the build-root sweep AND the relay sweep decline (fail-safe),
// printing the skip line rather than deleting.
func TestBuildRootDeclineWhenUnknown(t *testing.T) {
	o, gs := baseOpts(t)
	// An old orphan generation that WOULD be swept if liveness were known.
	old := filepath.Join(gs, "nix-build-root.old.deadbeef")
	mustMkdir(t, old)
	mustWrite(t, filepath.Join(old, "f"), []byte("x"))
	backdate(t, old, o.Now().Add(-48*time.Hour))

	o.Exec = func([]string, time.Duration) prune.ProbeResult { return prune.ProbeResult{Ran: false} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	if !hasLine(&buf, "  skipped — could not enumerate running jails (podman); declining to sweep") {
		t.Errorf("expected decline line (appears for relays AND build-roots):\n%s", buf.String())
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("declined sweep must not delete the orphan generation")
	}
}

func TestFmtComma(t *testing.T) {
	cases := map[int]string{0: "0", 5: "5", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1500: "-1,500"}
	for in, want := range cases {
		if got := fmtComma(in); got != want {
			t.Errorf("fmtComma(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- live-Python differential parity ---

// TestParityVsLivePython runs the native Run and the live-Python
// tests/oracles/prune_oracle.py over the SAME populated storage tree + runtime
// mapping, asserting byte-identical ANSI-stripped output. Dry-run only (apply
// mutates the shared tree). SKIPs when python is unavailable.
func TestParityVsLivePython(t *testing.T) {
	root := repoRoot(t)
	py := pythonRunner(t, root)
	if py == nil {
		t.Skip("python oracle unavailable (uv/python3 not found)")
	}

	gs := t.TempDir()
	mustMkdir(t, filepath.Join(gs, "cache", "uv"))
	mustMkdir(t, filepath.Join(gs, "cache", "images"))
	mustMkdir(t, filepath.Join(gs, "home"))

	fixedNow := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	// Old cache wheel (purged), fresh cache file (kept), image tarballs + orphan
	// tmp, shadowed seed content, a stray top-level file, an old build-root
	// generation (swept when liveness known-empty).
	oldWheel := filepath.Join(gs, "cache", "uv", "old.whl")
	mustWrite(t, oldWheel, bytes.Repeat([]byte("x"), 3000))
	backdate(t, oldWheel, fixedNow.Add(-90*24*time.Hour))
	freshWheel := filepath.Join(gs, "cache", "uv", "fresh.whl")
	mustWrite(t, freshWheel, bytes.Repeat([]byte("y"), 1500))
	backdate(t, freshWheel, fixedNow.Add(-1*24*time.Hour))

	mustWrite(t, filepath.Join(gs, "cache", "images", "a.tar"), bytes.Repeat([]byte("t"), 5000))
	mustWrite(t, filepath.Join(gs, "cache", "images", "b.tmp"), bytes.Repeat([]byte("t"), 700))

	shadowedChild := filepath.Join(gs, "home", ".npm", "cacache")
	mustMkdir(t, shadowedChild)
	mustWrite(t, filepath.Join(shadowedChild, "blob"), bytes.Repeat([]byte("z"), 8192))

	mustWrite(t, filepath.Join(gs, "strayfile"), bytes.Repeat([]byte("s"), 42))

	oldGen := filepath.Join(gs, "nix-build-root.old.cafebabe")
	mustMkdir(t, oldGen)
	mustWrite(t, filepath.Join(oldGen, "store-thing"), bytes.Repeat([]byte("g"), 10000))
	backdate(t, oldGen, fixedNow.Add(-48*time.Hour))

	// Runtime mapping: a stopped + a live container, three yolo-jail images.
	// No /workspace mounts (workspaces empty keeps the dedup tree hermetic).
	mapping := map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}}"):                                                 "",
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"):                                      "yolo-dead-1 Exited\nyolo-live-2 Running\nother Exited\n",
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): "id1 yolo-jail:latest 2026-07-01 09:00:00 +0000 UTC\nid2 yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\nid3 yolo-jail:latest 2026-07-10 09:00:00 +0000 UTC\nid4 yolo-jail:latest 2026-06-15 09:00:00 +0000 UTC\n",
	}

	// --- Go side ---
	var goBuf bytes.Buffer
	o := NewDefaultOptions()
	o.DetectRuntime = func() string { return "podman" }
	o.Exec = stubExec(mapping, nil)
	o.Now = func() time.Time { return fixedNow }
	o.GlobalStorage = func() string { return gs }
	o.GlobalHome = func() string { return filepath.Join(gs, "home") }
	o.GlobalCache = func() string { return filepath.Join(gs, "cache") }
	o.RelayBase = t.TempDir() // empty → relay sweep reports none
	o.RelayKill = func(string) {}
	o.Out = &goBuf
	Run(o)

	// --- Python side (same tree, dry-run) ---
	payload := map[string]any{
		"gs":      gs,
		"home":    filepath.Join(gs, "home"),
		"cache":   filepath.Join(gs, "cache"),
		"runtime": "podman",
		"mapping": mappingForPython(mapping),
		"live":    []string{"yolo-live-2"},
		"flags":   map[string]any{},
	}
	payloadJSON, _ := json.Marshal(payload)
	cmd := py("tests/oracles/prune_oracle.py", string(payloadJSON))
	cmd.Dir = root
	var pyBuf bytes.Buffer
	cmd.Stdout = &pyBuf
	cmd.Stderr = &pyBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("python oracle failed: %v\n%s", err, pyBuf.String())
	}

	goLines := lines(&goBuf)
	pyLines := lines(&pyBuf)
	if len(goLines) != len(pyLines) {
		t.Fatalf("line count go=%d py=%d\n--- GO ---\n%s\n--- PY ---\n%s", len(goLines), len(pyLines), goBuf.String(), pyBuf.String())
	}
	for i := range goLines {
		if goLines[i] != pyLines[i] {
			t.Errorf("line %d differs:\n go=%q\n py=%q", i, goLines[i], pyLines[i])
		}
	}
}

// mappingForPython re-keys the \x00-joined mapping for JSON transport (the
// oracle splits on \x00). JSON can carry \x00 in strings, so pass through.
func mappingForPython(m map[string]string) map[string]string { return m }

// --- test file helpers ---

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func backdate(t *testing.T, p string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func pythonRunner(t *testing.T, root string) func(args ...string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}
