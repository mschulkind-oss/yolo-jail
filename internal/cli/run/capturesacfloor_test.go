package run

// The capture store's Apple Container behaviour ON BOTH SIDES of acROBindsFloor.
//
// capturesArgs was the one `:ro` site that compared `rt == "container"` by hand instead of
// asking roBindsUnsupported, so it refused the store on every Apple Container launch —
// including the versions that honor the suffix. The refusal's own argument is what dates it:
// "a writable machine-wide store is bytes every other workspace on this machine executes",
// which is an argument about `:ro` NOT BEING ENFORCED, and 1.1.0 enforces it. Measured
// 2026-09-16 in an AC jail: an overwrite of a file in a `:ro` bind and a create beside it
// both fail `Read-only file system`, and the host bytes are unchanged.
//
// The version is injected through Options.acVersion rather than a fake `container --version`
// on PATH, so these rows do not change answer on a machine that happens to have the CLI
// installed — the trap the surrounding fixtures already guard against for /dev and
// perf_logging. G27 in docs/plans/setup-support-gaps.md.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// acCaptureArgs runs capturesArgs for Apple Container at a pinned CLI version. An empty
// version means the probe could not answer, which is the fail-closed row.
func acCaptureArgs(t *testing.T, version, store string) (args []string, out string) {
	t.Helper()
	o := goldenOptions("/ws", t.TempDir())
	var buf strings.Builder
	o.Stdout = &buf
	o.PathExists = func(string) bool { return true }
	o.acVersion = &acVersionProbe{v: version, ok: version != ""}
	return o.capturesArgs("container", store), buf.String()
}

// At and above the floor the store arrives, read-only, with the env var that makes it
// usable — the whole point of the mount is that materialize is a reflink clone rather than
// a download.
func TestAppleContainerAtTheFloorGetsTheCaptureStore(t *testing.T) {
	for _, version := range []string{"1.1.0", "1.2.0", "2.0"} {
		args, out := acCaptureArgs(t, version, "/store")

		if want := "/store:" + capturesCtxDir + ":ro"; !slices.Contains(args, want) {
			t.Errorf("AC %s: no %q in %v — the store is still refused on a version that "+
				"honors :ro, which is what the hand-written rt check did", version, want, args)
		}
		if want := entrypoint.CapturesDirEnv + "=" + capturesCtxDir; !slices.Contains(args, want) {
			t.Errorf("AC %s: the store is bound but the jail is not told where: %v", version, args)
		}
		// A version that CAN have the store must not also be warned that it cannot. A
		// false warning is worse than none here: the user's response is to stop relying
		// on a feature they have.
		if strings.Contains(out, "not mounted") {
			t.Errorf("AC %s printed the no-captures warning while binding the store:\n%s",
				version, out)
		}
	}
}

// Below the floor — and on any version the probe cannot read — the refusal stands, because
// there the bind really would be writable. What changes is that it now says so.
func TestAppleContainerBelowTheFloorIsToldWhyThereIsNoStore(t *testing.T) {
	for _, version := range []string{"0.12.3", "1.0.9", ""} {
		args, out := acCaptureArgs(t, version, "/store")

		if len(args) != 0 {
			t.Errorf("AC %q: emitted %v — below the floor :ro is ignored, so this hands the "+
				"jail write access to the machine-wide store every other workspace runs",
				version, args)
		}
		if !strings.Contains(out, "not mounted on this runtime") {
			t.Errorf("AC %q: the store is silently absent; a user whose installs download "+
				"again has nothing to read:\n%s", version, out)
		}
		// The reason has to travel with the refusal, or the line is just an apology.
		if !strings.Contains(out, "podman") {
			t.Errorf("AC %q: the notice does not name the way forward:\n%s", version, out)
		}
	}
}

// podman is unaffected in both directions: it honors :ro, so it neither loses the store nor
// gains a warning about a floor that is not its own.
func TestPodmanIsUntouchedByTheAppleContainerFloor(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	var buf strings.Builder
	o.Stdout = &buf
	o.PathExists = func(string) bool { return true }
	o.acVersion = &acVersionProbe{v: "0.12.3", ok: true} // would refuse, were it consulted

	args := o.capturesArgs("podman", "/store")
	if want := "/store:" + capturesCtxDir + ":ro"; !slices.Contains(args, want) {
		t.Errorf("podman lost the capture store: %v", args)
	}
	if buf.String() != "" {
		t.Errorf("podman was warned about Apple Container's :ro floor:\n%s", buf.String())
	}
}
