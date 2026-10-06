package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAssembleRunCmdForwardsTheReadinessDials: the readiness act's hatch and its off-switch are
// typed on the HOST and read by the jail's provisioning stage (docs/design/jail-notch-readiness.md
// OQ-JR1), and a container inherits nothing from the launcher's environment — so this forwarding
// is the whole mechanism, and the hatch the refusal offers is one nobody could reach without it.
//
// Against the FULL assembled argv, for holdOnRefusal_test's reason: deleting the
// `runCmd = append(runCmd, o.programReadinessArgs()...)` line in assemble.go must fail something.
// Swept over both container runtimes and the nested case, since the stage runs on all of them.
func TestAssembleRunCmdForwardsTheReadinessDials(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		for _, inContainer := range []bool{false, true} {
			name := rt
			if inContainer {
				name += "/nested"
			}
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				emptyLoopholeDirs(t)
				o, _ := pastaHostOptions(t, "/ws", home, inContainer)
				o.Getenv = func(k string) string {
					switch k {
					case paths.AllowMissingProgramsEnv:
						return "1"
					case paths.NoProgramReadinessEnv:
						return "yes"
					}
					return ""
				}
				argv := o.assembleRunCmd(relocationInput(t, rt, t.TempDir(), nil))
				for k, want := range map[string]string{
					paths.AllowMissingProgramsEnv: "1",
					paths.NoProgramReadinessEnv:   "yes",
				} {
					if got, ok := envValue(argv, k); !ok || got != want {
						t.Errorf("%s must reach the stage that honours it (got %q, present=%v)", k, got, ok)
					}
				}
			})
		}
	}
}

// TestAssembleRunCmdWithoutTheReadinessDialsIsUnchanged: a launch that asked for neither carries
// neither in its frozen environment — the golden argv is a contract (assemble_test.go).
func TestAssembleRunCmdWithoutTheReadinessDialsIsUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)
	o.Getenv = func(string) string { return "" }
	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	for _, k := range []string{paths.AllowMissingProgramsEnv, paths.NoProgramReadinessEnv} {
		if val, ok := envValue(argv, k); ok {
			t.Errorf("an unset dial must not appear in the argv, got %s=%q", k, val)
		}
	}
}

// TestACaptureJailTurnsTheReadinessActOff: a capture or build jail's command IS an install
// (yolo capture's installer, a fork's build), so a readiness act that installed or refused the
// program first broke both: the capture recorded an empty delta, and the build jail was refused
// before its build ran. Options.NoProgramReadiness forwards the off-switch with the capture
// jail's own value, whatever the host environment holds, and exactly once.
func TestACaptureJailTurnsTheReadinessActOff(t *testing.T) {
	for _, hostValue := range []string{"", "1"} {
		t.Run("host="+hostValue, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o, _ := pastaHostOptions(t, "/ws", home, false)
			o.Getenv = func(k string) string {
				if k == paths.NoProgramReadinessEnv {
					return hostValue
				}
				return ""
			}
			o.NoProgramReadiness = true
			argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
			if got, ok := envValue(argv, paths.NoProgramReadinessEnv); !ok || got != paths.NoProgramReadinessCaptureJail {
				t.Errorf("%s = %q (present=%v), want %q", paths.NoProgramReadinessEnv, got, ok,
					paths.NoProgramReadinessCaptureJail)
			}
			n := 0
			for _, a := range argv {
				if strings.HasPrefix(a, paths.NoProgramReadinessEnv+"=") {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s appears %d times in the argv, want once:\n%q", paths.NoProgramReadinessEnv, n, argv)
			}
		})
	}
}
