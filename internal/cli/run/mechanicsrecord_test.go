package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestMechanicsSnapshotRequiresRunningPodmanTokenAndPreservesPackContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname, token = "yolo-mechanics-test", "launch-token"
	facts := &jailcontent.FactSheet{
		Briefing: jailcontent.BriefingInput{Mechanism: "podman", ContextMounts: []jailcontent.ContextMount{
			{Path: "/ctx/pack", Host: "/pack/data", Pack: "pack-a"},
			{Path: "/ctx/old", Host: "/home/user/old"},
		}},
		Timing: []changetiming.Row{{Key: "network", Backend: changetiming.Podman, Outcome: changetiming.AtNextRestart}},
		Paths: []jailcontent.PathFact{
			{Path: "/workspace", HostSource: "/old/project"},
			{Path: "/ctx/pack", HostSource: "/pack/data", Origin: "pack-a"},
			{Path: "/ctx/old", HostSource: "/home/user/old"},
		},
	}
	if err := writeMechanicsApplied(cname, token, facts); err != nil {
		t.Fatal(err)
	}
	if _, ok := readMechanicsApplied(cname, []string{ctxMountsTokenEnv + "=other-token"}); ok {
		t.Fatal("snapshot answered for a different running podman token")
	}
	loaded, ok := readMechanicsApplied(cname, []string{ctxMountsTokenEnv + "=" + token})
	if !ok || loaded.Briefing.Mechanism != "podman" {
		t.Fatalf("matching running token did not load the applied snapshot: %+v, %v", loaded, ok)
	}

	if err := writeCtxMountRecord(cname, token, []ctxMountEntry{
		{Source: "/home/user/data", Dest: "/ctx/data"},
		{Source: "/home/user/repo", Dest: "/ctx/repo", RW: true},
	}); err != nil {
		t.Fatal(err)
	}
	if !applyLiveMountsToMechanics(loaded, cname, []string{ctxMountsTokenEnv + "=" + token}) {
		t.Fatal("valid live mount record was not applied")
	}
	ctxPaths := map[string]bool{}
	for _, path := range loaded.Paths {
		if len(path.Path) >= 5 && path.Path[:5] == "/ctx/" {
			ctxPaths[path.Path] = true
		}
	}
	if !ctxPaths["/ctx/pack"] || ctxPaths["/ctx/data"] == false || ctxPaths["/ctx/repo"] == false || ctxPaths["/ctx/old"] {
		t.Fatalf("pack context row not preserved or user rows not reconciled: %+v", loaded.Paths)
	}
	if loaded.Timing[0].Outcome != changetiming.AtNextRestart {
		t.Fatalf("a live apply changed the nominal timing class: %+v", loaded.Timing[0])
	}
}

