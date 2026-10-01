package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloornewer_test.go pins, at each call site, that a host floor record a newer yolo wrote is
// refused rather than installed over (hostfloor.Floor.Ensure; docs/design/host-tool-provisioning.md
// HP-D8): the launch, `yolo host apply`'s dry run and --assert, and the dependency line
// check-deps prints. Each used to read the record's error as "missing", and the launch and the
// --assert installed this yolo's copy over the newer one's.

// writeNewerFloorRecord puts a record for floorcli that a newer yolo wrote in the test floor, and
// returns its path and its bytes.
func writeNewerFloorRecord(t *testing.T) (string, string) {
	t.Helper()
	rec := filepath.Join(paths.HostFloorDir(), "records", "floorcli.json")
	body := `{"schema": 99, "bin": "floorcli"}`
	writeFile(t, rec, body)
	return rec, body
}

func assertRecordKept(t *testing.T, rec, body string) {
	t.Helper()
	if got, err := os.ReadFile(rec); err != nil || string(got) != body {
		t.Errorf("the newer yolo's record was rewritten (%v):\n%s", err, got)
	}
}

// The launch refuses, names `yolo update`, and execs nothing.
func TestHostLaunchRefusesToInstallOverANewerYolosFloorRecord(t *testing.T) {
	dist, _ := floorLaunchFixture(t, "")
	rec, body := writeNewerFloorRecord(t)
	got := captureHostExec(t)
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil)
	if rc != 127 || got.execed {
		t.Fatalf("rc=%d execed=%v (target %s), want 127 and no exec\n%s", rc, got.execed, got.target, errw.String())
	}
	for _, want := range []string{"will not install floorcli over a newer yolo's copy", rec,
		"a newer yolo wrote it", "run `yolo update`",
		// The step that works when `yolo update` finds nothing newer (the record is another
		// install's, or this yolo was chosen on purpose).
		"To run this yolo's own copy instead, remove that record: the next `yolo host -- floorcli` installs one"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
	if n := len(dist.NpmCalls("install")); n != 0 {
		t.Errorf("npm install ran %d time(s) over a newer yolo's record", n)
	}
	assertRecordKept(t, rec, body)
}

// The dry run says it will not install it and why; the --assert refuses it; check-deps does not
// say the floor installs it.
func TestHostApplyRefusesToInstallOverANewerYolosFloorRecord(t *testing.T) {
	dist, _ := floorHostFixture(t, "")
	rec, body := writeNewerFloorRecord(t)

	rc, report := applyWith(t, false, nil)
	if rc != 0 || strings.Contains(report, "floorcli: would install") {
		t.Fatalf("dry run rc=%d, and it must not say it would install over the newer record:\n%s", rc, report)
	}
	if !strings.Contains(report, "floorcli: will not install it: host floor record "+rec) ||
		!strings.Contains(report, "run `yolo update`") {
		t.Errorf("the dry run does not say why it will not install floorcli:\n%s", report)
	}
	if strings.Contains(report, "yolo's floor installs it") {
		t.Errorf("the dependency line says the floor installs floorcli:\n%s", report)
	}
	doc, raw, _ := hostApplyJSON(t, "--format", "json")
	if len(doc.HostFloor) != 1 || doc.HostFloor[0].Action != "would refuse" ||
		!strings.Contains(doc.HostFloor[0].Reason, "run `yolo update`") {
		t.Errorf("host_floor = %+v, want floorcli refused with the update named:\n%s", doc.HostFloor, raw)
	}

	// The Node a newer yolo's program may run on: this yolo cannot read that record to know, so
	// the --assert's Reconcile must not drop it as unused.
	newerNode := filepath.Join(paths.HostFloorDir(), "node", "v99.0.0")
	writeFile(t, filepath.Join(newerNode, "bin", "node"), "#!/bin/sh\n")
	rc, report = applyWith(t, true, nil)
	if rc == 0 || !strings.Contains(report, "floorcli: will not install it: host floor record "+rec) ||
		!strings.Contains(report, "run `yolo update`") {
		t.Fatalf("--assert rc=%d, want a refusal naming `yolo update`:\n%s", rc, report)
	}
	if strings.Contains(report, "could not install") {
		t.Errorf("the --assert calls a refusal a failed install:\n%s", report)
	}
	if _, err := os.Stat(newerNode); err != nil {
		t.Errorf("the --assert removed the Node a newer yolo's record may run on: %v", err)
	}
	if n := len(dist.NpmCalls("install")); n != 0 {
		t.Errorf("npm install ran %d time(s) over a newer yolo's record", n)
	}
	assertRecordKept(t, rec, body)

	var out, errw bytes.Buffer
	checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	if strings.Contains(out.String(), "yolo's floor installs it") || !strings.Contains(out.String(), "run `yolo update`") {
		t.Errorf("check-deps must name `yolo update`, not an install the floor refuses:\n%s%s",
			out.String(), errw.String())
	}
	// Marked as the warning it is (floorDepMark), never as a pass, here and on the dependency line
	// `yolo host apply --verbose` prints.
	t.Setenv(paths.VerboseEnv, "1")
	_, verbose := applyWith(t, false, nil)
	for label, report := range map[string]string{"check-deps": out.String(), "host apply": verbose} {
		if !strings.Contains(report, "! floorcli ") || strings.Contains(report, "✓ floorcli ") {
			t.Errorf("%s does not mark floorcli `!`:\n%s", label, report)
		}
	}
}

