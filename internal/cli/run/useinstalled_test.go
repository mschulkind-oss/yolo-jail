package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
)

// TestAssembleRunCmdForwardsUseInstalled: `YOLO_USE_INSTALLED=claude yolo -- claude` is typed on the
// HOST, and the launcher that reads it — the one refusing to run an installed version after a failed
// update (docs/design/program-delivery.md PD-NF2) — runs in the container, which inherits nothing from
// the launcher's environment. So the forwarding is the whole mechanism. Against the FULL assembled
// argv, so deleting the call (programReadinessArgs → useInstalledArgs, appended in assemble.go) fails
// this; and only when set, so an ordinary launch's frozen environment does not grow it.
func TestAssembleRunCmdForwardsUseInstalled(t *testing.T) {
	for _, value := range []string{"claude", "1", ""} {
		t.Run("value="+value, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o, _ := pastaHostOptions(t, "/ws", home, false)
			o.Getenv = func(k string) string {
				if k == patchrecovery.UseInstalledEnv {
					return value
				}
				return ""
			}
			argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
			got, ok := envValue(argv, patchrecovery.UseInstalledEnv)
			if value == "" {
				if ok {
					t.Errorf("an unset hatch must not appear in the argv, got %q", got)
				}
				return
			}
			if !ok || got != value {
				t.Errorf("%s=%q must reach the jail (got %q, present=%v)", patchrecovery.UseInstalledEnv, value, got, ok)
			}
		})
	}
}

// TestAttachExecCarriesUseInstalled: on an ATTACH the hatch is this invocation's, so it rides the
// exec before the container name; and a value the fresh launch froze into the container is cleared
// when this invocation has none, since the hatch belongs to the command it was typed in front of.
func TestAttachExecCarriesUseInstalled(t *testing.T) {
	env := patchrecovery.UseInstalledEnv
	for _, tc := range []struct {
		name, invocation, frozen string
		want                     []string
	}{
		{"set at attach", "claude", podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n", []string{env + "=claude"}},
		{"frozen, unset at attach", "", podmanSetupNetworkEnv + env + "=claude\n", []string{env + "="}},
		{"never set", "", podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := attachExecArgv(t, map[string]string{env: tc.invocation}, tc.frozen)
			var got []string
			for _, e := range execEnvBeforeContainer(t, argv) {
				if strings.HasPrefix(e, env+"=") {
					got = append(got, e)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the exec sets %q, want %q; argv: %q", got, tc.want, argv)
			}
		})
	}
}
