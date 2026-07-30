package packdecl

import (
	"testing"
)

// The legacy-shaped projections re-derive the per-field views the read paths
// consume, from a contributes[] manifest.
func TestProjectionsFromContributes(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindProgram, Bin: "claude", Via: "installer", URL: "https://x/i.sh"},
		{Kind: KindSkills, From: "skills", Into: ".claude/skills"},
		{Kind: KindBriefing, From: "AGENTS.md", Into: ".claude/CLAUDE.md", After: "host:.claude/CLAUDE.md"},
		{Kind: KindState, At: ".claude", Scope: "workspace"},
		{Kind: KindState, At: ".creds", Scope: "machine", Why: "shared creds"},
		{Kind: KindReadsHost, Host: ".claude/settings.json", Into: "host-claude/settings.json"},
		{Kind: KindLaunch, Bin: "claude", Flags: []string{"--yolo"}},
		{Kind: KindHook, Hook: "shared_credentials", From: ".claude/.credentials.json", At: ".creds"},
	}}

	if in := m.InstallContribution(); in == nil || in.Kind != "native" || in.InstallerURL != "https://x/i.sh" {
		t.Errorf("InstallContribution wrong: %+v", in)
	}
	if hf := m.HostFileContributions(); len(hf) != 1 || hf[0].From != ".claude/settings.json" {
		t.Errorf("HostFileContributions wrong: %+v", hf)
	}
	if wd := m.WritableDirContributions(); len(wd) != 1 || wd[0] != ".claude" {
		t.Errorf("WritableDirContributions wrong: %+v", wd)
	}
	if sd := m.SharedDirContributions(); len(sd) != 1 || sd[0] != ".creds" {
		t.Errorf("SharedDirContributions wrong: %+v", sd)
	}
	if lf := m.LaunchFlagContributions(); len(lf["claude"]) != 1 || lf["claude"][0] != "--yolo" {
		t.Errorf("LaunchFlagContributions wrong: %+v", lf)
	}
	if hk := m.HookContributions(); len(hk) != 1 || hk[0].Name != "shared_credentials" {
		t.Errorf("HookContributions wrong: %+v", hk)
	}
	// mounts: skills + briefing (with host overlay reconstructed).
	mounts := m.MountContributions()
	var sawSkills, sawBriefing bool
	for _, mt := range mounts {
		if mt.From == "skills" {
			sawSkills = true
		}
		if mt.From == "AGENTS.md" && mt.HostOverlay == ".claude/CLAUDE.md" {
			sawBriefing = true
		}
	}
	if !sawSkills || !sawBriefing {
		t.Errorf("MountContributions missing skills/briefing: %+v", mounts)
	}
	// origin gate: reads-host + installer both flagged.
	reasons := m.NeedsHostAccess()
	if len(reasons) < 2 {
		t.Errorf("NeedsHostAccess should flag reads-host + installer: %v", reasons)
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
