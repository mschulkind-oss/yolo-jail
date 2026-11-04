package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

func loadLegacyCheckConfig(t *testing.T, body string) *jsonx.OrderedMap {
	t.Helper()
	workspace := t.TempDir()
	path := filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWorkspaceConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("read legacy config through standard reader: %v", err)
	}
	return loaded
}

func runCheckOverStandardWorkspaceConfig(t *testing.T, workspace, filename, body string, inJail bool) string {
	t.Helper()
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Workspace = workspace
	if err := os.WriteFile(filepath.Join(workspace, filename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for name, contents := range map[string]string{"flake.nix": "{}", "go.mod": "module test\n", "flake.lock": "{}"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	opts.PathExists = func(path string) bool {
		if path == "/nix" || strings.HasPrefix(path, "/nix/") {
			return true
		}
		_, err := os.Stat(path)
		return err == nil
	}
	// Every case runs at the host notch: YOLO_SETUP names host-self, the setup `yolo host` runs.
	opts.Getenv = func(key string) string {
		if key == "YOLO_VERSION" && inJail {
			return "9.9.9-test"
		}
		if key == "YOLO_SETUP" {
			return "host-self"
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		switch name {
		case "podman", "nix", "python3":
			return "/usr/bin/" + name, true
		default:
			return "", false
		}
	}
	podmanAnswers(&opts, ExecResult{Stdout: "{}", Ran: true, RC: 0})
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":  {Stdout: "", Ran: true, RC: 0},
		"python3":          {Stdout: "ok", Ran: true, RC: 0},
		"podman images":    {Stdout: "", Ran: true, RC: 0},
		"podman ps":        {Stdout: "", Ran: true, RC: 0},
	})
	Check(opts)
	return stripANSI(out.String())
}

func TestCheckUsesStandardReaderCanonicalPackageAndGPUDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, filename, body, packageLine, gpuSection string
	}{
		{
			name:     "TOML boolean GPU",
			filename: paths.WorkspaceConfigTOMLName,
			body: `[install]
nix = [{ name = "mesa", outputs = ["lib", "dev"], platforms = ["linux"] }]

[machine]
gpu = true
`,
			packageLine: "`packages:` declares 1 entry, inert at the `host` notch by design",
			gpuSection:  "GPU (NVIDIA) checks",
		},
		{
			name:     "TOML rich GPU object",
			filename: paths.WorkspaceConfigTOMLName,
			body: `[install]
nix = [{ name = "mesa", outputs = ["lib", "dev"], platforms = ["linux"] }, { name = "darwin-tool", platforms = ["darwin"] }]

[machine.gpu]
enabled = true
vendor = "amd"
mode = "cdi"
vaapi = true
`,
			packageLine: "`packages:` declares 3 entries, inert at the `host` notch by design",
			gpuSection:  "GPU (AMD/ROCm)",
		},
		{
			name:        "legacy format 1",
			filename:    paths.WorkspaceConfigLegacyJSONCName,
			body:        `{"packages":[{"name":"mesa","outputs":["lib","dev"],"platforms":["linux"]}],"gpu":{"enabled":true}}`,
			packageLine: "`packages:` declares 1 entry, inert at the `host` notch by design",
			gpuSection:  "GPU (NVIDIA) checks",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			workspace := t.TempDir()
			path := filepath.Join(workspace, tc.filename)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			beforeConfig, err := config.LoadWorkspaceConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			before, err := config.SnapshotJSON(beforeConfig)
			if err != nil {
				t.Fatal(err)
			}

			got := runCheckOverStandardWorkspaceConfig(t, workspace, tc.filename, tc.body, true)
			if !strings.Contains(got, tc.packageLine) {
				t.Errorf("standard %s reader did not reach the canonical package-profile caller:\n%s", tc.name, got)
			}
			if !strings.Contains(got, tc.gpuSection) {
				t.Errorf("standard %s reader did not reach the canonical GPU caller:\n%s", tc.name, got)
			}
			if tc.gpuSection == "GPU (NVIDIA) checks" && !strings.Contains(got, "Run `yolo check` there") {
				t.Errorf("in-jail NVIDIA diagnostics did not identify host facts:\n%s", got)
			}
			if tc.gpuSection == "GPU (AMD/ROCm)" && !strings.Contains(got, "AMD CDI spec check") {
				t.Errorf("in-jail AMD diagnostics did not identify the CDI host fact:\n%s", got)
			}

			afterConfig, err := config.LoadWorkspaceConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			after, err := config.SnapshotJSON(afterConfig)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("check mutated canonical input:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

func TestCheckKeepsAbsentCanonicalGPUSilent(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := runCheckOverStandardWorkspaceConfig(t, workspace, paths.WorkspaceConfigTOMLName,
		"", true)
	for _, section := range []string{"GPU (NVIDIA)", "GPU (AMD/ROCm)"} {
		if strings.Contains(got, section) {
			t.Errorf("absent machine.gpu should keep %s silent:\n%s", section, got)
		}
	}
}

func TestGPUConfigReadsCanonicalMachineGPUBooleanAndObject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "boolean", value: true},
		{name: "object", value: func() *jsonx.OrderedMap {
			gpu := jsonx.NewOrderedMap()
			gpu.Set("enabled", true)
			gpu.Set("vendor", "amd")
			gpu.Set("mode", "cdi")
			return gpu
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := jsonx.NewOrderedMap()
			machine.Set("gpu", tc.value)
			merged := jsonx.NewOrderedMap()
			merged.Set("machine", machine)
			enabled, vendor, gpu, mode := gpuConfig(merged)
			if !enabled {
				t.Fatal("canonical machine.gpu did not enable GPU checks")
			}
			if tc.name == "boolean" {
				if vendor != "nvidia" || gpu.Len() != 0 || mode != "" {
					t.Fatalf("boolean machine.gpu = (%t,%q,%v,%q), want enabled NVIDIA defaults", enabled, vendor, gpu, mode)
				}
				return
			}
			if vendor != "amd" || mode != "cdi" || gpu.Len() != 3 {
				t.Fatalf("object machine.gpu = (%t,%q,%v,%q), want AMD CDI fields", enabled, vendor, gpu, mode)
			}
		})
	}
}
