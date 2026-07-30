package packdecl

import (
	"testing"
)

// A manifest with no contributes[] synthesizes them from the legacy fields —
// the same field→kind mapping the Phase-1 footprint shim used.
func TestSynthesizeFromLegacyFields(t *testing.T) {
	m := &Manifest{
		Install:      &Install{Kind: "native", Bin: "claude", InstallerURL: "https://x/i.sh"},
		Mounts:       []Mount{{From: "skills", To: ".claude/skills"}, {From: "AGENTS.md", To: ".claude/CLAUDE.md", HostOverlay: ".claude/CLAUDE.md"}, {From: "prompts", To: ".claude/prompts"}},
		WritableDirs: []string{".claude"},
		SharedDirs:   []string{".creds"},
		HostFiles:    []HostFile{{From: ".claude/settings.json", To: "host-claude/settings.json"}},
		LaunchFlags:  map[string][]string{"claude": {"--yolo"}},
		Hooks:        []Hook{{Name: "shared_credentials", File: ".claude/.credentials.json", SharedDir: ".creds"}},
	}
	got := map[Kind][]Contribution{}
	for _, c := range m.Contributions() {
		got[c.Kind] = append(got[c.Kind], c)
	}

	if len(got[KindProgram]) != 1 || got[KindProgram][0].Via != "installer" || got[KindProgram][0].URL != "https://x/i.sh" {
		t.Errorf("program synthesis wrong: %+v", got[KindProgram])
	}
	if len(got[KindSkills]) != 1 || got[KindSkills][0].Into != ".claude/skills" {
		t.Errorf("skills synthesis wrong: %+v", got[KindSkills])
	}
	if len(got[KindBriefing]) != 1 || got[KindBriefing][0].After != "host:.claude/CLAUDE.md" {
		t.Errorf("briefing synthesis wrong (host overlay): %+v", got[KindBriefing])
	}
	if len(got[KindFiles]) != 1 || got[KindFiles][0].Into != ".claude/prompts" {
		t.Errorf("files synthesis wrong: %+v", got[KindFiles])
	}
	// state: one workspace, one machine.
	scopes := map[string]bool{}
	for _, c := range got[KindState] {
		scopes[c.Scope] = true
	}
	if !scopes["workspace"] || !scopes["machine"] {
		t.Errorf("state synthesis should have both scopes: %+v", got[KindState])
	}
	if len(got[KindReadsHost]) != 1 || got[KindReadsHost][0].Host != ".claude/settings.json" {
		t.Errorf("reads-host synthesis wrong: %+v", got[KindReadsHost])
	}
	if len(got[KindLaunch]) != 1 || got[KindLaunch][0].Bin != "claude" {
		t.Errorf("launch synthesis wrong: %+v", got[KindLaunch])
	}
	if len(got[KindHook]) != 1 || got[KindHook][0].Hook != "shared_credentials" {
		t.Errorf("hook synthesis wrong: %+v", got[KindHook])
	}
}

// A non-empty contributes[] WINS and the legacy fields are ignored.
func TestContributesWinsOverLegacy(t *testing.T) {
	m := &Manifest{
		Install:     &Install{Kind: "npm", Bin: "legacy", Package: "legacy-pkg"},
		Contributes: []Contribution{{Kind: KindProgram, Bin: "new", Via: "npm", Package: "new-pkg"}},
	}
	cs := m.Contributions()
	if len(cs) != 1 || cs[0].Bin != "new" {
		t.Errorf("contributes[] must win over legacy Install: %+v", cs)
	}
}

// contributes[] validates per-kind: an unknown kind, and a missing required field.
func TestValidateContributes(t *testing.T) {
	cases := []struct {
		name string
		c    Contribution
		want string // substring the problem must contain; "" = must be valid
	}{
		{"good program", Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "p"}, ""},
		{"unknown kind", Contribution{Kind: "mcp-server", Bin: "x"}, "unknown kind"},
		{"program no via", Contribution{Kind: KindProgram, Bin: "x"}, "needs \"via\""},
		{"skills no into", Contribution{Kind: KindSkills, From: "skills"}, "needs \"into\""},
		{"machine state no because", Contribution{Kind: KindState, At: ".x", Scope: "machine"}, "needs a \"because\""},
		{"state escaping path", Contribution{Kind: KindState, At: "../etc"}, "must not contain"},
		{"unknown hook", Contribution{Kind: KindHook, Hook: "nope"}, "unknown hook"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Contributes: []Contribution{tc.c}}
			problems := m.validateContributions()
			if tc.want == "" {
				if len(problems) != 0 {
					t.Errorf("expected valid, got %v", problems)
				}
				return
			}
			found := false
			for _, p := range problems {
				if contains(p, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a problem containing %q, got %v", tc.want, problems)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
