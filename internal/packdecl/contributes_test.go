package packdecl

import (
	"strings"
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

	if in := m.InstallContributions(); len(in) != 1 || in[0].Kind != "native" ||
		in[0].InstallerURL != "https://x/i.sh" {
		t.Errorf("InstallContributions wrong: %+v", in)
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
		// `from` is CONVENTIONAL on skills/briefing and MANDATORY on files. The three
		// live together because the boundary between them is the schema decision, and a
		// regression would move exactly one of these rows.
		{"skills no from", Contribution{Kind: KindSkills, Into: ".acme/skills"}, ""},
		{"briefing no from", Contribution{Kind: KindBriefing, Into: ".acme/A.md"}, ""},
		{"files no from", Contribution{Kind: KindFiles, Into: ".acme/prompts"}, "needs \"from\""},
		// `into` stays required on all three — a destination has one right answer per
		// AGENT, so there is no convention to fall back to (see validateContribution).
		{"briefing no into", Contribution{Kind: KindBriefing, From: "AGENTS.md"}, "needs \"into\""},
		{"files no into", Contribution{Kind: KindFiles, From: "prompts"}, "needs \"into\""},
		{"machine state no because", Contribution{Kind: KindState, At: ".x", Scope: "machine"}, "needs a \"because\""},
		{"state escaping path", Contribution{Kind: KindState, At: "../etc"}, "must not contain"},
		{"unknown hook", Contribution{Kind: KindHook, Hook: "nope"}, "unknown hook"},
		{"good mount", Contribution{Kind: KindMount, Host: "datasets/acme", Into: "acme-data"}, ""},
		{"mount no host", Contribution{Kind: KindMount, Into: "acme-data"}, "needs \"host\""},
		{"mount escaping host", Contribution{Kind: KindMount, Host: "../../etc", Into: "x"}, "must not"},
		{"good env", Contribution{Kind: KindEnv, Vars: map[string]string{"ACME_MODE": "fast"}}, ""},
		{"env empty map", Contribution{Kind: KindEnv}, "non-empty \"vars\""},
		{"env empty key", Contribution{Kind: KindEnv, Vars: map[string]string{"": "x"}}, "empty variable name"},
		// `loophole` points at a module dir. `from` is REQUIRED (there is no conventional
		// location to fall back to, and the dir's basename IS the loophole's name), it runs
		// through the same traversal guard every path-bearing field gets, and `into` is
		// refused BY NAME rather than accepted-and-ignored.
		{"good loophole", Contribution{Kind: KindLoophole, From: "loopholes/acme"}, ""},
		{"loophole no from", Contribution{Kind: KindLoophole}, "needs \"from\""},
		{"loophole absolute from", Contribution{Kind: KindLoophole, From: "/etc/loopholes/acme"},
			"must be relative"},
		{"loophole escaping from", Contribution{Kind: KindLoophole, From: "../../etc"},
			"must not contain"},
		{"loophole colon in from", Contribution{Kind: KindLoophole, From: "loopholes/a:b"},
			"must not contain"},
		{"loophole with into", Contribution{Kind: KindLoophole, From: "loopholes/acme", Into: ".acme"},
			"does not take \"into\""},
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

	// A `loophole` contributes NOTHING here, and that absence is the design rather than an
	// omission: its claims live in a manifest.jsonc outside pack.json, so this package —
	// which has no pack root and no internal imports — cannot enumerate them. Producing a
	// bare "loophole acme" here would be WORSE than producing nothing: it is a claim string
	// that never changes no matter what the daemon becomes, i.e. content-blind consent that
	// looks like consent. The real producer is packload.Pack.LoopholeHostAccessClaims.
	lp := &Manifest{Contributes: []Contribution{{Kind: KindLoophole, From: "loopholes/acme"}}}
	if c := lp.HostAccessClaims(); len(c) != 0 {
		t.Errorf("packdecl must not emit a loophole claim (%v) — it cannot read the module "+
			"manifest, so any string it produced would be a consent key blind to the daemon "+
			"it approves", c)
	}
	// What it DOES own is the pointer.
	if got := lp.LoopholeSources(); len(got) != 1 || got[0] != "loopholes/acme" {
		t.Errorf("LoopholeSources() = %v, want the declared module dir", got)
	}
}

// LoopholeSources dedupes a repeated `from` (one module named twice is one loophole) and
// skips a contribution with no `from` (already reported as a validation problem, and there
// is no name to derive from an empty path).
func TestLoopholeSourcesDedupes(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindLoophole, From: "loopholes/a"},
		{Kind: KindLoophole, From: "loopholes/b"},
		{Kind: KindLoophole, From: "loopholes/a"},
		{Kind: KindLoophole}, // invalid; must not yield an empty source
		{Kind: KindSkills, From: "skills", Into: ".x/skills"},
	}}
	got := m.LoopholeSources()
	if len(got) != 2 || got[0] != "loopholes/a" || got[1] != "loopholes/b" {
		t.Errorf("LoopholeSources() = %v, want [loopholes/a loopholes/b] in declaration order", got)
	}
}

