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
		"a newer yolo wrote it", "run `yolo update`"} {
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

	rc, report = applyWith(t, true, nil)
	if rc == 0 || !strings.Contains(report, "run `yolo update`") {
		t.Fatalf("--assert rc=%d, want a refusal naming `yolo update`:\n%s", rc, report)
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
}
