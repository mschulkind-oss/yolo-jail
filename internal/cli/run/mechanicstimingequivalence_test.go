package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

func TestMechanicsTimingMatchesProductionPlannerForEveryConfigKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	mountSource := filepath.Join(workspace, "schema-mount")
	if err := os.Mkdir(mountSource, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, backend := range []struct {
		name       string
		runtime    string
		isMacOS    bool
		liveMounts bool
		liveScope  bool
	}{
		{name: "podman-linux", runtime: "podman", liveMounts: true, liveScope: true},
		{name: "podman-macos", runtime: "podman", isMacOS: true},
		{name: "apple-container", runtime: "container", isMacOS: true},
		{name: "macos-user", runtime: "macos-user", isMacOS: true, liveScope: true},
	} {
		t.Run(backend.name, func(t *testing.T) {
			packs := claudePackFixture(t)
			staged := stagedPacks{packs: packs}
			o := &Options{Workspace: workspace, IsMacOS: backend.isMacOS,
				PathExists: func(string) bool { return false }, Getenv: func(string) string { return "" },
				LookPath: func(string) (string, bool) { return "", false }}
			if backend.runtime == "podman" {
				swapSeam(t, &ctxMountsLiveSeam, func(*Options, string, string, []string) (bool, string) {
					return backend.liveMounts, ""
				})
				swapSeam(t, &readCtxMountRecordSeam, func(string, []string) ([]liveCtxMount, bool) {
					return nil, true
				})
				swapSeam(t, &ctxMountRecordInvalidatedSeam, func(string, []string) bool { return false })
			}
			swapSeam(t, &liveScopeFilesSeam, func(context.Context, string, string, string) ([]liveScopeFile, error) {
				if !backend.liveScope {
					return []liveScopeFile{{Path: "/scope", File: brokerscope.File{Form: "github", Repos: []string{"org/old"}}}}, nil
				}
				return []liveScopeFile{{Path: "/scope", File: brokerscope.File{Form: "github", Repos: []string{"org/old"}}, LiveScope: true}}, nil
			})

			timingContext := changetiming.Context{
				Backend: changetiming.Backend(backend.runtime), LiveMounts: backend.liveMounts,
				LiveScope: backend.liveScope, PackageDelivery: changetiming.BakedImage,
			}
			if backend.runtime == "macos-user" {
				timingContext.PackageDelivery = changetiming.StoreDelivered
			}
			for _, key := range config.TopLevelConfigKeys() {
				t.Run(key, func(t *testing.T) {
					current := jsonx.NewOrderedMap()
					switch key {
					case "mounts":
						current.Set(key, []any{mountSource})
					case "brokered":
						current.Set(key, mapOf(t, `{"github":{"repos":["org/new"]}}`))
					default:
						current.Set(key, "changed")
					}
					if _, err := config.WriteWorkspaceBootBaseline(workspace, o.setupName(), jsonx.NewOrderedMap()); err != nil {
						t.Fatal(err)
					}

					cname := "schema-timing-" + backend.name + "-" + key
					facts := o.mechanicsFactSheet(cname, current, backend.runtime, staged, storePackagesPlan{}, nil)
					staging, err := o.refreshJailBriefings(cname, current, backend.runtime, staged, ioprio.Normal, facts)
					if err != nil {
						t.Fatalf("production mechanics staging: %v", err)
					}
					recipePath := filepath.Join(staging, jailcontent.SkillStagingName("claude"), "jail-mechanics", "recipes", "workspace-config.md")
					rendered, err := os.ReadFile(recipePath)
					if err != nil {
						t.Fatalf("read production rendered timing: %v", err)
					}
					guidance := renderedTimingRows(string(rendered))
					actualGuidance, ok := guidance[key]
					if !ok {
						t.Fatalf("schema key %q is absent from generated workspace timing recipe:\n%s", key, rendered)
					}

					var scope *config.ScopeCheck
					if key == "brokered" {
						scope = &config.ScopeCheck{Sources: []config.ScopeSource{{
							Source: "github", Label: "github-broker",
							Entry: []config.EntryRepo{{Repo: "org/new", Files: []string{"yolo-jail.jsonc"}}},
						}}}
					}
					run := &applyRun{cfg: current, current: current, rt: backend.runtime, cname: cname,
						envLines: []string{ctxMountsTokenEnv + "=schema-token",
							entrypoint.ContractTagsEnv + "=" + strings.Join(launchContractTags, ",")}, mode: applyMode{running: true}}
					plan := o.applyPlan(run, scope)
					planned, ok := productionPlanOutcome(plan, key)
					if !ok {
						t.Fatalf("production applyPlan omitted changed schema key %q: %+v", key, plan)
					}
					if matches, err := mechanicsTimingMatchesPlanner(actualGuidance, planned, key, facts, current, timingContext); err != nil || !matches {
						t.Errorf("generated timing for %s = %q does not normalize to production applyPlan = %q (match=%t err=%v); plan=%+v",
							key, actualGuidance, planned, matches, err, plan)
					}
				})
			}
		})
	}
}

