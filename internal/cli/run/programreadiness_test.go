package run

import (
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
