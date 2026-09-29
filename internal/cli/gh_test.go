package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// runMainCaptured runs Main with os.Stdout and os.Stderr captured.
func runMainCaptured(t *testing.T, argv ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	outF, _ := os.Create(filepath.Join(dir, "out"))
	errF, _ := os.Create(filepath.Join(dir, "err"))
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	rc := Main(append([]string{"yolo"}, argv...))
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outF.Close()
	_ = errF.Close()
	out, _ := os.ReadFile(filepath.Join(dir, "out"))
	errb, _ := os.ReadFile(filepath.Join(dir, "err"))
	return rc, string(out), string(errb)
}

// `yolo gh` carries gh's own stdout and stderr, so Main routes it before the front door:
// no startup banner reaches stderr, and no global flag is taken out of gh's argv. Both
// fail if the early route in Main is deleted, because the registry path prints the banner
// and strips `-v`.
func TestMainRoutesGHBeforeTheFrontDoor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(ghbroker.EndpointEnv, "")
	t.Setenv(paths.VerboseEnv, "")
	os.Unsetenv(paths.VerboseEnv)
	t.Chdir(t.TempDir())

	rc, out, errOut := runMainCaptured(t, "gh", "run", "view", "-v")
	if rc != ghbroker.ExitUnavailable {
		t.Fatalf("rc %d (want 69: no endpoint in this test), stderr %q", rc, errOut)
	}
	if !strings.HasPrefix(errOut, "gh (github-broker):") || strings.Contains(errOut, "yolo-jail ") {
		t.Fatalf("stderr must be the forwarder's alone, with no startup banner: %q", errOut)
	}
	if out != "" {
		t.Fatalf("stdout %q", out)
	}
	if v := os.Getenv(paths.VerboseEnv); v != "" {
		t.Fatalf("gh's own -v was taken as yolo's verbose flag (%s=%q)", paths.VerboseEnv, v)
	}

	rc, out, errOut = runMainCaptured(t, "gh", "--help")
	if rc != 0 || strings.TrimSpace(out) != strings.TrimSpace(ghUsage) || errOut != "" {
		t.Fatalf("`yolo gh --help`: rc %d out %q err %q", rc, out, errOut)
	}
}

// After `--`, --help is gh's.
func TestGHHelpAfterTheSeparatorIsForwarded(t *testing.T) {
	t.Setenv(ghbroker.EndpointEnv, "")
	rc, out, _ := runMainCaptured(t, "gh", "--", "--help")
	if rc != ghbroker.ExitUnavailable || strings.Contains(out, "Usage: yolo gh") {
		t.Fatalf("rc %d out %q", rc, out)
	}
}
