package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackBuildNativeConstructorAdmitsOnlyTheOwnedLiteralGraph(t *testing.T) {
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
	fixture := packBuildNativeFixture(home, root)
	build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	manifest := `{"name":"packbuildfx","contributes":[{"kind":"program","bin":"packbuildfx-x","via":"pack","from":"tools/x","build":"` + buildJSON + `","run":"x"}]}`
	write(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), fixture.userConfig, 0644)
	write(filepath.Join(root, "pack.json"), manifest, 0644)
	write(filepath.Join(root, "tools", "x", "VERSION"), "1\n", 0644)
	privateNative = fixture
	construct := func() (*exec.Cmd, error) {
		if err := privateNativeWorkspaceAdmission(workspace); err != nil {
			return nil, err
		}
		name, args, err := privateLaunchCommandChecked(runConfig{}, yoloBin, append(jailRunArgs(), "--", "packbuildfx-x"))
		if err != nil {
			return nil, err
		}
		cmd, err := privateNativeCommandChecked(context.Background(), name, args, envWithout("YOLO_VERSION"), fixture)
		if err != nil {
			return nil, err
		}
		if cmd.Path != yoloBin {
			t.Fatal("pack build substituted a driver for its native CLI")
		}
		return cmd, nil
	}
	if _, err := construct(); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"missing-marker", "manifest-build", "version", "package-dependency", "workspace-floor", "workspace-config-link", "ancestor-config"} {
		t.Run(mutation, func(t *testing.T) {
			defer func() {
				write(marker, registry, 0600)
				write(filepath.Join(root, "pack.json"), manifest, 0644)
				write(filepath.Join(root, "tools", "x", "VERSION"), "1\n", 0644)
				_ = os.Remove(filepath.Join(root, "tools", "x", "package.json"))
				_ = os.Remove(filepath.Join(workspace, "mise.toml"))
				_ = os.Remove(filepath.Join(workspace, "yolo-jail.jsonc"))
				_ = os.Remove(filepath.Join(filepath.Dir(workspace), "yolo-jail.toml"))
			}()
			switch mutation {
			case "missing-marker":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			case "manifest-build":
				write(filepath.Join(root, "pack.json"), strings.Replace(manifest, "cat VERSION", "npm install", 1), 0644)
			case "version":
				write(filepath.Join(root, "tools", "x", "VERSION"), "99\n", 0644)
			case "package-dependency":
				write(filepath.Join(root, "tools", "x", "package.json"), `{"scripts":{"install":"unclassified"}}`, 0644)
			case "workspace-floor":
				write(filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n", 0644)
			case "workspace-config-link":
				if err := os.Symlink(filepath.Join(root, "pack.json"), filepath.Join(workspace, "yolo-jail.jsonc")); err != nil {
					t.Fatal(err)
				}
			case "ancestor-config":
				write(filepath.Join(filepath.Dir(workspace), "yolo-jail.toml"), "packages = [\"unclassified\"]\n", 0644)
			}
			if cmd, err := construct(); err == nil {
				_ = cmd.Run()
				t.Fatalf("native constructor accepted %s", mutation)
			}
			if _, err := os.Stat(events); !os.IsNotExist(err) {
				t.Fatal("unknown graph reached native execution")
			}
		})
	}
}
