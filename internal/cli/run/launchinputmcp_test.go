package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestRunSnapshotsAndFreezesSelectedMCPBeforeKeeperSpawn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix daemon fixture")
	}
	home := packHome(t)
	marker := filepath.Join(t.TempDir(), "selected-daemon.marker")
	manifest := fmt.Sprintf(`{"name":"fixture-input","description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":[%q,"-test.run=^TestLaunchInputFixtureDaemon$","--","{launch_input}","{socket}",%q],"publishes":"socket","launch_input":"fixture-input"}}`, os.Args[0], marker)
	writeLocalLoopholePack(t, home, "fixture-input", manifest)
	t.Setenv(launchInputChildEnv, "1")
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stateDir, "replicas"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeUserConfigJSON(t, home, `{"packs":[],"mcp_servers":{"user":{"url":"https://user.example/mcp"}}}`)
	workspace := t.TempDir()
	writeWorkspaceConfig(t, workspace, `{"mcp_servers":{"private":{"url":"https://workspace.example/mcp"}}}`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, workspace, "podman", &stdout, &stderr, nil)
	produced := false
	o.launchInputProducers = map[string]LaunchInputProducer{"fixture-input": {
		StateDir: func(LaunchInputContext) string { return stateDir },
		Produce: func(in LaunchInputContext) ([]byte, error) {
			produced = true
			if !in.MCPProven || in.MCPScopes["user"].Level != "user" || in.MCPScopes["private"].Level != "workspace" {
				return nil, fmt.Errorf("Run's selected producer lost source attribution: %+v", in.MCPScopes)
			}
			return []byte("synthetic-selected-input"), nil
		},
	}}
	o.AcceptConfigChanges = true
	o.NeverAttach = true
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	var plan *keeperPlan
	previousSpawner := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = previousSpawner })
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
		var err error
		plan, err = readKeeperPlan(planPath)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(plan.Services, "fixture-input") || plan.LaunchInputMCP == nil || !plan.LaunchInputMCP.Proven {
			return nil, errors.New("selected Run lost its pre-staging snapshot or post-gate freeze")
		}
		k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
		mkdirHostServicesDir(hostServiceSocketsDir(plan.Cname, false))
		if err := launchInputOutsideJail(filepath.Join(stateDir, "replicas", plan.Cname+".json"), plan.RunCmd); err != nil {
			return nil, fmt.Errorf("fixture private path: %w", err)
		}
		k.o.launchInputProducers = o.launchInputProducers
		k.o.PathExists = func(string) bool { return false }
		k.o.ServiceReadyTimeout = 2 * time.Second
		k.o.Stdout, k.o.Stderr = &stdout, &stderr
		module := filepath.Join(plan.PackTree, "local", "loopholes", "fixture-input")
		set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
		sockets := hostServiceSocketsDir(plan.Cname, false)
		handles := k.o.startLoopholesMatching(set, plan.Cname, "podman", nil, func(string) bool { return true })
		if len(handles) != 1 {
			return nil, fmt.Errorf("selected daemon did not start: %s", stderr.String())
		}
		k.o.stopLoopholes(handles, sockets, plan.Cname, "podman")
		return nil, errors.New("fixture stopped before container spawn")
	}
	if rc := Run(*o); rc == 0 || plan == nil {
		t.Fatalf("selected Run did not reach the keeper plan (rc=%d): %s %s", rc, stdout.String(), stderr.String())
	}
	if !produced {
		t.Fatalf("selected Run did not hand its frozen composition to the synthetic daemon producer: %s %s", stdout.String(), stderr.String())
	}
	body, err := os.ReadFile(marker)
	if err != nil || !bytes.Contains(body, []byte("synthetic-selected-input")) || !bytes.HasPrefix(body, []byte("600\n")) {
		t.Fatalf("selected daemon did not read the private mode-0600 input: %q %v; output: %s %s", body, err, stdout.String(), stderr.String())
	}
	if plan.LaunchInputMCP == nil || !plan.LaunchInputMCP.Proven {
		t.Fatal("selected Run lost its pre-staging user snapshot or its post-gate freeze")
	}
	effective, err := jsonx.Decode([]byte(plan.LaunchInputMCP.EffectiveJSON))
	if err != nil {
		t.Fatal(err)
	}
	userOnly, err := jsonx.Decode([]byte(plan.LaunchInputMCP.UserOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	scopes := packload.MCPServerScopes(effective.(*jsonx.OrderedMap), userOnly.(*jsonx.OrderedMap))
	if scopes["user"].Level != "user" || scopes["private"].Level != "workspace" {
		t.Fatalf("Run's selected effective/user MCP scopes = %+v", scopes)
	}
}

func TestFrozenMCPForLaunchUsesOnlyProvenSelectedPackSources(t *testing.T) {
	pack := &packload.Pack{Name: "selected", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindMCP, Name: "packed", Raw: json.RawMessage(`{"url":"https://pack.example/mcp"}`)}}}}
	user := newConfig()
	cfg := newConfig()
	first := frozenMCPForLaunch(cfg, user, nil, []*packload.Pack{pack})
	if !first.Proven {
		t.Fatal("empty user selection must be provable")
	}
	decoded, err := jsonx.Decode([]byte(first.EffectiveJSON))
	if err != nil {
		t.Fatal(err)
	}
	effective := decoded.(*jsonx.OrderedMap)
	decoded, err = jsonx.Decode([]byte(first.UserOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	userOnly := decoded.(*jsonx.OrderedMap)
	if got := packload.MCPServerScopes(effective, userOnly)["packed"].Level; got != "workspace" {
		t.Fatalf("CLI-only pack promoted to %q", got)
	}
	second := frozenMCPForLaunch(cfg, user, []*packload.Pack{pack}, []*packload.Pack{pack})
	decoded, err = jsonx.Decode([]byte(second.UserOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	if got := packload.MCPServerScopes(effective, decoded.(*jsonx.OrderedMap))["packed"].Level; got != "user" {
		t.Fatalf("user-selected pack classified %q", got)
	}
	replaced := &packload.Pack{Name: "selected", Decl: &packdecl.Manifest{Name: "different"}}
	if got := frozenMCPForLaunch(cfg, user, []*packload.Pack{replaced}, []*packload.Pack{pack}); got.Proven {
		t.Fatal("staged declaration different from selection was trusted")
	}
	if sameSelectedMCPPacks([]*packload.Pack{pack}, []*packload.Pack{replaced}) {
		t.Fatal("changed user selection was trusted")
	}
}

func TestFrozenMCPForLaunchUsesEffectiveURLAndDropsInheritedHeaders(t *testing.T) {
	user := newConfig()
	userMCP, err := jsonx.Decode([]byte(`{"linear":{"url":"https://user.example/mcp","headers":{"Authorization":"user-token"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	user.Set("mcp_servers", userMCP)
	workspace := newConfig()
	wsMCP, err := jsonx.Decode([]byte(`{"linear":{"url":"https://workspace.example/mcp"}}`))
	if err != nil {
		t.Fatal(err)
	}
	workspace.Set("mcp_servers", wsMCP)
	effective := config.MergeConfig(user, workspace)
	frozen := frozenMCPForLaunch(effective, user, nil, nil)
	decoded, err := jsonx.Decode([]byte(frozen.EffectiveJSON))
	if err != nil {
		t.Fatal(err)
	}
	server, _ := decoded.(*jsonx.OrderedMap).Get("linear")
	entry := server.(*jsonx.OrderedMap)
	if url, _ := entry.Get("url"); url != "https://workspace.example/mcp" {
		t.Fatalf("url = %v", url)
	}
	if headers, exists := entry.Get("headers"); exists && headers != nil {
		t.Fatalf("inherited authorization headers reached changed URL: %v", headers)
	}
	userOnly, err := jsonx.Decode([]byte(frozen.UserOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	if scope := packload.MCPServerScopes(decoded.(*jsonx.OrderedMap), userOnly.(*jsonx.OrderedMap))["linear"]; scope.Level != "workspace" {
		t.Fatalf("URL override was given user scope: %+v", scope)
	}
}

func TestUnselectedLaunchHasNoMCPInputInKeeperPlan(t *testing.T) {
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	if o.frozenLaunchMCPForKeeper() != nil {
		t.Fatal("unselected launch retained MCP composition")
	}
	plan, err := o.keeperPlanFor(newConfig(), "podman", "jail", stagedPacks{}, nil, nil, nil, "", "", []string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || json.Valid(data) == false {
		t.Fatal("keeper plan did not encode")
	}
	var parsed map[string]any
	if err = json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["launch_input_mcp"]; ok {
		t.Fatal("unselected plan carried dormant MCP field")
	}
	o.launchInputMCP = frozenMCPForLaunch(newConfig(), newConfig(), nil, nil)
	plan, err = o.keeperPlanFor(newConfig(), "podman", "jail", stagedPacks{}, nil, nil, nil, "", "", []string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LaunchInputMCP == nil || !plan.LaunchInputMCP.Proven {
		t.Fatal("launcher did not carry frozen composition into the keeper plan")
	}
}