func TestRunApplyStepsRefreshesOnlyMechanicsIndexAndMapForSuccessfulMount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname, token = "yolo-mechanics-event", "event-token"
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "jail-mechanics"), 0o755); err != nil {
		t.Fatal(err)
	}
	facts := jailcontent.FactSheet{
		Briefing:      jailcontent.BriefingInput{Mechanism: "podman", ContextMounts: []jailcontent.ContextMount{{Path: "/ctx/old", Host: "/host/old"}}},
		HostWorkspace: "/host/project", WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc",
		RestartCommand: "yolo stop", SkillsDelivery: "workspace bind through /workspace",
		Timing: []changetiming.Row{{Key: "mounts", Backend: changetiming.Podman, Outcome: changetiming.AppliedNow}},
		Paths: []jailcontent.PathFact{
			{Path: "/ctx/old", HostSource: "/host/old"},
			{Path: "/home/agent/.claude/skills", HostSource: target, Origin: "pack skills staging"},
		},
	}
	if err := jailcontent.RefreshMechanicsFiles([]string{target}, facts); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(target, "jail-mechanics", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "configuring-the-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(filepath.Join(target, "jail-mechanics", "recipes", "mounts.md"), "recipe stays")
	mustWrite(filepath.Join(target, "configuring-the-jail", "SKILL.md"), "configuring stays")
	beforeIndex, _ := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	beforeMap, _ := os.Stat(filepath.Join(target, "jail-mechanics", "host-map.md"))
	beforeRecipe, _ := os.ReadFile(filepath.Join(target, "jail-mechanics", "recipes", "mounts.md"))
	beforeConfiguring, _ := os.ReadFile(filepath.Join(target, "configuring-the-jail", "SKILL.md"))

	if err := writeMechanicsApplied(cname, token, &facts); err != nil {
		t.Fatal(err)
	}
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}
	env := []string{ctxMountsTokenEnv + "=" + token}
	o := &Options{Stderr: &bytes.Buffer{}}
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error { return nil })
	run := &applyRun{cname: cname, rt: "podman", envLines: env, mode: applyMode{running: true}, have: nil}
	result := o.runApplySteps(run, []applyChange{{kind: changeMountAdd, mountAdd: configCtxMount{source: "/host/new", dest: "/ctx/new"}, outcome: outcomeAppliedNow}})
	afterIndexInfo, _ := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	afterMapInfo, _ := os.Stat(filepath.Join(target, "jail-mechanics", "host-map.md"))
	afterMap, _ := os.ReadFile(filepath.Join(target, "jail-mechanics", "host-map.md"))
	if len(result) != 1 || result[0].outcome != outcomeAppliedNow {
		t.Fatalf("mount step did not complete: %+v", result)
	}
	if os.SameFile(beforeIndex, afterIndexInfo) || os.SameFile(beforeMap, afterMapInfo) {
		t.Fatal("effective mount apply did not replace both mechanics outputs")
	}
	if !bytes.Contains(afterMap, []byte("/ctx/new")) || bytes.Contains(afterMap, []byte("/ctx/old")) {
		t.Fatalf("host map does not describe the authenticated live mount set: %s", afterMap)
	}
	for path, before := range map[string][]byte{
		filepath.Join(target, "jail-mechanics", "recipes", "mounts.md"): beforeRecipe,
		filepath.Join(target, "configuring-the-jail", "SKILL.md"):       beforeConfiguring,
	} {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("non-event guidance changed at %s: err=%v", path, err)
		}
	}
	updated, ok := readMechanicsApplied(cname, env)
	if !ok || updated.Timing[0].Outcome != changetiming.AppliedNow {
		t.Fatalf("applied refresh lost the nominal timing class: %+v, %v", updated, ok)
	}

	// Repository-scope apply is another real non-mount operation. Its approval changes
	// authorization, not the path map, so both mechanics outputs are regenerated from the
	// authenticated applied snapshot while the map bytes remain unchanged.
	beforeScopeIndex, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeScopeMap, err := os.ReadFile(filepath.Join(target, "jail-mechanics", "host-map.md"))
	if err != nil {
		t.Fatal(err)
	}
	o.approvedScopes = map[string][]config.ScopeRepo{"gb": {{Repo: "new/repo"}}}
	swapSeam(t, &replaceScopeReposSeam, func(_ context.Context, _ string, _ brokerscope.File, _ []string, _ []brokerscope.Grant) error {
		return nil
	})
	scope := applyChange{kind: changeScope, key: "brokered", outcome: outcomeAppliedNow,
		scope: scopeDelta{label: "gb", files: []liveScopeFile{{File: brokerscope.File{Form: "gitlab"}}}}}
	result = o.runApplySteps(run, []applyChange{scope})
	afterScopeIndex, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil || os.SameFile(beforeScopeIndex, afterScopeIndex) {
		t.Fatalf("successful live scope apply did not regenerate mechanics index: stat err=%v", err)
	}
	afterScopeMap, err := os.ReadFile(filepath.Join(target, "jail-mechanics", "host-map.md"))
	if err != nil || !bytes.Equal(beforeScopeMap, afterScopeMap) {
		t.Fatalf("scope authorization altered the path map: err=%v map=%s", err, afterScopeMap)
	}
	if len(result) != 1 || result[0].outcome != outcomeAppliedNow {
		t.Fatalf("live scope operation did not complete: %+v", result)
	}

	// A successful next-session key is effective for the next entry even though its requested
	// config differs from this jail's previous applied baseline.
	o.Workspace = t.TempDir()
	if _, err := config.WriteWorkspaceBootBaseline(o.Workspace, "podman-self", mapOf(t, `{"agents_md_extra":"prior"}`)); err != nil {
		t.Fatal(err)
	}
	run.current = mapOf(t, `{"agents_md_extra":"approved"}`)
	beforeNextSessionIndex, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	result = o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})
	if len(result) != 1 || result[0].outcome != outcomeNextSession {
		t.Fatalf("next-session apply did not complete: %+v", result)
	}
	afterNextSessionIndex, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil || os.SameFile(beforeNextSessionIndex, afterNextSessionIndex) {
		t.Fatalf("successful next-session config apply did not regenerate mechanics index: stat err=%v", err)
	}
	folded, ok, err := config.ReadWorkspaceBootBaseline(o.Workspace, "podman-self")
	if err != nil || !ok {
		t.Fatalf("read effective-config baseline: ok=%t err=%v", ok, err)
	}
	if value, exists := folded.Get("agents_md_extra"); !exists || value != "approved" {
		t.Fatalf("approved next-session key was not folded: value=%#v exists=%t", value, exists)
	}

	// A failed render must keep both the old tree and its matching applied snapshot.
	priorSnapshot, err := os.ReadFile(mechanicsRecordPath(cname))
	if err != nil {
		t.Fatal(err)
	}
	beforeFailedRender, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	swapSeam(t, &refreshMechanicsFiles, func([]string, jailcontent.FactSheet) error { return errors.New("injected render failure") })
	o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})
	afterFailedRender, err := os.Stat(filepath.Join(target, "jail-mechanics", "SKILL.md"))
	if err != nil || !os.SameFile(beforeFailedRender, afterFailedRender) {
		t.Fatalf("failed renderer replaced the prior skill index: stat err=%v", err)
	}
	afterSnapshot, err := os.ReadFile(mechanicsRecordPath(cname))
	if err != nil || !bytes.Equal(priorSnapshot, afterSnapshot) {
		t.Fatalf("failed renderer left a newer mechanics snapshot than staged guidance: err=%v", err)
	}
}

