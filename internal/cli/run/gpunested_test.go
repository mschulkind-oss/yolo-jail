package run

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// nvidiaHostOptions returns the golden fixture wired as a host whose NVIDIA
// passthrough probe SUCCEEDS: nvidia-smi on PATH, `nvidia-smi -L` exiting 0, and a CDI
// spec at /etc/cdi/nvidia.yaml. nested adds /run/.containerenv, which is what
// o.inContainer() reads.
//
// The probe has to pass for these rows to mean anything: with the golden's stubs it
// declines for want of nvidia-smi, so a test that only asserted "no CDI flags" would
// pass on a fixture where the GPU was never available in the first place.
func nvidiaHostOptions(t *testing.T, nested bool) (*Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.LookPath = func(name string) (string, bool) {
		if name == "nvidia-smi" {
			return "/usr/bin/nvidia-smi", true
		}
		return "", false
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0}
	}
	o.PathExists = func(p string) bool {
		switch p {
		case "/etc/cdi/nvidia.yaml":
			return true
		case "/run/.containerenv":
			return nested
		}
		return false
	}
	var stdout, stderr bytes.Buffer
	o.Stdout = &stdout
	o.Stderr = &stderr
	return o, &stdout, &stderr
}

// gpuInput is the golden assembleInput with a `gpu` section of the given vendor.
func gpuInput(t *testing.T, vendor string) *assembleInput {
	t.Helper()
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	gpuSec := jsonx.NewOrderedMap()
	gpuSec.Set("enabled", true)
	gpuSec.Set("vendor", vendor)
	return &assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec, "gpu", gpuSec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
}

// TestNestedLaunchDeclinesNVIDIAPassthrough: a launcher that is itself inside a jail
// must take the ordinary warn-and-skip path for NVIDIA, not emit half the feature.
//
// podmanNestingArgs tests inContainer FIRST and returns — it has to, `--userns host` is
// not optional when a doubly-nested user namespace cannot mount /proc — so the NVIDIA
// branch's `--runtime runc` never reaches the argv. `--runtime runc` is the documented
// workaround for crun's CDI handling (podman#27483); without it a container carrying
// `--device nvidia.com/gpu=…` dies as `conmon bytes "": readObjectStart`. gpuArgs added
// the CDI flags anyway, so the user got a raw runtime error instead of yolo's
// "GPU requested but … — starting without GPU passthrough" line, on a configuration the
// user guide already called unsupported.
//
// Both halves are asserted because either alone is passable by the wrong fix: no CDI
// flags and no warning is a silent drop, and a warning beside the flags is a lie.
func TestNestedLaunchDeclinesNVIDIAPassthrough(t *testing.T) {
	o, stdout, stderr := nvidiaHostOptions(t, true)
	got := strings.Join(o.assembleRunCmd(gpuInput(t, "nvidia")), " ")

	if strings.Contains(got, "nvidia.com/gpu") || strings.Contains(got, "NVIDIA_VISIBLE_DEVICES") {
		t.Errorf("a nested launch carries NVIDIA CDI flags with no `--runtime runc` to make "+
			"them work — the nesting branch of podmanNestingArgs returns before the GPU "+
			"branch, so this argv is half a feature and podman fails it at run time:\nargv: %s", got)
	}
	if strings.Contains(got, "--runtime runc") {
		t.Errorf("a nested launch must not switch the OCI runtime: `--runtime runc` is the "+
			"NVIDIA branch's flag and that branch is unreachable when nesting\nargv: %s", got)
	}
	if !strings.Contains(stderr.String(), "GPU requested but") ||
		!strings.Contains(stderr.String(), "starting without GPU passthrough") {
		t.Errorf("the skip was silent — a requested capability that is dropped has to say so "+
			"on stderr, in the one line every other unavailable-GPU verdict uses:\nstderr: %s",
			stderr.String())
	}
	if strings.Contains(stdout.String(), "GPU passthrough") {
		t.Errorf("gpuArgs announced passthrough on a launch that has none:\nstdout: %s", stdout.String())
	}
}

// TestHostLaunchStillGetsNVIDIAPassthrough is the other half of the same seam: the
// identical fixture, one fact changed (no /run/.containerenv), must still get the whole
// feature. Without this row the test above passes for a fix that disabled NVIDIA
// passthrough everywhere.
func TestHostLaunchStillGetsNVIDIAPassthrough(t *testing.T) {
	o, stdout, _ := nvidiaHostOptions(t, false)
	got := strings.Join(o.assembleRunCmd(gpuInput(t, "nvidia")), " ")

	for _, want := range []string{"nvidia.com/gpu=all", "--runtime runc", "NVIDIA_VISIBLE_DEVICES=all"} {
		if !strings.Contains(got, want) {
			t.Errorf("a real GPU host lost %q — the nested gate must read the launcher's own "+
				"containment and nothing else:\nargv: %s", want, got)
		}
	}
	if !strings.Contains(stdout.String(), "GPU passthrough") {
		t.Errorf("the GPU announcement went missing on a launch that has passthrough:\nstdout: %s",
			stdout.String())
	}
}

// TestNestedAMDStillAsksTheROCmProbe pins the deliberate LIMIT of the gate above.
//
// AMD's default path passes raw device nodes and needs neither `--runtime runc` (CDI mode
// is verified to run under crun) nor the identity uid/gid maps, so nesting drops nothing
// it depends on. Declining it would be a claim nobody has measured, and this row is here
// so that widening the gate to "any vendor" is a decision somebody makes on purpose
// rather than a side effect: reorder the switch so `inContainer` is tested first and the
// AMD verdict stops being the ROCm probe's.
//
// It asserts the VERDICT'S AUTHOR, not the verdict. rocmHostAvailable globs the real
// /dev/dri for a render node — no seam injects that — so whether AMD passthrough is
// available here depends on the machine running the test, while "who decided" does not.
func TestNestedAMDStillAsksTheROCmProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.PathExists = func(p string) bool {
		switch p {
		// The ROCm probe's own facts, plus the nested marker o.inContainer() reads.
		case "/sys/module/amdgpu", "/dev/kfd", "/dev/dri", "/run/.containerenv":
			return true
		}
		return false
	}
	var stdout, stderr bytes.Buffer
	o.Stdout = &stdout
	o.Stderr = &stderr

	o.assembleRunCmd(gpuInput(t, "amd"))
	if strings.Contains(stderr.String(), "nested podman-in-podman") {
		t.Errorf("the nested gate swallowed an AMD launch: the NVIDIA-only reason answered a "+
			"vendor whose path needs neither `--runtime runc` nor the identity maps, so the "+
			"drop is unmeasured rather than known\nstderr: %s", stderr.String())
	}
}
