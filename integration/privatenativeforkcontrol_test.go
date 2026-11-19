package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestPlainForkNativeConstructorRejectsUnknownGitGraphAndGeneratedPins(t *testing.T) {
	previousSuite, previousBin, previousNative := privateSuite, yoloBin, privateNative
	defer func() { privateSuite, yoloBin, privateNative = previousSuite, previousBin, previousNative }()
	machine, home, registry, base, fork, workspace := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
	repo, _ := forkFixtureRepo(t)
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
	fixture := plainForkNativeFixture(home, base, fork, repo)
	configPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	write(configPath, fixture.userConfig, 0644)
	baseManifest := `{"name":"forkfixture-base","contributes":[{"kind":"program","bin":"forkfixture","via":"installer","url":"file:///ctx/packs/forkfixture-base/install.sh"}]}`
	installer := "#!/bin/bash\nmkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/bash\\necho FORKFIXTURE_BASE_INSTALLER_RAN\\n' > \"$HOME/.local/bin/forkfixture\"\nchmod +x \"$HOME/.local/bin/forkfixture\"\n"
	build := `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho FORKFIXTURE_FORK_BUILD_%s\n' "$(cat rev.txt)" > "$HOME/.local/bin/forkfixture" && chmod +x "$HOME/.local/bin/forkfixture"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	forkManifest := `{"name":"forkfixture-fork","contributes":[{"kind":"program","bin":"forkfixture","via":"source","fork_of":"forkfixture-base","source":"git+file://` + repo + `?ref=main","build":"` + buildJSON + `","produces":[".local/bin/forkfixture"]}]}`
	write(filepath.Join(base, "pack.json"), baseManifest, 0644)
	write(filepath.Join(base, "install.sh"), installer, 0644)
	write(filepath.Join(fork, "pack.json"), forkManifest, 0644)
	privateNative = fixture
	gitConfigPath := filepath.Join(repo, ".git", "config")
	gitConfig, err := os.ReadFile(gitConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	construct := func(execute bool) error {
		if err := privateNativeWorkspaceAdmission(workspace); err != nil {
			return err
		}
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
	for _, mutation := range []string{"marker", "home", "installer", "build", "source", "revision", "git-config", "git-hook", "git-alternate", "pin-schema", "pin-key", "pin-source", "pin-commit", "pin-link", "index-schema", "index-identity", "index-link", "index-mode", "index-lock-content", "pack-record-schema", "pack-record-foreign", "pack-record-ref", "pack-record-mode", "unknown-lock", "workspace"} {
		t.Run(mutation, func(t *testing.T) {
			defer func() {
				write(marker, registry, 0600)
				write(filepath.Join(base, "install.sh"), installer, 0644)
				write(filepath.Join(fork, "pack.json"), forkManifest, 0644)
				write(filepath.Join(repo, "rev.txt"), "1\n", 0644)
				write(gitConfigPath, string(gitConfig), 0644)
				_ = os.Remove(filepath.Join(repo, ".git", "hooks", "pre-commit"))
				_ = os.Remove(filepath.Join(repo, ".git", "objects", "info", "alternates"))
				_ = os.Remove(packsrc.ForkLockPath(configPath))
				_ = os.Remove(packsrc.LockPath(configPath))
				_ = os.Remove(packsrc.IdentityIndexPath(configPath))
				_ = os.Remove(filepath.Join(filepath.Dir(configPath), "."+packsrc.IdentityIndexName+".lock"))
				_ = os.Remove(filepath.Join(filepath.Dir(configPath), "unknown.lock"))
				_ = os.Remove(filepath.Join(workspace, "mise.toml"))
			}()
			switch mutation {
			case "marker":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			case "home":
				t.Setenv("HOME", machine)
			case "installer":
				write(filepath.Join(base, "install.sh"), "#!/bin/sh\nnpm install\n", 0644)
			case "build":
				write(filepath.Join(fork, "pack.json"), strings.Replace(forkManifest, "cat rev.txt", "npm install", 1), 0644)
			case "source":
				write(filepath.Join(fork, "pack.json"), strings.Replace(forkManifest, "git+file://"+repo, "git+https://example.invalid/source", 1), 0644)
			case "revision":
				write(filepath.Join(repo, "rev.txt"), "unclassified\n", 0644)
			case "git-config":
				write(gitConfigPath, string(gitConfig)+"[include]\npath = /unclassified\n", 0644)
			case "git-hook":
				write(filepath.Join(repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 1\n", 0755)
			case "git-alternate":
				write(filepath.Join(repo, ".git", "objects", "info", "alternates"), "/unclassified\n", 0644)
			case "pin-schema":
				write(packsrc.ForkLockPath(configPath), `{"schema":99,"forks":{}}`, 0644)
			case "pin-key":
				write(packsrc.ForkLockPath(configPath), `{"schema":1,"forks":{"unclassified":{"key":"unclassified"}}}`, 0644)
			case "pin-source":
				write(packsrc.ForkLockPath(configPath), `{"schema":1,"forks":{"forkfixture-fork/forkfixture":{"key":"forkfixture-fork/forkfixture","source":"git+https://example.invalid","ref":"main","commit":"0000000000000000000000000000000000000000"}}}`, 0644)
			case "pin-commit":
				write(packsrc.ForkLockPath(configPath), `{"schema":1,"forks":{"forkfixture-fork/forkfixture":{"key":"forkfixture-fork/forkfixture","source":"git+file://`+repo+`?ref=main","ref":"main","commit":"0000000000000000000000000000000000000000"}}}`, 0644)
			case "pin-link":
				if err := os.Symlink(filepath.Join(base, "pack.json"), packsrc.ForkLockPath(configPath)); err != nil {
					t.Fatal(err)
				}
			case "index-schema", "index-identity", "index-link", "index-mode", "index-lock-content":
				indexPath := packsrc.IdentityIndexPath(configPath)
				lockPath := filepath.Join(filepath.Dir(configPath), "."+packsrc.IdentityIndexName+".lock")
				write(lockPath, "", 0644)
				switch mutation {
				case "index-schema":
					write(indexPath, `{"schema":99,"packs":{}}`, 0600)
				case "index-identity":
					write(indexPath, `{"schema":1,"packs":{"dir:/unclassified":"forkfixture-fork"}}`, 0600)
				case "index-link":
					if err := os.Symlink(filepath.Join(base, "pack.json"), indexPath); err != nil {
						t.Fatal(err)
					}
				case "index-mode":
					write(indexPath, "{}", 0644)
				case "index-lock-content":
					write(indexPath, "{}", 0600)
					write(lockPath, "unclassified", 0644)
				}
			case "pack-record-schema":
				write(packsrc.LockPath(configPath), `{"schema":99,"packs":{}}`, 0644)
			case "pack-record-foreign":
				write(packsrc.LockPath(configPath), `{"schema":1,"packs":{"unknown":{"name":"unknown","source":"git+https://example.invalid"}}}`, 0644)
			case "pack-record-ref":
				write(packsrc.LockPath(configPath), `{"schema":1,"packs":{"forkfixture-base":{"name":"forkfixture-base","source":"file://`+base+`","ref":"main"},"forkfixture-fork":{"name":"forkfixture-fork","source":"file://`+fork+`"}}}`, 0644)
			case "pack-record-mode":
				write(packsrc.LockPath(configPath), "{}", 0600)
			case "unknown-lock":
				write(filepath.Join(filepath.Dir(configPath), "unknown.lock"), "unclassified", 0644)
			case "workspace":
				write(filepath.Join(workspace, "mise.toml"), "[tools]\nnode = \"99\"\n", 0644)
			}
			if err := construct(true); err == nil {
				t.Fatalf("native plain fork constructor admitted %s", mutation)
			}
			if _, err := os.Stat(events); !os.IsNotExist(err) {
				t.Fatal("unclassified plain fork input reached executable effects")
			}
		})
	}
}
