package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// amdHostOptions returns the golden fixture wired as a Linux podman host whose ROCm
// device-node probe PASSES — amdgpu loaded, /dev/kfd present, one render node — with an
// AMD CDI spec at specPath, or none when specPath is "".
//
// The device-node half has to pass for these rows to mean anything: with the golden's stubs
// the probe declines for want of the amdgpu module, so a "no amd.com/gpu flag" assertion
// would pass on a fixture where the GPU was never available in the first place.
func amdHostOptions(t *testing.T, specPath string) (*Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	dri := t.TempDir()
	if err := os.WriteFile(filepath.Join(dri, "renderD128"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := driDir
	driDir = dri
	t.Cleanup(func() { driDir = old })

	o := goldenOptions("/ws", home)
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0}
	}
	o.PathExists = func(p string) bool {
		switch p {
		case "/sys/module/amdgpu", "/dev/kfd":
			return true
		}
		return specPath != "" && p == specPath
	}
	var stdout, stderr bytes.Buffer
	o.Stdout = &stdout
	o.Stderr = &stderr
	return o, &stdout, &stderr
}

// amdInput is gpuInput for vendor amd with gpu.mode set.
func amdInput(t *testing.T, mode string) *assembleInput {
	t.Helper()
	in := gpuInput(t, "amd")
	cfgMap(in.cfg, "gpu").Set("mode", mode)
	return in
}

// TestAMDCDIModeWithoutSpecDeclinesPassthrough is G28 (docs/plans/setup-support-gaps.md):
// `gpu.mode: "cdi"` on a host with the AMD driver and no CDI spec. The probe used to pass,
// gpuArgs emitted `--device amd.com/gpu=all`, and podman died on
// `unresolvable CDI devices amd.com/gpu=all` — a raw runtime error where every other
// unavailable-GPU verdict, NVIDIA's missing spec included, warns and starts without GPU.
//
// Both halves are asserted, as TestNestedLaunchDeclinesNVIDIAPassthrough does: no flag and
// no warning is a silent drop, and a warning beside the flag is a lie. The warning must also
// name the command that makes the spec, since a stop that only reports is a defect.
func TestAMDCDIModeWithoutSpecDeclinesPassthrough(t *testing.T) {
	o, stdout, stderr := amdHostOptions(t, "")
	got := strings.Join(o.assembleRunCmd(amdInput(t, "cdi")), " ")

	if strings.Contains(got, "amd.com/gpu") {
		t.Errorf("a cdi-mode launch with no AMD CDI spec on the host still asks podman for "+
			"amd.com/gpu, which it cannot resolve:\nargv: %s", got)
	}
	for _, want := range []string{"GPU requested but", "no AMD CDI spec", AMDCDISpecGenerate,
		"starting without GPU passthrough"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the skip warning lacks %q — it has to say the spec is missing and how "+
				"to make one:\nstderr: %s", want, stderr.String())
		}
	}
	if strings.Contains(stdout.String(), "ROCm passthrough") {
		t.Errorf("gpuArgs announced passthrough on a launch that has none:\nstdout: %s", stdout.String())
	}
}

// TestAMDCDIModeWithSpecPassesThrough is the other half: one fact changed (a spec exists,
// at the second of the two paths so the lookup is known to read both), the launch must get
// the CDI device. Without this row the test above passes for a fix that disabled AMD CDI
// everywhere.
func TestAMDCDIModeWithSpecPassesThrough(t *testing.T) {
	o, _, stderr := amdHostOptions(t, "/var/run/cdi/amd.json")
	got := strings.Join(o.assembleRunCmd(amdInput(t, "cdi")), " ")

	for _, want := range []string{"--device amd.com/gpu=all", "--group-add keep-groups"} {
		if !strings.Contains(got, want) {
			t.Errorf("a cdi-mode launch with a spec on the host lacks %q:\nargv: %s", want, got)
		}
	}
	if strings.Contains(stderr.String(), "GPU requested but") {
		t.Errorf("a host with a spec was told its GPU is unavailable:\nstderr: %s", stderr.String())
	}
}

// TestAMDDevicesModeNeedsNoSpec: the default mode passes raw device nodes and needs no
// toolkit, so the spec requirement must be cdi-only.
func TestAMDDevicesModeNeedsNoSpec(t *testing.T) {
	o, _, stderr := amdHostOptions(t, "")
	got := strings.Join(o.assembleRunCmd(amdInput(t, "devices")), " ")

	for _, want := range []string{"--device /dev/kfd", "--device /dev/dri"} {
		if !strings.Contains(got, want) {
			t.Errorf("a devices-mode launch with no CDI spec lacks %q — the spec is a "+
				"cdi-mode requirement only:\nargv: %s", want, got)
		}
	}
	if strings.Contains(stderr.String(), "GPU requested but") {
		t.Errorf("devices mode was declined:\nstderr: %s", stderr.String())
	}
}

// TestFindAMDCDISpecReadsBothPaths pins the shared probe `yolo check` also calls.
func TestFindAMDCDISpecReadsBothPaths(t *testing.T) {
	for _, tc := range []struct{ have, want string }{
		{"", ""},
		{"/etc/cdi/amd.json", "/etc/cdi/amd.json"},
		{"/var/run/cdi/amd.json", "/var/run/cdi/amd.json"},
		{"/etc/cdi/nvidia.yaml", ""},
	} {
		got := FindAMDCDISpec(func(p string) bool { return tc.have != "" && p == tc.have })
		if got != tc.want {
			t.Errorf("FindAMDCDISpec with %q present = %q, want %q", tc.have, got, tc.want)
		}
	}
}
