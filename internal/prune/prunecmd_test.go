package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// stubExec builds a RunFunc keyed by the joined argv, returning canned
// stdout (RC=0, Ran=true). rm/rmi calls (mutations) are recorded when a sink is
// passed. An unmapped argv returns Ran=true, RC=0, "".
func stubExec(mapping map[string]string, calls *[]string) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		if calls != nil && len(argv) >= 2 && (argv[1] == "rm" || argv[1] == "rmi") {
			*calls = append(*calls, strings.Join(argv, " "))
		}
		return ProbeResult{Stdout: mapping[strings.Join(argv, "\x00")], RC: 0, Ran: true}
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
	// Isolate the build/agents/containers dirs under the temp root too, so the
	// image-root reaper and agent-staging sweep never touch the real host store.
	o.BuildDir = func() string { return filepath.Join(gs, "build") }
	o.AgentsDir = func() string { return filepath.Join(gs, "agents") }
	o.ContainerDir = func() string { return filepath.Join(gs, "containers") }
	o.RelayBase = relayBase
	o.RelayKill = func(string) {}
	return o, gs
}

// mustPointAt records one workspace's current-image pointer — the retention
// evidence the old-image section reads (OQ-LS3). The workspace is a fresh temp
// dir because CurrentImageTags only honours a pointer whose workspace still
// EXISTS, so a fixture that skipped that would be pinning the wrong thing.
func mustPointAt(t *testing.T, buildDir, container, storePath string) {
	t.Helper()
	if err := RecordCurrentImage(buildDir, container, t.TempDir(), storePath); err != nil {
		t.Fatalf("record current image for %s: %v", container, err)
	}
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
		// The header reports the EVIDENCE, not a dial: `--keep-images` was removed
		// by OQ-LS3 and retention is one current-image pointer per workspace, so
		// what a reader needs in order to predict this section is how many
		// pointers it found. Zero on an empty storage root.
		"Old yolo-jail images  (keeping each workspace's current image: 0 pointer(s))",
		// keep=0, not 3: OQ-BF6 made the tar default per-RUNTIME, and this fixture
		// runs on podman, which streams into `podman load` and writes no tar at
		// all since C3. The Apple Container arm keeps 3 and is pinned separately
		// (TestImageCacheKeepDefaultsPerRuntime).
		"Cached image tarballs  (keep=0)",
		"Legacy build-root staging dirs",
		"Dangling build out-links",
		"Orphaned agent staging",
		"Superseded install captures",
		"Shadowed seed subtrees",
		"  targets: .cache, .npm, .npm-global, .local, go (each overlay-masked at runtime)",
		// `nce` joined the list under OQ-BF2 (1.86 GiB already dead here, covered
		// by nothing). `staticcheck` is still absent on purpose — it SELF-TRIMS,
		// so a 30-day rule there can only ever be a no-op that costs a walk.
		"Cache purge  (subdirs=uv,pip,npm,go-build,mise,pex,pants,node-gyp,gopls,nce, age > 30d)",
		"Agent log purge  (copilot/logs, gemini/tmp, gemini-cli/logs; age > 30d)",
		"  Claude transcripts (claude/projects) are durable user data — never purged",
	} {
		if !hasLine(&buf, want) {
			t.Errorf("missing line %q in:\n%s", want, buf.String())
		}
	}
	last := lines(&buf)[len(lines(&buf))-1]
	wantSummary := "DRY-RUN: would reclaim 0 B via 0 hardlinks, remove 0 container(s), 0 image(s), 0 image tar(s), 0 legacy build-root dir(s), 0 agent staging dir(s), 0 shadowed seed path(s), 0 cache file(s), 0 agent log file(s).  Re-run with --apply to execute."
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

	// CURRENT-IMAGE POINTERS. Not decoration: with no honourable pointer the
	// old-image pass fails safe and declines entirely (unknown ≠ "no workspace
	// wants anything"), so a fixture with images and no pointer asserts against a
	// state a real machine cannot reach — podman cannot hold a yolo-jail image
	// that no launch ever put there. Two workspaces point at id2 and id3, which
	// leaves id1 as the superseded copy this test expects to see selected.
	pathTwo, pathThree := "/nix/store/2222-ws-two", "/nix/store/3333-ws-three"
	mustPointAt(t, o.BuildDir(), "yolo-two-22222222", pathTwo)
	mustPointAt(t, o.BuildDir(), "yolo-three-33333333", pathThree)

	// Stopped + running containers, plus an old image.
	rmCalls := []string{}
	mapping := map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}}"):            "",
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "yolo-dead-1 Exited\nyolo-live-2 Running\n",
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
			"id2 localhost/yolo-jail:" + image.ImageStoreKey(pathTwo) + " 2026-07-18 09:00:00 +0000 UTC\n" +
			"id2 localhost/yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\n" +
			"id3 localhost/yolo-jail:" + image.ImageStoreKey(pathThree) + " 2026-07-10 09:00:00 +0000 UTC\n",
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
		t.Errorf("old-image dry-run should list id1 (the only image no workspace "+
			"points at):\n%s", buf.String())
	}
	// Image cache on PODMAN: keep=0 since OQ-BF6, so the 1024 B tar goes too, not
	// just the 512 B orphan tmp. This assertion used to read "512 B across 1
	// file(s)" under the old flat keep=3 and is REWRITTEN rather than repaired —
	// the retained tar is exactly what that ruling reclaims, since C3 stopped
	// writing one on this runtime at all.
	if !hasLine(&buf, "  would remove: 1.5 KiB across 2 file(s)") {
		t.Errorf("image-cache dry-run wrong (podman keeps zero tars since OQ-BF6):\n%s", buf.String())
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

	// See the note in TestDryRunReportsButDoesNotMutate: with no honourable
	// current-image pointer the old-image pass fails safe and removes nothing, so
	// a fixture that expects an `rmi` has to supply one. id2 and id3 are pointed
	// at; id1 is the superseded copy.
	pathTwo, pathThree := "/nix/store/2222-ws-two", "/nix/store/3333-ws-three"
	mustPointAt(t, o.BuildDir(), "yolo-two-22222222", pathTwo)
	mustPointAt(t, o.BuildDir(), "yolo-three-33333333", pathThree)

	rmCalls := []string{}
	mapping := map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "yolo-dead-1 Exited\n",
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
			"id2 localhost/yolo-jail:" + image.ImageStoreKey(pathTwo) + " 2026-07-18 09:00:00 +0000 UTC\n" +
			"id2 localhost/yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\n" +
			"id3 localhost/yolo-jail:" + image.ImageStoreKey(pathThree) + " 2026-07-10 09:00:00 +0000 UTC\n",
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
	// rm called for the stopped container; a plain (never forcing) rmi for the
	// 1 old image (id1).
	if !containsCall(rmCalls, "podman rm yolo-dead-1") {
		t.Errorf("expected 'podman rm yolo-dead-1' in %v", rmCalls)
	}
	if !containsCall(rmCalls, "podman rmi id1") {
		t.Errorf("expected 'podman rmi id1' in %v", rmCalls)
	}
}

