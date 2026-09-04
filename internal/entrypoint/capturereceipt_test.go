package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capturereceipt_test.go is the WRITER→READER round trip for the one receipt kind written
// from Go, and it is deliberately shaped like
// TestReceiptRoundTripThroughAGeneratedLauncher: the writer produces a real line, the
// production reader parses it, and the assertions are on what came back out.
//
// The reason it has to exist is install-capture.md's constraint that the schema be EXTENDED
// and never forked. A capture receipt nothing but capture could read would be the parallel
// ledger program-delivery.md §6 says was killed once already, and the only way that stays
// true is a test in which the reader is the one the boot uses.

func sampleCaptureReceipt() CaptureReceipt {
	return CaptureReceipt{
		Bin:      "probetool",
		Declared: "https://example.invalid/install.sh",
		Key:      "3f2a1b0c9d8e7f60",
		Digest:   "3f2a1b0c9d8e7f60" + strings.Repeat("a", 48),
		Bytes:    1234567,
		Path:     "/home/u/.local/share/yolo-jail/captures/entries/3f2a1b0c9d8e7f60",
		Platform: "linux/arm64",
		Act:      ReceiptActRecord,
		Time:     time.Date(2026, 9, 4, 11, 22, 33, 456, time.UTC),
	}
}

// The line a capture writes is read by the reader the boot uses, field for field.
func TestCaptureReceiptRoundTripsThroughTheProductionReader(t *testing.T) {
	want := sampleCaptureReceipt()

	got, err := parseReceiptLine(want.Line())
	if err != nil {
		t.Fatalf("the production reader could not parse a capture receipt: %v\n%s", err, want.Line())
	}

	for _, c := range []struct{ name, got, want string }{
		{"kind", got.Kind, ReceiptKindCapture},
		{"bin", got.Bin, want.Bin},
		{"declared", got.Declared, want.Declared},
		{"resolved", got.Resolved, want.Key},
		{"sha256", got.SHA256, want.Digest},
		{"path", got.Path, want.Path},
		{"platform", got.Platform, want.Platform},
		{"act", got.Act, ReceiptActRecord},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.Schema != 1 {
		t.Errorf("schema = %d, want 1 — the head comes from receiptPrefix and must not fork", got.Schema)
	}
	if got.Bytes != want.Bytes {
		t.Errorf("bytes = %d, want %d", got.Bytes, want.Bytes)
	}
	// The same stamp shape every other writer emits: `date -u +%Y-%m-%dT%H:%M:%SZ`.
	if len(got.Time) != 20 || !strings.HasSuffix(got.Time, "Z") {
		t.Errorf("time = %q, want a 20-char UTC stamp", got.Time)
	}
	// HasResolved is the predicate every comparison consults first, so a capture receipt
	// that did not satisfy it would be invisible to any reconcile built on it.
	if !got.HasResolved() {
		t.Error("a capture receipt must state a resolved identity")
	}
	// The KEY is the digest's own prefix, which is what lets a reader check one against
	// the other without re-walking a gigabyte-scale tree.
	if !strings.HasPrefix(got.SHA256, got.Resolved) {
		t.Errorf("resolved %q is not a prefix of sha256 %q", got.Resolved, got.SHA256)
	}
}

// `bytes` is the schema's one NUMERIC field and it must stay one: the reader models it as a
// *int64 precisely so an absent field is distinguishable from a written 0, and a writer that
// quoted it would make every capture receipt read as "size unknown".
func TestCaptureReceiptBytesIsAJSONNumber(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(sampleCaptureReceipt().Line()), &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := raw["bytes"].(float64); !ok {
		t.Errorf("bytes = %T, want a JSON number", raw["bytes"])
	}
	if _, ok := raw["spec"]; ok {
		t.Errorf("a capture carries no install SPEC — the installer URL is the whole "+
			"declaration; got %v", raw["spec"])
	}
}

// A declaration carrying a quote or a backslash must not produce a line no reader can parse.
//
// The shell writer cannot escape and therefore SCRUBS (`tr -d '\"'`); the Go writer can, and
// uses jsonStringLiteral for every string field exactly as receiptPrefix already does for
// the head. That divergence is only safe if it is actually escaping, which is what this
// measures.
func TestCaptureReceiptEscapesHostileStrings(t *testing.T) {
	r := sampleCaptureReceipt()
	r.Declared = `https://x/"; rm -rf /` + "\\ \n"
	r.Bin = `pro"be`

	got, err := parseReceiptLine(r.Line())
	if err != nil {
		t.Fatalf("a hostile declaration broke the line: %v\n%s", err, r.Line())
	}
	if got.Declared != r.Declared || got.Bin != r.Bin {
		t.Errorf("escaping lost data:\n bin      %q want %q\n declared %q want %q",
			got.Bin, r.Bin, got.Declared, r.Declared)
	}
	if strings.Count(r.Line(), "\n") != 0 {
		t.Error("a receipt is ONE line; a newline in a field would split the record")
	}
}

// AppendReceiptLine is append-only and creates the parent directory, because an entry root
// is fresh out of Store.AdmitEntry and a second capture of the same bytes appends beside the
// first rather than replacing it.
func TestAppendReceiptLineAppendsAndCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entries", "3f2a", "receipts.jsonl")
	first := sampleCaptureReceipt()
	second := first
	second.Time = first.Time.Add(time.Hour)

	if err := AppendReceiptLine(path, first.Line()); err != nil {
		t.Fatal(err)
	}
	if err := AppendReceiptLine(path, second.Line()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 — the log is append-only:\n%s", len(lines), data)
	}
	for i, l := range lines {
		if _, err := parseReceiptLine(l); err != nil {
			t.Errorf("line %d does not parse: %v", i+1, err)
		}
	}
}

// The capture kind is NOT the installer kind, and the reason is receiptKey: it is
// (kind, bin), so a shared kind would collapse an installer receipt and a capture receipt
// for one program onto a single entry in latestReceipts — after which the reconcile's
// installer arm would sha256 whichever won, and a capture's `path` is a DIRECTORY.
func TestCaptureAndInstallerReceiptsAreDistinctKeys(t *testing.T) {
	cap := sampleCaptureReceipt()
	capRec, err := parseReceiptLine(cap.Line())
	if err != nil {
		t.Fatal(err)
	}
	inst := receipt{Kind: "installer", Bin: cap.Bin}
	if keyOf(capRec) == keyOf(inst) {
		t.Errorf("a capture and an installer receipt for %q share the key %+v", cap.Bin, keyOf(inst))
	}
	if capRec.Kind == "installer" {
		t.Error("kind must distinguish the store from the resolver that filled it")
	}
}
