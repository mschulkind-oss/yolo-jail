package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// holdOptions builds the launcher fixture with the hold opt-in set in the HOST
// environment — which is the only place a user can type it, and the whole reason
// the forwarding under test exists.
func holdOptions(t *testing.T, rt string, inContainer bool) (*Options, *assembleInput) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, inContainer)
	o.Getenv = func(k string) string {
		if k == paths.HoldOnRefusalEnv {
			return "1"
		}
		return ""
	}
	return o, relocationInput(t, rt, t.TempDir(), nil)
}

// TestAssembleRunCmdForwardsTheHoldOptIn. The opt-in is typed on the host in front
// of `yolo` (the YOLO_ALLOW_STALE_IMAGE convention) and honoured by the entrypoint
// INSIDE the jail, and a container inherits nothing from the launcher's environment
// — so without this forwarding the variable is one nobody can reach, from the only
// place the user it exists for can reach anything.
//
// This asserts against the FULL assembled argv rather than against
// holdOnRefusalArgs, deliberately: a test that pins the helper while the call site
// is unpinned is not a test (AGENTS.md), and deleting the
// `runCmd = append(runCmd, o.holdOnRefusalArgs(...))` line in assemble.go must fail
// something. It fails here.
//
// Swept over both container runtimes because a refused boot is a refused boot on
// either, and a diagnostic that exists on one of them is not one.
func TestAssembleRunCmdForwardsTheHoldOptIn(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		for _, inContainer := range []bool{false, true} {
			name := rt
			if inContainer {
				name += "/nested"
			}
			t.Run(name, func(t *testing.T) {
				o, in := holdOptions(t, rt, inContainer)
				argv := o.assembleRunCmd(in)

				got, ok := envValue(argv, paths.HoldOnRefusalEnv)
				if !ok || got != "1" {
					t.Errorf("%s must reach the entrypoint that honours it (got %q, present=%v)",
						paths.HoldOnRefusalEnv, got, ok)
				}
			})
		}
	}
}

// TestAssembleRunCmdComposesTheExecLineTheJailCannotCompose. The jail cannot build
// this sentence for itself and must not try: `cname` is minted host-side, and
// commonEnvBlock emits a hard-coded YOLO_RUNTIME=podman on EVERY backend, so an
// entrypoint under Apple Container that derived the command would print `podman
// exec`, naming a binary that does not exist on that host. Both halves are asserted
// per runtime for exactly that reason.
func TestAssembleRunCmdComposesTheExecLineTheJailCannotCompose(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			o, in := holdOptions(t, rt, false)
			argv := o.assembleRunCmd(in)

			got, ok := envValue(argv, paths.HoldExecEnv)
			if !ok {
				t.Fatalf("%s absent: the held jail would print an exec line it guessed",
					paths.HoldExecEnv)
			}
			want := rt + " exec -it " + in.cname + " bash"
			if got != want {
				t.Errorf("%s = %q, want %q — the line has to name THIS runtime and THIS "+
					"container, or it is a command that does not work",
					paths.HoldExecEnv, got, want)
			}
		})
	}
}

// TestAssembleRunCmdWithoutTheHoldIsUnchanged. The hold may not cost anything on a
// launch that did not ask for it: the golden argv is a contract (assemble_test.go),
// and a variable on every argv is one more thing frozen into every jail's
// environment — here one that would make every boot carry the machinery for a hang.
func TestAssembleRunCmdWithoutTheHoldIsUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)

	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	for _, k := range []string{paths.HoldOnRefusalEnv, paths.HoldExecEnv} {
		if val, ok := envValue(argv, k); ok {
			t.Errorf("an unset opt-in must not appear in the argv, got %s=%q", k, val)
		}
	}
}