// A pack declaring TWO programs gets both, and in declaration order. The accessor used to
// `return` inside its loop, so the second binary was silently dropped in the jail while
// DepRequirements (the host path) reported it — one declaration, two answers. Asserted
// against DepRequirements in the same test, because the asymmetry is the actual defect.
func TestInstallContributionsReturnsEveryProgram(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{
		{Kind: KindProgram, Bin: "shellcheck", Via: "npm", Package: "shellcheck-bin"},
		{Kind: KindSkills, From: "skills", Into: ".x/skills"}, // interleaved, must not confuse the walk
		{Kind: KindProgram, Bin: "shfmt", Via: "installer", URL: "https://x/shfmt.sh"},
	}}
	got := m.InstallContributions()
	if len(got) != 2 {
		t.Fatalf("InstallContributions = %d (%+v), want 2 — a pack needing two tools is "+
			"ordinary (shellcheck+shfmt, jq+yq); `program` is exclusive by BIN, not per pack", len(got), got)
	}
	if got[0].Bin != "shellcheck" || got[0].Kind != "npm" || got[0].Package != "shellcheck-bin" {
		t.Errorf("first install wrong: %+v", got[0])
	}
	if got[1].Bin != "shfmt" || got[1].Kind != "native" || got[1].InstallerURL != "https://x/shfmt.sh" {
		t.Errorf("second install wrong: %+v", got[1])
	}
	// The jail projection and the host projection must agree on how many programs exist.
	if n := len(m.DepRequirements()); n != len(got) {
		t.Errorf("DepRequirements returned %d but InstallContributions %d — the host and "+
			"jail paths must see the same set of programs", n, len(got))
	}
}

// A `requires` contribution asserts presence: it feeds the host dep probe exactly as a
// program does (DepRequirements folds both in) but generates no install of any kind, so it
// carries no SelfInstall and is the only kind RequiredBins reports.
func TestRequiresAssertsPresenceAndInstallsNothing(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"requires","bin":"fzf","install_hints":{"brew":"fzf","apt":"fzf"}},
	  {"kind":"program","bin":"claude","via":"installer","url":"https://claude.ai/install.sh"}]}`))
	if len(probs) != 0 {
		t.Fatalf("a requires contribution should decode cleanly, got: %v", probs)
	}

	// Both kinds reach the HOST probe: below jail there is no image, so "must exist" and
	// "yolo would install this" are the same question about the host.
	deps := m.DepRequirements()
	if len(deps) != 2 {
		t.Fatalf("DepRequirements = %d (%+v), want 2 (program + requires)", len(deps), deps)
	}
	byBin := map[string]DepRequirement{}
	for _, d := range deps {
		byBin[d.Bin] = d
	}
	if byBin["fzf"].Hints["brew"] != "fzf" {
		t.Errorf("a requires' install_hints must reach the host probe: %+v", byBin["fzf"])
	}
	if byBin["fzf"].SelfInstall != "" {
		t.Errorf("a requires installs NOTHING, so it has no self-install command: %q",
			byBin["fzf"].SelfInstall)
	}
	// The program's own installer is derived from the fields it already declares — no new
	// schema, which is the whole point of item #6.
	if got := byBin["claude"].SelfInstall; got != "curl -fsSL https://claude.ai/install.sh | sh" {
		t.Errorf("a program via installer should derive its own curl remedy, got %q", got)
	}

	// The JAIL asserts only `requires`: a `program` bin being absent is normal, since its
	// launcher installs it on first use.
	req := m.RequiredBins()
	if len(req) != 1 || req[0].Bin != "fzf" {
		t.Errorf("RequiredBins should be the requires set only, got %+v", req)
	}

	// And it generates no launcher, because there is no install to project.
	if in := m.InstallContributions(); len(in) != 1 || in[0].Bin != "claude" {
		t.Errorf("a requires must not appear as an install: %+v", in)
	}
}

// An npm program derives `npm install -g <package>`; a via-less or package-less program
// derives nothing rather than a half-command.
func TestSelfInstallCommandDerivation(t *testing.T) {
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"npm", Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "@o/x"},
			"npm install -g @o/x"},
		{"installer", Contribution{Kind: KindProgram, Bin: "x", Via: "installer", URL: "https://h/i.sh"},
			"curl -fsSL https://h/i.sh | sh"},
		{"npm with no package", Contribution{Kind: KindProgram, Bin: "x", Via: "npm"}, ""},
		{"installer with no url", Contribution{Kind: KindProgram, Bin: "x", Via: "installer"}, ""},
		{"requires", Contribution{Kind: KindRequires, Bin: "x"}, ""},
	}
	for _, tc := range cases {
		if got := selfInstallCommand(tc.c); got != tc.want {
			t.Errorf("%s: selfInstallCommand = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A `requires` carrying a program's install fields is the author confusing the two kinds,
// and it is silent otherwise — the fields are simply never read, so the tool never installs
// and nothing says why. Loud instead.
func TestRequiresRejectsProgramInstallFields(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"requires","bin":"fzf","via":"npm","package":"fzf"}]}`))
	joined := strings.Join(probs, "\n")
	for _, want := range []string{`does not take "via"`, `does not take "package"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems should name the misplaced field (%s):\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, `kind "program"`) {
		t.Errorf("the diagnostic should point at the kind that DOES install:\n%s", joined)
	}

	// And `bin` is still required.
	_, noBin := Decode([]byte(`{"name":"x","contributes":[{"kind":"requires"}]}`))
	if !strings.Contains(strings.Join(noBin, "\n"), `needs "bin"`) {
		t.Errorf("a requires with no bin must be a problem: %v", noBin)
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

// An autonomy contribution decodes both postures through strict Decode, and PostureFor
// selects by the policy bit (§4.2). A pack that is permissive by default (pi-like) may
// leave autonomous empty and only carry a guarded block.
func TestAutonomyDecodesAndSelects(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"claude","contributes":[
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"claude","name":"settings","managed":{"skipDangerousModePermissionPrompt":true}}],
	                 "launch":[{"bin":"claude","flags":["--dangerously-skip-permissions"]}]},
	   "guarded":{"config":[{"agent":"claude","name":"settings","managed":{"skipDangerousModePermissionPrompt":false}}]}}]}`))
	if len(probs) != 0 {
		t.Fatalf("autonomy should decode cleanly, got: %v", probs)
	}
	ac := m.AutonomyContributions()
	if ac == nil || ac.Autonomous == nil || ac.Guarded == nil {
		t.Fatalf("both postures should decode: %+v", ac)
	}
	// autonomy ON selects the autonomous posture (with the launch flag); OFF selects guarded.
	on := m.PostureFor(true)
	if on == nil || len(on.Launch) != 1 || on.Launch[0].Flags[0] != "--dangerously-skip-permissions" {
		t.Errorf("PostureFor(true) should be the autonomous posture with the launch flag: %+v", on)
	}
	off := m.PostureFor(false)
	if off == nil || len(off.Launch) != 0 {
		t.Errorf("PostureFor(false) should be the guarded posture (no bypass launch flag): %+v", off)
	}
}

