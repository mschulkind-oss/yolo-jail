package openaiauthhost

// privatepin_test.go pins docs/reference/pinned-host-helpers.md ("Who execs it"): the OpenAI credential
// daemon a jail-less launch starts runs from the launch's pinned yolo, never from os.Executable(),
// is told which launch it serves, and is not started at all when the pin is gone.

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
)

// recordingYolo is a bundle whose host yolo is a script that records its own path, its arguments and
// the jail it was told it serves, then exits 1, so a start that reaches it fails fast and leaves the
// record. It returns the pin, and the record's path.
func recordingYolo(t *testing.T) (*helperpin.Pin, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "record")
	dir := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n{ echo \"argv0=$0\"; echo \"args=$*\"; echo \"jail=$YOLO_HELPER_JAIL\"; } > '" + record + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := helperpin.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	return &pin, record
}

// The daemon's argv[0] is the pin's path and it is handed the jail label. Fails if startPrivate
// goes back to execx.SelfExecArgv (argv[0] would be this test binary, and no record is written).
func TestThePrivateDaemonRunsFromThePin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pin, record := recordingYolo(t)
	_, _, err := startPrivate(io.Discard, pinnedExec(func() (*helperpin.Pin, error) { return pin, nil }), "/work/space")
	if err == nil {
		t.Fatal("the recording yolo exits at once, so the start must report it")
	}
	data, rerr := os.ReadFile(record)
	if rerr != nil {
		t.Fatalf("the pinned yolo never ran: %v (start said: %v)", rerr, err)
	}
	for _, want := range []string{"argv0=" + pin.Path, "args=internal daemon " + BrokerName, "jail=/work/space"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the daemon's record lacks %q:\n%s", want, data)
		}
	}
}

// A pin whose file was replaced starts nothing, and the refusal names the file and the next step.
func TestThePrivateDaemonIsNotStartedFromAReplacedPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pin, record := recordingYolo(t)
	if err := os.WriteFile(pin.Path, []byte("#!/bin/sh\nexit 0 # replaced, so a different size\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := startPrivate(io.Discard, pinnedExec(func() (*helperpin.Pin, error) { return pin, nil }), "")
	if err == nil || !strings.Contains(err.Error(), pin.Path) || !strings.Contains(err.Error(), "Reinstall yolo") {
		t.Fatalf("a replaced pin must refuse, naming %s and the reinstall step; got %v", pin.Path, err)
	}
	if _, serr := os.Stat(record); serr == nil {
		t.Error("the replaced yolo ran anyway")
	}
}

// A launch that resolved no pin refuses; there is no fallback to the running binary.
func TestThePrivateDaemonIsNotStartedWithNoPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, _, err := startPrivate(io.Discard, pinnedExec(nil), "")
	if err == nil || !strings.Contains(err.Error(), "Reinstall yolo") {
		t.Fatalf("no pin must refuse with the reinstall step; got %v", err)
	}
}
