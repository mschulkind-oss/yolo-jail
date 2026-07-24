package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// preflightExec builds an Exec seam returning canned results for the two
// subprocess probes preflightBuilderNeeds drives: `nix build … --dry-run`
// (whose stderr classifies WillBuild) and `nix config show` (whose stdout feeds
// hasLinuxBuilder). Anything else degrades to "not ran".
func preflightExec(dryRunStderr string, dryRunRC int, configShow string) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		switch {
		case strings.Contains(key, "--dry-run"):
			return ExecResult{Stderr: dryRunStderr, RC: dryRunRC, Ran: true}
		case strings.Contains(key, "config show"):
			return ExecResult{Stdout: configShow, RC: 0, Ran: true}
		}
		return ExecResult{Ran: false}
	}
}

// buildStderr is a `nix build --dry-run` stderr that classifies as WillBuildYes
// with one offending derivation (a from-source package).
const buildStderr = "these 2 derivations will be built:\n" +
	"  /nix/store/aaa-yolo-jail-conf.json.drv\n" +
	"  /nix/store/bbb-foo.drv\n" +
	"these paths will be fetched:\n  /nix/store/ccc\n"

// TestPreflightBuilderNeeds_MacOSNoBuilder is the core rewire regression: on
// macOS, when a package must be built from source and the user has NO Linux
// builder configured, check must NOT tell the user to run `yolo builder …` or
// `nix run nixpkgs#darwin.linux-builder`. Instead it WARNs that a real `yolo`
// run offloads to a container builder, and returns false (skip the doomed local
// build — image.BuildOCIImage has no offload seam).
func TestPreflightBuilderNeeds_MacOSNoBuilder(t *testing.T) {
	var out bytes.Buffer
	o := &Options{IsMacOS: true, Exec: preflightExec(buildStderr, 0, "" /* no builders configured */)}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); viable {
		t.Error("WillBuildYes on macOS with no builder must be non-viable (return false)")
	}
	got := out.String()
	if r.failed != 0 {
		t.Errorf("must not FAIL (yolo builds fine via the offload); got %d fails:\n%s", r.failed, got)
	}
	if r.warned != 1 {
		t.Errorf("expected exactly one WARN, got %d:\n%s", r.warned, got)
	}
	if !strings.Contains(got, "container builder") {
		t.Errorf("WARN should name the container builder:\n%s", got)
	}
	for _, dead := range []string{"yolo builder", "darwin.linux-builder", "first boot", "first-boot"} {
		if strings.Contains(got, dead) {
			t.Errorf("dangling VM-builder reference %q in output:\n%s", dead, got)
		}
	}
}

// TestPreflightBuilderNeeds_MacOSOwnBuilder covers the §8 escape hatch: a user
// who configured their OWN aarch64-linux builder (nix-darwin linux-builder or
// /etc/nix/machines) keeps working — the build is viable (return true) with a
// PASS, no warning.
func TestPreflightBuilderNeeds_MacOSOwnBuilder(t *testing.T) {
	var out bytes.Buffer
	cfg := "builders = ssh-ng://mybox aarch64-linux /key 4\n"
	o := &Options{IsMacOS: true, Exec: preflightExec(buildStderr, 0, cfg)}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); !viable {
		t.Error("a user-configured Linux builder must make the build viable (return true)")
	}
	if r.failed != 0 || r.warned != 0 {
		t.Errorf("configured builder path should be a clean PASS; fails=%d warns=%d:\n%s", r.failed, r.warned, out.String())
	}
	if r.passed != 1 {
		t.Errorf("expected one PASS, got %d:\n%s", r.passed, out.String())
	}
}

// TestPreflightBuilderNeeds_Linux confirms the Linux branch is unchanged: a
// from-source build is a native Linux build, always viable, no builder question.
func TestPreflightBuilderNeeds_Linux(t *testing.T) {
	var out bytes.Buffer
	o := &Options{IsMacOS: false, Exec: preflightExec(buildStderr, 0, "")}
	r := newReporter(&out, false)

	if viable := o.preflightBuilderNeeds(r, t.TempDir(), []any{"foo"}); !viable {
		t.Error("Linux from-source build must be viable")
	}
	if r.failed != 0 || r.warned != 0 {
		t.Errorf("Linux path emits a dim note only; fails=%d warns=%d", r.failed, r.warned)
	}
}

// TestPreflightBuilderNeeds_FullyCached confirms the WillBuildNo happy path:
// everything substitutable → viable, no builder needed, on either platform.
func TestPreflightBuilderNeeds_FullyCached(t *testing.T) {
	substOnly := "these paths will be fetched (10 MiB download):\n  /nix/store/x\n"
	for _, mac := range []bool{true, false} {
		var out bytes.Buffer
		o := &Options{IsMacOS: mac, Exec: preflightExec(substOnly, 0, "")}
		r := newReporter(&out, false)
		if viable := o.preflightBuilderNeeds(r, t.TempDir(), nil); !viable {
			t.Errorf("fully-cached (macOS=%v) must be viable", mac)
		}
		if r.failed != 0 || r.warned != 0 {
			t.Errorf("fully-cached (macOS=%v) must be clean; fails=%d warns=%d", mac, r.failed, r.warned)
		}
	}
}
