package jailcontent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
)

func TestWorkspaceConfigRecipeRendersOnlySuppliedCooperativeCPUFacts(t *testing.T) {
	facts := FactSheet{
		HostWorkspace:          "/Users/test/project",
		ConfigPath:             "/Users/test/project/yolo-jail.json",
		CooperativeCPUDefaults: []string{"CPU_PARALLELISM_DEFAULT"},
		Briefing:               BriefingInput{Mechanism: "macos-user"},
	}
	recipe := renderRecipe("workspace-config.md", facts)
	for _, want := range append(append([]string(nil), facts.CooperativeCPUDefaults...), "cooperative parallelism defaults", "do not enforce a CPU cap") {
		if !strings.Contains(recipe, want) {
			t.Errorf("native resource recipe omits %q:\n%s", want, recipe)
		}
	}
	withoutCPUFacts := renderRecipe("workspace-config.md", FactSheet{HostWorkspace: facts.HostWorkspace, ConfigPath: facts.ConfigPath})
	if strings.Contains(withoutCPUFacts, "Native CPU resource guidance") {
		t.Error("renderer inferred CPU guidance without producer-supplied applied facts")
	}
}

func TestRenderBuiltinSkillsKeepsNativeFactPathsLiteral(t *testing.T) {
	workspace := "/Users/alice/workspace/project"
	config := workspace + "/project/yolo-jail.jsonc"
	localConfig := workspace + "/local-{{.WorkspacePath}}.jsonc"
	userConfig := "/Users/alice/.config/yolo-jail/{{.WorkspacePath}}.jsonc"
	home := "/Users/alice/home/agent/{{.WorkspacePath}}"
	log := workspace + "/.yolo/{{.LogPath}}.log"
	files, err := RenderBuiltinSkills(FactSheet{
		HostWorkspace:          workspace,
		WorkspacePath:          workspace,
		ConfigPath:             config,
		WorkspaceConfigPresent: true,
		LocalConfigPresent:     true,
		LocalConfigPath:        localConfig,
		UserConfigPath:         userConfig,
		LogPath:                log,
		Briefing: BriefingInput{
			Mechanism:  "macos-user",
			Home:       home,
			ConfigName: "yolo-jail.jsonc",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	all := joinRendered(files)
	for _, literal := range []string{workspace, config, localConfig, userConfig, log} {
		if !strings.Contains(all, literal) {
			t.Errorf("rendered skills do not preserve supplied path literally %q:\n%s", literal, all)
		}
	}
	for _, corrupted := range []string{
		"/Users/alice/Users/alice/workspace/project",
		"/Users/alice/home/agent/" + workspace,
		"/Users/alice/home/agent/" + log,
		workspace + "/local-" + workspace + ".jsonc",
		"/Users/alice/.config/yolo-jail/" + workspace + ".jsonc",
		workspace + "/.yolo/" + log + ".log",
	} {
		if strings.Contains(all, corrupted) {
			t.Errorf("renderer reprocessed literal fact %q", corrupted)
		}
	}

	containerFiles, err := RenderBuiltinSkills(FactSheet{
		WorkspacePath: workspace,
		ConfigPath:    config,
		Briefing:      BriefingInput{Mechanism: "podman", Home: home},
	})
	if err != nil {
		t.Fatal(err)
	}
	containerText := joinRendered(containerFiles)
	if !strings.Contains(containerText, home) || strings.Contains(containerText, "/Users/alice/home/agent/"+workspace) {
		t.Errorf("renderer did not preserve supplied home path literally %q:\n%s", home, containerText)
	}
}

func TestRenderBuiltinSkillsKeepsCrossPlaceholderFactsLiteral(t *testing.T) {
	workspace := "/Users/alice/home/agent/project"
	config := workspace + "/yolo-jail.jsonc"
	localConfig := workspace + "/# Configuring the Jail"
	log := workspace + "/.yolo/# Diagnose the Jail.log"
	files, err := RenderBuiltinSkills(FactSheet{
		HostWorkspace:          workspace,
		WorkspacePath:          workspace,
		ConfigPath:             config,
		WorkspaceConfigPresent: true,
		LocalConfigPresent:     true,
		LocalConfigPath:        localConfig,
		LogPath:                log,
		Briefing: BriefingInput{
			Mechanism: "macos-user",
			Home:      "/Users/shared/jail",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	all := joinRendered(files)
	for _, literal := range []string{workspace, config, localConfig, log} {
		if !strings.Contains(all, literal) {
			t.Errorf("rendered skills do not preserve supplied path literally %q:\n%s", literal, all)
		}
	}
	for _, corrupted := range []string{
		"/Users/alice/Users/shared/jail/project",
		workspace + "/# Configuring the environment",
		workspace + "/.yolo/# Diagnose this environment.log",
	} {
		if strings.Contains(all, corrupted) {
			t.Errorf("renderer reprocessed literal fact %q", corrupted)
		}
	}
}

func TestRendererRepairsAppliedFactGuidance(t *testing.T) {
	facts := FactSheet{
		HostWorkspace: "/Users/test/project", WorkspacePath: "/Users/test/project",
		ConfigPath: "/Users/test/project/local.yolo.json", UserConfigPath: "/Users/test/.config/yolo-jail/config.jsonc",
		WorkspaceConfigPresent: true, LocalConfigPath: "/Users/test/project/local.yolo.local.json", LocalConfigPresent: true,
		RestartCommand: "yolo", LogPath: "/Users/test/project/.yolo/startup.log",
		PackageDelivery: "native tools", SkillsDelivery: "live filesystem",
		Briefing: BriefingInput{Mechanism: "macos-user", IsMacOS: true, Home: "/Users/_yolojail", ConfigName: "local.yolo.json", ConfigLocked: true, AppliedNetMode: "shared", Loopholes: []Loophole{{Name: "audio"}}},
		Switches: []SwitchFact{{Name: "audio", Available: true}},
		Timing: []changetiming.Row{
			{Key: "mounts", Outcome: changetiming.NextSession, Reason: "takes effect in the next session"},
			{Key: "packages", Outcome: changetiming.RestartAndRebuild, Reason: "the baked image inputs change"},
			{Key: "packs", Outcome: changetiming.UserScopeOnly, Reason: "the workspace config cannot carry this setting"},
			{Key: "loopholes", Outcome: changetiming.HostSwitch, Reason: "host-side switch controls this change"},
			{Key: "host_files", Outcome: changetiming.AtNextLaunch, Reason: "the next launch reads the edit"},
		},
	}
	got, err := RenderBuiltinSkills(facts)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.ToLower(joinRendered(got))
	for _, forbidden := range []string{"/home/agent", "host.containers.internal", "localhost inside the jail is the jail's", "yolo stop", "edit `/users/test/project/local.yolo.json`"} {
		if strings.Contains(all, strings.ToLower(forbidden)) {
			t.Errorf("applied native facts still produce forbidden guidance %q", forbidden)
		}
	}
	for _, want := range []string{"local.yolo.json", "/users/test/project/local.yolo.json", "/users/test/project/local.yolo.local.json", "locked", "restart and rebuild", "user scope only", "end this session", "`audio` runs whenever its pack is selected", "yolo request <pack> <kind>", "yolo pack add <pack>", "at next launch"} {
		if !strings.Contains(all, strings.ToLower(want)) {
			t.Errorf("rendered guidance omits %q", want)
		}
	}
	// The retired verbs are named only as retired, never as a step to run.
	if strings.Contains(all, "run `yolo loopholes enable") || strings.Contains(all, "yolo loopholes enable audio") {
		t.Errorf("rendered guidance tells the agent to run the retired `yolo loopholes enable`")
	}
	configuring := string(got["configuring-the-jail/SKILL.md"])
	if strings.Contains(configuring, "Edit this one") || !strings.Contains(configuring, "/Users/test/project/local.yolo.json") || !strings.Contains(configuring, "/Users/test/project/local.yolo.local.json") || !strings.Contains(configuring, "Do not edit this file from the jail") {
		t.Errorf("configuring skill contradicts locked state or omits resolved file paths:\n%s", configuring)
	}
	workspace := string(got["jail-mechanics/recipes/workspace-config.md"])
	if strings.Contains(workspace, "1. Edit") || !strings.Contains(workspace, "Do not edit it from the jail") || !strings.Contains(workspace, "local.yolo.json") {
		t.Errorf("locked config recipe offers an edit or misses its actual name:\n%s", workspace)
	}
	user := string(got["jail-mechanics/recipes/user-config.md"])
	if strings.Contains(strings.ToLower(user), "please run `yolo config apply`") || !strings.Contains(user, "fresh `yolo` launch") {
		t.Errorf("user-scope recipe claims workspace apply activates packs or misses fresh-launch step:\n%s", user)
	}
	workspaceLines := strings.Count(workspace, "\n")
	index := string(got["jail-mechanics/SKILL.md"])
	if workspaceLines == 0 || strings.Contains(index, "recipes/restart.md") && facts.RestartCommand == "" {
		t.Errorf("empty or irrelevant recipes were rendered")
	}
}

func joinRendered(files map[string][]byte) string {
	var b strings.Builder
	for _, data := range files {
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestNativeAndHostNetworkAndACLTextFollowAppliedFacts(t *testing.T) {
	native := FactSheet{
		HostWorkspace: "/Users/test/project", WorkspacePath: "/Users/test/project", ConfigPath: "/Users/test/project/yolo-jail.json", WorkspaceConfigPresent: true,
		UserConfigPath: "/Users/test/.config/yolo-jail/config.jsonc", RestartCommand: "yolo",
		LogPath: "/Users/test/project/.yolo/startup.log", NativeAccountMode: "account", NativeAccountUser: "_yolojail", Briefing: BriefingInput{Mechanism: "macos-user", IsMacOS: true, Home: "/Users/_yolojail"},
	}
	got, err := RenderBuiltinSkills(native)
	if err != nil {
		t.Fatal(err)
	}
	text := joinRendered(got)
	for _, want := range []string{"yolo yolo-account share", "_yolojail", "yolo-jail.json", "/Users/test/project/.yolo/startup.log"} {
		if !strings.Contains(text, want) {
			t.Errorf("native render lacks applied account/config/log fact %q", want)
		}
	}
	for _, forbidden := range []string{"host.containers.internal", "/home/agent", "container backends", "there are two loopbacks"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("native render contains non-applicable container fact %q", forbidden)
		}
	}
	withoutAccount := native
	withoutAccount.Briefing.Home = ""
	withoutAccount.Briefing.IsMacOS = false
	withoutAccount.NativeAccountMode = "self"
	withoutAccount.NativeAccountUser = ""
	withoutAccountGot, err := RenderBuiltinSkills(withoutAccount)
	if err != nil {
		t.Fatal(err)
	}
	withoutAccountText := strings.ToLower(joinRendered(withoutAccountGot))
	if strings.Contains(joinRendered(withoutAccountGot), "yolo yolo-account share") {
		t.Error("ACL troubleshooting rendered without supplied separate-account facts")
	}
	if strings.Contains(withoutAccountText, "/home/agent") {
		t.Errorf("native render invented a container home without an applied home fact:\n%s", withoutAccountText)
	}

	hostNetwork := native
	hostNetwork.Briefing = BriefingInput{Mechanism: "podman", AppliedNetMode: "host"}
	hostNetwork.WorkspacePath = "/workspace"
	hostNetwork.ConfigPath = "/workspace/yolo-jail.jsonc"
	hostNetwork.RestartCommand = "yolo stop"
	hostNetworkGot, err := RenderBuiltinSkills(hostNetwork)
	if err != nil {
		t.Fatal(err)
	}
	hostText := strings.ToLower(joinRendered(hostNetworkGot))
	for _, forbidden := range []string{"host.containers.internal", "there are two loopbacks", "localhost inside the jail is the"} {
		if strings.Contains(hostText, forbidden) {
			t.Errorf("host-network render claims a separate jail network: %q", forbidden)
		}
	}
	if !strings.Contains(hostText, "shares the host network stack") || !strings.Contains(hostText, "there is no separate jail loopback") {
		t.Error("applied host networking was not described as shared")
	}
}

func TestRendererUsesRestartFactAndReportsSizeTargets(t *testing.T) {
	facts := FactSheet{HostWorkspace: "/home/test/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", WorkspaceConfigPresent: true, UserConfigPath: "/home/test/.config/yolo-jail/config.jsonc", RestartCommand: "yolo stop", Briefing: BriefingInput{Mechanism: "podman"}, Timing: []changetiming.Row{{Key: "mounts", Outcome: changetiming.AtNextRestart, Reason: "mounts are fixed at launch"}}}
	got, err := RenderBuiltinSkills(facts)
	if err != nil {
		t.Fatal(err)
	}
	index := string(got["jail-mechanics/SKILL.md"])
	restart := string(got["jail-mechanics/recipes/workspace-config.md"])
	for _, rendered := range []string{index, restart} {
		if !strings.Contains(rendered, "yolo stop") {
			t.Fatalf("restart differs from supplied stopRemedy fact: %s", rendered)
		}
	}
	for name, content := range got {
		if !strings.HasPrefix(name, "jail-mechanics/") {
			continue
		}
		lines := strings.Count(string(content), "\n")
		t.Logf("size target (informational): %s = %d lines, %d bytes", name, lines, len(content))
	}
}

func TestRenderBuiltinSkillsUsesOnlySuppliedFacts(t *testing.T) {
	facts := FactSheet{
		HostWorkspace: "/home/u/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", WorkspaceConfigPresent: true,
		UserConfigPath: "/home/u/.config/yolo-jail/config.jsonc", RestartCommand: "yolo stop",
		LogPath: "/home/u/project/.yolo/startup.log", PackageDelivery: "baked image", SkillsDelivery: "live bind",
		Paths:    []PathFact{{Path: "/workspace", HostSource: "/home/u/project", Mode: "read-write", Lifetime: "kept", Delivery: "live bind"}},
		Packs:    []string{"claude"},
		Timing:   []changetiming.Row{{Key: "mounts", Outcome: changetiming.AppliedNow, Reason: "live mount delivery was proved"}, {Key: "brokered", Outcome: changetiming.AppliedNow, Reason: "live broker scope was proved"}},
		Briefing: BriefingInput{Loopholes: []Loophole{{Name: "audio", Desc: "audio"}}},
	}
	got, err := RenderBuiltinSkills(facts)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"configuring-the-jail/SKILL.md", "diagnosing-the-jail/SKILL.md", "jail-mechanics/SKILL.md", "jail-mechanics/host-map.md", "jail-mechanics/recipes/workspace-config.md", "jail-mechanics/recipes/packs-and-loopholes.md", "jail-mechanics/recipes/restart.md", "jail-mechanics/recipes/yolo-update.md"} {
		if len(got[path]) == 0 {
			t.Errorf("render omitted %q", path)
		}
	}
	if !strings.Contains(string(got["jail-mechanics/host-map.md"]), "| `/workspace` | `/home/u/project` |") {
		t.Errorf("host map did not use supplied path facts:\n%s", got["jail-mechanics/host-map.md"])
	}
	config := string(got["jail-mechanics/recipes/workspace-config.md"])
	for _, want := range []string{"mounts", "applied now", "live mount delivery was proved", changetiming.HostCommand("yolo config apply", facts.HostWorkspace, false)} {
		if !strings.Contains(config, want) {
			t.Errorf("workspace recipe lacks %q:\n%s", want, config)
		}
	}
	loopholes := string(got["jail-mechanics/recipes/packs-and-loopholes.md"])
	if !strings.Contains(loopholes, "brokered.<source>.repos") || strings.Contains(loopholes, "yolo loopholes enable") {
		t.Errorf("packs/loopholes recipe invented a host switch command:\n%s", loopholes)
	}

	// Empty path facts stay empty: rendering does not infer a path from the backend.
	empty, err := RenderBuiltinSkills(FactSheet{Host: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty["jail-mechanics/host-map.md"]), "| `/") {
		t.Errorf("renderer invented host paths without path facts:\n%s", empty["jail-mechanics/host-map.md"])
	}
	if strings.Contains(string(empty["jail-mechanics/SKILL.md"]), "yolo stop") || strings.Contains(string(empty["jail-mechanics/SKILL.md"]), "packs-and-loopholes") {
		t.Errorf("host rendering included jail-only recipes:\n%s", empty["jail-mechanics/SKILL.md"])
	}
}

func TestRendererKeepsOriginalAndBackingSourcesSeparateAndOmitsUnavailableSwitches(t *testing.T) {
	facts := FactSheet{
		HostWorkspace: "/host/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", WorkspaceConfigPresent: true,
		UserConfigPath: "/host/config.jsonc", RestartCommand: "yolo stop", PackageDelivery: "baked image",
		Paths:    []PathFact{{Path: "/workspace/.config/tool", OriginalSource: "/host/original", BackingSource: "/host/staged-copy", Mode: "read-only", Lifetime: "per launch", Delivery: "copy at launch"}},
		Timing:   []changetiming.Row{{Key: "loopholes", Outcome: changetiming.HostSwitch}},
		Switches: []SwitchFact{{Name: "brokered", Available: false}},
		Briefing: BriefingInput{Mechanism: "podman"},
	}
	files, err := RenderBuiltinSkills(facts)
	if err != nil {
		t.Fatal(err)
	}
	pathMap := string(files["jail-mechanics/host-map.md"])
	if !strings.Contains(pathMap, "`/host/original` | `/host/staged-copy`") {
		t.Fatalf("original and backing sources collapsed:\n%s", pathMap)
	}
	recipe := string(files["jail-mechanics/recipes/workspace-config.md"])
	if strings.Contains(recipe, "yolo loopholes enable brokered") || strings.Contains(recipe, "yolo loopholes disable brokered") ||
		strings.Contains(recipe, "`brokered` runs") {
		t.Fatalf("renderer named an unavailable switch:\n%s", recipe)
	}
}

func TestPrepareSkillsWithFactSheetStagesAllBuiltinsAndOverridesCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pack := filepath.Join(home, "pack-skills")
	must(t, os.MkdirAll(filepath.Join(pack, "jail-mechanics"), 0o755))
	must(t, os.WriteFile(filepath.Join(pack, "jail-mechanics", "SKILL.md"), []byte("pack version"), 0o644))
	withPackSkillDirs(t, pack)
	withSkillTargets(t, ".claude/skills")
	facts := &FactSheet{HostWorkspace: "/home/u/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", RestartCommand: "yolo stop"}
	staging, report, err := PrepareSkillsWithFactSheet("facts-stage", nil, facts)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("missing staging report")
	}
	staged := filepath.Join(staging, SkillStagingName("claude"))
	for _, name := range []string{"configuring-the-jail", "diagnosing-the-jail", "jail-mechanics"} {
		if _, err := os.Stat(filepath.Join(staged, name, "SKILL.md")); err != nil {
			t.Errorf("rendered built-in %q missing from staging: %v", name, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(staged, "jail-mechanics", "SKILL.md"))
	if err != nil || !strings.Contains(string(got), "This jail") || strings.Contains(string(got), "pack version") {
		t.Errorf("rendered built-in did not override pack collision: content=%q err=%v", got, err)
	}
	foundCollision := false
	for _, notice := range report.PackNotices {
		if notice.Name == "jail-mechanics" && strings.Contains(notice.Detail, "overrides") {
			foundCollision = true
		}
	}
	if !foundCollision {
		t.Errorf("rendered built-in collision was not disclosed: %+v", report.PackNotices)
	}
}

func TestRefreshMechanicsFilesRestoresBothFilesWhenSecondWriteFails(t *testing.T) {
	target := t.TempDir()
	mechanics := filepath.Join(target, "jail-mechanics")
	if err := os.Mkdir(mechanics, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(mechanics)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mechanics, "SKILL.md"), []byte("old index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(mechanics, "host-map.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RefreshMechanicsFiles([]string{target}, FactSheet{Host: true}); err == nil {
		t.Fatal("expected second file write failure")
	}
	got, err := os.ReadFile(filepath.Join(mechanics, "SKILL.md"))
	if err != nil || string(got) != "old index" {
		t.Fatalf("first file was not restored: %q, %v", got, err)
	}
	after, err := os.Stat(mechanics)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("refresh replaced the target directory inode")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 || entries[0].Name() != "jail-mechanics" {
		t.Fatalf("refresh touched unrelated target: %v, %v", entries, err)
	}
}

func TestRenderMechanicsIndexAndMapReturnsOnlyTwoFiles(t *testing.T) {
	files, err := RenderMechanicsIndexAndMap(FactSheet{Host: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || len(files["jail-mechanics/SKILL.md"]) == 0 || len(files["jail-mechanics/host-map.md"]) == 0 {
		t.Fatalf("unexpected mechanics file set: %v", files)
	}
}

func TestPrepareSkillsRenderFailurePreservesPreviousSuite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	withSkillTargets(t, ".claude/skills")
	staging, _, err := PrepareSkillsWithFactSheet("render-failure", nil, &FactSheet{WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc"})
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, SkillStagingName("claude"))
	before := map[string][]byte{}
	for _, name := range []string{"configuring-the-jail", "diagnosing-the-jail", "jail-mechanics"} {
		content, err := os.ReadFile(filepath.Join(staged, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = content
	}
	renderBuiltinSkillFiles = func(FactSheet) (map[string][]byte, error) { return nil, errors.New("render failure") }
	t.Cleanup(func() { renderBuiltinSkillFiles = RenderBuiltinSkills })
	if _, _, err := PrepareSkillsWithFactSheet("render-failure", nil, &FactSheet{WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc"}); err == nil {
		t.Fatal("expected injected render failure")
	}
	for name, content := range before {
		after, err := os.ReadFile(filepath.Join(staged, name, "SKILL.md"))
		if err != nil || string(after) != string(content) {
			t.Errorf("previous %s skill was not preserved: err=%v", name, err)
		}
	}
}

func TestRenderBuiltinSkillsGoldenFixtures(t *testing.T) {
	fixtures := map[string]FactSheet{
		"rootless-container": {HostWorkspace: "/home/test/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", WorkspaceConfigPresent: true, UserConfigPath: "/home/test/.config/yolo-jail/config.jsonc", RestartCommand: "yolo stop", LogPath: "/home/test/project/.yolo/startup.log", PackageDelivery: "baked image", SkillsDelivery: "live read-only bind", Packs: []string{"claude"}, Briefing: BriefingInput{Mechanism: "podman", Loopholes: []Loophole{{Name: "audio"}}}, Paths: []PathFact{{Path: "/workspace", HostSource: "/home/test/project", Mode: "read-write", Lifetime: "kept", Delivery: "live bind"}}, Timing: []changetiming.Row{{Key: "mounts", Outcome: changetiming.AppliedNow, Reason: "live bind is applied now"}}},
		"copy-container":     {HostWorkspace: "/home/test/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc", WorkspaceConfigPresent: true, UserConfigPath: "/home/test/.config/yolo-jail/config.jsonc", RestartCommand: "yolo stop", LogPath: "/home/test/project/.yolo/startup.log", PackageDelivery: "baked image", SkillsDelivery: "read-only copy at next launch", Packs: []string{"claude"}, Briefing: BriefingInput{Mechanism: "container"}, Paths: []PathFact{{Path: "/workspace/ctx", HostSource: "/home/test/shared", Mode: "read-only", Lifetime: "kept", Delivery: "copy at launch"}}, Timing: []changetiming.Row{{Key: "mounts", Outcome: changetiming.AtNextRestart, Reason: "copy delivery is fixed at launch"}}},
		"native-session":     {HostWorkspace: "/Users/test/project", WorkspacePath: "/Users/test/project", ConfigPath: "/Users/test/project/yolo-jail.jsonc", WorkspaceConfigPresent: true, UserConfigPath: "/Users/test/.config/yolo-jail/config.jsonc", RestartCommand: "yolo", LogPath: "/Users/test/project/.yolo/startup.log", PackageDelivery: "native tools", SkillsDelivery: "live filesystem", Briefing: BriefingInput{Mechanism: "macos-user"}, Paths: []PathFact{{Path: "/Users/test/project", HostSource: "/Users/test/project", Mode: "read-write", Lifetime: "kept", Delivery: "native"}}, Timing: []changetiming.Row{{Key: "mounts", Outcome: changetiming.NextSession, Reason: "takes effect in the next session"}}},
		"host":               {Host: true, HostWorkspace: "/home/test/project", UserConfigPath: "/home/test/.config/yolo-jail/config.jsonc", LogPath: "/home/test/.local/share/yolo-jail/host-apply.log", PackageDelivery: "host package manager", Paths: []PathFact{{Path: "/home/test/project", HostSource: "/home/test/project", Mode: "read-write", Lifetime: "kept", Delivery: "native"}}},
	}
	for name, facts := range fixtures {
		t.Run(name, func(t *testing.T) {
			got, err := RenderBuiltinSkills(facts)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Host {
				hostConfigure := string(got["configuring-the-jail/SKILL.md"])
				hostDiagnose := string(got["diagnosing-the-jail/SKILL.md"])
				if strings.Contains(hostConfigure, "Use `yolo config-ref` for accepted settings. Edit") || !strings.Contains(hostConfigure, facts.UserConfigPath) {
					t.Errorf("host configuring skill is a placeholder or misses the actual config path:\n%s", hostConfigure)
				}
				if !strings.Contains(hostDiagnose, facts.LogPath) || !strings.Contains(hostDiagnose, "Inspect the host apply") {
					t.Errorf("host diagnosis does not use its own log path and apply triage:\n%s", hostDiagnose)
				}
			}
			var rendered strings.Builder
			var names []string
			for path := range got {
				names = append(names, path)
			}
			sort.Strings(names)
			for _, path := range names {
				fmt.Fprintf(&rendered, "===== %s =====\n%s\n", path, got[path])
			}
			renderedText := strings.TrimRight(rendered.String(), "\n") + "\n"
			golden := filepath.Join("testdata", "mechanics-"+name+".golden")
			if os.Getenv("UPDATE_MECHANICS_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(renderedText), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != renderedText {
				t.Errorf("rendering differs from %s", golden)
			}
			flat := strings.ToLower(rendered.String())
			for _, forbidden := range []string{"if configured", "depending on", "on this backend only", "otherwise"} {
				if strings.Contains(flat, forbidden) {
					t.Errorf("output contains conditional prose %q", forbidden)
				}
			}
			if facts.Briefing.Mechanism == "macos-user" {
				for _, forbidden := range []string{"`/workspace`", "`/home/agent`", "host.containers.internal", "image", "yolo stop"} {
					if strings.Contains(flat, forbidden) {
						t.Errorf("native golden contains non-applied fact %q", forbidden)
					}
				}
			}
			if facts.Host {
				for _, forbidden := range []string{"yolo stop", "loophole", "restart the jail", "start the jail"} {
					if strings.Contains(flat, forbidden) {
						t.Errorf("host golden contains jail-only guidance %q", forbidden)
					}
				}
			}
			if facts.Briefing.Mechanism == "macos-user" && strings.Contains(flat, "yolo stop") {
				t.Error("native session names yolo stop")
			}
			for term, active := range map[string]string{"podman": "podman", "apple container": "container", "macos-user": "macos-user", "seatbelt": "macos-user"} {
				if facts.Briefing.Mechanism != active && strings.Contains(flat, term) {
					t.Errorf("output contains other backend %q", term)
				}
			}
		})
	}
}
