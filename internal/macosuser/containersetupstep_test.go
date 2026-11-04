package macosuser

// containersetupstep_test.go pins the step every message here offers when a container would
// shadow, mount or build what this backend cannot: it names a setup the launch can take
// (setup.ContainerStep, or the Apple Container setup alone), spelled `--setup <name>` outside a
// launched session and `YOLO_SETUP=<name>` inside one, and never a removed spelling (`--at`, or
// the `runtime` and `confinement` keys). Every one of these refusals is reached on an ordinary
// Seatbelt launch, so a stale step here is one every user of this backend meets.
//
// Every case goes through the production caller (RunMacosUser or buildPlan), once outside a
// session and once inside one.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// removedSteps are spellings a next step may no longer name: each selected a runtime or a notch
// before named setups replaced both.
var removedSteps = []string{"--at ", "runtime:", "runtime =", "confinement", "runtime (`runtime"}

// inAndOutOfSession runs launch outside a launched session and inside one, and checks each run
// printed marker, named want(inSession) once, and named no removed spelling.
func inAndOutOfSession(t *testing.T, marker string, want func(inSession bool) string, launch func() string) {
	t.Helper()
	fold := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, inSession := range []bool{false, true} {
		v := ""
		if inSession {
			v = "0.0.0-test"
		}
		t.Setenv("YOLO_VERSION", v)
		out := fold(launch())
		if !strings.Contains(out, fold(marker)) {
			t.Fatalf("in session %v: the launch did not print %q:\n%s", inSession, marker, out)
		}
		if n := strings.Count(out, fold(want(inSession))); n != 1 {
			t.Errorf("in session %v: the step %q appears %d times, want 1:\n%s", inSession, want(inSession), n, out)
		}
		for _, r := range removedSteps {
			if strings.Contains(out, r) {
				t.Errorf("in session %v: the message names the removed spelling %q:\n%s", inSession, r, out)
			}
		}
	}
}

func appleContainer(inSession bool) string { return setup.Choose("apple-container-self", inSession) }

func TestContextPreflightRefusalNamesAContainerSetup(t *testing.T) {
	inAndOutOfSession(t, "cannot reach every context mount", setup.ContainerStep, func() string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.Run = func(argv []string) int {
			if len(argv) > 0 && argv[0] == "/usr/bin/sudo" && strings.Contains(strings.Join(argv, " "), testBin) {
				return 1 // the sandbox account cannot reach the source
			}
			return 0
		}
		opts := newOpts("/Users/Shared/yolo/proj")
		opts.HostCtx.Links = []ContextLink{{Dest: "/ctx/notes", Source: "/Users/matt/notes", Dir: true}}
		if rc := RunMacosUser(d, opts); rc != 1 {
			t.Errorf("rc = %d, want 1: an unreachable context mount refuses the launch", rc)
		}
		return buf.String()
	})
}

func TestMaterializeFailureNamesTheAppleContainerSetup(t *testing.T) {
	inAndOutOfSession(t, "Could not materialize packages natively", appleContainer, func() string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) { return nil, false, errFake("boom") }
		opts := pkgOpts("/Users/Shared/yolo/proj", []any{"ripgrep"})
		RunMacosUser(d, opts)
		return buf.String()
	})
}

func TestSkippedPackagesRefusalNamesTheAppleContainerSetup(t *testing.T) {
	inAndOutOfSession(t, "have no aarch64-darwin build", appleContainer, func() string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
			m := mockDarwin()
			m.Skipped = []string{"strace"}
			return m, true, nil
		}
		opts := pkgOpts("/Users/Shared/yolo/proj", []any{"strace"})
		RunMacosUser(d, opts)
		return buf.String()
	})
}

func TestPerSideDisclosureNamesAContainerSetup(t *testing.T) {
	ws := perSideWorkspace(t, "node_modules/")
	inAndOutOfSession(t, "per-side paths are SHARED with the host", setup.ContainerStep, func() string {
		opts := newOpts(ws)
		var buf bytes.Buffer
		d := mockDeps(nil)
		d.Out = &buf
		buildPlan(d, opts, nil)
		return perSideLine(buf.String())
	})
}
