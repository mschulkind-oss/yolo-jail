package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// captureFixture builds a home with a populated capture surface and an installer script that
// adds to it, and returns (home, installer path).
func captureFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "from-the-boot"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "install.sh")
	body := "#!/bin/sh\nset -eu\n" +
		"mkdir -p \"$HOME/.local/share/vendor/1.0\"\n" +
		"printf 'bin\\n' > \"$HOME/.local/share/vendor/1.0/vendor\"\n" +
		"ln -s \"$HOME/.local/share/vendor/1.0/vendor\" \"$HOME/.local/bin/vendor\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, script
}

// `yolo internal capture-run` reaches the driver, and reaches it through the hidden namespace
// SWITCH rather than through a function this test happens to know the name of.
//
// That is the whole point of driving runInternal here: delete `case "capture-run"` from
// internal.go and the argv the host act will emit becomes "unknown command", with the driver
// and all of its own tests still green. This is the test that goes red instead.
//
// It also pins the --home default. The driver's contract with all three backends is "a process
// with a HOME"; the OUT path is an argument for entrypoint.receiptsFile's reason (YOLO_WORKSPACE
// does not exist in a live container and macos-user execs under `env -i`), but HOME is exactly
// the variable that does exist everywhere, so it is read here and nowhere deeper.
func TestInternalCaptureRunDrivesACaptureThroughTheHiddenSwitch(t *testing.T) {
	home, script := captureFixture(t)
	out := filepath.Join(t.TempDir(), "staging-1")
	t.Setenv("HOME", home)

	if code := runInternal([]string{"capture-run", "--out=" + out, "--", "/bin/sh", script}); code != 0 {
		t.Fatalf("yolo internal capture-run exited %d", code)
	}

	m, err := capture.ReadManifest(out)
	if err != nil {
		t.Fatalf("no manifest beside the tree: %v", err)
	}
	want := map[string]bool{
		".local": true, ".local/bin": true, ".local/bin/vendor": true,
		".local/share": true, ".local/share/vendor": true,
		".local/share/vendor/1.0": true, ".local/share/vendor/1.0/vendor": true,
	}
	for _, e := range m.Entries {
		if !want[e.Path] {
			t.Errorf("captured %q, which the installer did not write", e.Path)
		}
		delete(want, e.Path)
	}
	for p := range want {
		t.Errorf("did not capture %q", p)
	}
	if len(m.AbsoluteRefs) != 1 || m.AbsoluteRefs[0].Path != ".local/bin/vendor" {
		t.Errorf("AbsoluteRefs = %+v, want the one absolute symlink", m.AbsoluteRefs)
	}
	if _, err := os.Stat(filepath.Join(capture.TreeDir(out), ".local", "bin", "from-the-boot")); !os.IsNotExist(err) {
		t.Errorf("the boot's own file was captured: %v", err)
	}
}

// The two arguments the driver cannot invent are refused with the usage, not a panic and not a
// capture against the wrong home.
func TestInternalCaptureRunRequiresOutAndACommand(t *testing.T) {
	home, script := captureFixture(t)
	t.Setenv("HOME", home)
	cases := [][]string{
		{"capture-run"},
		{"capture-run", "--out=" + filepath.Join(t.TempDir(), "s")},
		{"capture-run", "--", "/bin/sh", script},
		{"capture-run", "--out=" + filepath.Join(t.TempDir(), "s"), "-x", "--", "/bin/sh", script},
	}
	for _, args := range cases {
		if code := runInternal(args); code != 2 {
			t.Errorf("runInternal(%v) = %d, want 2", args, code)
		}
	}
}