func TestMechanicsTimingHostContextIsSchemaScopedNotAJailApplyPlan(t *testing.T) {
	// The host notch has its own host-apply authority, not the jail applyPlan. Exercise its
	// canonical timing context across the schema without manufacturing a jail planner result.
	workspace := t.TempDir()
	facts := jailcontent.FactSheet{Host: true, HostWorkspace: workspace, UserConfigPath: filepath.Join(workspace, "config.jsonc")}
	for _, key := range config.TopLevelConfigKeys() {
		disposition, _ := render.HostFields().ConfigKey(key)
		if disposition == render.KeyUnclassified {
			t.Errorf("host applicability authority has no canonical entry for schema key %q", key)
		}
		row, err := changetiming.Classify(key, changetiming.Context{Backend: changetiming.Host})
		if err != nil {
			t.Fatalf("host timing classification for schema key %q: %v", key, err)
		}
		facts.Timing = append(facts.Timing, row)
	}
	files, err := jailcontent.RenderBuiltinSkills(facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := files["jail-mechanics/recipes/workspace-config.md"]; exists {
		t.Fatal("host mechanics rendering emitted a jail workspace-config recipe")
	}
	if _, exists := files["jail-mechanics/SKILL.md"]; !exists {
		t.Fatal("canonical host mechanics context did not render the host skill index")
	}
	for _, row := range facts.Timing {
		if row.Backend != changetiming.Host {
			t.Errorf("host schema row has non-host backend: %+v", row)
		}
	}
}

func renderedTimingRows(body string) map[string]string {
	rows := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		colon := strings.LastIndex(line, ": ")
		if colon < 0 {
			continue
		}
		outcome := strings.TrimSpace(line[colon+2:])
		for _, field := range strings.Split(strings.TrimPrefix(line[:colon], "## "), ", ") {
			key := strings.Trim(strings.TrimSpace(field), "` ")
			if key != "" {
				rows[key] = outcome
			}
		}
	}
	return rows
}

func productionPlanOutcome(plan []applyChange, key string) (string, bool) {
	var outcome string
	found := false
	for _, change := range plan {
		if change.key != key {
			continue
		}
		if found && outcome != string(canonicalApplyTiming(change.outcome)) {
			return "", false
		}
		outcome, found = string(canonicalApplyTiming(change.outcome)), true
	}
	return outcome, found
}

func canonicalApplyTiming(outcome applyOutcome) changetiming.Outcome {
	switch outcome {
	case outcomeAppliedNow:
		return changetiming.AppliedNow
	case outcomeNextSession:
		return changetiming.NextSession
	case outcomeNextLaunch:
		return changetiming.AtNextLaunch
	default:
		return changetiming.AtNextRestart
	}
}

