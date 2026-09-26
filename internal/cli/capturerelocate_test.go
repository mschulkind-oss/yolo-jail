package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// capturerelocate_test.go drives install-capture hand-off H2 — the relocation rewrite — through
// the two argvs a relocated capture actually goes through: `yolo internal capture-run
// --scan-content-refs` records it under one home, and `yolo internal capture-materialize` puts it
// into another.
//
// This is the macos-user shape with Linux temp dirs standing in for its two homes: the capture
// runs against a throwaway staging home (/Users/Shared/yolo-captures/<bin>/home on a Mac) and
// the materialize targets the account home (/Users/_yolojail). No Mac has run it. And no
// macos-user LAUNCH reaches capture-materialize yet: the generated launcher there bakes an empty
// store path (install-capture.md hand-off H4), so these tests call the subcommand the launcher
// would call.

// relocInstallerScript is a vendor installer that embeds its own HOME the two ways a relocation
// has to undo: an absolute symlink, and the path spelled inside a text shim.
const relocInstallerScript = `#!/bin/sh
set -eu
v="$HOME/.local/share/vendor/1.0.0"
mkdir -p "$v" "$HOME/.local/bin"
printf '#!/bin/sh\necho "vendor ran as $0"\n' > "$v/vendor"
chmod 755 "$v/vendor"
ln -s "$v/vendor" "$HOME/.local/bin/vendor"
printf '#!/bin/sh\nexec %s/.local/share/vendor/1.0.0/vendor "$@"\n' "$HOME" > "$HOME/.local/bin/vendor-shim"
chmod 755 "$HOME/.local/bin/vendor-shim"
`

// recordThroughCaptureRun records the fixture installer under a fresh capture home through the
// HIDDEN SWITCH, admits it, and files the `record` receipt beside it — what captureHost does
// after its capture jail exits. It returns the capture home and the admitted entry; the capture
// home is left in place for the caller to delete.
func recordThroughCaptureRun(t *testing.T, store *capture.Store, scan bool) (string, *capture.Entry) {
	t.Helper()
	from := t.TempDir()
	staged, err := store.Stage("vendor")
	if err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(installer, []byte(relocInstallerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	argv := []string{"capture-run", "--out=" + staged, "--home=" + from}
	if scan {
		argv = append(argv, "--scan-content-refs")
	}
	argv = append(argv, "--", "/bin/sh", installer)
	if rc := runInternal(argv); rc != 0 {
		t.Fatalf("capture-run exited %d", rc)
	}
	m, err := capture.ReadManifest(staged)
	if err != nil {
		t.Fatal(err)
	}
	if m.Relocatable != scan {
		t.Fatalf("recorded relocatable=%v with scan=%v: %v", m.Relocatable, scan, m.NotRelocatable)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	line := entrypoint.CaptureReceipt{
		Bin: "vendor", Declared: "https://example.invalid/install.sh", Key: entry.Key,
		Digest: capture.DigestHash(entry.Digest), Bytes: m.TotalBytes(),
		Path: entry.Root, Platform: m.Platform,
		Act: entrypoint.ReceiptActRecord, Time: time.Now(),
	}.Line()
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), line); err != nil {
		t.Fatal(err)
	}
	return from, entry
}

// THE WHOLE PATH: recorded under one home by capture-run, the capture home thrown away, and
// materialized into another home by capture-materialize — after which the program runs from the
// new home through both of its rewritten references.
//
// Delete the relocation from capture.Materialize and this goes red at the materialize exit code,
// because the entry is then refused exactly as it was before H2.
func TestCaptureMaterializeRelocatesACaptureMadeUnderAnotherHome(t *testing.T) {
	store := &capture.Store{Dir: t.TempDir()}
	from, entry := recordThroughCaptureRun(t, store, true)
	if err := os.RemoveAll(from); err != nil {
		t.Fatal(err)
	}
	to := t.TempDir()
	receipts := filepath.Join(t.TempDir(), "ws", ".yolo", "receipts.jsonl")

	rc := runInternal([]string{"capture-materialize",
		"--store=" + store.Dir, "--home=" + to, "--bin=vendor",
		"--declared=https://example.invalid/install.sh", "--receipts=" + receipts})
	if rc != 0 {
		t.Fatalf("capture-materialize into another home exited %d", rc)
	}

	link, err := os.Readlink(filepath.Join(to, ".local", "bin", "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	if want := to + "/.local/share/vendor/1.0.0/vendor"; link != want {
		t.Errorf("the vendor link -> %q, want %q", link, want)
	}
	for _, bin := range []string{"vendor", "vendor-shim"} {
		out, err := exec.Command(filepath.Join(to, ".local", "bin", bin)).CombinedOutput()
		if err != nil {
			t.Errorf("running %s from the relocated home: %v\n%s", bin, err, out)
			continue
		}
		if !strings.Contains(string(out), "vendor ran as "+to+"/") {
			t.Errorf("%s did not run the relocated vendor: %s", bin, out)
		}
	}
	rec := readOnlyReceipt(t, receipts)
	if got, _ := rec["act"].(string); got != entrypoint.ReceiptActMaterialize {
		t.Errorf("receipt act = %q, want %q", got, entrypoint.ReceiptActMaterialize)
	}
	if got, _ := rec["resolved"].(string); got != entry.Key {
		t.Errorf("receipt resolved = %q, want %q", got, entry.Key)
	}
}

// What the launcher's user READS. A relocation names both homes and what it rewrote, and a
// capture the record says may not move is a LOUD failure naming why — after which the launcher
// falls through to the vendor installer, with nothing of the entry in the home.
func TestCaptureMaterializeReportsARelocationAndARefusal(t *testing.T) {
	store := &capture.Store{Dir: t.TempDir()}
	from, _ := recordThroughCaptureRun(t, store, true)
	to := t.TempDir()
	var errw bytes.Buffer
	if rc := materializeCapture(materializeArgs{store: store.Dir, bin: "vendor", home: to}, &errw); rc != 0 {
		t.Fatalf("relocating materialize exited %d:\n%s", rc, errw.String())
	}
	want := "captured under " + filepath.Clean(from) + ": rewrote 1 links and 1 files to name " + to
	if !strings.Contains(errw.String(), want) {
		t.Errorf("the report does not say %q:\n%s", want, errw.String())
	}

	// Recorded WITHOUT the content scan, as every container capture is: not relocatable.
	unscanned := &capture.Store{Dir: t.TempDir()}
	recordThroughCaptureRun(t, unscanned, false)
	elsewhere := t.TempDir()
	errw.Reset()
	if rc := materializeCapture(materializeArgs{store: unscanned.Dir, bin: "vendor", home: elsewhere}, &errw); rc == 0 {
		t.Fatalf("a non-relocatable capture materialized into another home:\n%s", errw.String())
	}
	for _, want := range []string{"FAILED", "not relocatable", "file CONTENTS were not enumerated"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errw.String())
		}
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Errorf("the refused materialize wrote into the home: %v", ents)
	}
}