func TestRunApplyStepsFailedAgentsMDExtraFoldPreservesMechanics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname, token = "yolo-mechanics-failed-fold", "failed-fold-token"
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "jail-mechanics"), 0o755); err != nil {
		t.Fatal(err)
	}
	facts := jailcontent.FactSheet{
		Briefing:      jailcontent.BriefingInput{Mechanism: "podman"},
		HostWorkspace: t.TempDir(), WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc",
		RestartCommand: "yolo stop", SkillsDelivery: "live read-only bind",
		Paths: []jailcontent.PathFact{{Path: "/home/agent/.claude/skills", HostSource: target, Origin: "pack skills staging"}},
	}
	if err := jailcontent.RefreshMechanicsFiles([]string{target}, facts); err != nil {
		t.Fatal(err)
	}
	if err := writeMechanicsApplied(cname, token, &facts); err != nil {
		t.Fatal(err)
	}
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}
	mechanicsDir := filepath.Join(target, "jail-mechanics")
	indexPath := filepath.Join(mechanicsDir, "SKILL.md")
	mapPath := filepath.Join(mechanicsDir, "host-map.md")
	beforeIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMap, err := os.Stat(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeIndexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMapBytes, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, err := os.ReadFile(mechanicsRecordPath(cname))
	if err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir() // Deliberately has no boot baseline: the producer fold must fail.
	o := &Options{Workspace: workspace, Stderr: &bytes.Buffer{}}
	run := &applyRun{cname: cname, rt: "podman", envLines: []string{ctxMountsTokenEnv + "=" + token},
		mode: applyMode{running: true}, current: mapOf(t, `{"agents_md_extra":"approved"}`)}
	result := o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})
	if len(result) != 1 || result[0].outcome != outcomeNextSession {
		t.Fatalf("nominal next-session result changed: %+v", result)
	}

	afterIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	afterMap, err := os.Stat(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	afterIndexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	afterMapBytes, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	afterSnapshot, err := os.ReadFile(mechanicsRecordPath(cname))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeIndex, afterIndex) || !beforeIndex.ModTime().Equal(afterIndex.ModTime()) || !bytes.Equal(beforeIndexBytes, afterIndexBytes) {
		t.Errorf("failed agents_md_extra fold replaced or changed mechanics index")
	}
	if !os.SameFile(beforeMap, afterMap) || !beforeMap.ModTime().Equal(afterMap.ModTime()) || !bytes.Equal(beforeMapBytes, afterMapBytes) {
		t.Errorf("failed agents_md_extra fold replaced or changed mechanics host map")
	}
	if !bytes.Equal(beforeSnapshot, afterSnapshot) {
		t.Errorf("failed agents_md_extra fold changed the authenticated applied snapshot")
	}
}

