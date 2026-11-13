package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloorretain_test.go pins two host-floor rules of launch-update-preparation.md ("Explicit
// update and launch materialization") through the `yolo host` front door:
//
//   - a host launch that installs a newly admitted generation never deletes an older complete one,
//     because a host session started on it may still read its bytes; and
//   - a capture is published only on the Node the install runs with, when that Node matches every
//     producer fact.

// hostLaunchProbe runs `yolo host <args> -- probe` with the exec captured, failing on a refusal,
// and returns what the launch said.
func hostLaunchProbe(t *testing.T, args ...string) string {
	t.Helper()
	got := captureHostExec(t)
	var out bytes.Buffer
	if rc := hostMain(append(args, "--", "probe"), io.Discard, &out, false, nil); rc != 0 || got.target == "" {
		t.Fatalf("yolo host %v -- probe: rc %d target %q\n%s", args, rc, got.target, out.String())
	}
	return out.String()
}

// A HOST SESSION'S GENERATION SURVIVES LATER LAUNCHES: three successive launches across three
// admitted generations leave the first generation's directory in place.
func TestHostFloorRetainsAnOlderGenerationAcrossLaterInstalls(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	selectedNPMSharedWorker(t)
	if _, err := prepareOrdinaryForLaunch(req, io.Discard); err != nil {
		t.Fatal(err)
	}
	record, err := readNPMCaptureRecord(f.store, f.in)
	if err != nil {
		t.Fatal(err)
	}
	selectedNPMHostFloor(t, f, record.Resolution.Runtime.Node)
	program := floorPrograms(req.Packs)[0]
	status := func() *hostfloor.Record {
		return newHostFloor(io.Discard, []hostfloor.Program{program}).Status(program).Record
	}

	hostLaunchProbe(t)
	first := status()
	if first == nil || first.Capture == "" {
		t.Fatalf("first launch did not install the admitted capture: %+v", first)
	}
	for _, v := range []string{"2.0.0", "3.0.0"} {
		f.version(t, v)
		hostLaunchProbe(t, "--update")
		if rec := status(); rec == nil || rec.Version != v || rec.Dir == first.Dir {
			t.Fatalf("launch for %s did not install a new generation: %+v", v, rec)
		}
	}
	if _, err := os.Stat(first.Dir); err != nil {
		t.Fatalf("generation A's directory %s was deleted by later launches while a host session may still run it: %v", first.Dir, err)
	}
}

// A CAPTURE IS NEVER PUBLISHED ON A NODE IT WAS NOT PROVEN AGAINST: the installed record runs on
// Node v1, the floor now ships v2, and a newer capture is admitted whose producer facts match v1.
// The install runs on v2, so it must not pair that capture with v2.
func TestHostFloorDoesNotPublishACaptureOnAShippedNodeItWasNotProvenAgainst(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	selectedNPMSharedWorker(t)
	if _, err := prepareOrdinaryForLaunch(req, io.Discard); err != nil {
		t.Fatal(err)
	}
	record, err := readNPMCaptureRecord(f.store, f.in)
	if err != nil {
		t.Fatal(err)
	}
	v1 := record.Resolution.Runtime.Node
	selectedNPMHostFloor(t, f, v1)
	program := floorPrograms(req.Packs)[0]
	hostLaunchProbe(t)
	first := newHostFloor(io.Discard, []hostfloor.Program{program}).Status(program).Record
	if first == nil || first.Capture == "" || "v"+first.Node != v1 {
		t.Fatalf("first launch did not install the capture on %s: %+v", v1, first)
	}

	// THE SHIPPED NODE MOVES to v2: a different binary (a wrapper around the same node, so its
	// digest differs from every producer fact), beside the same fixture npm.
	const v2 = "99.0.0"
	v2Bin := filepath.Join(paths.HostFloorDir(), "node", "v"+v2, "bin")
	if err := os.MkdirAll(v2Bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(v2Bin, "node"), "#!/bin/sh\nexec "+f.node+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(v2Bin, "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	npm, err := os.ReadFile(filepath.Join(paths.HostFloorDir(), "node", v1, "bin", "npm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2Bin, "npm"), npm, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(filepath.Dir(v2Bin), ".yolo-floor-complete"), "")
	previous := newHostFloor
	newHostFloor = func(out io.Writer, p []hostfloor.Program) *hostfloor.Floor {
		floor := previous(out, p)
		floor.Node.Shipped = v2
		return floor
	}
	t.Cleanup(func() { newHostFloor = previous })

	// A NEWER CAPTURE IS ADMITTED, proven against the producer's (v1's) facts.
	f.version(t, "2.0.0")
	said := hostLaunchProbe(t, "--update")
	rec := newHostFloor(io.Discard, []hostfloor.Program{program}).Status(program).Record
	if rec == nil || rec.Version != "2.0.0" {
		t.Fatalf("the update did not install 2.0.0: %+v\n%s", rec, said)
	}
	if rec.Node == v2 && rec.Capture != "" {
		t.Fatalf("the capture %s (proven against Node %s) was published on the shipped Node v%s: %+v", rec.Capture, v1, v2, rec)
	}
	// NOT EVEN FOR A MOMENT: a later pass of the same launch repairing a published mismatch still
	// served it through the launcher. Every install after the bump runs on v2, so none may hold a
	// materialized capture (its home/), and nothing may need repairing.
	installs, err := os.ReadDir(filepath.Dir(first.Dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range installs {
		dir := filepath.Join(filepath.Dir(first.Dir), e.Name())
		if dir == first.Dir {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "home")); err == nil {
			t.Errorf("an install after the Node bump materialized a capture proven against Node %s: %s\n%s", v1, dir, said)
		}
	}
	if strings.Contains(said, "capture no longer matches this runtime") {
		t.Errorf("the launch published a capture it then had to repair:\n%s", said)
	}
	if !strings.Contains(said, "not proven on the Node it installs on") {
		t.Errorf("the fallback to the bounded installer was not said:\n%s", said)
	}
}
