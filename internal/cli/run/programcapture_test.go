package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestSelectedOrdinaryEmptyPackSealActualRuntimeInputs(t *testing.T) {
	fixture := sealCrossingsFixture
	fixture.only = []string{}
	argv, ws, _, printed := fixture.launch(t, true)
	cname := yoloruntime.FromWorkspace(ws)
	for _, pair := range argvValues(argv, "-e") {
		name, _, _ := strings.Cut(pair, "=")
		if !sealedEnvAllowlist[name] {
			t.Fatalf("sealed producer inherited %s", name)
		}
	}
	repo := strings.TrimSuffix(sealedBindSources(argv)["/opt/yolo-jail/share/yolo-jail"], "/")
	for dest, src := range sealedBindSources(argv) {
		if !strings.HasPrefix(src, "/") || sealWithin(ws, src) || sealWithin(filepath.Join(paths.AgentsDir(), cname), src) || repo != "" && sealWithin(repo, src) || src == hostNixStore && dest == hostNixStore {
			continue
		}
		t.Fatalf("ordinary producer received host bind %s:%s", src, dest)
	}
	for _, root := range sealedJailReadable(ws) {
		if hits := grepTree(t, root, sealFixtureSecrets...); len(hits) > 0 {
			t.Fatalf("producer can read withheld bytes: %v", hits)
		}
	}
	if hits := argvCarrying(argv, sealFixtureSecrets...); len(hits) > 0 {
		t.Fatalf("producer argv contains withheld values: %v", hits)
	}
	if raw, err := os.ReadFile(config.WorkspaceAssembledConfigPath(ws)); err != nil || strings.TrimSpace(string(raw)) != "{}" {
		t.Fatalf("assembled config %q: %v", raw, err)
	}
	if got := launchedJailEnv(t, argv, ws)["YOLO_MCP_SERVERS"]; got != "{}" {
		t.Fatalf("MCP table crossed: %s", got)
	}
	if got, _ := envValue(argv, entrypoint.SealedBuildEnv); got != "1" {
		t.Fatal("actual runtime not told seal")
	}
	if _, err := os.Stat(servicesAtRun(ws)); !os.IsNotExist(err) {
		t.Fatalf("host service available from runtime start: %v", err)
	}
	if strings.Contains(printed, "runs pack code on your machine") {
		t.Fatal("producer started host code")
	}
	root, _ := envValue(argv, "YOLO_PACK_ROOT")
	// The env names the jail destination; inspect its actual bound host source.
	for dest, src := range sealedBindSources(argv) {
		if dest == root {
			files, err := filepath.Glob(filepath.Join(src, "*", "pack.json"))
			tomlFiles, tomlErr := filepath.Glob(filepath.Join(src, "*", "pack.toml"))
			files = append(files, tomlFiles...)
			if err == nil {
				err = tomlErr
			}
			if err != nil || len(files) != 0 {
				t.Fatalf("owner/unrelated manifests entered producer: %v %v", files, err)
			}
		}
	}
}

func TestFreshContainerProductionCallsOrdinaryPreparationAndDeliversExactVector(t *testing.T) {
	var called int
	identity := strings.Repeat("a", 64)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.CapturesDir = func() string { return t.TempDir() }
		repo, _ := o.RepoRoot()
		if err := os.WriteFile(filepath.Join(repo.Root, "flake.nix"), []byte("fixture-flake"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Root, "flake.lock"), []byte("fixture-lock"), 0o644); err != nil {
			t.Fatal(err)
		}
		o.PreparePrograms = func(req ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
			called++
			if req.Env == nil || req.Fingerprint == "" || req.Act == nil || req.Platform == "" || req.UnsupportedRuntime != "" {
				t.Fatalf("unresolved/unsupported default production request: %+v", req)
			}
			return map[string]entrypoint.ProgramCaptureDelivery{"probe": {Declared: "probe-pkg", Compatibility: identity, Platform: req.Platform}}, nil
		}
	})
	if called != 1 || argv == nil {
		t.Fatalf("deleted production caller: %d\n%s", called, printed)
	}
	if wire, ok := envValue(argv, entrypoint.ProgramCapturesEnv); !ok || !strings.Contains(wire, identity) {
		t.Fatalf("exact decision not delivered: %q", wire)
	}
}

func TestCaptureJailDoesNotPrepareOrdinaryPrograms(t *testing.T) {
	var called int
	fakePodmanLaunch(t, func(o *Options) {
		o.CapturesDir = func() string { return "" }
		o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
			called++
			return nil, nil
		}
	})
	if called != 0 {
		t.Fatal("capture recursively prepared ordinary vector")
	}
}

