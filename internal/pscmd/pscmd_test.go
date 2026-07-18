package pscmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestPsNoJails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	rc := Run(Deps{
		DetectRuntime: func() string { return "podman" },
		RunCmd:        func([]string) (string, bool) { return "", true },
		PathIsDir:     func(string) bool { return true },
		Out:           &buf,
	})
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if buf.String() != "No running jails.\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestPsTableAndProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Seed a tracking file so workspace resolves without an inspect call.
	must(t, runtime.WriteContainerTracking("yolo-a-1111", "/exists"))
	must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))

	psOut := "yolo-a-1111\tUp 2 hours\t2 hours ago\n" +
		"yolo-b-2222\tUp 5 minutes\t5 minutes ago\n"
	deps := Deps{
		DetectRuntime: func() string { return "podman" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return psOut, true
			}
			if len(argv) >= 2 && argv[1] == "top" {
				// yolo-a healthy (has a user proc); yolo-b n/a (workspace gone
				// short-circuits before top).
				return "COMMAND\nbash\nclaude\n", true
			}
			return "", true
		},
		PathIsDir: func(path string) bool { return path == "/exists" },
		Out:       &bytes.Buffer{},
	}
	var buf bytes.Buffer
	deps.Out = &buf
	Run(deps)
	out := buf.String()
	// Table header + both rows present.
	if !strings.Contains(out, "CONTAINER") || !strings.Contains(out, "yolo-a-1111") || !strings.Contains(out, "yolo-b-2222") {
		t.Errorf("table missing rows:\n%s", out)
	}
	// yolo-b flagged workspace-gone.
	if !strings.Contains(out, "1 problem jail(s)") || !strings.Contains(out, "yolo-b-2222  (workspace gone)") {
		t.Errorf("problem section wrong:\n%s", out)
	}
	if !strings.Contains(out, "Run 'yolo doctor' to clean up") {
		t.Errorf("missing doctor hint:\n%s", out)
	}
}

func TestPsPrunesStaleTracking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	must(t, runtime.WriteContainerTracking("yolo-live", "/ws"))
	must(t, runtime.WriteContainerTracking("yolo-dead", "/ws2"))

	deps := Deps{
		DetectRuntime: func() string { return "podman" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return "yolo-live\tUp\tnow\n", true
			}
			return "", true
		},
		PathIsDir: func(string) bool { return true },
		Out:       &bytes.Buffer{},
	}
	Run(deps)
	// The dead tracking file must be gone; the live one kept.
	if _, ok := runtime.ReadContainerWorkspace("yolo-dead"); ok {
		t.Error("stale tracking file should be pruned")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live"); !ok {
		t.Error("live tracking file should survive")
	}
	_ = paths.ContainerDir()
}

// TestPsEnumerationFailureDoesNotPrune is the audit §D11 regression: when the
// runtime probe FAILS (ok=false — e.g. `podman ps` on a macOS host running only
// Apple Container), ps must NOT prune tracking files (they belong to live jails
// the failed probe couldn't see) and must NOT print the misleading "No running
// jails."
func TestPsEnumerationFailureDoesNotPrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	must(t, runtime.WriteContainerTracking("yolo-live-1", "/ws1"))
	must(t, runtime.WriteContainerTracking("yolo-live-2", "/ws2"))

	var buf bytes.Buffer
	Run(Deps{
		DetectRuntime: func() string { return "podman" },
		// Probe fails to enumerate (ok=false).
		RunCmd:    func([]string) (string, bool) { return "", false },
		PathIsDir: func(string) bool { return true },
		Out:       &buf,
	})
	// Both tracking files MUST survive — pruning them would orphan live jails.
	if _, ok := runtime.ReadContainerWorkspace("yolo-live-1"); !ok {
		t.Error("D11 regression: tracking file deleted on enumeration failure")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live-2"); !ok {
		t.Error("D11 regression: tracking file deleted on enumeration failure")
	}
	if strings.Contains(buf.String(), "No running jails.") {
		t.Errorf("must not claim 'No running jails' on a failed probe: %q", buf.String())
	}
}

// TestPsRuntimePlatformAware asserts macOS prefers Apple Container (§D11).
func TestPsRuntimePlatformAware(t *testing.T) {
	t.Setenv("YOLO_RUNTIME", "")
	os.Unsetenv("YOLO_RUNTIME")
	// macOS with the container CLI present → "container", not "podman".
	if rt := runtime.PsRuntime(true, func(b string) bool { return b == "container" }); rt != "container" {
		t.Errorf("macOS should prefer container, got %q", rt)
	}
	// Linux → podman.
	if rt := runtime.PsRuntime(false, func(string) bool { return true }); rt != "podman" {
		t.Errorf("Linux should use podman, got %q", rt)
	}
	// Explicit override wins everywhere.
	t.Setenv("YOLO_RUNTIME", "podman")
	if rt := runtime.PsRuntime(true, func(string) bool { return true }); rt != "podman" {
		t.Errorf("YOLO_RUNTIME override should win, got %q", rt)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