func TestRunApplyStepsRefreshesIdenticalMechanicsAndSuccessfulMountDespiteKeyFoldFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname, token = "yolo-mechanics-fold-controls", "fold-controls-token"
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "jail-mechanics"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	facts := jailcontent.FactSheet{
		Briefing:      jailcontent.BriefingInput{Mechanism: "podman"},
		HostWorkspace: workspace, WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc",
		RestartCommand: "yolo stop", SkillsDelivery: "live read-only bind",
		Paths: []jailcontent.PathFact{{Path: "/home/agent/.claude/skills", HostSource: target, Origin: "pack skills staging"}},
	}
	if err := jailcontent.RefreshMechanicsFiles([]string{target}, facts); err != nil {
		t.Fatal(err)
	}
	if err := writeMechanicsApplied(cname, token, &facts); err != nil {
		t.Fatal(err)
	}
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}
	mechanicsDir := filepath.Join(target, "jail-mechanics")
	indexPath := filepath.Join(mechanicsDir, "SKILL.md")
	mapPath := filepath.Join(mechanicsDir, "host-map.md")
	run := &applyRun{cname: cname, rt: "podman", envLines: []string{ctxMountsTokenEnv + "=" + token},
		mode: applyMode{running: true}, current: mapOf(t, `{"agents_md_extra":"approved"}`)}
	o := &Options{Workspace: workspace, Stderr: &bytes.Buffer{}}

	// A successful next-session fold regenerates both mechanics files even though this key
	// does not alter their rendered bytes.
	if _, err := config.WriteWorkspaceBootBaseline(workspace, "podman-self", mapOf(t, `{"agents_md_extra":"prior"}`)); err != nil {
		t.Fatal(err)
	}
	beforeIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMap, err := os.Stat(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeIndexBytes, _ := os.ReadFile(indexPath)
	beforeMapBytes, _ := os.ReadFile(mapPath)
	o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})
	afterIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	afterMap, err := os.Stat(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	afterIndexBytes, _ := os.ReadFile(indexPath)
	afterMapBytes, _ := os.ReadFile(mapPath)
	if os.SameFile(beforeIndex, afterIndex) || os.SameFile(beforeMap, afterMap) {
		t.Fatal("successful identical-byte fold did not regenerate both mechanics files")
	}
	if !bytes.Equal(beforeIndexBytes, afterIndexBytes) || !bytes.Equal(beforeMapBytes, afterMapBytes) {
		t.Fatalf("identical-byte fold unexpectedly changed mechanics output")
	}

	// The other key cannot fold after the baseline is removed, but the successful mount
	// operation still refreshes from its updated authenticated live-mount record.
	if err := os.Remove(config.WorkspaceConfigBootPath(workspace, "podman-self")); err != nil {
		t.Fatal(err)
	}
	swapSeam(t, &injectCtxMountSeam, func(*Options, string, string, configCtxMount, time.Time) error { return nil })
	beforeMixedIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMixedMap, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	result := o.runApplySteps(run, []applyChange{
		{kind: changeMountAdd, key: "mounts", mountAdd: configCtxMount{source: "/host/new", dest: "/ctx/new"}, outcome: outcomeAppliedNow},
		{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession},
	})
	if len(result) != 2 || result[0].outcome != outcomeAppliedNow || result[1].outcome != outcomeNextSession {
		t.Fatalf("mixed mount/key results changed: %+v", result)
	}
	afterMixedIndex, err := os.Stat(indexPath)
	if err != nil || os.SameFile(beforeMixedIndex, afterMixedIndex) {
		t.Fatalf("successful mount did not refresh after unrelated fold failure: stat err=%v", err)
	}
	afterMixedMap, err := os.ReadFile(mapPath)
	if err != nil || bytes.Equal(beforeMixedMap, afterMixedMap) || !bytes.Contains(afterMixedMap, []byte("/ctx/new")) {
		t.Fatalf("mixed successful mount was not reflected in mechanics host map: err=%v map=%s", err, afterMixedMap)
	}

	// A successful live-scope operation also regenerates the index when its baseline fold,
	// and an unrelated key fold, both fail.
	o.approvedScopes = map[string][]config.ScopeRepo{"github-broker": {{Repo: "org/new"}}}
	swapSeam(t, &replaceScopeReposSeam, func(context.Context, string, brokerscope.File, []string, []brokerscope.Grant) error {
		return nil
	})
	beforeScopeIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeScopeMap, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	scopeChange := applyChange{kind: changeScope, key: "brokered", outcome: outcomeAppliedNow,
		scope: scopeDelta{label: "github-broker", files: []liveScopeFile{{Path: "/host/scope", File: brokerscope.File{Form: "github"}}}}}
	result = o.runApplySteps(run, []applyChange{
		scopeChange,
		{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession},
	})
	if len(result) != 2 || result[0].outcome != outcomeAppliedNow || result[1].outcome != outcomeNextSession {
		t.Fatalf("mixed scope/key results changed: %+v", result)
	}
	afterScopeIndex, err := os.Stat(indexPath)
	if err != nil || os.SameFile(beforeScopeIndex, afterScopeIndex) {
		t.Fatalf("successful scope did not refresh after unrelated fold failure: stat err=%v", err)
	}
	afterScopeMap, err := os.ReadFile(mapPath)
	if err != nil || !bytes.Equal(beforeScopeMap, afterScopeMap) {
		t.Fatalf("scope refresh changed unrelated mount facts: err=%v map=%s", err, afterScopeMap)
	}
}

