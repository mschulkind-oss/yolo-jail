package macosuser

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// recordedrun_test.go pins the capture bootstrap's output reaching the act's writer, Out, which a
// build jail's log keeps (the 2026-10-23 Mac suite run: a self-mode
// build's bootstrap failed on a Mac and the log held nothing of why).

// isBootstrapArgv reports whether argv is a capture's bootstrap crossing.
func isBootstrapArgv(argv []string) bool { return slices.Contains(argv, "darwin-bootstrap") }

// bootstrapRecordingDeps is captureActDeps with a RunRecorded that answers the bootstrap with
// output and status, and a Run that fails the test if the bootstrap reaches it instead.
func bootstrapRecordingDeps(t *testing.T, rec *[]string, root string, output, stderr string, status int) (Deps, *int) {
	t.Helper()
	d := captureActDeps(t, rec, root)
	run := d.Run
	d.Run = func(argv []string) int {
		if isBootstrapArgv(argv) {
			t.Errorf("the bootstrap ran through Run, whose output no build log keeps: %v", argv)
		}
		return run(argv)
	}
	recorded := 0
	d.RunRecorded = func(argv []string, keep int) RecordedRun {
		if !isBootstrapArgv(argv) {
			t.Errorf("RunRecorded ran something other than the bootstrap: %v", argv)
		}
		if keep != bootstrapOutputKept {
			t.Errorf("the bootstrap's output is kept to %d bytes, want %d", keep, bootstrapOutputKept)
		}
		recorded++
		*rec = append(*rec, "run:"+strings.Join(argv, " "))
		return RecordedRun{Output: output, Stderr: stderr, Status: status}
	}
	return d, &recorded
}

// A FAILED BOOTSTRAP'S OWN OUTPUT, EXIT STATUS AND STDERR REACH OUT, in both acts, and the stop
// names a next step. A bracket the bootstrap printed is not read as markup. Fails if
// runCaptureSteps runs the bootstrap through Run again, or stops printing what it kept.
func TestAFailedCaptureBootstrapReportsItsOwnOutput(t *testing.T) {
	const deny = "sandbox-exec: deny(1) file-read-data /Users/you/.local/share/yolo-jail/home [bold]x"
	for name, act := range captureActs(t) {
		var rec []string
		var buf bytes.Buffer
		d, recorded := bootstrapRecordingDeps(t, &rec, act.root, "boot: step one\n"+deny+"\n", deny+"\n", 71)
		d.Out = &buf
		if rc := act.run(d); rc == 0 {
			t.Fatalf("%s: a failed bootstrap did not stop the act", name)
		}
		got := buf.String()
		if *recorded != 1 {
			t.Fatalf("%s: the bootstrap was recorded %d times, want 1:\n%s", name, *recorded, got)
		}
		for what, want := range map[string]string{
			"its output":      "boot: step one",
			"its stderr":      "deny(1) file-read-data /Users/you/.local/share/yolo-jail/home",
			"the literal tag": "[\u2060bold]x",
			"its exit status": "capture bootstrap failed (exit 71)",
			"the next step":   "Fix what its output names, then run the build or capture again",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the refusal does not carry %s (%q):\n%s", name, what, want, got)
			}
		}
		if strings.Count(got, "deny(1)") != 2 {
			t.Errorf("%s: want the deny once in the output block and once in the stderr tail:\n%s", name, got)
		}
	}
}

// A BOOTSTRAP THAT SUCCEEDS STILL LEAVES ITS OUTPUT IN OUT, so the build log holds what it did, and
// a bootstrap that wrote nothing on stderr says so when it fails.
func TestACaptureBootstrapsOutputIsKeptWhenItSucceeds(t *testing.T) {
	act := captureActs(t)["capture"]
	var rec []string
	var buf bytes.Buffer
	d, _ := bootstrapRecordingDeps(t, &rec, act.root, "boot: launchers written\n", "", 0)
	d.Out = &buf
	if rc := act.run(d); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "boot: launchers written") {
		t.Errorf("a successful bootstrap's output did not reach Out:\n%s", buf.String())
	}

	buf.Reset()
	act = captureActs(t)["capture"]
	d, _ = bootstrapRecordingDeps(t, &rec, act.root, "", "", 1)
	d.Out = &buf
	if rc := act.run(d); rc == 0 {
		t.Fatal("a failed bootstrap did not stop the act")
	}
	if !strings.Contains(buf.String(), "It wrote nothing on stderr.") {
		t.Errorf("a silent failed bootstrap does not say it was silent:\n%s", buf.String())
	}
}

// THE PRODUCTION SEAM RECORDS: RealDeps carries runRecordedReal, which keeps a child's stdout and
// stderr together and stderr alone, its exit status, and no more than it was told to keep.
func TestRunRecordedRealKeepsTheTailAndTheStatus(t *testing.T) {
	if RealDeps(nil, nil, false).RunRecorded == nil {
		t.Fatal("RealDeps has no RunRecorded, so a real capture's bootstrap output is never kept")
	}
	r := runRecordedReal([]string{"/bin/sh", "-c", "echo out; echo err >&2; exit 3"}, 1024)
	if r.Status != 3 || !strings.Contains(r.Output, "out") || !strings.Contains(r.Output, "err") ||
		strings.TrimSpace(r.Stderr) != "err" || r.Cut {
		t.Errorf("runRecordedReal kept %+v", r)
	}
	r = runRecordedReal([]string{"/bin/sh", "-c", "i=0; while [ $i -lt 400 ]; do echo line-$i-0123456789 >&2; i=$((i+1)); done; echo last >&2"}, 512)
	if len(r.Output) > 512 || len(r.Stderr) > 512 || !r.Cut || !strings.HasSuffix(r.Stderr, "last\n") {
		t.Errorf("runRecordedReal did not keep only the last 512 bytes: output %d, stderr %d, cut %v, tail %q",
			len(r.Output), len(r.Stderr), r.Cut, r.Stderr[max(0, len(r.Stderr)-20):])
	}
}