func normalizeMechanicsOutcome(outcome, key string, facts *jailcontent.FactSheet, cfg *jsonx.OrderedMap, nominalContext changetiming.Context) (string, error) {
	switch outcome {
	case string(changetiming.UserScopeOnly):
		if !config.WorkspaceUserScopeOnlyKey(key) {
			return "", fmt.Errorf("invalid enriched user-scope outcome for %s", key)
		}
	case string(changetiming.RestartAndRebuild):
		if facts.PackageDelivery != string(changetiming.BakedImage) || (key != "packages" && (key != "gpu" || !gpuAddsImagePackages(cfg))) {
			return "", fmt.Errorf("invalid enriched rebuild outcome for %s", key)
		}
	case string(changetiming.HostSwitch):
		if key != "loopholes" || len(facts.Switches) == 0 {
			return "", fmt.Errorf("invalid enriched host-switch outcome for %s", key)
		}
	default:
		return outcome, nil
	}

	// Drop only the guidance enrichment, then ask the shared contract for this key's
	// nominal lifecycle under the actual backend/capability context. Never use the planner
	// result here: the independent comparison below must detect planner drift.
	nominalContext.UserScopeOnly = false
	nominalContext.HostSwitch = false
	nominalContext.Rebuild = false
	row, err := changetiming.Classify(key, nominalContext)
	if err != nil {
		return "", err
	}
	return string(row.Outcome), nil
}

func mechanicsTimingMatchesPlanner(outcome, planner, key string, facts *jailcontent.FactSheet, cfg *jsonx.OrderedMap, nominalContext changetiming.Context) (bool, error) {
	normalized, err := normalizeMechanicsOutcome(outcome, key, facts, cfg, nominalContext)
	if err != nil {
		return false, err
	}
	return normalized == planner, nil
}

func TestMechanicsEnrichedTimingNormalizationRejectsWrongPlannerOutcome(t *testing.T) {
	userScopeKey := ""
	for _, key := range config.TopLevelConfigKeys() {
		if config.WorkspaceUserScopeOnlyKey(key) {
			userScopeKey = key
			break
		}
	}
	if userScopeKey == "" {
		t.Fatal("schema has no whole-key user-scope restriction for the normalization negative control")
	}

	for _, tc := range []struct {
		name    string
		key     string
		outcome changetiming.Outcome
		facts   jailcontent.FactSheet
		ctx     changetiming.Context
	}{
		{name: "user-scope", key: userScopeKey, outcome: changetiming.UserScopeOnly,
			ctx: changetiming.Context{Backend: changetiming.Podman}},
		{name: "rebuild", key: "packages", outcome: changetiming.RestartAndRebuild,
			facts: jailcontent.FactSheet{PackageDelivery: string(changetiming.BakedImage)},
			ctx:   changetiming.Context{Backend: changetiming.Podman, PackageDelivery: changetiming.BakedImage}},
		{name: "host-switch", key: "loopholes", outcome: changetiming.HostSwitch,
			facts: jailcontent.FactSheet{Switches: []jailcontent.SwitchFact{{Name: "supported"}}},
			ctx:   changetiming.Context{Backend: changetiming.Podman}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nominal, err := normalizeMechanicsOutcome(string(tc.outcome), tc.key, &tc.facts, jsonx.NewOrderedMap(), tc.ctx)
			if err != nil {
				t.Fatalf("normalize enriched class: %v", err)
			}
			wrong := string(changetiming.NextSession)
			if nominal == wrong {
				wrong = string(changetiming.AtNextRestart)
			}
			matches, err := mechanicsTimingMatchesPlanner(string(tc.outcome), wrong, tc.key, &tc.facts, jsonx.NewOrderedMap(), tc.ctx)
			if err != nil {
				t.Fatalf("compare with intentionally wrong planner outcome: %v", err)
			}
			if matches {
				t.Fatalf("enriched outcome %q for %s normalized to %q but accepted intentionally wrong planner outcome %q",
					tc.outcome, tc.key, nominal, wrong)
			}
		})
	}
}
