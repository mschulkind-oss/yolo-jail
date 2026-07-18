package pscmd

import (
	"bytes"
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
		RunCmd:        func([]string) (string, error) { return "", nil },
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
		RunCmd: func(argv []string) (string, error) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return psOut, nil
			}
			if len(argv) >= 2 && argv[1] == "top" {
				// yolo-a healthy (has a user proc); yolo-b n/a (workspace gone
				// short-circuits before top).
				return "COMMAND\nbash\nclaude\n", nil
			}
			return "", nil
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
		RunCmd: func(argv []string) (string, error) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return "yolo-live\tUp\tnow\n", nil
			}
			return "", nil
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