// ConfigOverlayContributions keeps each overlay's TARGET with its BODY, in declaration
// order — which is the fold order, so it must not be normalized.
//
// It is deliberately not folded into SurfaceContributions' one concatenated array: a config
// contribution declares a surface (so several are just a longer list), while an overlay
// names someone else's, and the two halves are only meaningful together.
func TestConfigOverlayContributions(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"house-rules","contributes":[
	  {"kind":"config-overlay","surface":"claude/settings","config":{"managed":{"a":1}}},
	  {"kind":"config-overlay","surface":"codex/config","config":{"managed":{"b":2}}},
	  {"kind":"config","config":[{"agent":"house","name":"own","codec":"json","path":"~/x.json"}]}]}`))
	if len(probs) != 0 {
		t.Fatalf("config-overlay should decode cleanly, got: %v", probs)
	}
	got := m.ConfigOverlayContributions()
	if len(got) != 2 {
		t.Fatalf("want 2 overlays (the `config` contribution is not one), got %d: %+v", len(got), got)
	}
	if got[0].Surface != "claude/settings" || got[1].Surface != "codex/config" {
		t.Errorf("declaration order lost (it IS the fold order): %+v", got)
	}
	if !contains(string(got[0].Config), `"a":1`) {
		t.Errorf("the overlay body did not travel with its target: %s", got[0].Config)
	}
	// The pack's OWN config surface is unaffected by the overlay accessor.
	if s := m.SurfaceContributions(); !contains(string(s), "house") {
		t.Errorf("SurfaceContributions lost the pack's own surface: %s", s)
	}
	// A pack with no overlays gets none.
	none := &Manifest{Contributes: []Contribution{{Kind: KindEnv, Vars: map[string]string{"X": "1"}}}}
	if c := none.ConfigOverlayContributions(); len(c) != 0 {
		t.Errorf("a pack declaring no overlay should have none, got %+v", c)
	}
}

// config-overlay validation: both `surface` and a `config` body are required, because
// either alone is a declaration that can never take effect.
func TestConfigOverlayValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Contribution
		want string
	}{
		{"no surface", Contribution{Kind: KindConfigOverlay, Raw: []byte(`{"managed":{"a":1}}`)},
			"needs \"surface\""},
		{"no body", Contribution{Kind: KindConfigOverlay, Surface: "claude/settings"},
			"needs a \"config\" body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Contributes: []Contribution{tc.c}}
			joined := ""
			for _, p := range m.validateContributions() {
				joined += p + "\n"
			}
			if !contains(joined, tc.want) {
				t.Errorf("expected a problem containing %q, got %q", tc.want, joined)
			}
		})
	}
}

// A `skills` contribution with no `from` must both VALIDATE and RESOLVE — through the
// authoring boundary (Decode, strict) rather than validateContributions alone, because that
// is the door a pack author actually knocks on.
//
// The resolution half is the point: a schema that accepts an omitted `from` and then
// resolves it to "" would be worse than requiring it, since "" reads as the pack ROOT at
// every call site that joins it onto p.Root — the whole tree delivered as skills instead of
// a clear validation error.
func TestSkillsFromIsOptionalAndDefaults(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"skills","into":".acme/skills"}]}`))
	if len(probs) != 0 {
		t.Fatalf("omitting `from` on skills must validate, got %v", probs)
	}
	c := m.Contributions()[0]
	if got := c.SkillsSource(); got != DefaultSkillsDir {
		t.Errorf("SkillsSource() = %q, want %q — an omitted `from` must resolve to the "+
			"convention, never to the empty pack root", got, DefaultSkillsDir)
	}
	if got := m.SkillsSources(); len(got) != 1 || got[0] != DefaultSkillsDir {
		t.Errorf("SkillsSources() = %v, want [%q]", got, DefaultSkillsDir)
	}
	// A DECLARED source still wins — the default must not have flattened the field.
	m, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"skills","from":"my-skills","into":".acme/skills"}]}`))
	if len(probs) != 0 {
		t.Fatalf("declared `from` must still validate, got %v", probs)
	}
	if got := m.Contributions()[0].SkillsSource(); got != "my-skills" {
		t.Errorf("SkillsSource() = %q, want %q", got, "my-skills")
	}
}

// Same pair for `briefing`, whose convention is two names rather than one: an omitted
// `from` resolves to AGENTS.md then CLAUDE.md, in that order, and a declared one is tried
// FIRST without dropping the convention behind it (the precedence hostBriefingProse
// implements).
func TestBriefingFromIsOptionalAndDefaults(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"briefing","into":".acme/A.md","after":"host:.acme/A.md"}]}`))
	if len(probs) != 0 {
		t.Fatalf("omitting `from` on briefing must validate, got %v", probs)
	}
	want := []string{"AGENTS.md", "CLAUDE.md"}
	if got := m.Contributions()[0].BriefingCandidates(); !equalStrings(got, want) {
		t.Errorf("BriefingCandidates() = %v, want %v — an omitted `from` must resolve to "+
			"the conventional pair, in precedence order", got, want)
	}
	m, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"briefing","from":"prose/BRIEF.md","into":".acme/A.md"}]}`))
	if len(probs) != 0 {
		t.Fatalf("declared `from` must still validate, got %v", probs)
	}
	want = []string{"prose/BRIEF.md", "AGENTS.md", "CLAUDE.md"}
	if got := m.Contributions()[0].BriefingCandidates(); !equalStrings(got, want) {
		t.Errorf("BriefingCandidates() = %v, want %v", got, want)
	}
}

// `files` KEEPS `from` required, and this is the row that must not follow the other two.
// The kind is CombineExclusive over an ARBITRARY path — there is no conventional location
// for an opaque tree, so the declaration is the only thing that can name it, and defaulting
// would mean claiming the pack root. Asserted at the authoring boundary because that is
// where the author gets told.
func TestFilesFromStaysRequired(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"files","into":".acme/prompts"}]}`))
	joined := strings.Join(probs, "; ")
	if !contains(joined, `needs "from"`) {
		t.Errorf("omitting `from` on files must be a validation error, got %q", joined)
	}
	// And the combine rule that is the reason: if this ever stops being exclusive
	// ownership of an arbitrary path, revisit whether the field is still mandatory.
	if fp, ok := FootprintOf(KindFiles); !ok || fp.Combine != CombineExclusive {
		t.Errorf("files combine = %v, want %v — the premise of the required `from`",
			fp.Combine, CombineExclusive)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// autonomy validation: at least one posture, and a launch entry needs a bin.
func TestAutonomyValidation(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"autonomy"}]}`))
	if len(probs) == 0 {
		t.Error("an autonomy contribution with neither posture should be a validation error")
	}
	_, probs = Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"autonomy","autonomous":{"launch":[{"flags":["--x"]}]}}]}`))
	if len(probs) == 0 {
		t.Error("an autonomy launch entry with no bin should be a validation error")
	}
}