// check-deps exits 1 over a newer yolo's floor record and ends with the re-check, once, as it does
// for a missing dependency: nothing installs that program until the user acts on the step its line
// names. It exited 0 under the `!` line, the one problem the report found and did not count.
func TestCheckDepsFailsOverANewerYolosFloorRecord(t *testing.T) {
	const recheck = "\n  yolo check-deps  # check again\n"
	for _, tc := range []struct{ name, others string }{
		{"the floor's program alone", ""},
		{"a dep present too", `,{"kind":"requires","bin":"sh"}`},
		{"a dep missing too", `,{"kind":"requires","bin":"yolo-cd-absent"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			floorHostFixtureWith(t,
				`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`+tc.others, "")
			writeNewerFloorRecord(t)
			var out, errw bytes.Buffer
			rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
			report := out.String() + errw.String()
			if rc != 1 {
				t.Fatalf("rc = %d over a newer yolo's floor record, want 1:\n%s", rc, report)
			}
			if !strings.Contains(report, "! floorcli ") || !strings.Contains(report, "run `yolo update`") {
				t.Errorf("the report does not mark floorcli `!` with its step:\n%s", report)
			}
			if !strings.HasSuffix(report, recheck) {
				t.Errorf("the report does not end with the re-check %q:\n%s", recheck, report)
			}
			if n := strings.Count(report, "yolo check-deps"); n != 1 {
				t.Errorf("the report names the re-check %d times, want once, at its end:\n%s", n, report)
			}
		})
	}
}

// The verdict counts the floor stage. A program yolo's floor will not install over a newer yolo's
// record leaves an --assert incomplete, so the dry run's verdict says so, with the step that clears
// it, where it said "Nothing to do — this home is up to date." under the floor line saying it
// would not install it; the machine document carries the same outcome. The --assert's verdict says
// it too, where it said "Nothing to apply — this home is up to date." and exited 1. The dry run
// still exits 0: its output is the finding (OQ-RO5).
func TestHostApplyVerdictCountsAProgramTheFloorWillNotInstall(t *testing.T) {
	floorHostFixture(t, "")
	writeNewerFloorRecord(t)
	const blocker = "yolo's floor will not install floorcli over a record a newer yolo wrote until you " +
		"run `yolo update` or remove that record (above)"

	rc, report := applyWith(t, false, nil)
	dryVerdict := "An --assert would be incomplete — " + blocker + "."
	if rc != 0 {
		t.Fatalf("dry run rc = %d, want 0 whatever it finds:\n%s", rc, report)
	}
	if !strings.Contains(report, "\n"+dryVerdict+"\n") || strings.Contains(report, "up to date") {
		t.Errorf("the dry run's verdict is not\n%s\ngot:\n%s", dryVerdict, report)
	}
	doc, raw, _ := hostApplyJSON(t, "--format", "json")
	if doc.Outcome != outcomeIncomplete || doc.Verdict != dryVerdict {
		t.Errorf("outcome %q, verdict %q; want %q and the dry run's verdict:\n%s", doc.Outcome,
			doc.Verdict, outcomeIncomplete, raw)
	}

	rc, report = applyWith(t, true, nil)
	assertVerdict := "Incomplete — " + blocker + "; nothing else needed changing."
	if rc != 1 {
		t.Fatalf("--assert rc = %d, want 1:\n%s", rc, report)
	}
	if !strings.Contains(report, "\n"+assertVerdict+"\n") || strings.Contains(report, "up to date") {
		t.Errorf("the --assert's verdict is not\n%s\ngot:\n%s", assertVerdict, report)
	}
}
