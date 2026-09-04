package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// capturescan_test.go pins `--scan-content-refs` — the flag the macos-user capture cannot work
// without, because that backend's staging home is not the home a materialize will use.
//
// Driven through runInternal, not through capture.Run, so it is the FLAG PARSE and its handoff to
// capture.Options that are measured: drop the case from the switch, or stop passing the bool
// through, and this goes red while internal/capture stays entirely green.

// captureScanFixture builds a booted-looking home plus an installer that embeds $HOME in a text
// file — the launcher-shim shape a real vendor installer writes.
func captureScanFixture(t *testing.T) (home, script string) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script = filepath.Join(t.TempDir(), "install.sh")
	body := "#!/bin/sh\nset -eu\n" +
		"mkdir -p \"$HOME/.local/share/vendor/1.0\"\n" +
		"printf 'bin\\n' > \"$HOME/.local/share/vendor/1.0/vendor\"\n" +
		"printf '#!/bin/sh\\nexec %s/.local/share/vendor/1.0/vendor\\n' \"$HOME\" " +
		"> \"$HOME/.local/bin/vendor-shim\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, script
}

func TestInternalCaptureRunScanContentRefsReachesTheDriver(t *testing.T) {
	home, script := captureScanFixture(t)
	t.Setenv("HOME", home)
	out := filepath.Join(t.TempDir(), "staging-scan")

	if code := runInternal([]string{"capture-run", "--out=" + out, "--scan-content-refs",
		"--", "/bin/sh", script}); code != 0 {
		t.Fatalf("yolo internal capture-run --scan-content-refs exited %d", code)
	}
	m, err := capture.ReadManifest(out)
	if err != nil {
		t.Fatalf("no manifest beside the tree: %v", err)
	}
	if m.RefScan != capture.RefScanFull {
		t.Errorf("refScan = %q, want %q — the flag did not reach capture.Options",
			m.RefScan, capture.RefScanFull)
	}
	if !m.Relocatable {
		t.Errorf("a fully scanned text-only capture is not relocatable: %v", m.NotRelocatable)
	}
	var found bool
	for _, r := range m.AbsoluteRefs {
		if r.Kind == capture.RefFileContent && r.Path == ".local/bin/vendor-shim" {
			found = true
		}
	}
	if !found {
		t.Errorf("the shim's embedded absolute path was not recorded: %+v", m.AbsoluteRefs)
	}
}

// Without the flag the same install is NOT relocatable, so the two runs differ in exactly the
// thing the flag controls and nothing else.
func TestInternalCaptureRunWithoutTheScanFlagIsNotRelocatable(t *testing.T) {
	home, script := captureScanFixture(t)
	t.Setenv("HOME", home)
	out := filepath.Join(t.TempDir(), "staging-noscan")

	if code := runInternal([]string{"capture-run", "--out=" + out, "--", "/bin/sh", script}); code != 0 {
		t.Fatalf("yolo internal capture-run exited %d", code)
	}
	m, err := capture.ReadManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.RefScan != capture.RefScanSymlinks || m.Relocatable {
		t.Errorf("refScan = %q relocatable = %v, want %q / false",
			m.RefScan, m.Relocatable, capture.RefScanSymlinks)
	}
}
