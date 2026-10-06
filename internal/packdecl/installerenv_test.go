package packdecl

import (
	"strings"
	"testing"
)

// installerenv_test.go covers `program`'s `installer_env`: the environment the jail's native
// launcher gives a vendor installer, which copilot needs so GitHub's script lands the binary
// where the launcher looks (PREFIX, OQ-NI1). The launcher side, which runs the variable through
// a real installer, is internal/entrypoint/nativelauncher_test.go
// (TestNativeLauncherGivesTheInstallerItsDeclaredEnv).

// TestInstallerEnvSurvivesDecodeAndProjection: InstallContributions is the only way core reads a
// program, so a declaration that does not reach Install never reaches a launcher. The projection
// is a COPY, so a consumer that edits its Install cannot reach back into the manifest.
func TestInstallerEnvSurvivesDecodeAndProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"copilot","via":"installer","url":"https://x/copilot.sh",
	   "installer_env":{"PREFIX":"~/.local","VERSION":"1.0.92"}},
	  {"kind":"program","bin":"claude","via":"installer","url":"https://x/claude.sh"}]}`))
	if len(probs) != 0 {
		t.Fatalf("an installer_env should decode cleanly, got: %v", probs)
	}
	byBin := map[string]Install{}
	for _, in := range m.InstallContributions() {
		byBin[in.Bin] = in
	}
	got := byBin["copilot"].InstallerEnv
	if got["PREFIX"] != "~/.local" || got["VERSION"] != "1.0.92" || len(got) != 2 {
		t.Errorf("copilot's installer_env did not survive the projection: %v", got)
	}
	if byBin["claude"].InstallerEnv != nil {
		t.Errorf("a program declaring no installer_env must project none, got %v",
			byBin["claude"].InstallerEnv)
	}
	got["PREFIX"] = "/elsewhere"
	for _, c := range m.Contributions() {
		if c.Bin == "copilot" && c.InstallerEnv["PREFIX"] != "~/.local" {
			t.Errorf("editing the projection reached the manifest: %v", c.InstallerEnv)
		}
	}
}

// TestInstallerEnvIsRefusedWhereNoInstallerRuns: only the native launcher runs a vendor
// installer, so the field anywhere else is read by nothing and is refused by name.
func TestInstallerEnvIsRefusedWhereNoInstallerRuns(t *testing.T) {
	for _, entry := range []string{
		`{"kind":"program","bin":"pi","via":"npm","package":"pi","installer_env":{"PREFIX":"~/.local"}}`,
		`{"kind":"requires","bin":"fzf","installer_env":{"PREFIX":"~/.local"}}`,
		`{"kind":"skills","into":".claude/skills","installer_env":{"PREFIX":"~/.local"}}`,
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[` + entry + `]}`))
		if !strings.Contains(strings.Join(probs, "\n"), `takes "installer_env"`) {
			t.Errorf("%s should be refused by name, got: %v", entry, probs)
		}
	}
}

// TestInstallerEnvRefusesWhatNoInstallerShouldGet: a name no environment can carry, the names
// that decide where the program lands and how the installer finds its tools, yolo's own
// variables, a NUL byte, and an object that sets nothing. Each refusal names the field.
func TestInstallerEnvRefusesWhatNoInstallerShouldGet(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{`{}`, "sets nothing"},
		{`{"1X":"a"}`, "is not an environment variable name"},
		{`{"A-B":"a"}`, "is not an environment variable name"},
		{`{"":"a"}`, "is not an environment variable name"},
		{`{"HOME":"/tmp"}`, "is the launcher's to set"},
		{`{"PATH":"/tmp"}`, "is the launcher's to set"},
		{`{"YOLO_BYPASS_SHIMS":"1"}`, "one of yolo's own variables"},
		{`{"_YOLO_LAUNCHER_ACTIVE":""}`, "one of yolo's own variables"},
		{`{"PREFIX":"a\u0000b"}`, "NUL byte"},
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"program","bin":"copilot",` +
			`"via":"installer","url":"https://x/i.sh","installer_env":` + tc.env + `}]}`))
		joined := strings.Join(probs, "\n")
		if !strings.Contains(joined, "installer_env") || !strings.Contains(joined, tc.want) {
			t.Errorf("installer_env %s should be refused with %q, got: %v", tc.env, tc.want, probs)
		}
	}
	// And the names an installer is meant to get pass: copilot's own, a lower-case one, and one
	// with digits after the first character.
	_, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"program","bin":"copilot",` +
		`"via":"installer","url":"https://x/i.sh","installer_env":{"PREFIX":"~/.local","v2_x":""}}]}`))
	if len(probs) != 0 {
		t.Errorf("a valid installer_env was refused: %v", probs)
	}
}

// TestInstallerEnvHomePathIsTheOneGrammar: `~` and `~/…` are under the home, and nothing else
// is — a `~user` or a `~` further in is a literal, the same rule a shell's tilde prefix follows.
func TestInstallerEnvHomePathIsTheOneGrammar(t *testing.T) {
	for _, tc := range []struct {
		in, rest string
		home     bool
	}{
		{"~/.local", "/.local", true},
		{"~", "", true},
		{"~/", "/", true},
		{"~user/.local", "~user/.local", false},
		{"/usr/local", "/usr/local", false},
		{"a~/b", "a~/b", false},
		{"", "", false},
	} {
		rest, home := InstallerEnvHomePath(tc.in)
		if rest != tc.rest || home != tc.home {
			t.Errorf("InstallerEnvHomePath(%q) = (%q, %v), want (%q, %v)", tc.in, rest, home,
				tc.rest, tc.home)
		}
	}
}

// TestTheHostRemedyRunsTheInstallerWithoutTheJailsEnv pins a decision rather than an accident:
// installer_env exists so the JAIL's launcher finds the program where it looks, and the host's
// dependency remedy leaves the vendor's default alone, since on the host that default is the one
// the user's PATH expects. A remedy that grew the variable would, for copilot under a root shell,
// move the binary from /usr/local/bin to a directory that shell may not have on its PATH.
func TestTheHostRemedyRunsTheInstallerWithoutTheJailsEnv(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"program","bin":"copilot",` +
		`"via":"installer","url":"https://gh.io/copilot-install","installer_env":{"PREFIX":"~/.local"}}]}`))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	deps := m.DepRequirements()
	if len(deps) != 1 {
		t.Fatalf("DepRequirements = %+v, want one", deps)
	}
	if got, want := deps[0].SelfInstall, InstallerRemedy("https://gh.io/copilot-install"); got != want {
		t.Errorf("the host remedy = %q, want the plain installer remedy %q", got, want)
	}
	if strings.Contains(deps[0].SelfInstall, "PREFIX") {
		t.Errorf("the host remedy carries the jail's installer env: %q", deps[0].SelfInstall)
	}
}