func TestAuthenticatedAttachRefreshPreservesCompleteMechanicsSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const cname, token = "yolo-mechanics-complete-attach", "complete-attach-token"
	o := goldenOptions(workspace, home)
	packs := claudePackFixture(t)
	staged := stagedPacks{packs: packs}
	cfg := appliedTestConfig()
	o.durable = &jailcontent.DurableDir{Path: "/workspace/.yolo/durable"}
	facts := o.mechanicsFactSheet(cname, cfg, "podman", staged, storePackagesPlan{}, nil)
	freshStaging, err := o.refreshJailBriefings(cname, cfg, "podman", staged, ioprio.Normal, facts)
	if err != nil {
		t.Fatalf("fresh production refresh: %v", err)
	}
	beforeMap, err := os.ReadFile(filepath.Join(freshStaging, jailcontent.SkillStagingName("claude"), "jail-mechanics", "host-map.md"))
	if err != nil {
		t.Fatal(err)
	}
	beforePaths := append([]jailcontent.PathFact(nil), facts.Paths...)
	beforeProjectDirs := append([]string(nil), facts.WorkspaceProjectDirs...)
	if err := writeMechanicsApplied(cname, token, facts); err != nil {
		t.Fatal(err)
	}
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}
	applied, ok := readMechanicsApplied(cname, []string{ctxMountsTokenEnv + "=" + token})
	if !ok || applied.Briefing.Mechanism == "" {
		t.Fatal("fresh production snapshot was not authenticated as applied facts")
	}
	attachStaging, err := o.refreshJailBriefings(cname, cfg, "podman", staged, ioprio.Normal, applied)
	if err != nil {
		t.Fatalf("authenticated attach refresh: %v", err)
	}
	afterMap, err := os.ReadFile(filepath.Join(attachStaging, jailcontent.SkillStagingName("claude"), "jail-mechanics", "host-map.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeMap, afterMap) {
		t.Errorf("authenticated attach changed complete map bytes:\n--- before ---\n%s\n--- after ---\n%s", beforeMap, afterMap)
	}
	if !reflect.DeepEqual(beforePaths, applied.Paths) {
		t.Errorf("authenticated attach augmented applied paths: before=%+v after=%+v", beforePaths, applied.Paths)
	}
	if !reflect.DeepEqual(beforeProjectDirs, applied.WorkspaceProjectDirs) {
		t.Errorf("authenticated attach augmented project dirs: before=%+v after=%+v", beforeProjectDirs, applied.WorkspaceProjectDirs)
	}
}

