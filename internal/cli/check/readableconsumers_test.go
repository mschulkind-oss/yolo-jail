package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio/iopriotest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

func TestCheckPreflightRendersReadableMiseAndReportsMachineSettings(t *testing.T) {
	for _, tc := range []struct {
		name          string
		userConfig    string
		workspaceFile string
		localFile     string
		wantTools     map[string]string
	}{
		{
			name:          "project and local TOML",
			workspaceFile: "[install.mise]\nnode = '22'\npython = '3.12'\n[machine]\ngpu = true\n[machine.resources.io]\npriority = 'idle'\n",
			localFile:     "[install.mise]\npython = '3.13'\n",
			wantTools:     map[string]string{"node": "22", "python": "3.13"},
		},
		{
			name:       "legacy user JSONC",
			userConfig: `{"mise_tools":{"node":"22","python":"3.12"},"gpu":{"enabled":true},"resources":{"io":{"priority":"idle"}}}`,
			wantTools:  map[string]string{"node": "22", "python": "3.12"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			workspace := t.TempDir()
			if tc.workspaceFile != "" {
				if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceConfigTOMLName), []byte(tc.workspaceFile), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName), []byte(`{}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.localFile != "" {
				if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), []byte(tc.localFile), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.userConfig != "" {
				userConfig := paths.UserConfigPath()
				if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(userConfig, []byte(tc.userConfig), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var output bytes.Buffer
			opts := baseOptions(t, &output)
			opts.Workspace = workspace
			// The disk under the workspace is a fixture, not this host's /proc: a macOS runner
			// has no mountinfo, so the section reported only that it could not grade. The
			// resolver grades the physical path, so the fixture mounts the resolved spelling.
			physicalWorkspace, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			opts.ioSysRoot = iopriotest.Scheduler(t, physicalWorkspace, "bfq").Root
			opts.Getenv = func(key string) string {
				if key == "YOLO_VERSION" {
					return "9.9.9-test"
				}
				return ""
			}
			repo := t.TempDir()
			for name, body := range map[string]string{"flake.nix": "{}", "go.mod": "module test\n"} {
				if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			opts.RepoRoot = func() (reporoot.Resolution, bool) {
				return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
			}
			opts.LookPath = func(name string) (string, bool) {
				switch name {
				case "podman", "nix", "python3":
					return "/usr/bin/" + name, true
				default:
					return "", false
				}
			}
			podmanAnswers(&opts, ExecResult{Stdout: `{"store":{"graphRoot":"/tmp/podman"}}`, Ran: true, RC: 0})
			opts.Exec = fakeExec(map[string]ExecResult{
				"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
				"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
				"nix config show":  {Stdout: "", Ran: true, RC: 0},
				"python3":          {Stdout: "ok", Ran: true, RC: 0},
				"podman images":    {Stdout: "", Ran: true, RC: 0},
				"podman ps":        {Stdout: "", Ran: true, RC: 0},
			})

			var rendered [][]byte
			opts.observeMisePreflight = func(data []byte) {
				rendered = append(rendered, append([]byte(nil), data...))
				clear(data) // The observer receives a detached copy, not the parsed output buffer.
			}
			Check(opts)

			if len(rendered) != 1 {
				t.Fatalf("public Check preflight observer calls = %d, want exactly one\n%s", len(rendered), output.String())
			}
			generated, err := tomlx.DecodeOrdered(rendered[0])
			if err != nil {
				t.Fatalf("generated mise TOML did not decode: %v\n%s", err, rendered[0])
			}
			toolsValue, present := generated.Get("tools")
			tools, ok := toolsValue.(*jsonx.OrderedMap)
			if !present || !ok {
				t.Fatalf("generated mise TOML tools = %#v, present=%v", toolsValue, present)
			}
			for tool, want := range tc.wantTools {
				got, _ := tools.Get(tool)
				if got != want {
					t.Errorf("public Check rendered mise pin %s = %#v, want %q\n%s", tool, got, want, rendered[0])
				}
			}
			got := stripANSI(output.String())
			for _, want := range []string{
				"Entrypoint Dry-Run",
				"Generated per-agent jail config in a temp home",
				"GPU (NVIDIA) checks",
				`resources.io.priority "idle" acts on sda`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("public Check report lacks %q:\n%s", want, got)
				}
			}
		})
	}
}
