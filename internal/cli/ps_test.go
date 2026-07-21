package cli

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
	rc := psRun(psDeps{
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
	deps := psDeps{
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
	psRun(deps)
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

	deps := psDeps{
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
	psRun(deps)
	// The dead tracking file must be gone; the live one kept.
	if _, ok := runtime.ReadContainerWorkspace("yolo-dead"); ok {
		t.Error("stale tracking file should be pruned")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live"); !ok {
		t.Error("live tracking file should survive")
	}
	_ = paths.ContainerDir()
}

// TestPsContainerRuntimeKeepsLiveTracking documents the end state the unified
// resolver enables on an Apple Container host (the destructive §B/D11 bug): with
// the runtime resolved to "container", ps enumerates via `container ls` and the
// stale-tracking prune keeps the live jail's file while dropping the dead one —
// rather than the old config-blind "podman" pick that saw nothing and wiped both.
func TestPsContainerRuntimeKeepsLiveTracking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	must(t, runtime.WriteContainerTracking("yolo-live", "/ws"))
	must(t, runtime.WriteContainerTracking("yolo-dead", "/ws2"))

	deps := psDeps{
		DetectRuntime: func() string { return "container" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[0] == "container" && argv[1] == "ls" {
				return "ID  IMAGE  STATE\nyolo-live img running\n", true
			}
			return "", true
		},
		PathIsDir: func(string) bool { return true },
		Out:       &bytes.Buffer{},
	}
	psRun(deps)
	if _, ok := runtime.ReadContainerWorkspace("yolo-dead"); ok {
		t.Error("dead tracking file should be pruned on the container runtime")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live"); !ok {
		t.Error("live AC jail's tracking file must survive")
	}
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
	psRun(psDeps{
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

// TestDetectListingRuntimeHonorsConfig covers audit finding 5: `yolo ps` (and
// prune) never consulted the workspace `runtime` key. detectListingRuntime now
// loads the config and feeds runtime.ResolveRuntime, so a workspace pinned to
// Apple Container resolves to "container" even on the Linux test host (config
// precedence wins before the platform branch). RED before the commands.go wiring
// (detectListingRuntime did not exist and ps loaded no config).
func TestDetectListingRuntimeHonorsConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "")
	os.Unsetenv("YOLO_RUNTIME")
	ws := t.TempDir()
	if err := os.WriteFile(ws+"/yolo-jail.jsonc", []byte(`{"runtime":"container"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectListingRuntime(ws); got != "container" {
		t.Errorf("detectListingRuntime honoring config = %q, want container", got)
	}

	// A workspace with no config and no override → the Linux platform default.
	empty := t.TempDir()
	if got := detectListingRuntime(empty); got != "podman" {
		t.Errorf("detectListingRuntime default = %q, want podman", got)
	}
}

// TestPsColorParity locks the additive-color contract: with Color=false the
// output is byte-identical to the pre-change raw-fmt bytes (no ESC anywhere),
// and with Color=true the idle / problem / doctor-tip framing lines carry ANSI.
func TestPsColorParity(t *testing.T) {
	t.Run("idle plain is byte-exact", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		var buf bytes.Buffer
		psRun(psDeps{
			DetectRuntime: func() string { return "podman" },
			RunCmd:        func([]string) (string, bool) { return "", true },
			PathIsDir:     func(string) bool { return true },
			Out:           &buf,
			Color:         false,
		})
		if got := buf.String(); got != "No running jails.\n" {
			t.Errorf("plain idle output = %q, want the pre-change bytes", got)
		}
	})

	t.Run("idle color emits ANSI", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		var buf bytes.Buffer
		psRun(psDeps{
			DetectRuntime: func() string { return "podman" },
			RunCmd:        func([]string) (string, bool) { return "", true },
			PathIsDir:     func(string) bool { return true },
			Out:           &buf,
			Color:         true,
		})
		got := buf.String()
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("colored idle output has no ANSI: %q", got)
		}
		if !strings.Contains(got, "No running jails.") {
			t.Errorf("colored idle output lost its literal text: %q", got)
		}
	})

	t.Run("problems: plain byte-exact, color has ANSI", func(t *testing.T) {
		mkDeps := func(out *bytes.Buffer, color bool) psDeps {
			return psDeps{
				DetectRuntime: func() string { return "podman" },
				RunCmd: func(argv []string) (string, bool) {
					if len(argv) >= 2 && argv[1] == "ps" {
						return "yolo-b-2222\tUp 5 minutes\t5 minutes ago\n", true
					}
					return "", true
				},
				PathIsDir: func(string) bool { return false }, // workspace gone
				Out:       out,
				Color:     color,
			}
		}

		home := t.TempDir()
		t.Setenv("HOME", home)
		must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))
		var plain bytes.Buffer
		psRun(mkDeps(&plain, false))
		if strings.Contains(plain.String(), "\x1b[") {
			t.Errorf("plain problem output leaked ANSI: %q", plain.String())
		}
		// The stripped bytes match the current raw-fmt rendering exactly.
		wantTail := "\n⚠  1 problem jail(s):\n" +
			"  yolo-b-2222  (workspace gone)\n" +
			"\n  Run 'yolo doctor' to clean up\n"
		if !strings.HasSuffix(plain.String(), wantTail) {
			t.Errorf("plain problem tail = %q, want suffix %q", plain.String(), wantTail)
		}

		home2 := t.TempDir()
		t.Setenv("HOME", home2)
		must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))
		var col bytes.Buffer
		psRun(mkDeps(&col, true))
		if !strings.Contains(col.String(), "\x1b[") {
			t.Errorf("colored problem output has no ANSI: %q", col.String())
		}
		// Stripping the color must reproduce the plain bytes exactly.
		if got := stripANSI(col.String()); got != plain.String() {
			t.Errorf("color-stripped != plain:\n color=%q\n plain=%q", got, plain.String())
		}
	})
}

// stripANSI removes CSI SGR escape sequences for the parity assertion.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
