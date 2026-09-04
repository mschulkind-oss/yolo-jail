package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
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

// pruneRunOnTemp confines every path seam to a temp storage root and stubs the runtime probes,
// so an --apply in a unit test cannot reach the developer's own storage. Cache purging is
// switched off outright: its subdirs can be RELOCATED by the user's real config, which
// pruneOptions loads, and a purge follows the relocation off the temp root.
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
	return opts
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
