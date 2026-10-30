package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio/iopriotest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func loadLaunchConsumerConfig(t *testing.T, body string, toml bool) (*Options, *jsonx.OrderedMap) {
	t.Helper()
	o, _, _ := amdHostOptions(t, map[string]string{"/etc/cdi/amd.json": amdSpecJSON})
	workspace := t.TempDir()
	name := paths.WorkspaceConfigLegacyJSONCName
	if toml {
		name = paths.WorkspaceConfigTOMLName
	}
	if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := config.LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadReadableConfig: %v", err)
	}
	return o, merged
}

func TestReadableMachineSettingsReachFreshRunArgv(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	// Resolved where minted: the I/O priority resolver grades the workspace's physical path
	// against the Kyber mount table below, and macOS's temp directory is under /var, a link
	// to /private/var.
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	body := `[machine]

gpu = true
ports = [{ host = 8080, jail = 80 }]
forward = [{ jail = 5432, host = 15432 }]
[machine.resources]
memory = "2g"
cpus = 2
pids_limit = 4000
[machine.resources.io]
priority = "idle"
`
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "agents")
	onRun := "cp -a " + shquote.Quote(paths.AgentsDir()) + " " + shquote.Quote(snap)
	argv, printed := fakePodmanLaunchIn(t, workspace, onRun, func(o *Options) {
		o.NeverAttach, o.AcceptConfigChanges = true, true
		o.ioSysRoot = iopriotest.Kyber(t, workspace).Root
	})
	if argv == nil {
		t.Fatalf("public Run did not reach the fake Podman runtime:\n%s", printed)
	}
	for _, want := range []string{
		"-p", "8080:80", "--memory", "2g", "--cpus", "2", "--pids-limit", "4000",
		`YOLO_PUBLISHED_PORTS=["80/tcp"]`, `YOLO_FORWARD_HOST_PORTS=["5432:15432"]`,
	} {
		if !slices.Contains(argv, want) {
			t.Errorf("public Run argv lacks %q: %v", want, argv)
		}
	}
	if !strings.Contains(printed, "GPU requested but") || !strings.Contains(printed, "starting without GPU passthrough") {
		t.Errorf("public Run did not report the machine.gpu request when the host probe found no GPU:\n%s", printed)
	}
	for _, want := range []string{
		"Resource limits: memory=2g, cpus=2, pids=4000",
		`resources.io.priority "idle" has no effect`,
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("public Run output lacks canonical resource effect %q:\n%s", want, printed)
		}
	}
	briefingPath := filepath.Join(snap, yoloruntime.FromWorkspace(workspace), briefingStagingName(claudeBriefingDest))
	briefingBytes, err := os.ReadFile(briefingPath)
	if err != nil {
		t.Fatalf("public Run did not stage its claude briefing: %v", err)
	}
	briefing := string(briefingBytes)
	if !strings.Contains(resourceLine(briefing), "memory=2g") || !strings.Contains(resourceLine(briefing), "cpus=2") {
		t.Errorf("public Run briefing omitted canonical resource limits: %q", resourceLine(briefing))
	}
	if !strings.Contains(ioPriorityBriefingLine(briefing), "`idle` (idle class)") {
		t.Errorf("public Run briefing omitted canonical IO priority: %q", ioPriorityBriefingLine(briefing))
	}
	forwards := []any{"5432:15432"}
	planned := (&Options{}).planPortForwards(forwards)
	if len(planned) != 1 || planned[0].LocalPort != 5432 || planned[0].HostPort != 15432 {
		t.Fatalf("forward plan = %#v, want jail port 5432 -> host port 15432", planned)
	}
}

