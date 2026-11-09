package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// prunecaptures_test.go joins the three halves of the superseded-capture sweep that each own
// only their own piece: the RULE (internal/capture: newest record per (bin, platform), reap the
// complement), the SECTION (internal/prune: report, dry-run, reclaim total), and the READER
// (captureRecords, the one adapter over the receipt schema).
//
// It is the test that fails when the WIRING goes, which neither of the other two can be:
// pruneOptions' `opts.CaptureRecords = captureRecords` is one line, and without it prune's own
// suite stays green while every real `yolo prune` declines the section forever.

// writeRecordReceipt appends the `record` line `yolo capture` leaves beside an entry — the real
// schema through its real writer, so this test cannot pass on a receipt only it understands.
func writeRecordReceipt(t *testing.T, entryRoot, bin, platform string, when time.Time) {
	t.Helper()
	line := entrypoint.CaptureReceipt{
		Bin: bin, Declared: "https://example.invalid/i.sh", Key: filepath.Base(entryRoot),
		Digest: strings.Repeat("a", 64), Bytes: 1, Path: entryRoot, Platform: platform,
		Act: entrypoint.ReceiptActRecord, Time: when,
	}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entryRoot), line); err != nil {
		t.Fatal(err)
	}
}

// admitPruneFixture stages and admits a one-file capture entry.
func admitPruneFixture(t *testing.T, store *capture.Store, id, body string) *capture.Entry {
	t.Helper()
	staged, err := store.Stage(id)
	if err != nil {
		t.Fatal(err)
	}
	tree := capture.TreeDir(staged)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "probetool"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// THE WIRING, and then the whole chain through it: real receipts on disk, the reader the front
// door actually installs, and prune reaping exactly the entry the resolver would not return.
func TestPruneWiresTheCaptureReceiptReaderAndReapsThroughIt(t *testing.T) {
	opts := pruneOptions([]string{"prune"})
	if opts.CaptureRecords == nil {
		t.Fatal("pruneOptions did not wire a capture-receipt reader — `yolo prune` will decline " +
			"the superseded-capture section on every machine")
	}

	gs := t.TempDir()
	store := &capture.Store{Dir: filepath.Join(gs, "captures")}
	old := admitPruneFixture(t, store, "old", "OLD-BODY\n")
	newest := admitPruneFixture(t, store, "new", "NEW-BODY\n")
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeRecordReceipt(t, old.Root, "probetool", capture.Platform(), when.Add(-time.Hour))
	writeRecordReceipt(t, newest.Root, "probetool", capture.Platform(), when)

	// The resolver's answer, through the same reader, for the same store.
	entry, _, err := resolveCaptureFor(store, "probetool", capture.Platform())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if entry.Key != newest.Key {
		t.Fatalf("the resolver chose %s, want %s", entry.Key, newest.Key)
	}

	var buf bytes.Buffer
	opts = pruneRunOnTemp(t, opts, gs, &buf)
	opts.Apply = true
	if rc := prune.Run(opts); rc != 0 {
		t.Fatalf("prune rc=%d", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "removed: 9 B across 1 entry(ies)") ||
		!strings.Contains(out, old.Key) {
		t.Errorf("prune did not reap the superseded entry:\n%s", out)
	}
	if _, err := os.Stat(old.Tree); !os.IsNotExist(err) {
		t.Errorf("the superseded tree survived: %v", err)
	}
	// THE ENTRY THE RESOLVER RETURNED IS STILL THERE — the complement, from the other side.
	if _, _, err := resolveCaptureFor(store, "probetool", capture.Platform()); err != nil {
		t.Errorf("prune reaped the entry the resolver would return: %v", err)
	}
}

// THE FRONT-DOOR WIRING: an ended host session is captured by prune's configured callback
// before the real reaper removes it. Without the callback assignment in pruneOptions, this source
// stays unrecorded and the test's normal host overlay is absent.
func TestPruneWiresCaptureForEndedHostSessionHomes(t *testing.T) {
	if opts := pruneOptions([]string{"prune"}); opts.CaptureHostSessionHome == nil {
		t.Fatal("pruneOptions did not wire owned host session-home capture")
	}
	home := hostGateHome(t, ownedSessionHomeConfig(piSubagentsCodexCfg), nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
	if !ok {
		t.Fatalf("malformed session-home variable %q", homes.vars[0])
	}
	if err := os.WriteFile(filepath.Join(sessionHome, "settings.json"),
		[]byte(`{"customUserSetting":"saved-by-user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	homes.preserveForRetry() // the exec'd session has ended; the reaper now owns this directory.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts := pruneRunOnTemp(t, pruneOptions([]string{"prune"}), paths.GlobalStorage(), &out)
	opts.Now = time.Now
	opts.Apply = true
	opts.NoEmbeddedPacks = true
	if rc := prune.Run(opts); rc != 0 {
		t.Fatalf("prune rc=%d:\n%s", rc, out.String())
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("prune did not remove the captured ended home: %v", err)
	}
	overlay, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatalf("prune did not persist the standard host capture overlay: %v\n%s", err, out.String())
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil || captured["customUserSetting"] != "saved-by-user" {
		t.Fatalf("prune did not capture the ended session edit: %s (%v)", overlay, err)
	}
}

// A retained home the current selection cannot account for is KEPT WHOLE, never deleted and never
// a launch-blocking refusal (design §5, SHR-D8; §3's stray rule). The two cases are the two ways
// it happens: a baseline naming a surface the packs no longer declare, and a rendered file the
// program renamed (a stray). Either way the sweep reports the kept path and returns success; the
// bytes are the user's to inspect.
func TestPruneKeepsHostSessionHomeWholeWhenItCannotBeAccountedFor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, string)
		// kept is the file inside the kept home that must still hold the user's edit.
		kept string
	}{
		{
			name: "surface identity removed",
			change: func(t *testing.T, dir string) {
				target := render.Host(filepath.Join(dir, hostScratchHomeName), nil, render.OwnershipOwn)
				from := target.LastRenderPath("pi", "settings")
				to := filepath.Join(filepath.Dir(from), "removed-contributor-settings.last_render")
				if err := os.Rename(from, to); err != nil {
					t.Fatal(err)
				}
			},
			kept: ".pi/agent/settings.json",
		},
		{
			name: "rendered path changed",
			change: func(t *testing.T, dir string) {
				settings := filepath.Join(dir, hostScratchHomeName, ".pi", "agent", "settings.json")
				if err := os.Rename(settings, settings+".old"); err != nil {
					t.Fatal(err)
				}
			},
			kept: ".pi/agent/settings.json.old",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			piSessionHomeEnv(t)
			home := hostGateHome(t, ownedSessionHomeConfig(piSubagentsCodexCfg), nil)
			piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
			launch := composeHostLaunch("pi", "codex", nil, nil)
			homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
			if err != nil || homes == nil || len(homes.dirs) != 1 {
				t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
			}
			dir := homes.dirs[0]
			_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
			if !ok {
				t.Fatalf("malformed session-home variable %q", homes.vars[0])
			}
			const edited = `{"defaultProvider":"openrouter","customUserSetting":"saved-by-user"}`
			if err := os.WriteFile(filepath.Join(sessionHome, "settings.json"), []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.change(t, dir)
			for _, lease := range homes.leases {
				_ = lease.Close()
			}
			homes.leases = nil
			old := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			opts := pruneRunOnTemp(t, pruneOptions([]string{"prune"}), paths.GlobalStorage(), &out)
			opts.Now = time.Now
			opts.Apply = true
			opts.NoEmbeddedPacks = true
			if rc := prune.Run(opts); rc != 0 {
				t.Fatalf("prune failed instead of reporting a kept home:\n%s", out.String())
			}
			if existsHostPath(dir) {
				t.Fatal("the unaccountable home was left in the swept root")
			}
			kept := filepath.Join(paths.GlobalStorage(), prune.HostSessionHomesKeptLeaf, filepath.Base(dir))
			if data, err := os.ReadFile(filepath.Join(kept, "home", filepath.FromSlash(tc.kept))); err != nil || string(data) != edited {
				t.Fatalf("the kept home did not retain the session bytes: %q (%v)", data, err)
			}
			for _, want := range []string{"kept whole, not deleted", kept} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("the report must name %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestPruneFailsAndRetainsHostSessionHomeWhenCaptureFails(t *testing.T) {
	home := hostGateHome(t, ownedSessionHomeConfig(piSubagentsCodexCfg), nil)
	root := filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
	dir := filepath.Join(root, "pi-ended")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packload.EmbeddedLeaseName), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts := pruneRunOnTemp(t, pruneOptions([]string{"prune"}), paths.GlobalStorage(), &out)
	opts.Now = time.Now
	opts.Apply = true
	opts.NoEmbeddedPacks = true
	opts.CaptureHostSessionHome = func(string) (string, string, error) {
		return "", "", errors.New("capture refused; retry with `yolo prune --apply`")
	}
	if rc := prune.Run(opts); rc == 0 {
		t.Fatalf("prune returned success after capture failed; source exists=%v:\n%s", existsHostPath(dir), out.String())
	}
	if !existsHostPath(dir) {
		t.Fatal("prune removed the source after capture failed")
	}
	for _, want := range []string{"FAILED", "NOT removed", "source retained for retry", "rerun `yolo prune --apply`"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prune failure report omitted %q:\n%s", want, out.String())
		}
	}
}

// pruneRunOnTemp confines every path seam to a temp storage root and stubs the runtime probes,
// so an --apply in a unit test cannot reach the developer's own storage. Cache purging is
// switched off outright: its subdirs can be RELOCATED by the user's real config, which
// pruneOptions loads, and a purge follows the relocation off the temp root.
//
// The embedded-tree sweep's TMPDIR scan and process table are confined too. Their defaults
// are the machine's own os.TempDir() and /tmp and the live process table, so without these
// two lines every `go test ./internal/cli` — `just check-ci` included — ran a real --apply sweep
// of the developer's /tmp, deleting any legacy tree the fixture clock made look old.
func pruneRunOnTemp(t *testing.T, opts prune.Options, gs string, out *bytes.Buffer) prune.Options {
	t.Helper()
	for _, sub := range []string{"cache", "home", "build", "agents", "containers"} {
		if err := os.MkdirAll(filepath.Join(gs, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	opts.Out = out
	opts.Color = false
	opts.CacheAge = 0
	opts.CacheRelocations = nil
	opts.DetectRuntime = func() string { return "podman" }
	opts.Exec = func([]string, time.Duration) prune.ProbeResult {
		return prune.ProbeResult{Ran: true}
	}
	opts.Now = func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }
	opts.GlobalStorage = func() string { return gs }
	opts.GlobalHome = func() string { return filepath.Join(gs, "home") }
	opts.GlobalCache = func() string { return filepath.Join(gs, "cache") }
	opts.BuildDir = func() string { return filepath.Join(gs, "build") }
	opts.AgentsDir = func() string { return filepath.Join(gs, "agents") }
	opts.ContainerDir = func() string { return filepath.Join(gs, "containers") }
	opts.RelayBase = t.TempDir()
	opts.RelayKill = func(string) {}
	tempScan := t.TempDir()
	opts.TempDirs = func() []string { return []string{tempScan} }
	opts.ProcStarts = func() ([]prune.ProcStart, bool) { return nil, true }
	// These fixtures exercise owned host state, never the real machine's guest
	// staging root, process table, or sandbox account. Guest sweep wiring has its
	// own tests in prunepacktrees_test.go.
	opts.GuestPackTrees = func(bool) (prune.EmbeddedReap, bool) { return prune.EmbeddedReap{}, false }
	return opts
}

// pruneRunOnTemp must confine the embedded-tree sweep's TMPDIR scan: its default is the real
// os.TempDir() and /tmp, and the test above runs with --apply. Checked on the options rather
// than by running an --apply, because the failure this guards against IS a real sweep of
// /tmp — a pin that caught it by running one would do the damage it exists to prevent.
func TestPruneRunOnTempConfinesTheTempDirSweep(t *testing.T) {
	root := t.TempDir()
	input := pruneOptions([]string{"prune"})
	input.GuestPackTrees = func(bool) (prune.EmbeddedReap, bool) {
		t.Fatal("pruneRunOnTemp leaked the machine's guest-pack-tree callback")
		return prune.EmbeddedReap{}, false
	}
	opts := pruneRunOnTemp(t, input, t.TempDir(), &bytes.Buffer{})
	if opts.GuestPackTrees == nil {
		t.Fatal("pruneRunOnTemp must explicitly replace the machine's guest sweep")
	}
	if _, present := opts.GuestPackTrees(true); present {
		t.Fatal("a temp-only prune fixture must not run a guest sweep")
	}
	if opts.TempDirs == nil || opts.ProcStarts == nil {
		t.Fatal("pruneRunOnTemp leaves TempDirs/ProcStarts nil: prune defaults them to the " +
			"machine's real /tmp and process table, and a test --apply then sweeps them")
	}
	dirs := opts.TempDirs()
	if len(dirs) == 0 {
		t.Fatal("pruneRunOnTemp's TempDirs is empty; want one confined temp dir")
	}
	parent := filepath.Dir(root)
	for _, d := range dirs {
		if filepath.Dir(d) != parent || d == os.TempDir() || d == "/tmp" {
			t.Errorf("pruneRunOnTemp scans %s, which is not one of this test's own temp dirs", d)
		}
	}
	if procs, known := opts.ProcStarts(); !known || len(procs) != 0 {
		t.Errorf("ProcStarts = (%v, %v), want a stubbed empty table", procs, known)
	}
}

// The adapter takes `record` lines and nothing else. A `materialize` receipt is written per
// WORKSPACE, never beside an entry, but the two share one schema and one reader — so if this
// filter went, a materialize line beside an entry would become a selection candidate carrying
// the wrong stamp.
func TestCaptureRecordsReadsOnlyRecordActs(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeRecordReceipt(t, dir, "probetool", "linux/amd64", when)
	line := entrypoint.CaptureReceipt{
		Bin: "probetool", Key: "deadbeef", Platform: "linux/amd64",
		Act: entrypoint.ReceiptActMaterialize, Time: when.Add(time.Hour),
	}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(dir), line); err != nil {
		t.Fatal(err)
	}
	recs, err := captureRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].Time.Equal(when) || recs[0].Bin != "probetool" {
		t.Errorf("captureRecords = %+v, want the one record line", recs)
	}
	// An entry with no log at all is attributed to nothing, not an error.
	empty, err := captureRecords(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Errorf("an absent receipt log gave (%v, %v)", empty, err)
	}
}