func TestSuccessfulNextSessionFoldFeedsTheNextAttachBriefingAndWorkspaceSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const cname, token = "yolo-mechanics-next-entry", "next-entry-token"
	o := goldenOptions(workspace, home)
	packs := claudePackFixture(t)
	staged := stagedPacks{packs: packs}
	oldCfg := appliedTestConfig("agents_md_extra", "old approved guidance")
	newCfg := appliedTestConfig("agents_md_extra", "new approved guidance")
	if err := os.MkdirAll(filepath.Join(workspace, ".agents", "skills", "project-note"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".agents", "skills", "project-note", "SKILL.md"), []byte("project skill refreshed on entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(paths.AgentsDir(), cname, jailcontent.SkillStagingName("claude"))
	facts := &jailcontent.FactSheet{
		Briefing:      jailcontent.BriefingInput{Mechanism: "podman"},
		HostWorkspace: workspace, WorkspacePath: "/workspace", ConfigPath: "/workspace/yolo-jail.jsonc",
		RestartCommand: "yolo stop", SkillsDelivery: "live read-only bind",
		AppliedAgentsMDExtra:   "old approved guidance",
		AppliedWorkspaceSkills: o.workspaceSkillsFor(oldCfg, "podman", packs),
		Paths:                  []jailcontent.PathFact{{Path: "/home/agent/.claude/skills", HostSource: target, Origin: "pack skills staging"}},
	}
	if err := os.MkdirAll(filepath.Join(target, "jail-mechanics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := jailcontent.RefreshMechanicsFiles([]string{target}, *facts); err != nil {
		t.Fatal(err)
	}
	if err := writeMechanicsApplied(cname, token, facts); err != nil {
		t.Fatal(err)
	}
	if err := writeCtxMountRecord(cname, token, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteWorkspaceBootBaseline(workspace, "podman-self", oldCfg); err != nil {
		t.Fatal(err)
	}
	run := &applyRun{rt: "podman", cname: cname, envLines: []string{ctxMountsTokenEnv + "=" + token},
		mode: applyMode{running: true}, current: newCfg}
	o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})

	applied, ok := readMechanicsApplied(cname, run.envLines)
	if !ok || applied.AppliedAgentsMDExtra != "new approved guidance" {
		t.Fatalf("effective snapshot did not capture the successful approved fold: snapshot=%+v ok=%t", applied, ok)
	}
	wantSkills := o.workspaceSkillsFor(oldCfg, "podman", packs)
	if !reflect.DeepEqual(applied.AppliedWorkspaceSkills, wantSkills) || applied.AppliedWorkspaceSkills.Root != workspace {
		t.Fatalf("applied workspace-skills pointer changed its approved root/aliases/dirs/per-side metadata: got=%+v want=%+v", applied.AppliedWorkspaceSkills, wantSkills)
	}
	attachStaging, err := o.refreshJailBriefings(cname, newCfg, "podman", staged, ioprio.Normal, applied)
	if err != nil {
		t.Fatalf("production attach briefing refresh: %v", err)
	}
	briefing, err := os.ReadFile(filepath.Join(attachStaging, briefingStagingName(claudeBriefingDest)))
	if err != nil || !bytes.Contains(briefing, []byte("new approved guidance")) || bytes.Contains(briefing, []byte("old approved guidance")) {
		t.Fatalf("next-entry briefing did not use the folded approved guidance: err=%v body=%s", err, briefing)
	}
	skill, err := os.ReadFile(filepath.Join(attachStaging, jailcontent.SkillStagingName("claude"), "project-note", "SKILL.md"))
	if err != nil || !bytes.Contains(skill, []byte("project skill refreshed on entry")) {
		t.Fatalf("next-entry workspace-skill reader did not refresh the approved source: err=%v body=%s", err, skill)
	}

	// A pending desired value is not an effective fold and cannot replace the approved
	// snapshot even though the next attach will see that it still needs an apply.
	run.current = appliedTestConfig("agents_md_extra", "pending unapproved guidance")
	o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextRestart}})
	pending, ok := readMechanicsApplied(cname, run.envLines)
	if !ok || pending.AppliedAgentsMDExtra != "new approved guidance" {
		t.Fatalf("pending desired config replaced the prior approved guidance: snapshot=%+v ok=%t", pending, ok)
	}

	// With no effective-config baseline, an otherwise-successful timing result cannot
	// promote the unapproved producer value into the authenticated mechanics snapshot.
	if err := os.Remove(config.WorkspaceConfigBootPath(workspace, "podman-self")); err != nil {
		t.Fatal(err)
	}
	run.current = appliedTestConfig("agents_md_extra", "not recorded")
	o.runApplySteps(run, []applyChange{{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}})
	afterFailure, ok := readMechanicsApplied(cname, run.envLines)
	if !ok || afterFailure.AppliedAgentsMDExtra != "new approved guidance" {
		t.Fatalf("failed baseline fold promoted an unrecorded value: snapshot=%+v ok=%t", afterFailure, ok)
	}
}

