package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestLegacyAttachRefreshPreservesPriorMechanicsAndDoesNotPromoteDesiredInputs(t *testing.T) {
	for _, tc := range []struct {
		name            string
		envToken        string
		sheetToken      string
		priorMechanics  bool
		appliedSnapshot bool
	}{
		{name: "missing authenticated snapshot with existing mechanics", priorMechanics: true},
		{name: "snapshot for another token", envToken: "trusted-launch-token", sheetToken: "different-launch-token", priorMechanics: true},
		{name: "matching snapshot but missing mount record", envToken: "trusted-launch-token", sheetToken: "trusted-launch-token", priorMechanics: true},
		{name: "authenticated snapshot and matching live mount record", envToken: "trusted-launch-token", sheetToken: "trusted-launch-token", appliedSnapshot: true},
		{name: "no prior mechanics files", priorMechanics: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, cname, _ := jailWithoutZai(t)
			mechanicsDir := ""
			for _, target := range packSkillTargets(claudePackFixture(t)) {
				mechanicsDir = filepath.Join(paths.AgentsDir(), cname, target.Staging, "jail-mechanics")
				break
			}
			if mechanicsDir == "" {
				t.Fatal("claude pack has no skills staging target")
			}
			prior := map[string][]byte{
				"SKILL.md":         []byte("prior applied mechanics index\n"),
				"host-map.md":      []byte("prior applied host map\n"),
				"recipes/prior.md": []byte("prior applied recipe\n"),
			}
			if tc.priorMechanics {
				for rel, content := range prior {
					path := filepath.Join(mechanicsDir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, content, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.sheetToken != "" {
				facts := &jailcontent.FactSheet{
					WorkspacePath: "/wrong/workspace",
					Briefing:      jailcontent.BriefingInput{Workspace: ws, Mechanism: "podman"},
				}
				if tc.appliedSnapshot {
					facts.AppliedAgentsMDExtra = "APPLIED_LAUNCH_GUIDANCE"
					facts.SkillsDelivery = "live read-only bind"
				}
				if err := writeMechanicsApplied(cname, tc.sheetToken, facts); err != nil {
					t.Fatal(err)
				}
			}
			if tc.appliedSnapshot {
				if err := writeCtxMountRecord(cname, tc.envToken, []ctxMountEntry{{Source: "/applied/host-source", Dest: "/ctx/applied"}}); err != nil {
					t.Fatal(err)
				}
			}

			configDir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.WriteFile(filepath.Join(configDir, "config.jsonc"), []byte(`{
  "packs": ["claude", "zai"],
  "agents_md_extra": "UNTRUSTED_CURRENT_AGENTS_MD_EXTRA"
}
`), 0o644); err != nil {
				t.Fatal(err)
			}
			workspaceSkill := filepath.Join(ws, ".pi", "skills", "unapplied-workspace-skill", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(workspaceSkill), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(workspaceSkill, []byte("unapplied workspace skill\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			env := podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
			if tc.envToken != "" {
				env += ctxMountsTokenEnv + "=" + tc.envToken + "\n"
			}
			r := runPackSkew(t, ws, env, false, "", map[string]string{AllowAttachSkewEnv: "1"}, func(o *Options) {
				o.ProfileName = "zai"
			})
			if r.rc != 0 || !r.execed {
				t.Fatalf("acknowledged attach did not complete: rc=%d execed=%v\n%s", r.rc, r.execed, r.stderr)
			}
			if tc.priorMechanics {
				for rel, want := range prior {
					got, err := os.ReadFile(filepath.Join(mechanicsDir, filepath.FromSlash(rel)))
					if err != nil || string(got) != string(want) {
						t.Errorf("legacy attach changed prior mechanics %s: got %q, err %v", rel, got, err)
					}
				}
			} else if !tc.appliedSnapshot {
				if _, err := os.Stat(mechanicsDir); !os.IsNotExist(err) {
					t.Errorf("legacy attach invented mechanics staging for an old jail: stat err %v", err)
				}
			}
			if tc.appliedSnapshot {
				// The briefing and the topic files its core points at: the applied mount is in
				// topics/mounts.md (docs/reference/agent-briefings.md).
				briefing := claudeBriefingOf(t, cname) + stagedTopics(t, filepath.Join(paths.AgentsDir(), cname))
				for _, want := range []string{"APPLIED_LAUNCH_GUIDANCE", "/ctx/applied"} {
					if !strings.Contains(briefing, want) {
						t.Errorf("authenticated attach briefing omitted applied value %q:\n%s", want, briefing)
					}
				}
				if strings.Contains(briefing, "UNTRUSTED_CURRENT_AGENTS_MD_EXTRA") {
					t.Errorf("authenticated attach promoted current desired guidance:\n%s", briefing)
				}
				mapPath := filepath.Join(mechanicsDir, "host-map.md")
				got, err := os.ReadFile(mapPath)
				if err != nil {
					t.Fatalf("read refreshed applied host map: %v", err)
				}
				for _, want := range []string{"/ctx/applied", "/applied/host-source"} {
					if !strings.Contains(string(got), want) {
						t.Errorf("authenticated attach host map omitted applied value %q:\n%s", want, got)
					}
				}
			} else if got := claudeBriefingOf(t, cname); strings.Contains(got, "UNTRUSTED_CURRENT_AGENTS_MD_EXTRA") {
				t.Errorf("legacy attach promoted current agents_md_extra into the running briefing:\n%s", got)
			}
			if _, err := os.Stat(filepath.Join(mechanicsDir, "..", "unapplied-workspace-skill")); !os.IsNotExist(err) {
				t.Errorf("legacy attach promoted a current workspace skill: stat err %v", err)
			}
		})
	}
}
