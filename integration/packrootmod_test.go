package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARootModReachesARealJail is the Claude Code mod in its own shape, end to end in a real
// container: a pack whose root IS the plugin (.claude-plugin/plugin.json) and whose only component
// is a hooks/hooks.json naming a hooks module, with no skills/ folder. Its tree reaches the jail's
// :ro ~/.claude/skills/first-mod, hooks module included, and the launch's jail-code line names the
// module (docs/research/claude-code-mods-management.md, G10 and G1).
//
// The unit tests drive the staging and the disclosure; this one proves the staged tree is what the
// running jail mounts, which is the half a staging test cannot see. No agent is started: the
// script only lists and reads files.
func TestARootModReachesARealJail(t *testing.T) {
	requireJail(t)

	mod := filepath.Join(t.TempDir(), "first-mod")
	for rel, body := range map[string]string{
		"pack.json":                  `{"name":"first-mod","skills_tier":"namespaced"}`,
		".claude-plugin/plugin.json": `{"name":"first-mod","description":"a mod in Claude Code's own shape"}`,
		"hooks/hooks.json":           `{"modules":["./register.js"]}`,
		"hooks/register.js": "export function register(on) {\n  on(\"session.start\", ($) => {\n" +
			"    $.ui.status(\"MODLOADED\");\n  });\n}\n",
	} {
		p := filepath.Join(mod, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", {"source": "file://`+mod+`", "name": "first-mod"}]}`)

	r := runYolo(t, dir,
		`rg -c MODLOADED /home/agent/.claude/skills/first-mod/hooks/register.js && `+
			`rg -c register.js /home/agent/.claude/skills/first-mod/hooks/hooks.json && `+
			`rg -c x-yolo-managed-by /home/agent/.claude/skills/first-mod/.claude-plugin/plugin.json`)
	if r.rc != 0 {
		t.Fatalf("the root mod did not reach the jail's ~/.claude/skills/first-mod: rc %d\nstdout: %s\nstderr: %s",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "the hooks include a hooks module") ||
		!strings.Contains(r.stderr, "claude plugin validate") {
		t.Errorf("the launch's jail-code line does not name the mod's hooks module:\n%s", r.stderr)
	}
}
