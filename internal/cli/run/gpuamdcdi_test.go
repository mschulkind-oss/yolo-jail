package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// amdSpecJSON and amdSpecYAML are minimal AMD CDI specs in the two encodings podman's CDI
// registry loads. The registry reads a spec's kind from its content and ignores its filename.
const (
	amdSpecJSON = `{"cdiVersion": "0.6.0", "kind": "amd.com/gpu", "devices": [{"name": "all", ` +
		`"containerEdits": {"deviceNodes": [{"path": "/dev/kfd"}]}}]}`
	amdSpecYAML = "---\ncdiVersion: 0.6.0\nkind: \"amd.com/gpu\" # written by amd-ctk\n" +
		"devices:\n  - name: all\n    containerEdits:\n      deviceNodes:\n        - path: /dev/kfd\n"
	nvidiaSpecYAML = "cdiVersion: 0.5.0\nkind: nvidia.com/gpu\ndevices:\n  - name: all\n"
)

// writeHostFiles writes each host path's content under root, making parent dirs.
func writeHostFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// amdHostOptions returns the golden fixture wired as a Linux podman host whose ROCm
// device-node probe PASSES — amdgpu loaded, /dev/kfd present, one render node — with the
// given host files (CDI specs, containers.conf) under the probe's machine root, cdiRoot.
//
// The device-node half has to pass for these rows to mean anything: with the golden's stubs
// the probe declines for want of the amdgpu module, so a "no amd.com/gpu flag" assertion
// would pass on a fixture where the GPU was never available in the first place.
func amdHostOptions(t *testing.T, hostFiles map[string]string) (*Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	dri := t.TempDir()
	if err := os.WriteFile(filepath.Join(dri, "renderD128"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	oldDri := driDir
	driDir = dri
	t.Cleanup(func() { driDir = oldDri })

	root := t.TempDir()
	writeHostFiles(t, root, hostFiles)
	oldRoot := cdiRoot
	cdiRoot = root
	t.Cleanup(func() { cdiRoot = oldRoot })

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
		return false
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
// The spec dirs hold an NVIDIA spec, so "no AMD spec" is decided by kind, not by an empty
// directory. Both halves are asserted, as TestNestedLaunchDeclinesNVIDIAPassthrough does: no
// flag and no warning is a silent drop, and a warning beside the flag is a lie. The warning
// must also name the command that makes the spec, since a stop that only reports is a defect.
func TestAMDCDIModeWithoutSpecDeclinesPassthrough(t *testing.T) {
	o, stdout, stderr := amdHostOptions(t, map[string]string{"/etc/cdi/nvidia.yaml": nvidiaSpecYAML})
	got := strings.Join(o.assembleRunCmd(amdInput(t, "cdi")), " ")

	if strings.Contains(got, "amd.com/gpu") {
		t.Errorf("a cdi-mode launch with no AMD CDI spec on the host still asks podman for "+
			"amd.com/gpu, which it cannot resolve:\nargv: %s", got)
	}
	for _, want := range []string{"GPU requested but", "no AMD CDI spec", "/etc/cdi", "/var/run/cdi",
		AMDCDISpecGenerate, "starting without GPU passthrough"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the skip warning lacks %q — it has to say the spec is missing, where it "+
				"looked and how to make one:\nstderr: %s", want, stderr.String())
		}
	}
	if strings.Contains(stdout.String(), "ROCm passthrough") {
		t.Errorf("gpuArgs announced passthrough on a launch that has none:\nstdout: %s", stdout.String())
	}
}

// TestAMDCDIModeWithSpecPassesThrough is the other half: one fact changed (a spec exists),
// the launch must get the CDI device. Each row is a spec podman's CDI registry resolves
// `amd.com/gpu=all` from — it loads every .json and .yaml in its spec dirs and goes by the
// spec's kind, never its filename — so declining any of them would take the GPU from a
// host where podman would have given it. Without these rows the test above passes for a fix
// that disabled AMD CDI everywhere, or that knew only the filename amd-ctk writes by default.
func TestAMDCDIModeWithSpecPassesThrough(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"amd.json in /etc/cdi", map[string]string{"/etc/cdi/amd.json": amdSpecJSON}},
		{"amd.yaml in /var/run/cdi", map[string]string{"/var/run/cdi/amd.yaml": amdSpecYAML}},
		{"another name in /etc/cdi", map[string]string{"/etc/cdi/amd-gpu.json": amdSpecJSON}},
		{"a configured cdi_spec_dirs", map[string]string{
			"/etc/containers/containers.conf": "[engine]\ncdi_spec_dirs = [\n  \"/opt/cdi\",\n]\n",
			"/opt/cdi/rocm.yaml":              amdSpecYAML,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _, stderr := amdHostOptions(t, tc.files)
			got := strings.Join(o.assembleRunCmd(amdInput(t, "cdi")), " ")

			for _, want := range []string{"--device amd.com/gpu=all", "--group-add keep-groups"} {
				if !strings.Contains(got, want) {
					t.Errorf("a cdi-mode launch with a spec on the host lacks %q:\nargv: %s", want, got)
				}
			}
			if strings.Contains(stderr.String(), "GPU requested but") {
				t.Errorf("a host with a spec was told its GPU is unavailable:\nstderr: %s", stderr.String())
			}
		})
	}
}

