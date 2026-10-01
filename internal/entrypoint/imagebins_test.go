package entrypoint

// imagebins_test.go keeps this package's tests off the machine's /bin and /usr/bin.
//
// THE IMAGE'S BIN DIRS ARE THE MACHINE'S UNLESS A TEST REPLACES THEM. The launcher generators
// write no launcher for a name imageProbeBase already provides (launchercollision.go), and its
// value is the absolute /bin:/usr/bin, which no fake HOME moves. So on a host whose /usr/bin
// holds pnpm, pi or claude (a distribution's package, or `npm install -g` with the prefix
// /usr), every test asserting that one of those launchers is written failed, while it passed
// in a jail and on CI, whose /usr/bin holds none. TestMain hands every test an empty stand-in
// instead; a test whose subject is a binary every machine has (sh) asks for the real dirs back
// with useProductionImageBins.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// productionImageProbeBase is imageProbeBase as the package initialized it, saved before
// TestMain replaces it.
var productionImageProbeBase string

// runWithStandInImageBins runs the tests with imageProbeBase pointed at an empty directory.
func runWithStandInImageBins(m *testing.M) int {
	productionImageProbeBase = imageProbeBase
	dir, err := os.MkdirTemp("", "yolo-test-image-bins-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "entrypoint tests: no stand-in for the image's bin dirs:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	defer OverrideImageProbeBase(dir)()
	return m.Run()
}

// useProductionImageBins points imageProbeBase back at the dirs a boot searches, the machine's
// own /bin and /usr/bin, for the rest of t.
func useProductionImageBins(t *testing.T) {
	t.Helper()
	t.Cleanup(OverrideImageProbeBase(productionImageProbeBase))
}

// TestTheGeneratorsReadTheImageBinsATestHandsThem: every test starts from an empty stand-in
// rather than the machine's folders, and a name in the stand-in a test installs gets no launcher
// from either generator, while the same names get theirs with the stand-in empty. It fails if
// TestMain stops installing the stand-in, or if OverrideImageProbeBase stops reaching what
// GenerateAgentLaunchers and GeneratePackageManagerLaunchers search, either of which leaves
// every launcher test here reading the machine's /bin and /usr/bin again.
func TestTheGeneratorsReadTheImageBinsATestHandsThem(t *testing.T) {
	if productionImageProbeBase == "" || imageProbeBase == productionImageProbeBase {
		t.Fatalf("the tests search the machine's own %s: TestMain installed no stand-in", imageProbeBase)
	}
	if entries, err := os.ReadDir(imageProbeBase); err != nil || len(entries) != 0 {
		t.Fatalf("the stand-in %s is not an empty directory (%d entries, err=%v)",
			imageProbeBase, len(entries), err)
	}

	image := t.TempDir()
	t.Cleanup(OverrideImageProbeBase(image))
	launchers := func() (agent, pnpm bool) {
		t.Helper()
		e := NewEnv(map[string]string{
			"JAIL_HOME":      t.TempDir(),
			"YOLO_PACK_ROOT": writePackWithProgram(t, "standin", "standin-agent"),
		})
		e.Stderr = io.Discard
		if err := GenerateAgentLaunchers(e); err != nil {
			t.Fatal(err)
		}
		if err := GeneratePackageManagerLaunchers(e); err != nil {
			t.Fatal(err)
		}
		_, errAgent := os.Stat(filepath.Join(e.LaunchDir(), "standin-agent"))
		_, errPnpm := os.Stat(filepath.Join(e.LaunchDir(), "pnpm"))
		return errAgent == nil, errPnpm == nil
	}

	if agent, pnpm := launchers(); !agent || !pnpm {
		t.Fatalf("with the stand-in empty: agent launcher %v, pnpm launcher %v, want both", agent, pnpm)
	}
	for _, name := range []string{"standin-agent", "pnpm"} {
		if err := os.WriteFile(filepath.Join(image, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if agent, pnpm := launchers(); agent || pnpm {
		t.Errorf("with both names in the image's stand-in: agent launcher %v, pnpm launcher %v, "+
			"want neither — the generators did not search the dirs the test handed them", agent, pnpm)
	}
}