func TestReadableMachineSettingsReachLaunchAndForwardPlans(t *testing.T) {
	cases := []struct {
		name string
		body string
		toml bool
	}{
		{
			name: "TOML machine settings",
			toml: true,
			body: `[machine]
 gpu = { enabled = true, vendor = "amd", mode = "cdi", devices = "all", vaapi = true }
 ports = [{ host = 8080, jail = 80 }]
 forward = [{ jail = 5432, host = 15432 }]
 [machine.resources]
 memory = "2g"
 cpus = 2
 pids_limit = 4000
 [machine.resources.io]
 priority = "idle"
`,
		},
		{
			name: "legacy JSONC settings",
			body: `{
 "gpu": {"enabled": true, "vendor": "amd", "mode": "cdi", "devices": "all", "vaapi": true},
 "resources": {"memory": "2g", "cpus": 2, "pids_limit": 4000, "io": {"priority": "idle"}},
 "network": {"ports": ["8080:80"], "forward_host_ports": ["5432:15432"]}
}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg := loadLaunchConsumerConfig(t, tc.body, tc.toml)
			in := gpuInput(t, "amd")
			in.cfg = cfg
			argv := o.assembleRunCmd(in)
			for _, want := range []string{
				"-p", "8080:80", "--device", "amd.com/gpu=all",
				"--memory", "2g", "--cpus", "2", "--pids-limit", "4000",
				"LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri", "YOLO_GPU_ENABLED=1",
				"YOLO_PUBLISHED_PORTS=[\"80/tcp\"]", "YOLO_IO_PRIORITY=idle",
			} {
				if !slices.Contains(argv, want) {
					t.Errorf("launch argv lacks %q: %v", want, argv)
				}
			}

			forwards := o.hostForwardPorts(cfg, "podman")
			if len(forwards) != 1 || forwards[0] != "5432:15432" {
				t.Fatalf("hostForwardPorts = %#v, want jail:host [5432:15432]", forwards)
			}
			planned := o.planPortForwards(forwards)
			if len(planned) != 1 || planned[0].LocalPort != 5432 || planned[0].HostPort != 15432 {
				t.Fatalf("planned host relays = %#v, want jail port 5432 -> host port 15432", planned)
			}
			macosPlan := planMacosUserPortRelays(cfg, "")
			if len(macosPlan.relays) != 2 {
				t.Fatalf("macos-user relay plan = %#v, want one published and one forwarded relay", macosPlan.relays)
			}
			if macosPlan.relays[0].listen != "0.0.0.0:8080" || macosPlan.relays[0].dial != "127.0.0.1:80" ||
				macosPlan.relays[1].listen != "127.0.0.1:5432" || macosPlan.relays[1].dial != "127.0.0.1:15432" {
				t.Fatalf("macos-user relay directions = %#v", macosPlan.relays)
			}
		})
	}
}

func TestReadablePublishPortsRetainResidualLegacyAddressBoundEntries(t *testing.T) {
	o, _, _ := amdHostOptions(t, map[string]string{"/etc/cdi/amd.json": amdSpecJSON})
	home := os.Getenv("HOME")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigLegacyJSONCName),
		[]byte(`{"network":{"ports":["127.0.0.1:9000:90"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	project := "[[machine.ports]]\nhost = 8000\njail = 80\n"
	if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceConfigTOMLName), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadReadableConfig with user JSONC and project TOML: %v", err)
	}
	in := gpuInput(t, "amd")
	in.cfg = cfg
	argv := o.assembleRunCmd(in)
	for _, want := range []string{"-p", "8000:80", "127.0.0.1:9000:90"} {
		if !slices.Contains(argv, want) {
			t.Errorf("mixed readable+residual legacy launch lacks %q: %v", want, argv)
		}
	}
	if got := argvValues(argv, "-p"); !slices.Equal(got, []string{"8000:80", "127.0.0.1:9000:90"}) {
		t.Errorf("canonical and residual legacy publish entries = %#v, want one of each without duplication", got)
	}
}

func TestReadableMachinePortDirectionsStayAbsentOrEmptyIndependently(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	machine := jsonx.NewOrderedMap()
	machine.Set("ports", []any{})
	cfg.Set("machine", machine)
	if got := readablePublishPorts(cfg); len(got) != 0 {
		t.Fatalf("empty canonical publish list = %#v", got)
	}
	if got := readableForwardPorts(cfg); got != nil {
		t.Fatalf("absent canonical forward list = %#v, want nil", got)
	}

	legacyNetwork := jsonx.NewOrderedMap()
	legacyNetwork.Set("forward_host_ports", []any{})
	cfg = jsonx.NewOrderedMap()
	cfg.Set("network", legacyNetwork)
	if got := readablePublishPorts(cfg); got != nil {
		t.Fatalf("absent legacy publish list = %#v, want nil", got)
	}
	if got := readableForwardPorts(cfg); len(got) != 0 {
		t.Fatalf("empty legacy forward list = %#v", got)
	}
}

func TestReadableMachinePresenceWinsLegacyAndDefaultsStayTyped(t *testing.T) {
	legacyGPU := jsonx.NewOrderedMap()
	legacyGPU.Set("enabled", true)
	legacyResources := jsonx.NewOrderedMap()
	legacyResources.Set("cpus", 8)
	machine := jsonx.NewOrderedMap()
	machine.Set("gpu", false)
	machine.Set("resources", jsonx.NewOrderedMap())
	cfg := jsonx.NewOrderedMap()
	cfg.Set("gpu", legacyGPU)
	cfg.Set("resources", legacyResources)
	cfg.Set("machine", machine)

	gpu := readableGPU(cfg)
	if gpu.enabled || gpu.vendor != "nvidia" || gpu.mode != "devices" {
		t.Fatalf("present false machine.gpu did not win with typed defaults: %+v", gpu)
	}
	resources := readableResources(cfg)
	if resources == nil || resources.Len() != 0 {
		t.Fatalf("present empty machine.resources did not win legacy resources: %#v", resources)
	}
	if got := readableGPU(nil); got.enabled || got.vendor != "nvidia" || got.mode != "devices" || got.options != nil {
		t.Fatalf("absent machine.gpu defaults = %+v", got)
	}
	if got := readableResources(nil); got != nil {
		t.Fatalf("absent machine.resources = %#v, want nil", got)
	}
}
