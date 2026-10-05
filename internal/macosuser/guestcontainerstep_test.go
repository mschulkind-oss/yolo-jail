package macosuser

// guestcontainerstep_test.go pins env-manager plan EMP-D5 on this backend's side: when the run
// pipeline launched the session at the guest notch, which it tells the session through
// config.NotchEnv in the launch env (EMP-D4), every message here whose next step names a container
// runtime names the jail notch too (config.ContainerStepClause). The notch gate refuses a container
// runtime beside a macOS guest (EMP-D1), so the runtime alone is a step into a second refusal.
//
// Every case goes through the production caller (RunMacosUser or buildPlan) with the marker in
// Options.PackEnv, where the run pipeline puts it, and has a control with no marker, the jail
// notch, whose message must not change.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// guestLaunchEnv is the launch env a macOS guest launch hands this backend: the marker alone.
func guestLaunchEnv() *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	env.Set(config.NotchEnv, string(config.ConfinementGuest))
	return env
}

// guestClauses is how many times the guest's clause appears in out, whitespace folded.
func guestClauses(out string) int {
	fold := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return strings.Count(fold(out), fold(config.ContainerStepClause(config.ConfinementGuest)))
}

// atBothNotches runs launch once with the guest marker and once without, and checks the message
// marker was printed both times and the clause only at the guest.
func atBothNotches(t *testing.T, marker string, launch func(packEnv *jsonx.OrderedMap) string) {
	t.Helper()
	guest, jail := launch(guestLaunchEnv()), launch(nil)
	for name, out := range map[string]string{"guest": guest, "jail": jail} {
		if !strings.Contains(out, marker) {
			t.Fatalf("the %s launch did not print %q:\n%s", name, marker, out)
		}
	}
	if n := guestClauses(guest); n != 1 {
		t.Errorf("at the guest notch the message names a container runtime without the jail notch "+
			"(clause seen %d times, want 1):\n%s", n, guest)
	}
	if n := guestClauses(jail); n != 0 {
		t.Errorf("at the jail notch the message grew the guest's clause:\n%s", jail)
	}
}

func TestGuestContextPreflightRefusalNamesTheJailNotch(t *testing.T) {
	atBothNotches(t, "cannot reach every context mount", func(packEnv *jsonx.OrderedMap) string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.Run = func(argv []string) int {
			if len(argv) > 0 && argv[0] == "sudo" && strings.Contains(strings.Join(argv, " "), testBin) {
				return 1 // the sandbox account cannot reach the source
			}
			return 0
		}
		opts := newOpts("/Users/Shared/yolo/proj")
		opts.PackEnv = packEnv
		opts.HostCtx.Links = []ContextLink{{Dest: "/ctx/notes", Source: "/Users/matt/notes", Dir: true}}
		if rc := RunMacosUser(d, opts); rc != 1 {
			t.Errorf("rc = %d, want 1: an unreachable context mount refuses the launch", rc)
		}
		return buf.String()
	})
}

func TestGuestMaterializeFailureNamesTheJailNotch(t *testing.T) {
	atBothNotches(t, "Could not materialize packages natively", func(packEnv *jsonx.OrderedMap) string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) { return nil, false, errFake("boom") }
		opts := pkgOpts("/Users/Shared/yolo/proj", []any{"ripgrep"})
		opts.PackEnv = packEnv
		RunMacosUser(d, opts)
		return buf.String()
	})
}

func TestGuestSkippedPackagesRefusalNamesTheJailNotch(t *testing.T) {
	atBothNotches(t, "have no aarch64-darwin build", func(packEnv *jsonx.OrderedMap) string {
		d := mockDeps(nil)
		var buf bytes.Buffer
		d.Out = &buf
		d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
			m := mockDarwin()
			m.Skipped = []string{"strace"}
			return m, true, nil
		}
		opts := pkgOpts("/Users/Shared/yolo/proj", []any{"strace"})
		opts.PackEnv = packEnv
		RunMacosUser(d, opts)
		return buf.String()
	})
}

func TestGuestPerSideDisclosureNamesTheJailNotch(t *testing.T) {
	ws := perSideWorkspace(t, "node_modules/")
	atBothNotches(t, "per-side paths are SHARED with the host", func(packEnv *jsonx.OrderedMap) string {
		opts := newOpts(ws)
		opts.PackEnv = packEnv
		var buf bytes.Buffer
		d := mockDeps(nil)
		d.Out = &buf
		buildPlan(d, opts, nil)
		return perSideLine(buf.String())
	})
}