// TestAMDDevicesModeNeedsNoSpec: the default mode passes raw device nodes and needs no
// toolkit, so the spec requirement must be cdi-only.
func TestAMDDevicesModeNeedsNoSpec(t *testing.T) {
	o, _, stderr := amdHostOptions(t, nil)
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

// TestFindAMDCDISpecReadsWhatPodmanReads pins the shared probe `yolo check` also calls
// against the CDI registry's own rules: every .json and .yaml file directly in a spec dir,
// recognized by its kind; the default dirs /etc/cdi and /var/run/cdi plus any
// `[engine] cdi_spec_dirs` a containers.conf (or a drop-in, or the user's own) names.
func TestFindAMDCDISpecReadsWhatPodmanReads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		env   map[string]string
		want  string
		dirs  []string // must be among the dirs searched
	}{
		{name: "nothing", want: "", dirs: []string{"/etc/cdi", "/var/run/cdi"}},
		{name: "amd.json", files: map[string]string{"/etc/cdi/amd.json": amdSpecJSON},
			want: "/etc/cdi/amd.json"},
		{name: "amd.yaml in /var/run/cdi", files: map[string]string{"/var/run/cdi/amd.yaml": amdSpecYAML},
			want: "/var/run/cdi/amd.yaml"},
		{name: "any filename", files: map[string]string{"/etc/cdi/zz-gpu.yaml": amdSpecYAML},
			want: "/etc/cdi/zz-gpu.yaml"},
		{name: "a YAML body in a .json file", files: map[string]string{"/etc/cdi/amd.json": amdSpecYAML},
			want: "/etc/cdi/amd.json"},
		{name: "an NVIDIA spec is not an AMD one",
			files: map[string]string{"/etc/cdi/nvidia.yaml": nvidiaSpecYAML, "/etc/cdi/amd.json": `{"kind": "nvidia.com/gpu"}`},
			want:  ""},
		{name: "an extension the registry skips", files: map[string]string{"/etc/cdi/amd.yml": amdSpecYAML},
			want: ""},
		{name: "a subdirectory the registry skips", files: map[string]string{"/etc/cdi/old/amd.json": amdSpecJSON},
			want: ""},
		{name: "an indented kind is not the spec's",
			files: map[string]string{"/etc/cdi/x.yaml": "kind: nvidia.com/gpu\ndevices:\n  - kind: amd.com/gpu\n"},
			want:  ""},
		{name: "system containers.conf",
			files: map[string]string{
				"/etc/containers/containers.conf": "[containers]\ncdi_spec_dirs = [\"/not/engine\"]\n" +
					"[engine]\ncdi_spec_dirs = [\"/opt/cdi\"]\n",
				"/opt/cdi/amd.json":    amdSpecJSON,
				"/not/engine/amd.json": amdSpecJSON,
			},
			want: "/opt/cdi/amd.json", dirs: []string{"/etc/cdi", "/var/run/cdi", "/opt/cdi"}},
		{name: "a drop-in",
			files: map[string]string{
				"/usr/share/containers/containers.conf.d/50-cdi.conf": "[engine]\ncdi_spec_dirs = [\"/srv/cdi\"]\n",
				"/srv/cdi/amd.yaml": amdSpecYAML,
			},
			want: "/srv/cdi/amd.yaml"},
		{name: "the user's containers.conf",
			files: map[string]string{
				"/home/u/.config/containers/containers.conf": "[engine]\ncdi_spec_dirs = [\"/home/u/cdi\"]\n",
				"/home/u/cdi/amd.yaml":                       amdSpecYAML,
			},
			env:  map[string]string{"HOME": "/home/u"},
			want: "/home/u/cdi/amd.yaml"},
		{name: "CONTAINERS_CONF",
			files: map[string]string{
				"/cfg/podman.conf":  "[engine]\ncdi_spec_dirs = [\"/cfg/cdi\"]\n",
				"/cfg/cdi/amd.json": amdSpecJSON,
			},
			env:  map[string]string{"CONTAINERS_CONF": "/cfg/podman.conf"},
			want: "/cfg/cdi/amd.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeHostFiles(t, root, tc.files)
			got, searched := FindAMDCDISpec(root, func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Errorf("FindAMDCDISpec = %q, want %q (searched %v)", got, tc.want, searched)
			}
			for _, d := range tc.dirs {
				found := false
				for _, s := range searched {
					found = found || s == d
				}
				if !found {
					t.Errorf("searched %v, want it to include %s", searched, d)
				}
			}
			for _, s := range searched {
				if strings.HasPrefix(s, root) {
					t.Errorf("searched dir %q is spelled under the probe's root; the reader "+
						"is told host paths", s)
				}
			}
		})
	}
}
