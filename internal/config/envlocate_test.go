package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envLocateFixture is a fresh HOME with a TOML user config, and a workspace beside it.
func envLocateFixture(t *testing.T, user, project string) (home, ws string) {
	t.Helper()
	home, ws = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if user != "" {
		dir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if project != "" {
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.toml"), []byte(project), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, ws
}

// SEP-D6, SEP-D7: a name is found in each scope's own file, with its set, its place spelled as a
// line prints it, and the dotenv file a line lives in; never its value.
func TestLocateEnvNameFindsEachScopesSites(t *testing.T) {
	home, ws := envLocateFixture(t, "[env]\nfiles = [\"keys.env\"]\n", "[env.secrets]\nset = { ZAI_API_KEY = \"z\" }\n")
	keys := filepath.Join(home, ".config", "yolo-jail", "keys.env")
	if err := os.WriteFile(keys, []byte("export ZAI_API_KEY=pub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := LocateEnvName(ws, "ZAI_API_KEY", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("two sites, got %+v", sites)
	}
	if s := sites[0]; s.Scope != EnvScopeUser || s.Place.Private || s.Dotenv != keys || s.Place.Describe() != "([env], from "+keys+")" {
		t.Errorf("the user file's public dotenv site: %+v %s", s, s.Place.Describe())
	}
	if s := sites[1]; s.Scope != EnvScopeProject || !s.Place.Private || !s.Inline() || s.Place.Describe() != "([env.secrets] set)" {
		t.Errorf("the project's private inline site: %+v", s)
	}
	if got := EnvNameScope(ws)("ZAI_API_KEY"); got != "" {
		t.Errorf("a name two scopes set names no one scope, got %q", got)
	}
	if got := EnvNameScope(ws)("NOPE"); got != "" {
		t.Errorf("a name no scope sets names none, got %q", got)
	}
}

func TestEnvNameScopeNamesTheOneScopeThatSetsTheName(t *testing.T) {
	_, ws := envLocateFixture(t, "", "[env.secrets]\nset = { STRIPE_KEY = \"s\" }\n")
	if got := EnvNameScope(ws)("STRIPE_KEY"); got != " --project" {
		t.Errorf("want \" --project\", got %q", got)
	}
}

func TestReadEnvScopeFilesMarksALegacyFile(t *testing.T) {
	home, ws := envLocateFixture(t, "", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"env_sources": [{"ZAI_API_KEY": "z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := LocateEnvName(ws, "ZAI_API_KEY", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || !sites[0].Legacy || sites[0].Place.Key != "env_sources" || !sites[0].Inline() {
		t.Errorf("one legacy inline site, got %+v", sites)
	}
}

// SEP-D12: a move copies the line verbatim, removes every line that sets the name, keeps an
// existing file's mode and creates a missing one 0600.
func TestDotenvEditMovesTheLastLineVerbatimAndKeepsModes(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.env"), filepath.Join(dir, "b.env")
	if err := os.WriteFile(src, []byte("A=1\nexport K='old'\n# K=comment\nK=\"new\"\nB=2"), 0o640); err != nil {
		t.Fatal(err)
	}
	line, found, err := DotenvLine(src, "K")
	if err != nil || !found || line != `K="new"` {
		t.Fatalf("the last assignment, verbatim: %q %v %v", line, found, err)
	}
	if err := AppendDotenvLine(dst, line); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RemoveDotenvName(src, "K"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(src); string(got) != "A=1\n# K=comment\nB=2" {
		t.Errorf("every line setting K left, comments stayed: %q", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "K=\"new\"\n" {
		t.Errorf("the destination holds the line: %q", got)
	}
	if info, _ := os.Stat(src); info.Mode().Perm() != 0o640 {
		t.Errorf("an existing file keeps its mode, got %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != 0o600 {
		t.Errorf("a created file is 0600, got %v", info.Mode().Perm())
	}
	if err := AppendDotenvLine(src, "C=3"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(src); !strings.HasSuffix(string(got), "B=2\nC=3\n") {
		t.Errorf("an append ends the last line first: %q", got)
	}
}