func TestSelectedOrdinaryPreparationFailureRefusesColdAndFreshBeforeRuntime(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh=%v", fresh), func(t *testing.T) {
			argv, printed := fakePodmanLaunch(t, func(o *Options) {
				o.Update = fresh
				repo, _ := o.RepoRoot()
				if err := os.WriteFile(filepath.Join(repo.Root, "flake.nix"), []byte("fixture-flake"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo.Root, "flake.lock"), []byte("fixture-lock"), 0644); err != nil {
					t.Fatal(err)
				}
				o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
					return nil, fmt.Errorf("synthetic selected installer failed")
				}
			})
			want := "ERROR: preparing this launch's programs failed\n" +
				"  synthetic selected installer failed\n" +
				"\n" +
				"Repair:\n" +
				"  fix what the error names, then retry the launch:\n" +
				"    yolo\n"
			if argv != nil || !strings.Contains(printed, want) {
				t.Fatalf("failed required preparation fell back/executed runtime, or left the refusal shape: %v\n%s", argv, printed)
			}
		})
	}
}

// A preparation's own refusal block (PD-NF1: a newer version whose preparation failed over a
// prepared one) is what the launch prints, whole, and the launch stops before the runtime.
func TestOrdinaryPreparationRefusalPrintsItsOwnBlock(t *testing.T) {
	refusal := &hostfloor.UpdateRefusal{Bin: "probe", Pack: "fixture", Installed: "1.0.0", Newer: "2.0.0",
		Act: "preparing the newer version", Err: fmt.Errorf("synthetic install failed"), Repair: "yolo", Command: "yolo",
		Runs: "run the prepared 1.0.0"}
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		repo, _ := o.RepoRoot()
		for _, name := range []string{"flake.nix", "flake.lock"} {
			if err := os.WriteFile(filepath.Join(repo.Root, name), []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
			return nil, fmt.Errorf("wrapped: %w", refusal)
		}
	})
	want := "ERROR: probe 1.0.0 → 2.0.0: preparing the newer version failed\n" +
		"  synthetic install failed\n" +
		"\n" +
		"Repair:\n" +
		"  fix what the error names, then retry it:\n" +
		"    yolo\n" +
		"\n" +
		"Start anyway:\n" +
		"  YOLO_USE_INSTALLED=probe yolo    run the prepared 1.0.0\n"
	if argv != nil || !strings.Contains(printed, want) {
		t.Fatalf("the launch did not stop on the preparation's refusal block: %v\n%s", argv, printed)
	}
	if strings.Contains(printed, "ERROR: preparing this launch's programs failed") {
		t.Fatalf("the generic block replaced the preparation's own:\n%s", printed)
	}
}

func TestCustomizedContainerRuntimeCallsAuthorizedFallbackWithoutCaptureDecision(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		store        bool
	}{
		{"packages", `{"install":{"nix":["hello"]}}`, false},
		{"configured-loader", `{"env_sources":[{"NODE_OPTIONS":"--max-old-space-size=1024"}]}`, false},
		{"configured-node", `{"install":{"mise":{"node":"24"}}}`, false},
		{"store-loader", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			argv, printed := fakePodmanLaunch(t, func(o *Options) {
				o.AcceptConfigChanges = true
				repo, _ := o.RepoRoot()
				if err := os.WriteFile(filepath.Join(repo.Root, "flake.nix"), []byte("fixture-flake"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo.Root, "flake.lock"), []byte("fixture-lock"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceConfigName), []byte(tc.config), 0644); err != nil {
					t.Fatal(err)
				}
				if tc.store {
					previous := o.Getenv
					o.Getenv = func(key string) string {
						if key == "YOLO_STORE_PACKAGES" {
							return "1"
						}
						return previous(key)
					}
				}
				o.PreparePrograms = func(req ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
					called = true
					if req.UnsupportedRuntime == "" {
						t.Fatal("actual pipeline asserted equality with unknown customized runtime")
					}
					return map[string]entrypoint.ProgramCaptureDelivery{}, nil
				}
			})
			if !called || argv == nil {
				t.Fatalf("customized normal runtime route was disabled: %v\n%s", called, printed)
			}
			if wire, ok := envValue(argv, entrypoint.ProgramCapturesEnv); ok && wire != "{}" {
				t.Fatalf("base artifact decision crossed customized runtime: %s", wire)
			}
		})
	}
}
