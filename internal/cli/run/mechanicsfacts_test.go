package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestMechanicsSwitchFactsUseTheStagedPackSelection(t *testing.T) {
	t.Cleanup(loopholes.ResetPackModules)
	stale := writeLoopholePack(t, "stale-pack", "stale-switch", `{"name":"stale-switch","description":"stale","transport":"none","lifecycle":"external"}`)
	control := writeLoopholePack(t, "selected-pack", "selected-switch", `{"name":"selected-switch","description":"selected","transport":"none","lifecycle":"external"}`)
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{stale}))

	// The selected pack's loophole is listed, and has no switch: a pack's loophole runs whenever
	// its pack is selected. The stale record's is not listed at all.
	available := mechanicsLoopholeSwitches(nil, []*packload.Pack{control})
	if on, listed := available["selected-switch"]; !listed || on {
		t.Fatalf("the selected pack's loophole is %v (listed %v), want listed with no switch: %v", on, listed, available)
	}
	if _, listed := available["stale-switch"]; listed {
		t.Fatalf("mechanics facts used the stale process pack record instead of selected packs: %v", available)
	}
}

func TestRefreshedMechanicsSwitchFactsFollowLoopholeDecider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	broker := writeLoopholePack(t, "broker-pack", "brokered", `{"name":"brokered","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["/bin/true","{repository_scope}"],"publishes":"socket"},"brokered":{"source":"git","remote_host":"github.com"}}`)
	brokerEntry := jsonx.NewOrderedMap()
	brokerEntry.Set("enabled", true)
	controlEntry := jsonx.NewOrderedMap()
	controlEntry.Set("enabled", true)
	controlEntry.Set("description", "supported switch")
	controlEntry.Set("command", []any{"/bin/true"})
	loops := jsonx.NewOrderedMap()
	loops.Set("brokered", brokerEntry)
	loops.Set("switch-control", controlEntry)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", loops)

	packs := append(claudePackFixture(t), broker)
	o := goldenOptions(workspace, home)
	facts := o.mechanicsFactSheet("yolo-ws-switchfacts", cfg, "podman", stagedPacks{packs: packs}, storePackagesPlan{}, nil)
	if _, err := o.refreshJailBriefings("yolo-ws-switchfacts", cfg, "podman", stagedPacks{packs: packs}, ioprio.Normal, facts); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	// The default-selected pack's loophole (cgroup-delegate, PD-13) is listed too, with no switch;
	// this test is about the two the config names.
	byName := map[string]jailcontent.SwitchFact{}
	for _, fact := range facts.Switches {
		if !config.IsSelectedByDefault(fact.Name) {
			byName[fact.Name] = fact
		}
	}
	if len(byName) != 2 {
		t.Fatalf("produced switch facts = %#v, want both brokered and supported entries", facts.Switches)
	}
	if got := byName["brokered"]; got.Available {
		t.Errorf("brokered switch fact = %#v, want unavailable", got)
	}
	if got := byName["switch-control"]; !got.Available {
		t.Errorf("supported switch fact = %#v, want available", got)
	}
	facts.Timing = append(facts.Timing, changetiming.Row{Key: "loopholes", Backend: changetiming.Podman, Outcome: changetiming.HostSwitch})
	files, err := jailcontent.RenderBuiltinSkills(*facts)
	if err != nil {
		t.Fatalf("render produced mechanics sheet: %v", err)
	}
	body := string(files["jail-mechanics/SKILL.md"]) + string(files["jail-mechanics/host-map.md"]) +
		string(files["jail-mechanics/recipes/workspace-config.md"])
	if strings.Contains(body, "yolo loopholes enable brokered") || strings.Contains(body, "yolo loopholes enable switch-control") {
		t.Errorf("rendered produced sheet names the retired enable command:\n%s", body)
	}
	if !strings.Contains(body, "`switch-control` runs whenever its pack is selected") {
		t.Errorf("rendered produced sheet does not name the available switch's loophole:\n%s", body)
	}
}

