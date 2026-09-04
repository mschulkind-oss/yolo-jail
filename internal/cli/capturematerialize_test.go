package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// capturematerialize_test.go covers `yolo internal capture-materialize` — the argv the
// generated native launcher emits — and the bin+platform lookup behind it.
//
// The MECHANISM is internal/capture's and is measured there. What is measured here is the
// POLICY: which entry answers "<bin> for <platform>", what a miss does, and that the hidden
// switch actually reaches the handler. That last one is not padding: slice 3 shipped a
// `yolo capture` whose unit tests were all green while the real dispatch dropped a token, so
// every capture entry point in this package is now driven through runInternal.

// admitCaptureEntry builds a one-file entry in a fresh store, admits it, and appends a
// `record` receipt beside it — the exact artifact `yolo capture` leaves behind.
func admitCaptureEntry(t *testing.T, store *capture.Store, id, bin, platform, home, body string, when time.Time) *capture.Entry {
	t.Helper()
	staged, err := store.Stage(id)
	if err != nil {
		t.Fatal(err)
	}
	tree := capture.TreeDir(staged)
	if err := os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".local", "bin", bin), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// A HAND-WRITTEN manifest, on purpose: materialize reads the manifest rather than
	// walking the tree, so this is also the test that the on-disk schema is what the
	// reader consumes.
	if err := capture.WriteManifest(staged, &capture.Manifest{
		Schema: capture.ManifestSchema, Home: home, Platform: platform,
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{},
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.CaptureReceipt{
		Bin: bin, Declared: "https://example.invalid/i.sh", Key: entry.Key,
		Digest: capture.DigestHash(entry.Digest), Bytes: int64(len(body)),
		Path: entry.Root, Platform: platform,
		Act: entrypoint.ReceiptActRecord, Time: when,
	}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	return entry
}

// The whole in-jail act, through the HIDDEN SWITCH: an admitted entry lands in the home and
// leaves an `act:"materialize"` line in the WORKSPACE receipt log.
//
// Driving runInternal rather than materializeCapture is the point — delete
// `case "capture-materialize"` from internal.go and the argv every generated launcher emits
// becomes "unknown command", with the materializer and all of its own tests still green.
func TestInternalCaptureMaterializeMaterializesThroughTheHiddenSwitch(t *testing.T) {
	home := t.TempDir()
	store := &capture.Store{Dir: t.TempDir()}
	entry := admitCaptureEntry(t, store, "probetool", "probetool", capture.Platform(),
		home, "#!/bin/sh\necho probe\n", time.Now())
	receipts := filepath.Join(t.TempDir(), "ws", ".yolo", "receipts.jsonl")

	rc := runInternal([]string{"capture-materialize",
		"--store=" + store.Dir, "--home=" + home, "--bin=probetool",
		"--declared=https://example.invalid/i.sh", "--receipts=" + receipts})
	if rc != 0 {
		t.Fatalf("capture-materialize exited %d", rc)
	}

	landed := filepath.Join(home, ".local", "bin", "probetool")
	fi, err := os.Lstat(landed)
	if err != nil {
		t.Fatalf("the program is not in the home: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the materialized program is not executable: %v", fi.Mode())
	}

	// THE RECEIPT GOES TO THE WORKSPACE, not beside the entry. `record` is machine-wide and
	// lives with the CAS entry; `materialize` is the claim that THESE BYTES ARE IN THIS
	// WORKSPACE, which is the one thing <ws>/.yolo/receipts.jsonl exists to carry — it is
	// the line that would have been a kind:"installer" install had there been no capture.
	rec := readOnlyReceipt(t, receipts)
	for _, c := range []struct{ field, want string }{
		{"kind", entrypoint.ReceiptKindCapture},
		{"act", entrypoint.ReceiptActMaterialize},
		{"bin", "probetool"},
		{"declared", "https://example.invalid/i.sh"},
		{"resolved", entry.Key},
		{"path", entry.Root},
		{"platform", capture.Platform()},
	} {
		if got, _ := rec[c.field].(string); got != c.want {
			t.Errorf("materialize receipt %s = %q, want %q", c.field, got, c.want)
		}
	}
	// The digest comes from the RECORD receipt rather than being recomputed — recomputing
	// it would mean walking the tree the whole design exists to avoid walking.
	if got, _ := rec["sha256"].(string); got != capture.DigestHash(entry.Digest) {
		t.Errorf("materialize receipt sha256 = %q, want the record's %q",
			got, capture.DigestHash(entry.Digest))
	}
}

// A MISS is one line and a non-zero exit, never an error the launcher has to interpret: the
// fallback to the vendor installer is not removable (install-capture.md, Blockers).
func TestCaptureMaterializeMissesWithoutWritingAnything(t *testing.T) {
	home := t.TempDir()
	store := &capture.Store{Dir: t.TempDir()}
	var errw bytes.Buffer

	rc := materializeCapture(materializeArgs{
		store: store.Dir, home: home, bin: "probetool",
	}, &errw)

	if rc == 0 {
		t.Fatalf("an empty store must be a miss, got rc 0:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "no capture for probetool") {
		t.Errorf("the miss does not name the program:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), capture.Platform()) {
		t.Errorf("the miss does not name the platform it looked for:\n%s", errw.String())
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("a miss wrote into the home: %v", ents)
	}
}

// THE LOOKUP: newest record wins, a foreign platform is not a candidate, and an entry no
// receipt attributes to a program is invisible.
//
// There is no bin→key index by design (see resolveCaptureFor); this is the test that the
// scan it uses instead actually distinguishes the cases an index would have.
func TestCaptureLookupPicksTheNewestRecordForThisBinAndPlatform(t *testing.T) {
	home := t.TempDir()
	store := &capture.Store{Dir: t.TempDir()}
	old := time.Now().Add(-2 * time.Hour)
	admitCaptureEntry(t, store, "old", "probetool", capture.Platform(), home, "OLD\n", old)
	newest := admitCaptureEntry(t, store, "new", "probetool", capture.Platform(), home,
		"NEW\n", time.Now())
	admitCaptureEntry(t, store, "foreign", "probetool", "plan9/mips", home, "FOREIGN\n", time.Now())
	admitCaptureEntry(t, store, "other", "othertool", capture.Platform(), home, "OTHER\n", time.Now())

	// An admitted entry with NO receipt beside it: in the store, attributable to nothing.
	orphan := admitCaptureEntry(t, store, "orphan", "probetool", capture.Platform(),
		home, "ORPHAN\n", time.Now().Add(time.Hour))
	if err := os.Remove(capture.ReceiptsPath(orphan.Root)); err != nil {
		t.Fatal(err)
	}

	entry, rec, err := resolveCaptureFor(store, "probetool", capture.Platform())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if entry.Key != newest.Key {
		t.Errorf("lookup chose %s, want the newest record %s", entry.Key, newest.Key)
	}
	if rec.Bin != "probetool" || rec.Platform != capture.Platform() {
		t.Errorf("the chosen record is for the wrong program: %+v", rec)
	}

	// A program nothing captured is a miss, and the message names the act that fixes it.
	if _, _, err := resolveCaptureFor(store, "nosuchtool", capture.Platform()); err == nil {
		t.Errorf("a bin with no record must be a miss")
	} else if !strings.Contains(err.Error(), "yolo capture nosuchtool") {
		t.Errorf("the miss does not name the fix: %v", err)
	}
	// So is a platform nothing captured FOR.
	if _, _, err := resolveCaptureFor(store, "probetool", "plan9/sparc"); err == nil {
		t.Errorf("a platform with no record must be a miss")
	}
}

// A TORN entry is not a candidate even with a good receipt beside it. The completion marker
// is the only thing that says an entry exists (Store.Resolve), and the lookup goes through it
// rather than trusting the receipt it just read.
func TestCaptureLookupSkipsATornEntry(t *testing.T) {
	home := t.TempDir()
	store := &capture.Store{Dir: t.TempDir()}
	good := admitCaptureEntry(t, store, "good", "probetool", capture.Platform(),
		home, "GOOD\n", time.Now().Add(-time.Hour))
	torn := admitCaptureEntry(t, store, "torn", "probetool", capture.Platform(),
		home, "TORN\n", time.Now())
	if err := os.Remove(filepath.Join(torn.Root, ".yolo-capture-complete")); err != nil {
		t.Fatal(err)
	}

	entry, _, err := resolveCaptureFor(store, "probetool", capture.Platform())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if entry.Key != good.Key {
		t.Errorf("lookup chose the torn entry %s over the complete %s", entry.Key, good.Key)
	}
}

// The two arguments the materializer cannot invent are refused with the usage.
func TestInternalCaptureMaterializeRequiresAStoreAndABin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"capture-materialize"},
		{"capture-materialize", "--store=" + t.TempDir()},
		{"capture-materialize", "--bin=probetool"},
		{"capture-materialize", "--store=" + t.TempDir(), "--bin=probetool", "-x"},
	} {
		if code := runInternal(args); code != 2 {
			t.Errorf("runInternal(%v) = %d, want 2", args, code)
		}
	}
}

// readOnlyReceipt reads a receipt log that must hold exactly one line.
func readOnlyReceipt(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt at %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d receipt lines, want 1:\n%s", len(lines), data)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("the receipt is not JSON: %v\n%s", err, lines[0])
	}
	return m
}
