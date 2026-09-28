package packdecl

import (
	"strings"
	"testing"
)

// versionsdir_test.go covers `program`'s `versions_dir`: where the program's own installer
// keeps one entry per installed version, which the native launcher's version prune (A7,
// docs/reference/agent-cli-copies.md) reads. The launcher side, run over codex's real layout,
// is internal/entrypoint/codexversionprune_test.go.

// TestVersionsDirSurvivesDecodeAndProjection: InstallContributions is the only way core reads
// a program, so a versions_dir that does not reach Install never reaches a launcher. Absence
// projects the default layout through VersionsDirOrDefault, the one spelling of it.
func TestVersionsDirSurvivesDecodeAndProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"codex","via":"installer","url":"https://x/codex.sh",
	   "versions_dir":".codex/packages/standalone/releases"},
	  {"kind":"program","bin":"claude","via":"installer","url":"https://x/claude.sh"}]}`))
	if len(probs) != 0 {
		t.Fatalf("a versions_dir should decode cleanly, got: %v", probs)
	}
	byBin := map[string]Install{}
	for _, in := range m.InstallContributions() {
		byBin[in.Bin] = in
	}
	if got := byBin["codex"].VersionsDirOrDefault(); got != ".codex/packages/standalone/releases" {
		t.Errorf("codex's versions_dir did not survive the projection: %q", got)
	}
	if got := byBin["claude"].VersionsDirOrDefault(); got != ".local/share/claude/versions" {
		t.Errorf("a program declaring no versions_dir must get the default layout, got %q", got)
	}
}

// TestVersionsDirIsRefusedWhereNoLauncherPrunes: only the native launcher prunes, so the field
// on an npm program, a `requires` or a content kind is read by nothing and is refused by name.
func TestVersionsDirIsRefusedWhereNoLauncherPrunes(t *testing.T) {
	for _, entry := range []string{
		`{"kind":"program","bin":"pi","via":"npm","package":"pi","versions_dir":"a/b"}`,
		`{"kind":"requires","bin":"fzf","versions_dir":"a/b"}`,
		`{"kind":"skills","into":".claude/skills","versions_dir":"a/b"}`,
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[` + entry + `]}`))
		if !strings.Contains(strings.Join(probs, "\n"), `takes "versions_dir"`) {
			t.Errorf("%s should be refused by name, got: %v", entry, probs)
		}
	}
}

// TestVersionsDirRefusesPathsOutsideTheHome: the prune deletes entries of this directory, so a
// path that is absolute, escapes the home, is unclean, or names the home itself is refused.
func TestVersionsDirRefusesPathsOutsideTheHome(t *testing.T) {
	for _, dir := range []string{"/opt/codex", "../elsewhere", ".codex/../..", ".codex/releases/", ".", "./x"} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"program","bin":"codex",` +
			`"via":"installer","url":"https://x/i.sh","versions_dir":"` + dir + `"}]}`))
		if !strings.Contains(strings.Join(probs, "\n"), "versions_dir") {
			t.Errorf("versions_dir %q should be refused, got: %v", dir, probs)
		}
	}
}
