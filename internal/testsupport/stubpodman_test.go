package testsupport

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStubPodmanAnswersFirstAndRefuses(t *testing.T) {
	cleanup := StubPodman()
	path, err := exec.LookPath("podman")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "yolo-test-podman-") {
		t.Errorf("podman resolves to %s, not the stub", path)
	}
	out, err := exec.Command("podman", "info").CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 125 {
		t.Errorf("the stub must exit 125, got %v: %s", err, out)
	}
	if out, err := exec.Command("podman", "ps", "-a", "-q").CombinedOutput(); err != nil || len(out) != 0 {
		t.Errorf("ps must answer an empty list, got %v: %q", err, out)
	}
	cleanup()
	if p, err := exec.LookPath("podman"); err == nil && strings.Contains(p, "yolo-test-podman-") {
		t.Errorf("cleanup left the stub on PATH: %s", p)
	}
}
