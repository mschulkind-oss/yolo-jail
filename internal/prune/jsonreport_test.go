package prune

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// seedReclaimable puts real bytes where two sweeps will find them, so the
// document under test is not an all-zeroes pass.
//
// A JSON test over an empty temp root is the vacuous shape CODE-RULES warns
// about: every field is zero, `json.Unmarshal` succeeds, and the same test
// passes over a report that reads nothing at all. These two files are what make
// the numeric assertions below able to fail.
func seedReclaimable(t *testing.T, gs string) (tarBytes int64) {
	t.Helper()
	images := filepath.Join(gs, "cache", "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two cached image tarballs with keep=0 (the podman default), so both are
	// reclaimable and the image_cache category is non-zero.
	for _, name := range []string{"yolo-jail-aaaa.tar", "yolo-jail-bbbb.tar"} {
		body := bytes.Repeat([]byte("x"), 4096)
		if err := os.WriteFile(filepath.Join(images, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		tarBytes += int64(len(body))
	}
	return tarBytes
}

// TestPruneJSONParsesAndCarriesTheSummary is enforcement item 4 of
// docs/design/self-documenting-cli.md for `prune`: the output is PARSED, and its
// numbers are checked against the measurement rather than against a string.
func TestPruneJSONParsesAndCarriesTheSummary(t *testing.T) {
	o, gs := baseOpts(t)
	tarBytes := seedReclaimable(t, gs)
	var buf bytes.Buffer
	o.Out = &buf
	o.Color = true // requested, and must not survive into the document
	o.IsTTYStdout = func() bool { return true }
	o.Format = outfmt.JSON

	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	raw := buf.String()
	if strings.Contains(raw, "\x1b[") {
		t.Error("JSON output carries ANSI; it must be plain even with Color+TTY on")
	}
	// The human report must be GONE from stdout, not merely accompanied by JSON.
	// A consumer reading stdout has to get a document and nothing else.
	for _, human := range []string{"yolo prune (DRY-RUN)", "Current usage", "Hardlink dedup"} {
		if strings.Contains(raw, human) {
			t.Errorf("the human report leaked into the JSON stream (%q):\n%s", human, raw)
		}
	}
	var rep Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("prune --format json did not parse: %v\n--- got ---\n%s", err, raw)
	}

	if rep.Mode != "DRY-RUN" {
		t.Errorf("mode = %q, want DRY-RUN — a plan must never be readable as a receipt",
			rep.Mode)
	}
	if rep.Runtime != "podman" {
		t.Errorf("runtime = %q, want the injected podman", rep.Runtime)
	}
	if rep.Declined {
		t.Errorf("declined = true on a fixture where every sweep can run: %+v", rep)
	}
	// The seeded tarballs, measured — this is the assertion the empty-tree
	// version of this test could not make.
	if rep.TotalBytes < tarBytes {
		t.Errorf("total_bytes = %d, want at least the %d bytes of seeded tarballs",
			rep.TotalBytes, tarBytes)
	}
	var imageCache *ReportCategory
	for i := range rep.Categories {
		if rep.Categories[i].Name == "image_cache" {
			imageCache = &rep.Categories[i]
		}
	}
	if imageCache == nil {
		t.Fatalf("no image_cache category; every category must be present even at "+
			"zero, so a consumer never has to guess: %+v", rep.Categories)
	}
	if imageCache.Bytes != tarBytes || imageCache.Count != 2 {
		t.Errorf("image_cache = %d bytes / %d files, want %d / 2",
			imageCache.Bytes, imageCache.Count, tarBytes)
	}
	// Empty lists are `[]`, never `null`: a consumer looping over the value
	// should not have to special-case "nothing happened".
	if rep.RemovedContainers == nil || rep.RemovedImages == nil {
		t.Errorf("removed_* must encode as [] not null: %s", raw)
	}
	// The pre-report accounting is real, not zeroed: the seeded tarballs are
	// under global storage.
	if rep.Usage.GlobalStorageBytes < tarBytes {
		t.Errorf("usage.global_storage_bytes = %d, want at least %d",
			rep.Usage.GlobalStorageBytes, tarBytes)
	}
}

// TestPruneJSONAndTextReportTheSameTotal is the "one measurement, two
// renderings" check. The document is built from the same locals the summary line
// prints, and this is what makes that a checked property: the two runs see the
// same fixture, so the byte total the human line states must be the number the
// document carries.
func TestPruneJSONAndTextReportTheSameTotal(t *testing.T) {
	run := func(t *testing.T, format string) (string, Options, string) {
		t.Helper()
		o, gs := baseOpts(t)
		seedReclaimable(t, gs)
		var buf bytes.Buffer
		o.Out = &buf
		o.IsTTYStdout = func() bool { return false }
		o.Format = format
		if rc := Run(o); rc != 0 {
			t.Fatalf("rc = %d for format %q", rc, format)
		}
		return buf.String(), o, gs
	}

	text, _, _ := run(t, outfmt.Text)
	jsonRaw, _, _ := run(t, outfmt.JSON)

	var rep Report
	if err := json.Unmarshal([]byte(jsonRaw), &rep); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// FmtBytes is what the summary line renders the total with, so the rendered
	// figure is what to look for — comparing against a re-derivation here would
	// just be a second implementation of the same formatter.
	want := FmtBytes(rep.TotalBytes)
	if !strings.Contains(text, want) {
		t.Errorf("the text report never states %q, the total the document carries "+
			"for the same fixture:\n%s", want, text)
	}
	if rep.TotalBytes == 0 {
		t.Error("total_bytes is 0, so the comparison above is vacuous — the fixture " +
			"is supposed to have reclaimable bytes")
	}
}

// TestPruneTextFormatIsUnchangedByTheFlag: the text path must be byte-identical
// with the format left at its zero value and with it spelled out. `--format` was
// added to prune, not woven into it, and the human report is the contract
// existing readers already have.
func TestPruneTextFormatIsUnchangedByTheFlag(t *testing.T) {
	render := func(t *testing.T, format string) string {
		t.Helper()
		o, gs := baseOpts(t)
		seedReclaimable(t, gs)
		var buf bytes.Buffer
		o.Out = &buf
		o.IsTTYStdout = func() bool { return false }
		o.Format = format
		Run(o)
		// The temp root differs per run, so normalize it out.
		return strings.ReplaceAll(buf.String(), gs, "$GS")
	}
	if zero, explicit := render(t, ""), render(t, outfmt.Text); zero != explicit {
		t.Errorf("`--format text` differs from no flag at all\n--- zero ---\n%s\n"+
			"--- text ---\n%s", zero, explicit)
	}
}
