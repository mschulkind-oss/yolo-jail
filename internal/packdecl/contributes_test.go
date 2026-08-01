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
		{"good mount", Contribution{Kind: KindMount, Host: "datasets/acme", Into: "acme-data"}, ""},
		{"mount no host", Contribution{Kind: KindMount, Into: "acme-data"}, "needs \"host\""},
		{"mount escaping host", Contribution{Kind: KindMount, Host: "../../etc", Into: "x"}, "must not"},
		{"good env", Contribution{Kind: KindEnv, Vars: map[string]string{"ACME_MODE": "fast"}}, ""},
		{"env empty map", Contribution{Kind: KindEnv}, "non-empty \"vars\""},
		{"env empty key", Contribution{Kind: KindEnv, Vars: map[string]string{"": "x"}}, "empty variable name"},
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

// HostAccessClaims returns the SPECIFIC, sorted claims a pack makes on the host —
// the set a user approves and a pin move is diffed against. Distinct from
// NeedsHostAccessContributions (generic display reasons).
func TestHostAccessClaims(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindMount, Host: "datasets/acme", Into: "acme-data"},
		{Kind: KindReadsHost, Host: ".config/acme/key"},
		{Kind: KindProgram, Bin: "acme", Via: "installer", URL: "https://acme/i.sh"},
		{Kind: KindBriefing, From: "AGENTS.md", Into: ".acme/A.md", After: "host:.acme/A.md"},
		{Kind: KindEnv, Vars: map[string]string{"ACME_MODE": "fast"}}, // NOT host access
		{Kind: KindSkills, From: "skills", Into: ".acme/skills"},      // NOT host access
	}}
	got := m.HostAccessClaims()
	want := []string{
		"briefing .acme/A.md",
		"installer https://acme/i.sh",
		"mount datasets/acme -> /ctx/acme-data",
		"reads-host .config/acme/key",
	}
	if len(got) != len(want) {
		t.Fatalf("HostAccessClaims() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("claim[%d] = %q, want %q (must be sorted + specific)", i, got[i], want[i])
		}
	}

	// A pack that reads nothing from the host makes no claims.
	none := &Manifest{Contributes: []Contribution{
		{Kind: KindEnv, Vars: map[string]string{"X": "1"}},
		{Kind: KindSkills, From: "skills", Into: ".x/skills"},
	}}
	if c := none.HostAccessClaims(); len(c) != 0 {
		t.Errorf("a pack reading nothing from the host should have no claims, got %v", c)
	}
}

// install_hints parses on a program contribution and DepRequirements projects it.
func TestDepRequirementsFromInstallHints(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindProgram, Bin: "psql", Via: "npm", Package: "x",
			InstallHints: map[string]string{"brew": "postgresql@16", "apt": "postgresql-16"}},
		{Kind: KindProgram, Bin: "nohints", Via: "npm", Package: "y"},
		{Kind: KindSkills, From: "skills", Into: ".x/skills"}, // not a program
	}}
	got := m.DepRequirements()
	if len(got) != 2 {
		t.Fatalf("DepRequirements = %d, want 2 (one per program with a bin)", len(got))
	}
	byBin := map[string]DepRequirement{}
	for _, d := range got {
		byBin[d.Bin] = d
	}
	if byBin["psql"].Hints["brew"] != "postgresql@16" {
		t.Errorf("psql brew hint wrong: %+v", byBin["psql"])
	}
	if len(byBin["nohints"].Hints) != 0 {
		t.Errorf("a program with no install_hints should have empty Hints: %+v", byBin["nohints"])
	}
}

// install_hints round-trips through strict Decode (DisallowUnknownFields).
func TestInstallHintsDecodes(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"psql","via":"npm","package":"p","install_hints":{"brew":"postgresql@16"}}]}`))
	if len(probs) != 0 {
		t.Fatalf("install_hints should decode cleanly, got: %v", probs)
	}
	d := m.DepRequirements()
	if len(d) != 1 || d[0].Hints["brew"] != "postgresql@16" {
		t.Errorf("decoded install_hints wrong: %+v", d)
	}
}
