package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationNativeConstructorsRefuseUnclassifiedCurrentInputsBeforeExecution(t *testing.T) {
	previousSuite, previousBin, previousFixture := privateSuite, yoloBin, privateNative
	defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousFixture }()
	machine, home, root := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	outer, marker := filepath.Join(machine, privateSuiteMarker), filepath.Join(home, ".private-official-producer")
	write(outer, "private-suite-v1\n", 0600)
	write(marker, root, 0600)
	write(filepath.Join(root, "home-binding"), home, 0600)
	privateSuite.armed, privateSuite.machine = true, machine
	tripwire := filepath.Join(machine, "executable-events")
	yoloBin = filepath.Join(machine, "native-constructor-tripwire")
	tripwireBytes := "#!/bin/sh\nprintf 'EXECUTED\\n' >> '" + tripwire + "'\nexit 96\n"
	write(yoloBin, tripwireBytes, 0755)
	copyBinary := filepath.Join(machine, "lifecycle-copy")
	write(copyBinary, tripwireBytes, 0755)
	t.Setenv("HOME", home)
	upstream := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	upstream.git("init", "-q", "-b", "main")
	upstream.release("1.0.0", "ten", "")
	pack := resolvedTempDir(t)
	initial := preparationNativeManifest(upstream.dir, "main", "printf 'PREPARED_TREE\\n' > prepared.txt")
	cfg := `{"packs":[{"source":"file://` + pack + `","name":"synthetic"}],"agent_updates":{"synthetic":"next-launch"}}`
	cfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(cfgPath, cfg, 0644)
	write(filepath.Join(pack, "pack.json"), initial, 0644)
	privateNative = preparationNativeFixture(home, cfg, pack, upstream.dir)
	privateNative.copyBinary = copyBinary
	constructors := map[string]func() (*exec.Cmd, error){
		"hidden-worker": func() (*exec.Cmd, error) {
			return preparationCommand(context.Background(), yoloBin, home, true, "internal", "preparation-scheduler")
		},
		"public-start": func() (*exec.Cmd, error) {
			return preparationCommand(context.Background(), yoloBin, home, false, "pack", "prepare", "start")
		},
		"replacement-start": func() (*exec.Cmd, error) {
			return preparationCommand(context.Background(), copyBinary, home, false, "pack", "prepare", "start")
		},
		"central-launch": func() (*exec.Cmd, error) {
			name, args, err := privateLaunchCommandChecked(runConfig{}, yoloBin, append(jailRunArgs(), "--", "bash", "-c", "true"))
			if err != nil {
				return nil, err
			}
			return exec.Command(name, args...), nil
		},
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			// A constructor-only positive does not execute this tripwire and is NOT
			// native artifact evidence. The runtime lifecycle test provides that proof.
			cmd, err := construct()
			wantBin := yoloBin
			if name == "replacement-start" {
				wantBin = copyBinary
			}
			if err != nil || cmd == nil || cmd.Path != wantBin {
				t.Fatalf("finite graph not admitted through actual %s constructor: %v", name, err)
			}
			for _, mutation := range []string{"missing-marker", "corrupt-marker", "missing-outer", "home-binding", "unknown-source", "unreadable-manifest", "npm", "native-installer", "refresh", "hook", "dependency", "floor", "build", "local-release", "source-dependency", "git-hook", "mcp", "packages", "config-fragment", "home"} {
				t.Run(mutation, func(t *testing.T) {
					defer func() {
						write(outer, "private-suite-v1\n", 0600)
						write(marker, root, 0600)
						write(filepath.Join(root, "home-binding"), home, 0600)
						write(cfgPath, cfg, 0644)
						write(filepath.Join(pack, "pack.json"), initial, 0644)
						write(filepath.Join(upstream.dir, "ver.txt"), "1.0.0\n", 0644)
						_ = os.Remove(filepath.Join(upstream.dir, "package.json"))
						_ = os.Remove(filepath.Join(upstream.dir, ".git", "hooks", "pre-upload"))
						_ = os.Remove(filepath.Join(home, ".config", "yolo-jail", "extra.toml"))
						privateNative.home = home
					}()
					switch mutation {
					case "missing-marker":
						if err := os.Remove(marker); err != nil {
							t.Fatal(err)
						}
					case "corrupt-marker":
						write(marker, "not-an-absolute-root", 0600)
					case "missing-outer":
						if err := os.Remove(outer); err != nil {
							t.Fatal(err)
						}
					case "home-binding":
						write(filepath.Join(root, "home-binding"), machine, 0600)
					case "unknown-source":
						write(filepath.Join(pack, "pack.json"), strings.Replace(initial, "git+file://"+upstream.dir, "git+https://example.invalid/unclassified", 1), 0644)
					case "unreadable-manifest":
						if err := os.Remove(filepath.Join(pack, "pack.json")); err != nil {
							t.Fatal(err)
						}
					case "npm", "native-installer":
						program := `{"kind":"program","bin":"unclassified","via":"npm","package":"unclassified"}`
						if mutation == "native-installer" {
							program = `{"kind":"program","bin":"unclassified","via":"installer","url":"https://example.invalid/install"}`
						}
						write(filepath.Join(pack, "pack.json"), `{"name":"synthetic","contributes":[`+program+`]}`, 0644)
					case "refresh", "hook", "dependency", "floor":
						field := map[string]string{"refresh": `"refresh":{},`, "hook": `"hooks":[],`, "dependency": `"needs":["claude"],`, "floor": `"node_floor":"99",`}[mutation]
						write(filepath.Join(pack, "pack.json"), "{"+field+initial[1:], 0644)
					case "build":
						write(filepath.Join(pack, "pack.json"), preparationNativeManifest(upstream.dir, "main", "npm install"), 0644)
					case "local-release":
						write(filepath.Join(upstream.dir, "ver.txt"), "99.0.0\n", 0644)
					case "source-dependency":
						write(filepath.Join(upstream.dir, "package.json"), `{"scripts":{"install":"unclassified"}}`, 0644)
					case "git-hook":
						write(filepath.Join(upstream.dir, ".git", "hooks", "pre-upload"), "#!/bin/sh\nexit 96\n", 0755)
					case "mcp", "packages":
						field := `"mcp_servers":{"unclassified":{"command":"npx","args":["unclassified"]}},`
						if mutation == "packages" {
							field = `"packages":["unclassified"],`
						}
						write(cfgPath, "{"+field+cfg[1:], 0644)
					case "config-fragment":
						write(filepath.Join(home, ".config", "yolo-jail", "extra.toml"), "packs = [\"claude\"]\n", 0644)
					case "home":
						privateNative.home = machine
					}
					cmd, err := construct()
					if err == nil {
						if cmd != nil {
							_ = cmd.Run()
						}
						t.Fatalf("%s accepted %s before native execution", name, mutation)
					}
					if _, err := os.Stat(tripwire); !os.IsNotExist(err) {
						t.Fatalf("%s/%s reached native executable: %v", name, mutation, err)
					}
				})
			}
		})
	}
}