func TestMechanicsApplyIgnoresUnsuccessfulOrNonMountOutcomes(t *testing.T) {
	o := &Options{Stderr: &bytes.Buffer{}}
	for _, tc := range []struct {
		name   string
		mode   applyMode
		change applyChange
	}{
		{"declined-or-record-only", applyMode{running: true, recordOnly: "recorded only"}, applyChange{kind: changeMountAdd, outcome: outcomeAppliedNow}},
		{"pending", applyMode{running: true}, applyChange{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextRestart}},
		{"failed-scope", applyMode{running: true}, applyChange{kind: changeScope, key: "brokered", outcome: outcomeNextRestart}},
		{"mount-summary-without-operation", applyMode{running: true}, applyChange{kind: changeKey, key: "mounts", outcome: outcomeAppliedNow}},
		{"broker-summary-without-operation", applyMode{running: true}, applyChange{kind: changeKey, key: "brokered", outcome: outcomeAppliedNow}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mode.recordOnly == "" && mechanicsApplyChanged([]applyChange{tc.change}, nil) {
				t.Fatal("non-effective outcome selected a mechanics refresh")
			}
			o.updateMechanicsAfterApply(&applyRun{mode: tc.mode}, []applyChange{tc.change}, nil)
		})
	}
	for _, key := range []string{"packages", "packs", "host_files"} {
		if mechanicsApplyChanged([]applyChange{{kind: changeKey, key: key, outcome: outcomeNextRestart}}, nil) {
			t.Errorf("deferred %s request was mistaken for an effective change", key)
		}
	}
	for _, tc := range []struct {
		change applyChange
		folded []foldedAppliedKey
		want   bool
	}{
		{change: applyChange{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}, folded: []foldedAppliedKey{{key: "agents_md_extra"}}, want: true},
		{change: applyChange{kind: changeKey, key: "agents_md_extra", outcome: outcomeNextSession}},
		{change: applyChange{kind: changeScope, key: "brokered", outcome: outcomeAppliedNow}, want: true},
		{change: applyChange{kind: changeMountRemove, key: "mounts", outcome: outcomeAppliedNow}, want: true},
		{change: applyChange{kind: changeKey, key: "mounts", outcome: outcomeAppliedNow}, folded: []foldedAppliedKey{{key: "mounts"}}},
		{change: applyChange{kind: changeKey, key: "brokered", outcome: outcomeAppliedNow}, folded: []foldedAppliedKey{{key: "brokered"}}},
	} {
		if got := mechanicsApplyChanged([]applyChange{tc.change}, tc.folded); got != tc.want {
			t.Errorf("mechanicsApplyChanged(%+v, %+v) = %t, want %t", tc.change, tc.folded, got, tc.want)
		}
	}
}
