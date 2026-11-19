package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackTreeNativeConstructorsRejectUnclassifiedBuildAndInstallerInputs(t *testing.T) {
	for _, family := range []string{"planned-versions", "readiness-failure"} {
		t.Run(family, func(t *testing.T) {
			previousSuite, previousBin, previousNative := privateSuite, yoloBin, privateNative
			defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousNative }()
			machine, home, registry, root, workspace := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
			if err := os.Chmod(registry, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(home, ".private-official-producer")
			write(filepath.Join(machine, privateSuiteMarker), "private-suite-v1\n", 0600)
			write(marker, registry, 0600)
			write(filepath.Join(registry, "home-binding"), home, 0600)
			privateSuite.armed, privateSuite.machine = true, machine
			t.Setenv("HOME", home)
			events := filepath.Join(machine, "native-execution-events")
			yoloBin = filepath.Join(machine, "constructor-tripwire")
			write(yoloBin, "#!/bin/sh\necho executed > '"+events+"'\n", 0755)
			fixture := packTreeNativeFixture(home, root)
			build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
			buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
			manifest := `{"name":"packtreefx","contributes":[{"kind":"program","bin":"packtreefx-x","via":"pack","from":"tools/x","build":"` + buildJSON + `","run":"x"}]}`
			input := filepath.Join(root, "tools", "x", "VERSION")
			value := "1\n"
			installer := ""
			if family == "readiness-failure" {
				fixture = packTreeFailureNativeFixture(home, root)
				manifest = `{"name":"packtreefail","contributes":[{"kind":"program","bin":"packtreefail-broken","via":"pack","from":"tools/broken","build":"echo building; exit 3","run":"x"},{"kind":"program","bin":"packtreefail-other","via":"installer","url":"file:///ctx/packs/packtreefail/install.sh"}]}`
				input, value = filepath.Join(root, "tools", "broken", "main.sh"), "echo never built\n"
				installer = "#!/bin/bash\nset -euo pipefail\nmkdir -p \"$HOME/.local/bin\"\n" +
					"printf '#!/bin/sh\\necho OTHER-RAN\\n' > \"$HOME/.local/bin/packtreefail-other\"\n" +
					"chmod +x \"$HOME/.local/bin/packtreefail-other\"\n"
				write(filepath.Join(root, "install.sh"), installer, 0644)
			}
			write(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), fixture.userConfig, 0644)
			write(filepath.Join(root, "pack.json"), manifest, 0644)
			write(input, value, 0644)
			privateNative = fixture
			construct := func(execute bool) error {
				if err := privateNativeWorkspaceAdmission(workspace); err != nil {
					return err
				}
				name, args, err := privateLaunchCommandChecked(runConfig{}, yoloBin, jailRunArgs())
				if err != nil {
					return err
				}
				cmd, err := privateNativeCommandChecked(context.Background(), name, args, envWithout("YOLO_VERSION"), fixture)
				if err == nil && cmd.Path != yoloBin {
					t.Fatal("packtree constructor replaced the native CLI")
				}
				if err == nil && execute {
					return cmd.Run()
				}
				return err
			}
			if err := construct(false); err != nil {
				t.Fatal(err)
			}
			if family == "planned-versions" {
				write(input, "2\n", 0644)
				if err := construct(false); err != nil {
					t.Fatal("planned VERSION 2 refused:", err)
				}
				write(input, value, 0644)
			}
			for _, mutation := range []string{"marker", "home", "recipe", "source-input", "dependency", "workspace", "installer-or-extra-file"} {
				t.Run(mutation, func(t *testing.T) {
					defer func() {
						write(marker, registry, 0600)
						write(filepath.Join(root, "pack.json"), manifest, 0644)
						write(input, value, 0644)
						_ = os.Remove(filepath.Join(filepath.Dir(input), "package.json"))
						_ = os.Remove(filepath.Join(workspace, "mise.toml"))
						if installer != "" {
							write(filepath.Join(root, "install.sh"), installer, 0644)
						} else {
							_ = os.Remove(filepath.Join(root, "install.sh"))
						}
					}()
					switch mutation {
					case "marker":
						if err := os.Remove(marker); err != nil {
							t.Fatal(err)
						}
					case "home":
						t.Setenv("HOME", machine)
					case "recipe":
						write(filepath.Join(root, "pack.json"), strings.Replace(manifest, `"build":`, `"node_floor":"99","refresh":"npm install","build":`, 1), 0644)
					case "source-input":
						write(input, "unclassified\n", 0644)
					case "dependency":
						write(filepath.Join(filepath.Dir(input), "package.json"), `{"scripts":{"install":"unclassified"}}`, 0644)
					case "workspace":
						write(filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n", 0644)
					case "installer-or-extra-file":
						write(filepath.Join(root, "install.sh"), "#!/bin/sh\nnpm install\n", 0644)
					}
					if err := construct(true); err == nil {
						t.Fatalf("native packtree constructor accepted %s", mutation)
					}
					if _, err := os.Stat(events); !os.IsNotExist(err) {
						t.Fatal("unclassified graph reached native execution")
					}
				})
			}
		})
	}
}
