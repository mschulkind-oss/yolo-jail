package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// captures_test.go covers the superseded-install-capture section of `yolo prune`.
//
// What is pinned HERE is the section: that Run reaches the store under the global storage
// root, honors dry-run, folds the reclaimed bytes into the total, and declines loudly when the
// front door did not hand it a receipt reader. The RULE it applies is internal/capture's and is
// measured there; the receipt reader is internal/cli's and is measured there.
//
// Delete the section from Run and every test in this file fails.

// admitCapture stages and admits a one-file entry in the store, returning its key and its
// entry root. Distinct bodies mean distinct keys (the key is the tree's content address).
func admitCapture(t *testing.T, store *capture.Store, id, body string) (string, string) {
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
	return entry.Key, entry.Root
}

// records is the capture.Records seam as a table — the same shape internal/cli's real adapter
// returns, without this package having to import the receipt schema (see
// Options.CaptureRecords for why it must not).
func records(table map[string][]capture.Record) capture.Records {
	return func(dir string) ([]capture.Record, error) { return table[dir], nil }
}

// The section reaps what the resolver would not select — dry-run reports it and touches
// nothing, --apply removes the tree and the bytes land in the reclaim total.
func TestSupersededCaptureSectionReportsThenReaps(t *testing.T) {
	o, gs := baseOpts(t)
	store := &capture.Store{Dir: filepath.Join(gs, "captures")}
	oldKey, oldRoot := admitCapture(t, store, "old", "OLD-BODY\n")
	newKey, newRoot := admitCapture(t, store, "new", "NEW-BODY\n")
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	probetool := capture.Record{Bin: "probetool", Platform: "linux/amd64"}
	o.CaptureRecords = records(map[string][]capture.Record{
		oldRoot: {{Bin: probetool.Bin, Platform: probetool.Platform, Time: when.Add(-time.Hour)}},
		newRoot: {{Bin: probetool.Bin, Platform: probetool.Platform, Time: when}},
	})

	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	out := buf.String()
	if !strings.Contains(out, "Superseded install captures") {
		t.Fatalf("no section header in:\n%s", out)
	}
	if !strings.Contains(out, "would remove: 9 B across 1 entry(ies)") {
		t.Errorf("dry run did not report the superseded entry:\n%s", out)
	}
	if !strings.Contains(out, oldKey) || !strings.Contains(out, "probetool (linux/amd64) superseded by "+newKey) {
		t.Errorf("the report does not name what would go and why:\n%s", out)
	}
	if !strings.Contains(out, "would reclaim 9 B") {
		t.Errorf("the summary does not carry the capture bytes:\n%s", out)
	}
	if _, err := store.Resolve(oldKey); err != nil {
		t.Errorf("the dry run reaped %s: %v", oldKey, err)
	}

	// --apply removes it for real; the selected entry survives.
	o.Apply = true
	buf.Reset()
	if rc := Run(o); rc != 0 {
		t.Fatalf("apply rc=%d", rc)
	}
	if !strings.Contains(buf.String(), "removed: 9 B across 1 entry(ies)") {
		t.Errorf("apply did not report the removal:\n%s", buf.String())
	}
	if _, err := os.Stat(capture.TreeDir(oldRoot)); !os.IsNotExist(err) {
		t.Errorf("the superseded tree survived --apply: %v", err)
	}
	if _, err := store.Resolve(newKey); err != nil {
		t.Errorf("--apply took the entry the resolver would return: %v", err)
	}
}

// With entries but no superseded one, the section says so and names how many it kept — the
// number that makes "reaped nothing" auditable rather than a claim.
func TestSupersededCaptureSectionKeepsTheOnlyEntry(t *testing.T) {
	o, gs := baseOpts(t)
	store := &capture.Store{Dir: filepath.Join(gs, "captures")}
	key, root := admitCapture(t, store, "only", "ONLY\n")
	o.CaptureRecords = records(map[string][]capture.Record{
		root: {{Bin: "probetool", Platform: "linux/amd64", Time: time.Now()}},
	})
	o.Apply = true
	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(buf.String(), "none  (1 entry(ies) kept — the newest per program)") {
		t.Errorf("the kept count is missing:\n%s", buf.String())
	}
	if _, err := store.Resolve(key); err != nil {
		t.Errorf("the only entry was reaped: %v", err)
	}
}

// UNWIRED IS A REFUSAL, NOT SILENCE. Without a reader every entry reads as attributed to
// nothing, and the complement of an empty selection is the entire store — so the section
// declines, says who has to fix it, and removes nothing.
func TestSupersededCaptureSectionDeclinesWithoutAReader(t *testing.T) {
	o, gs := baseOpts(t)
	store := &capture.Store{Dir: filepath.Join(gs, "captures")}
	key, _ := admitCapture(t, store, "only", "ONLY\n")
	o.Apply = true
	o.CaptureRecords = nil
	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(buf.String(), "skipped — no capture-receipt reader wired") {
		t.Errorf("the unwired section did not say so:\n%s", buf.String())
	}
	if _, err := store.Resolve(key); err != nil {
		t.Errorf("an unwired section reaped %s: %v", key, err)
	}
}

// The section derives the store from the global storage root, so its leaf must be the one
// paths.CapturesDir() uses. A drifted spelling here reports "none" forever while the real
// store grows — the failure mode with no symptom.
func TestCapturesLeafMatchesPathsSpelling(t *testing.T) {
	home := t.TempDir()
	want := paths.CapturesDirUnder(home)
	got := joinPath(paths.GlobalStorageUnder(home), capturesLeaf)
	if got != want {
		t.Errorf("prune looks in %s, the store lives at %s", got, want)
	}
}
