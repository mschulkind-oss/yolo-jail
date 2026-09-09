package check

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCheckJSONParsesAndAgreesWithTheText is enforcement item 4 of
// docs/design/self-documenting-cli.md for `check`: the output is PARSED, not
// string-matched, and it is compared against the human report of the SAME
// fixture rather than against a second expectation someone typed.
//
// That comparison is the substance. A JSON report assembled by a second
// traversal of the sections could agree with the text on the day it was written
// and diverge on any day after; asserting that every [PASS]/[WARN]/[FAIL] badge
// in the text has exactly one finding of that status in the document is what
// makes "one measurement, two renderings" a checked property rather than a
// comment.
func TestCheckJSONParsesAndAgreesWithTheText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// The same no-runtime fixture the golden pins, rendered twice.
	var textOut bytes.Buffer
	textOpts := baseOptions(t, &textOut)
	textRC := Check(textOpts)

	var jsonOut bytes.Buffer
	jsonOpts := baseOptions(t, &jsonOut)
	jsonOpts.Format = "json"
	jsonRC := Check(jsonOpts)

	if textRC != jsonRC {
		t.Errorf("exit codes differ by format: text=%d json=%d — `--format json` "+
			"must change the rendering, never the verdict", textRC, jsonRC)
	}

	raw := jsonOut.String()
	if strings.Contains(raw, "\x1b[") {
		t.Error("JSON output carries ANSI escapes; it must be plain and parseable")
	}
	// Parsed, not matched. A string-match test passes on output that no decoder
	// would accept.
	var rep Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("check --format json did not parse: %v\n--- got ---\n%s", err, raw)
	}

	// The human report must be GONE from stdout, not merely accompanied by JSON:
	// a consumer reading stdout has to get a document and nothing else.
	if strings.Contains(raw, "[PASS]") || strings.Contains(raw, "YOLO Jail Check") {
		t.Errorf("the human report leaked into the JSON stream:\n%s", raw)
	}

	// The fixture has real state — this is not an empty-report pass. The
	// no-runtime golden carries four FAILs and five WARNs.
	if len(rep.Findings) == 0 {
		t.Fatal("no findings recorded; the fixture grades several sections, so an " +
			"empty report means the recording is not wired to ok/fail/warn")
	}
	if rep.Version != "9.9.9-test" {
		t.Errorf("version = %q, want the injected 9.9.9-test", rep.Version)
	}

	// Counts vs the badges the text actually printed.
	text := stripANSI(textOut.String())
	for _, c := range []struct {
		badge  string
		status string
		got    int
	}{
		{"[PASS]", "pass", rep.Passed},
		{"[WARN]", "warn", rep.Warned},
		{"[FAIL]", "fail", rep.Failed},
	} {
		if want := strings.Count(text, c.badge); want != c.got {
			t.Errorf("%s: text printed %d badges, report counts %d",
				c.badge, want, c.got)
		}
		graded := 0
		for _, f := range rep.Findings {
			if f.Status == c.status {
				graded++
			}
		}
		if graded != c.got {
			t.Errorf("%s: report counts %d but carries %d findings with status %q",
				c.badge, c.got, graded, c.status)
		}
	}

	// Every finding is attributed to a section, and the section header really
	// appeared in the report. An unattributed finding is one a consumer cannot
	// route.
	for _, f := range rep.Findings {
		if f.Section == "" {
			t.Errorf("finding %q has no section", f.Message)
			continue
		}
		if !strings.Contains(text, f.Section) {
			t.Errorf("finding %q claims section %q, which the report never printed",
				f.Message, f.Section)
		}
	}
}

// TestCheckJSONEmittedAtAnEarlyExit pins the half a happy-path test cannot see.
// Check has five exits and four of them are EARLY — a config parse error and
// three accumulated-failure gates — and a caller that emits the document at the
// end only would answer those four with an empty stream, which is the one
// outcome a machine consumer cannot diagnose.
//
// The no-runtime fixture takes one of them: the accumulated-fail gate after
// Merged Configuration, which returns 1 without ever reaching the sections
// below it.
func TestCheckJSONEmittedAtAnEarlyExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Format = "json"

	if rc := Check(opts); rc != 1 {
		t.Fatalf("rc = %d, want 1 — this fixture must take an early failing exit", rc)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("early exit produced no parseable document: %v\n--- got ---\n%s",
			err, out.String())
	}
	if rep.Failed == 0 {
		t.Errorf("early exit reported no failures, but rc was 1: %+v", rep)
	}
	// It stopped early, so the sections after the gate must be absent — proof
	// this really is the early path and not the full run.
	for _, f := range rep.Findings {
		if f.Section == "Summary" {
			continue
		}
		if strings.Contains(f.Section, "Host launch wrappers") {
			t.Errorf("the run reached a post-gate section (%q); this fixture is "+
				"supposed to exit early", f.Section)
		}
	}
}