func TestMechanicsFactsResolveWorkspaceConfigPathsOnHostAndJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	mainConfig := filepath.Join(workspace, "yolo-jail.json")
	localConfig := filepath.Join(workspace, "yolo-jail.local.json")
	for path, data := range map[string]string{mainConfig: `{}`, localConfig: `{}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	o := &Options{Workspace: workspace}
	facts := o.mechanicsFactSheet("container-config", mapOf(t, `{}`), "podman", stagedPacks{}, storePackagesPlan{}, nil)
	if !facts.WorkspaceConfigPresent || facts.ConfigPath != "/workspace/yolo-jail.json" {
		t.Fatalf("container workspace config = (%q, %t), want resolved jail path and present", facts.ConfigPath, facts.WorkspaceConfigPresent)
	}
	if !facts.LocalConfigPresent || facts.LocalConfigPath != "/workspace/yolo-jail.local.json" {
		t.Fatalf("container local config = (%q, %t), want resolved jail path and present", facts.LocalConfigPath, facts.LocalConfigPresent)
	}
	for _, want := range []struct{ path, source string }{
		{facts.ConfigPath, mainConfig}, {facts.LocalConfigPath, localConfig},
	} {
		found := false
		for _, row := range facts.Paths {
			if row.Path == want.path && row.HostSource == want.source {
				found = true
			}
		}
		if !found {
			t.Errorf("missing config path fact %#v in %#v", want, facts.Paths)
		}
	}
}

func TestMechanicsFactsDoNotUsePrimaryWorkspaceConfigForPresence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	if _, err := os.Stat("/workspace/yolo-jail.jsonc"); err != nil {
		t.Skipf("primary /workspace config is not present to distinguish from target workspace: %v", err)
	}
	facts := (&Options{Workspace: workspace}).mechanicsFactSheet("missing-config", mapOf(t, `{}`), "podman", stagedPacks{}, storePackagesPlan{}, nil)
	if facts.WorkspaceConfigPresent {
		t.Fatalf("missing target workspace config reported present: %q", facts.ConfigPath)
	}
	if facts.LocalConfigPresent {
		t.Fatalf("missing target local config reported present: %q", facts.LocalConfigPath)
	}
	for _, row := range facts.Paths {
		if row.Origin == "workspace-local config" || row.Origin == "workspace config" {
			t.Fatalf("invented config path fact for absent target file: %#v", row)
		}
	}
}

func TestNativeMechanicsFactsUseWorkspaceAndAccountDecisions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "yolo-jail.jsonc")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: workspace, MacosUserMode: macosuser.ModeSelf}
	facts := o.mechanicsFactSheet("native-mechanics", mapOf(t, `{}`), "macos-user", stagedPacks{}, storePackagesPlan{}, nil)
	if facts.WorkspacePath != workspace || facts.HostWorkspace != workspace {
		t.Fatalf("native workspace paths = (%q, %q), want actual workspace %q", facts.WorkspacePath, facts.HostWorkspace, workspace)
	}
	if facts.NativeAccountMode != macosuser.ModeSelf.String() {
		t.Fatalf("native account mode = %q, want %q", facts.NativeAccountMode, macosuser.ModeSelf.String())
	}
	if facts.NativeAccountUser != "" {
		t.Errorf("self mode invented dedicated-account user %q", facts.NativeAccountUser)
	}
	if facts.NativeMiseData == "" {
		t.Fatal("native fact sheet omitted the actual MISE_DATA_DIR")
	}
	wantNativeHome := macosuser.LaunchHomes(macosuser.ModeSelf, workspace, o.macosUserHostHome()).Home
	foundNativeHome := false
	for _, path := range facts.Paths {
		if path.Path == wantNativeHome {
			foundNativeHome = true
		}
	}
	if !foundNativeHome {
		t.Fatalf("native fact sheet omitted the actual self-mode HOME %q", wantNativeHome)
	}
	o.MacosUserMode = macosuser.ModeAccount
	account := o.mechanicsFactSheet("native-account", mapOf(t, `{}`), "macos-user", stagedPacks{}, storePackagesPlan{}, nil)
	if account.NativeAccountUser != macosuser.SandboxUser {
		t.Fatalf("dedicated-account username = %q, want actual sandbox account %q", account.NativeAccountUser, macosuser.SandboxUser)
	}
	if facts.PackageDelivery == string(changetiming.BakedImage) {
		t.Fatal("native facts claim delivery from an image that this backend does not have")
	}
	for _, row := range facts.Timing {
		if row.Key == "packages" && row.Outcome != changetiming.NextSession {
			t.Fatalf("native packages outcome = %q, want the next session without an image rebuild", row.Outcome)
		}
	}
	if !facts.WorkspaceConfigPresent || facts.ConfigPath != configPath {
		t.Fatalf("workspace config facts = (%q, %t), want (%q, true)", facts.ConfigPath, facts.WorkspaceConfigPresent, configPath)
	}
}

func TestMechanicsFactsIncludeLocalConfigScopeAndHostBackendTiming(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "yolo-jail.jsonc")
	localPath := filepath.Join(workspace, config.WorkspaceLocalConfigName)
	readonlyPath := filepath.Join(workspace, "vendor")
	for path, data := range map[string]string{
		configPath:   `{"workspace_readonly":["vendor"]}`,
		localPath:    `{}`,
		readonlyPath: "",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := mapOf(t, `{"workspace_readonly":["vendor"]}`)
	o := &Options{Workspace: workspace, IsMacOS: true, PathExists: func(string) bool { return false }, Getenv: func(string) string { return "" }}
	facts := o.mechanicsFactSheet("host-macos", cfg, "podman", stagedPacks{}, storePackagesPlan{}, nil)
	mechanicsWorkspaceReadonlyFromArgv(o, facts, cfg,
		[]string{"-v", readonlyPath + ":/workspace/vendor:ro"}, "podman")
	if !facts.LocalConfigPresent || facts.LocalConfigPath != "/workspace/"+config.WorkspaceLocalConfigName {
		t.Fatalf("local config = (%q, %t), want jail path (%q, true)", facts.LocalConfigPath, facts.LocalConfigPresent, "/workspace/"+config.WorkspaceLocalConfigName)
	}
	foundLocalSource := false
	for _, row := range facts.Paths {
		if row.Path == facts.LocalConfigPath && row.HostSource == localPath {
			foundLocalSource = true
		}
	}
	if !foundLocalSource {
		t.Fatalf("local config path fact does not map to host source %q: %#v", localPath, facts.Paths)
	}
	if len(facts.WorkspaceReadonly) != 1 || facts.WorkspaceReadonly[0].Path != "/workspace/vendor" || !facts.WorkspaceReadonly[0].Enforced {
		t.Fatalf("workspace_readonly facts = %#v, want enforced exact path", facts.WorkspaceReadonly)
	}
	var mounts, providers *changetiming.Row
	for i := range facts.Timing {
		switch facts.Timing[i].Key {
		case "mounts":
			mounts = &facts.Timing[i]
		case "providers":
			providers = &facts.Timing[i]
		}
	}
	if mounts == nil || mounts.Outcome != changetiming.AtNextRestart {
		t.Fatalf("podman-on-macOS mounts timing = %#v, want restart (M3 is not live there)", mounts)
	}
	if providers == nil || providers.Outcome == changetiming.UserScopeOnly {
		t.Fatalf("mixed providers scope was widened: %#v", providers)
	}
	if len(facts.ScopeRestrictions) == 0 {
		t.Fatal("fact sheet did not consume config.WorkspaceScopeRestrictions")
	}
	foundMixed := false
	for _, rule := range facts.ScopeRestrictions {
		if rule.Path == "config.mounts[*]" && rule.When == "mount is read-write" {
			foundMixed = true
		}
	}
	if !foundMixed || config.WorkspaceUserScopeOnlyKey("mounts") {
		t.Fatalf("mixed scope metadata/predicate lost: found=%v whole-key=%v", foundMixed, config.WorkspaceUserScopeOnlyKey("mounts"))
	}
	for _, rule := range facts.ScopeRestrictions {
		if strings.Contains(rule.Path, "use_profiles") {
			t.Fatalf("retired config key leaked into scope metadata: %#v", rule)
		}
	}
}

func TestMechanicsHostFileFactUsesEmittedBackingAndOriginalSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "npmrc")
	if err := os.WriteFile(source, []byte("registry=https://example.invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := config.HostFileEntry{Path: ".npmrc", Source: source, Scope: "user", Mode: config.HostFileModeReadonly}
	argv, wsState, _ := hostFilesLaunch(t, []string{"claude"}, nil, []config.HostFileEntry{entry})
	facts := &jailcontent.FactSheet{Paths: []jailcontent.PathFact{{Path: "/home/agent/.npmrc", HostSource: source}}}
	mechanicsHostFileBackings(facts, []config.HostFileEntry{entry}, argv, "podman", wsState)
	got := facts.Paths[0]
	if got.OriginalSource != source || got.BackingSource != source || got.HostSource != source || got.Delivery != "read-only bind from original host source" {
		t.Fatalf("host_files path did not use actual argv backing and preserve origin: %#v", got)
	}
	_, testSource, _, _ := runtime.Caller(0)
	runSource, err := os.ReadFile(filepath.Join(filepath.Dir(testSource), "run.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runSource), "mechanicsHostFileBackings(facts, hostFiles, runCmd, rt, wsState)") {
		t.Fatal("fresh launch no longer wires host-file facts from assembled argv")
	}
}

func TestAppliedMechanicsSnapshotDoesNotAdoptCurrentWorkspaceTextOrSkills(t *testing.T) {
	oldSkills := &jailcontent.WorkspaceSkills{Root: "/old/workspace", Dirs: []string{".agents/skills"}}
	currentSkills := &jailcontent.WorkspaceSkills{Root: "/current/workspace", Dirs: []string{".claude/skills"}}
	sheet := &jailcontent.FactSheet{
		Briefing:               jailcontent.BriefingInput{Mechanism: "container"},
		AppliedAgentsMDExtra:   "applied guidance",
		AppliedWorkspaceSkills: oldSkills,
	}
	extra, skills := appliedWorkspaceMechanics(sheet, "edited but unapplied", currentSkills)
	if extra != "applied guidance" || skills != oldSkills {
		t.Fatalf("attach facts adopted current workspace edits: extra=%q skills=%#v", extra, skills)
	}
	fresh := &jailcontent.FactSheet{}
	extra, skills = appliedWorkspaceMechanics(fresh, "fresh guidance", currentSkills)
	if extra != "fresh guidance" || skills != currentSkills || fresh.AppliedWorkspaceSkills != currentSkills {
		t.Fatalf("fresh sheet did not capture applied workspace inputs: extra=%q skills=%#v", extra, skills)
	}
}

func TestFreshMechanicsRefreshFollowsStoreAndHostFileDecisions(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(source), "run.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	store := strings.Index(text, "storePkgs, storePkgsOK := o.planStorePackages")
	hostFiles := strings.Index(text, "hostFiles, hfErr = config.LoadHostFiles")
	refresh, persist := -1, -1
	if hostFiles >= 0 {
		refresh = strings.Index(text[hostFiles:], "o.refreshJailBriefings(cname")
		if refresh >= 0 {
			refresh += hostFiles
			persist = strings.Index(text[refresh:], "writeMechanicsApplied(cname, in.ctxMountsToken, facts)")
			if persist >= 0 {
				persist += refresh
			}
		}
	}
	if !strings.Contains(text, "mechanicsWorkspaceReadonlyFromArgv(o, facts, cfg, runCmd, rt)") ||
		!strings.Contains(text, "mechanicsHostFileBackings(facts, hostFiles, runCmd, rt, wsState)") ||
		!strings.Contains(text, "facts.HostLoopback = hostLoopbackFromArgv(runCmd)") ||
		!strings.Contains(text, "hostNixMountedFromArgv(runCmd)") {
		t.Fatal("fresh facts no longer consume assembled argv host-file, loopback, and Nix decisions")
	}
	if store < 0 || hostFiles < 0 || refresh < 0 || persist < 0 || !(store < hostFiles && hostFiles < refresh && refresh < persist) {
		t.Fatalf("fresh mechanics refresh and applied snapshot must follow actual store and host-file decisions (store=%d hostFiles=%d refresh=%d persist=%d)", store, hostFiles, refresh, persist)
	}
}

func TestMechanicsFactsUseCanonicalResourcesForNativeCPUGuidance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name string
		cfg  *jsonx.OrderedMap
	}{
		{
			name: "TOML project resources",
			cfg:  loadMechanicsReadableConfig(t, paths.WorkspaceConfigTOMLName, "[machine.resources]\ncpus = 2\n", ""),
		},
		{
			name: "metadata-free legacy resources",
			cfg:  mapOf(t, `{"resources":{"cpus":2}}`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := (&Options{Workspace: t.TempDir()}).mechanicsFactSheet("native-resources", tc.cfg, "macos-user", stagedPacks{}, storePackagesPlan{}, nil)
			if len(facts.CooperativeCPUDefaults) != len(macosuser.CooperativeCPUVars) {
				t.Fatalf("native cooperative CPU variables = %v, want %v", facts.CooperativeCPUDefaults, macosuser.CooperativeCPUVars)
			}
		})
	}
}

func TestMechanicsPackageFactsHonorReadableProjectAndLocalNixDeclarations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gpu := "[machine.gpu]\nenabled = true\nvendor = 'amd'\nvaapi = true\n"
	cases := []struct {
		name    string
		file    string
		project string
		local   string
		want    changetiming.Outcome
	}{
		{
			name:    "project TOML adds VA-API image packages",
			file:    paths.WorkspaceConfigTOMLName,
			project: gpu,
			want:    changetiming.RestartAndRebuild,
		},
		{
			name:    "local TOML already declares VA-API packages",
			file:    paths.WorkspaceConfigTOMLName,
			project: gpu,
			local:   "[install]\nnix = ['mesa', 'libva-utils']\n",
			want:    changetiming.AtNextRestart,
		},
		{
			name:    "legacy workspace already declares VA-API packages",
			file:    paths.WorkspaceConfigLegacyJSONCName,
			project: `{"gpu":{"enabled":true,"vendor":"amd","vaapi":true},"packages":["mesa","libva-utils"]}`,
			want:    changetiming.AtNextRestart,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadMechanicsReadableConfig(t, tc.file, tc.project, tc.local)
			facts := (&Options{Workspace: t.TempDir(), Sealed: true}).mechanicsFactSheet("package-facts", cfg, "podman", stagedPacks{}, storePackagesPlan{}, nil)
			for _, row := range facts.Timing {
				if row.Key == "gpu" {
					if row.Outcome != tc.want {
						t.Fatalf("gpu timing = %s, want %s", row.Outcome, tc.want)
					}
					return
				}
			}
			t.Fatal("mechanics facts have no gpu timing row")
		})
	}
}

func TestMechanicsGPUImagePackageCheckKeepsMetadataFreeLegacyFallback(t *testing.T) {
	cfg := mapOf(t, `{"machine":{"gpu":{"enabled":true,"vendor":"amd","vaapi":true}},"packages":["mesa","libva-utils"]}`)
	if gpuAddsImagePackages(cfg) {
		t.Fatal("metadata-free legacy declared package names were treated as newly added image inputs")
	}
}

func loadMechanicsReadableConfig(t *testing.T, filename, project, local string) *jsonx.OrderedMap {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, filename), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	merged, err := config.LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatalf("LoadReadableConfig: %v", err)
	}
	return merged
}

func TestMechanicsFactSheetUsesActualImagePackageDecision(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name       string
		cfg        string
		store      bool
		wantGPU    changetiming.Outcome
		wantReason string
	}{
		{"ordinary gpu passthrough", `{"gpu":{"enabled":true,"vendor":"amd"}}`, false, changetiming.AtNextRestart, ""},
		{"vaapi adds image packages", `{"gpu":{"enabled":true,"vendor":"amd","vaapi":true}}`, false, changetiming.RestartAndRebuild, "the baked image inputs change"},
		{"vaapi packages are store delivered", `{"gpu":{"enabled":true,"vendor":"amd","vaapi":true}}`, true, changetiming.AtNextRestart, ""},
		{"vaapi packages already declared", `{"packages":["mesa","libva-utils"],"gpu":{"enabled":true,"vendor":"amd","vaapi":true}}`, false, changetiming.AtNextRestart, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadMechanicsReadableConfig(t, paths.WorkspaceConfigLegacyJSONCName, tc.cfg, "")
			store := storePackagesPlan{}
			if tc.store {
				store.Active = true
			}
			o := &Options{Workspace: t.TempDir(), Sealed: true}
			facts := o.mechanicsFactSheet("mechanics-test", cfg, "podman", stagedPacks{}, store, nil)
			var gpu *changetiming.Row
			var profiles *changetiming.Row
			var providers *changetiming.Row
			for i := range facts.Timing {
				switch facts.Timing[i].Key {
				case "gpu":
					gpu = &facts.Timing[i]
				case "profiles":
					profiles = &facts.Timing[i]
				case "providers":
					providers = &facts.Timing[i]
				}
			}
			if gpu == nil || gpu.Outcome != tc.wantGPU || gpu.Reason != tc.wantReason {
				t.Fatalf("gpu timing = %#v, want outcome %q reason %q", gpu, tc.wantGPU, tc.wantReason)
			}
			if profiles == nil || profiles.Outcome != changetiming.UserScopeOnly {
				t.Errorf("profiles timing = %#v, want user-scope refusal", profiles)
			}
			if providers == nil || providers.Outcome == changetiming.UserScopeOnly {
				t.Errorf("mixed providers timing = %#v, must not be coarsened to user-scope-only", providers)
			}
		})
	}
}
