package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEmptyNativeDetachConstructorRejectsCurrentSelectionAndWorkspaceDrift(t *testing.T) {
	previousSuite, previousBin, previousNative := privateSuite, yoloBin, privateNative
	defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousNative }()
	machine, home, registry, workspace := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
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
	configPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(configPath, "{}", 0644)
	write(filepath.Join(workspace, "yolo-jail.jsonc"), "{}", 0644)
	events := filepath.Join(machine, "native-execution-events")
	yoloBin = filepath.Join(machine, "constructor-tripwire")
	write(yoloBin, "#!/bin/sh\necho executed > '"+events+"'\n", 0755)
	fixture := &privateNativeFixture{home: home, userConfig: "{}", packs: map[string]string{}, check: func() error { return privateNativeWorkspaceAdmission(workspace) }}
	privateNative = fixture
	construct := func(execute bool) error {
		program, args, err := privateLaunchCommandChecked(runConfig{}, yoloBin, jailRunArgs())
		if err != nil {
			return err
		}
		cmd, err := privateNativeCommandChecked(context.Background(), program, args, envWithout("YOLO_VERSION"), fixture)
		if err == nil && execute {
			return cmd.Run()
		}
		return err
	}
	if err := construct(false); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"marker", "home", "selected-vendor", "workspace-mcp", "workspace-package", "workspace-tool", "workspace-local"} {
		t.Run(mutation, func(t *testing.T) {
			defer func() {
				write(marker, registry, 0600)
				write(configPath, "{}", 0644)
				write(filepath.Join(workspace, "yolo-jail.jsonc"), "{}", 0644)
				_ = os.Remove(filepath.Join(workspace, "mise.toml"))
				_ = os.Remove(filepath.Join(workspace, "yolo-jail.local.toml"))
			}()
			switch mutation {
			case "marker":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			case "home":
				t.Setenv("HOME", machine)
			case "selected-vendor":
				write(configPath, `{"packs":["claude"]}`, 0644)
			case "workspace-mcp":
				write(filepath.Join(workspace, "yolo-jail.jsonc"), `{"mcp_servers":{"unknown":{"command":"npx"}}}`, 0644)
			case "workspace-package":
				write(filepath.Join(workspace, "yolo-jail.jsonc"), `{"packages":["unclassified"]}`, 0644)
			case "workspace-tool":
				write(filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n", 0644)
			case "workspace-local":
				write(filepath.Join(workspace, "yolo-jail.local.toml"), "packs = [\"claude\"]\n", 0644)
			}
			if err := construct(true); err == nil {
				t.Fatalf("native detach constructor accepted %s", mutation)
			}
			if _, err := os.Stat(events); !os.IsNotExist(err) {
				t.Fatal("unclassified empty fixture reached executable effects")
			}
		})
	}
}