func TestXBNativeRequesterRetainsRawRolesAndRefusesChangedFiniteGraph(t *testing.T) {
	previousSuite, previousBin, previousFixture := privateSuite, yoloBin, privateNative
	defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousFixture }()
	machine, home, root := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{filepath.Join(machine, privateSuiteMarker): "private-suite-v1\n", filepath.Join(home, ".private-official-producer"): root, filepath.Join(root, "home-binding"): home} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	privateSuite.armed, privateSuite.machine = true, machine
	yoloBin = filepath.Join(machine, "must-not-execute")
	tripwire := filepath.Join(machine, "native-events")
	if err := os.WriteFile(yoloBin, []byte("#!/bin/sh\necho executed > '"+tripwire+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("TERM", "private-native-pty-role")
	t.Setenv("YOLO_TEST_NATIVE_REQUEST_ROLE", "requester")
	up := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	up.git("init", "-q", "-b", "main")
	up.release("1.0.0", "ten", "")
	agent, ext := resolvedTempDir(t), resolvedTempDir(t)
	xbManifests(t, agent, ext, "git+file://"+up.dir+"?ref=main")
	cfg := `{"packs":[{"source":"file://` + agent + `","name":"` + xbAgent + `"},{"source":"file://` + ext + `","name":"` + xbExt + `"}],"profile":{"` + xbBin + `":"` + xbProfile + `"},"agent_updates":{"` + xbAgent + `":"next-launch"}}`
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	privateNative = xbNativeFixture(home, cfg, agent, ext, up.dir)
	workspace := resolvedTempDir(t)
	xbWrite(t, filepath.Join(workspace, "yolo-jail.jsonc"), "{}")
	cmd, err := xbRequesterCommand(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"TERM": "private-native-pty-role", "YOLO_TEST_NATIVE_REQUEST_ROLE": "requester", "YOLO_NO_AUTO_CAPTURE": "1", "PATH": os.Getenv("PATH")} {
		if actual := environmentValue(cmd.Env, key); actual != value {
			t.Fatalf("requester raw %s=%q, want %q", key, actual, value)
		}
	}
	for _, mutation := range []string{"missing-marker", "native-installer-bytes", "refresh", "floor", "source", "build", "workspace-mcp", "workspace-packages", "workspace-local", "workspace-tools"} {
		t.Run(mutation, func(t *testing.T) {
			agentFiles, extFiles := xbFixtureFiles("git+file://" + up.dir + "?ref=main")
			defer func() {
				_ = os.WriteFile(filepath.Join(home, ".private-official-producer"), []byte(root), 0600)
				xbWrite(t, filepath.Join(agent, "install.sh"), agentFiles["install.sh"])
				xbWrite(t, filepath.Join(agent, "pack.json"), agentFiles["pack.json"])
				xbWrite(t, filepath.Join(ext, "pack.json"), extFiles["pack.json"])
				xbWrite(t, filepath.Join(workspace, "yolo-jail.jsonc"), "{}")
				_ = os.Remove(filepath.Join(workspace, "yolo-jail.local.toml"))
				_ = os.Remove(filepath.Join(workspace, "mise.toml"))
			}()
			switch mutation {
			case "missing-marker":
				if err := os.Remove(filepath.Join(home, ".private-official-producer")); err != nil {
					t.Fatal(err)
				}
			case "native-installer-bytes":
				xbWrite(t, filepath.Join(agent, "install.sh"), "#!/bin/sh\nnpm install unclassified\n")
			case "refresh", "floor":
				field := `"refresh":{"argv":["update"],"lock":".xbfixture/update.lock"},`
				if mutation == "floor" {
					field = `"node_floor":"99",`
				}
				xbWrite(t, filepath.Join(agent, "pack.json"), strings.Replace(agentFiles["pack.json"], `"via":"installer",`, `"via":"installer",`+field, 1))
			case "source":
				xbWrite(t, filepath.Join(ext, "pack.json"), strings.Replace(extFiles["pack.json"], "git+file://"+up.dir, "git+https://example.invalid/source", 1))
			case "build":
				xbWrite(t, filepath.Join(ext, "pack.json"), strings.Replace(extFiles["pack.json"], "XB_LOCAL_BUILD", "UNCLASSIFIED_BUILD", 1))
			case "workspace-mcp":
				xbWrite(t, filepath.Join(workspace, "yolo-jail.jsonc"), `{"mcp_servers":{"unclassified":{"command":"npx","args":["unclassified"]}}}`)
			case "workspace-packages":
				xbWrite(t, filepath.Join(workspace, "yolo-jail.jsonc"), `{"packages":["unclassified"]}`)
			case "workspace-local":
				xbWrite(t, filepath.Join(workspace, "yolo-jail.local.toml"), "packages = [\"unclassified\"]\n")
			case "workspace-tools":
				xbWrite(t, filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n")
			}
			cmd, err := xbRequesterCommand(context.Background(), workspace)
			if err == nil {
				_ = cmd.Run()
				t.Fatal("specialized requester accepted current input drift")
			}
			if _, err := os.Stat(tripwire); !os.IsNotExist(err) {
				t.Fatal("specialized requester reached native executable")
			}
		})
	}
}