// TestApplyNeverRemovesAWorkspacesCurrentImage pins the CALL SITE of the
// retention read, not the read itself: probes_test.go drives PruneOldImages
// directly and would stay green if prunecmd stopped passing CurrentImageTags.
// This one goes through Run, so an empty set at the call site fails it.
//
// The scenario is the one C2 created and measured on the maintainer's host the
// day it landed: several workspaces with different `packages:`, each with its
// own permanently tagged image. `yolo prune --apply` — which `yolo check`
// actively recommends — would remove the older ones, and while `rmi` still
// forced that took the running container with it, killing another workspace's
// session mid-flight.
//
// IT IS A STRONGER PIN THAN THE VERSION IT REPLACES. With `keep=2`, two of the
// three workspaces' images survived on age even with the protected set emptied,
// so only the third assertion could ever fire. Retention is now the pointers and
// nothing else: delete the CurrentImageTags call at the call site and ALL THREE
// live images are removed.
func TestApplyNeverRemovesAWorkspacesCurrentImage(t *testing.T) {
	o, _ := baseOpts(t)
	o.Apply = true

	// THREE workspaces, each with its own current-image pointer — one per
	// workspace is exactly what the launch path writes (OQ-LS3) — plus a cold
	// image nothing points at.
	const pathA = "/nix/store/aaaa-workspace-a"
	const pathC = "/nix/store/cccc-workspace-c"
	const pathB = "/nix/store/bbbb-workspace-b"
	const pathCold = "/nix/store/dddd-long-gone"
	mustPointAt(t, o.BuildDir(), "yolo-a-aaaaaaaa", pathA)
	mustPointAt(t, o.BuildDir(), "yolo-b-bbbbbbbb", pathB)
	mustPointAt(t, o.BuildDir(), "yolo-c-cccccccc", pathC)

	// Newest-first by CreatedAt: A (which also wears :latest), C, B, then the cold
	// image. B is the OLDEST live one, which is where the rule this replaced went
	// wrong — CreatedAt is when the archive was streamed, so the workspace that
	// has been on one image longest sorted first.
	rows := "idA localhost/yolo-jail:" + image.ImageStoreKey(pathA) + " 2026-07-18 09:00:00 +0000 UTC\n" +
		"idA localhost/yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\n" +
		"idC localhost/yolo-jail:" + image.ImageStoreKey(pathC) + " 2026-07-15 09:00:00 +0000 UTC\n" +
		"idB localhost/yolo-jail:" + image.ImageStoreKey(pathB) + " 2026-07-10 09:00:00 +0000 UTC\n" +
		"idCold localhost/yolo-jail:" + image.ImageStoreKey(pathCold) + " 2026-06-01 09:00:00 +0000 UTC\n"

	rmCalls := []string{}
	o.Exec = stubExec(map[string]string{
		k("podman", "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"): rows,
	}, &rmCalls)
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	for _, live := range []string{"podman rmi idA", "podman rmi idB", "podman rmi idC"} {
		if containsCall(rmCalls, live) {
			t.Errorf("prune removed an image a workspace still points at (%q):\n%v",
				live, rmCalls)
		}
	}
	// And retention is not "remove nothing": an image no workspace points at is
	// still reclaimed.
	if !containsCall(rmCalls, "podman rmi idCold") {
		t.Errorf("expected 'podman rmi idCold' in %v — the gate must still let "+
			"genuinely unused images go", rmCalls)
	}
	// And it is never the FORCING form: `rmi -f` removes the containers using an
	// image, which is how an auto-reap killed four jails that had been up for
	// days (2026-09-08).
	if containsCall(rmCalls, "podman rmi -f idCold") {
		t.Errorf("prune used the forcing form: %v", rmCalls)
	}
	if !hasLine(&buf, "    • idCold") {
		t.Errorf("the removed image was not reported:\n%s", buf.String())
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
		"Legacy build-root staging dirs",
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

// TestSweepsDeclineWhenLivenessUnknown: when the runtime can't be enumerated
// (Ran=false), the liveness-GATED sweeps (relays, agent-staging, image-roots)
// decline (fail-safe), printing the skip line. The legacy build-root sweep is
// NO LONGER liveness-gated — nothing binds those dirs any more — so it proceeds
// regardless, reporting the orphan in this dry-run without deleting it.
func TestSweepsDeclineWhenLivenessUnknown(t *testing.T) {
	o, gs := baseOpts(t)
	// An old legacy staging dir: the build-root sweep must report it even under
	// unknown liveness (no gate), but the dry-run must not delete it.
	old := filepath.Join(gs, "nix-build-root.old.deadbeef")
	mustMkdir(t, old)
	mustWrite(t, filepath.Join(old, "f"), []byte("x"))
	backdate(t, old, o.Now().Add(-48*time.Hour))

	o.Exec = func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	if !hasLine(&buf, "  skipped — could not enumerate running jails (podman); declining to sweep") {
		t.Errorf("expected decline line (appears for relays/agent-staging/image-roots):\n%s", buf.String())
	}
	// Build-root sweep ran despite unknown liveness: it reports the orphan.
	if !hasLine(&buf, "  would remove: 1 B across 1 dir(s)") {
		t.Errorf("legacy build-root sweep should proceed under unknown liveness:\n%s", buf.String())
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("dry-run must not delete the orphan staging dir")
	}
}

// TestPrinterColorGate: the prune printer routes through internal/richtext —
// color=true renders known style tags ([bold], [green]) to ANSI escapes;
// color=false strips them to plain text; a literal like [y/N] is preserved in
// both modes (it is not a known style tag, so the renderer must not touch it).
func TestPrinterColorGate(t *testing.T) {
	const (
		esc   = "\x1b["
		bold  = "\x1b[1m"
		green = "\x1b[32m"
		reset = "\x1b[0m"
	)

	t.Run("color renders ANSI", func(t *testing.T) {
		var buf bytes.Buffer
		p := &printer{richtext.Printer{W: &buf, Color: true}}
		p.line("[bold]hi[/bold] [green]ok[/green] keep [y/N]")
		got := buf.String()
		for _, want := range []string{bold, green, reset, "[y/N]"} {
			if !strings.Contains(got, want) {
				t.Errorf("color output %q missing %q", got, want)
			}
		}
		if strings.Contains(got, "[bold]") || strings.Contains(got, "[green]") {
			t.Errorf("color output should not contain raw style tags: %q", got)
		}
	})

	t.Run("no color strips to plain", func(t *testing.T) {
		var buf bytes.Buffer
		p := &printer{richtext.Printer{W: &buf, Color: false}}
		p.line("[bold]hi[/bold] [green]ok[/green] keep [y/N]")
		got := buf.String()
		if strings.Contains(got, esc) {
			t.Errorf("no-color output must not contain ANSI escapes: %q", got)
		}
		if want := "hi ok keep [y/N]\n"; got != want {
			t.Errorf("no-color output = %q, want %q", got, want)
		}
	})
}

// TestRunColorGateHonorsTTY: the Run-level gate emits ANSI only when Color is
// requested AND stdout is a TTY. Piped output (IsTTYStdout=false) stays byte-
// identical stripped text — the output-parity contract — even with Color=true.
func TestRunColorGateHonorsTTY(t *testing.T) {
	render := func(color, tty bool) string {
		o, _ := baseOpts(t)
		o.Color = color
		o.IsTTYStdout = func() bool { return tty }
		var buf bytes.Buffer
		o.Out = &buf
		Run(o)
		return buf.String()
	}

	// The bold "yolo prune (DRY-RUN)" header is the first rich line.
	color := render(true, true)
	if !strings.Contains(color, "\x1b[1m") {
		t.Errorf("Color+TTY should emit ANSI bold in header:\n%q", color)
	}

	// Color requested but piped: no ANSI, and byte-identical to the color-off run.
	piped := render(true, false)
	plain := render(false, false)
	if strings.Contains(piped, "\x1b[") {
		t.Errorf("Color without a TTY must not emit ANSI:\n%q", piped)
	}
	if piped != plain {
		t.Errorf("piped Color output must be byte-identical to color-off output\ncolor:\n%q\nplain:\n%q", piped, plain)
	}
}

// TestRelocatedCacheSection: a relocated subdir gets its own labelled section
// showing the real size, the target and its backing filesystem — and its bytes
// stay out of the "Current usage" totals (they are on another device, so a
// prune here cannot free them). The heavy purge announces the real target too.
func TestRelocatedCacheSection(t *testing.T) {
	o, gs := baseOpts(t)
	target := t.TempDir()
	o.CacheRelocations = map[string]string{"huggingface": target}
	o.PurgeHeavyCaches = true

	// The host-side stub podman mounts over: present, empty.
	mustMkdir(t, filepath.Join(gs, "cache", "huggingface"))
	// 8 KiB of "model" on the relocation target, plus 1 KiB that really is on
	// the storage filesystem so the totals are visibly different numbers.
	mustWrite(t, filepath.Join(target, "model.safetensors"), bytes.Repeat([]byte("m"), 8192))
	mustMkdir(t, filepath.Join(gs, "cache", "uv"))
	mustWrite(t, filepath.Join(gs, "cache", "uv", "wheel.whl"), bytes.Repeat([]byte("x"), 1024))

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	if !hasLine(&buf, "  relocated cache subdirs (cache_relocations — on other filesystems, NOT in the totals above):") {
		t.Errorf("missing relocated section header:\n%s", buf.String())
	}
	var row string
	for _, l := range lines(&buf) {
		if strings.HasPrefix(l, "    cache/huggingface") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no relocated row for huggingface:\n%s", buf.String())
	}
	for _, want := range []string{"8.0 KiB", "→ " + target, "(fs "} {
		if !strings.Contains(row, want) {
			t.Errorf("relocated row %q missing %q", row, want)
		}
	}
	// Totals see the 1 KiB wheel only — never the relocated 8 KiB.
	if !hasLine(&buf, "Current usage  total=1.0 KiB  (workspaces=0 B, global=1.0 KiB)") {
		t.Errorf("relocated bytes leaked into the totals:\n%s", buf.String())
	}
	// The purge pass says which real directory it is about to walk.
	if !hasLine(&buf, "  huggingface is relocated — purging "+target) {
		t.Errorf("missing relocated-purge note:\n%s", buf.String())
	}
}

// TestNoRelocatedSectionWhenUnset: with no cache_relocations the report is
// exactly what it was before the feature — no empty section, no stray header.
func TestNoRelocatedSectionWhenUnset(t *testing.T) {
	o, _ := baseOpts(t)
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	for _, l := range lines(&buf) {
		if strings.Contains(l, "relocated") {
			t.Errorf("unexpected relocation line with no relocations configured: %q", l)
		}
	}
}

// TestImagesHintNamesCacheRelocations: the over-threshold cache/images hint must
// NOT read as a general "symlink your cache subdirs" technique — that breaks
// every subdir the jail actually uses. It stays true for images (host-side
// reads only) and points at cache_relocations for everything else.
func TestImagesHintNamesCacheRelocations(t *testing.T) {
	o, gs := baseOpts(t)
	imagesDir := filepath.Join(gs, "cache", "images")
	mustMkdir(t, imagesDir)
	// Sparse: the hint threshold is 20 GiB and dirSizeBytes reads st_size, so
	// no real bytes are written.
	f, err := os.Create(filepath.Join(imagesDir, "yolo-jail.tar"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(imagesHintThreshold + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)

	var hint, follow string
	for _, l := range lines(&buf) {
		if strings.HasPrefix(l, "  hint: cache/images") {
			hint = l
		}
		if strings.HasPrefix(l, "  cache/images is only ever read host-side") {
			follow = l
		}
	}
	if hint == "" || follow == "" {
		t.Fatalf("images hint missing:\n%s", buf.String())
	}
	if strings.Contains(hint, "symlink") {
		t.Errorf("the headline hint must not sell symlinking as the technique: %q", hint)
	}
	for _, want := range []string{"cache_relocations", "dangles", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(follow, want) {
			t.Errorf("hint follow-up %q missing %q", follow, want)
		}
	}
}

// TestNixGCOffByDefault: without --nix-gc the store-GC section never appears,
// and the seam is never called.
func TestNixGCOffByDefault(t *testing.T) {
	o, _ := baseOpts(t)
	called := false
	o.NixStoreGC = func(int64, bool) StoreGCOutcome { called = true; return StoreGCOutcome{} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if called {
		t.Error("store GC seam must not run when --nix-gc is off")
	}
	for _, l := range lines(&buf) {
		if strings.Contains(l, "Nix store GC") {
			t.Errorf("store-GC section must be absent by default: %q", l)
		}
	}
}

// TestNixGCRefusesInJail: even with --nix-gc, running inside a jail refuses —
// the host store must never be GC'd blind from in-jail.
func TestNixGCRefusesInJail(t *testing.T) {
	o, _ := baseOpts(t)
	o.NixGC = true
	o.InJail = func() bool { return true }
	called := false
	o.NixStoreGC = func(int64, bool) StoreGCOutcome { called = true; return StoreGCOutcome{} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if called {
		t.Error("in-jail must refuse to invoke the store GC")
	}
	if !hasLine(&buf, "  skipped — refusing to GC the host store from inside a jail; "+
		"the host's live jails are unenumerable here, so image rooting can't be confirmed") {
		t.Errorf("expected the in-jail refusal line:\n%s", buf.String())
	}
}

// TestNixGCDeclinesOnUnknownLiveness: liveness unknown (runtime unenumerable) →
// decline the GC (fail-safe), same polarity as every other sweep.
func TestNixGCDeclinesOnUnknownLiveness(t *testing.T) {
	o, _ := baseOpts(t)
	o.NixGC = true
	o.InJail = func() bool { return false }
	o.Exec = func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }
	called := false
	o.NixStoreGC = func(int64, bool) StoreGCOutcome { called = true; return StoreGCOutcome{} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if called {
		t.Error("unknown liveness must decline the store GC")
	}
	if !hasLine(&buf, "  skipped — could not enumerate running jails (podman); declining to GC the store") {
		t.Errorf("expected the decline line:\n%s", buf.String())
	}
}

// TestNixGCSkipsWhenImageUnrooted: liveness known, but a loaded image closure has
// no durable §1 root → refuse the GC and name the offending path.
func TestNixGCSkipsWhenImageUnrooted(t *testing.T) {
	o, gs := baseOpts(t)
	o.NixGC = true
	o.InJail = func() bool { return false }
	buildDir := filepath.Join(gs, "build")
	o.BuildDir = func() string { return buildDir }
	// A sentinel records a loaded image, but no roots/<sha16> exists for it.
	sp := "/nix/store/zzzz-stream-yolo-jail"
	mustMkdir(t, buildDir)
	mustWrite(t, filepath.Join(buildDir, "last-load-podman"), []byte(sp+"\n"))
	// Runtime enumerates (empty is fine — live.Known=true).
	o.Exec = stubExec(map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "\n",
	}, nil)
	called := false
	o.NixStoreGC = func(int64, bool) StoreGCOutcome { called = true; return StoreGCOutcome{} }
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if called {
		t.Error("an unrooted loaded image must block the store GC")
	}
	if !hasLine(&buf, "    • "+sp) {
		t.Errorf("expected the unrooted store path named:\n%s", buf.String())
	}
	found := false
	for _, l := range lines(&buf) {
		if strings.Contains(l, "lack a durable GC root (storage §1)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the §1-rooting skip explanation:\n%s", buf.String())
	}
}

// TestNixGCDryRunWhenRooted: liveness known and every loaded image is rooted →
// the dry-run store GC runs and reports the would-delete count (apply=false, so
// the seam is asked for a dry run).
func TestNixGCDryRunWhenRooted(t *testing.T) {
	o, gs := baseOpts(t)
	o.NixGC = true
	o.InJail = func() bool { return false }
	buildDir := filepath.Join(gs, "build")
	o.BuildDir = func() string { return buildDir }
	sp := "/nix/store/yyyy-stream-yolo-jail"
	mustMkdir(t, buildDir)
	mustWrite(t, filepath.Join(buildDir, "last-load-podman"), []byte(sp+"\n"))
	rootFor(t, filepath.Join(buildDir, "roots"), sp)
	o.Exec = stubExec(map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "\n",
	}, nil)
	var gotApply bool
	var gotMax int64
	o.NixStoreGC = func(maxBytes int64, apply bool) StoreGCOutcome {
		gotApply = apply
		gotMax = maxBytes
		return StoreGCOutcome{Ran: true, Paths: 2147}
	}
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if gotApply {
		t.Error("dry-run prune must ask the seam for a dry run, not apply")
	}
	if gotMax != nixGCDefaultMaxBytes {
		t.Errorf("default max = %d, want %d", gotMax, nixGCDefaultMaxBytes)
	}
	found := false
	for _, l := range lines(&buf) {
		if strings.Contains(l, "would delete up to 2,147 store path(s)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the dry-run would-delete line:\n%s", buf.String())
	}
}

// TestNixGCApplyBoundedAndNotInSummary: --apply --nix-gc runs a bounded GC (the
// seam sees apply=true and the custom max) and its freed count stays OUT of the
// golden-pinned reclaim summary (host store is a separate ledger).
func TestNixGCApplyBoundedAndNotInSummary(t *testing.T) {
	o, gs := baseOpts(t)
	o.Apply = true
	o.NixGC = true
	o.NixGCMaxBytes = 10 << 30
	o.InJail = func() bool { return false }
	buildDir := filepath.Join(gs, "build")
	o.BuildDir = func() string { return buildDir }
	sp := "/nix/store/wwww-stream-yolo-jail"
	mustMkdir(t, buildDir)
	mustWrite(t, filepath.Join(buildDir, "last-load-podman"), []byte(sp+"\n"))
	rootFor(t, filepath.Join(buildDir, "roots"), sp)
	o.Exec = stubExec(map[string]string{
		k("podman", "ps", "-a", "--format", "{{.Names}} {{.State}}"): "\n",
	}, nil)
	var gotApply bool
	var gotMax int64
	o.NixStoreGC = func(maxBytes int64, apply bool) StoreGCOutcome {
		gotApply = apply
		gotMax = maxBytes
		return StoreGCOutcome{Ran: true, Paths: 1234, HaveBytes: true, Bytes: 5 << 30}
	}
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if !gotApply || gotMax != 10<<30 {
		t.Errorf("apply seam call = (apply=%v, max=%d), want (true, %d)", gotApply, gotMax, int64(10<<30))
	}
	if !hasLine(&buf, "  freed 1,234 store path(s) (5.0 GiB)  (bounded at 10.0 GiB; host /nix/store, not counted in the reclaim total)") {
		t.Errorf("expected the apply freed line:\n%s", buf.String())
	}
	// The reclaim summary must NOT mention the 1,234 store paths or 5 GiB.
	last := lines(&buf)[len(lines(&buf))-1]
	if strings.Contains(last, "1,234") || strings.Contains(last, "store path") {
		t.Errorf("store-GC results leaked into the reclaim summary: %q", last)
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

// TestImageCacheKeepDefaultsPerRuntime pins OQ-BF6's whole content: the number
// follows the runtime, and the Apple Container arm is deliberately unchanged.
//
// The second case is the one worth having. Podman keeping zero is the headline,
// but it is the AC arm that a "simplification" would break — that backend still
// writes a tar per store path because `skopeo copy docker-archive:<path>` and
// `podman save -o <path>` both interpolate a real path and cannot consume a
// stream, so zero there deletes the only copy of an image it cannot re-stream.
func TestImageCacheKeepDefaultsPerRuntime(t *testing.T) {
	cases := []struct {
		rt   string
		flag int
		want int
		why  string
	}{
		{"podman", ImageCacheKeepUnset, 0,
			"podman streams into `load` and writes no tar (C3), so a retained tar is dead weight"},
		{"container", ImageCacheKeepUnset, ImageCacheKeepAppleContainer,
			"Apple Container cannot consume a stream, so its tar is the only copy — unchanged until OQ-DF2"},
		{"podman", 5, 5, "an explicit flag always wins over the per-runtime default"},
		{"container", 0, 0, "including an explicit zero, which the sentinel exists to distinguish from unset"},
	}
	for _, c := range cases {
		if got := ResolveImageCacheKeep(c.flag, c.rt); got != c.want {
			t.Errorf("ResolveImageCacheKeep(%d, %q) = %d, want %d — %s", c.flag, c.rt, got, c.want, c.why)
		}
	}
}

// TestImageCacheKeepUnsetIsNotZero: the sentinel is the whole reason this is not
// a one-line default change. Zero became a MEANINGFUL value for this flag, so
// "unset" could no longer be spelled as the zero value, and a future refactor
// that drops the sentinel silently pins podman's default to Apple Container's.
func TestImageCacheKeepUnsetIsNotZero(t *testing.T) {
	if ImageCacheKeepUnset == 0 {
		t.Fatal("ImageCacheKeepUnset == 0 — then `--image-cache-keep 0` is indistinguishable from " +
			"not passing the flag, and OQ-BF6's per-runtime default cannot be expressed")
	}
	if got := NewDefaultOptions().ImageCacheKeep; got != ImageCacheKeepUnset {
		t.Fatalf("NewDefaultOptions().ImageCacheKeep = %d, want the unset sentinel — the front door "+
			"builds Options before any runtime is detected, so it must not bake a number", got)
	}
}
