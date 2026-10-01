package loopholes

// listcolor_test.go pins `yolo loopholes list`'s color (docs/plans/cli-visual-polish.md, Group A's
// second item): with Deps.Color set, each state label is wrapped in its convention's color, the
// loophole's name bold, its tags and transport/intercepts metadata dim and its description dim;
// with it unset no escape is written, and the colored report with its escapes removed is the
// plain report byte for byte, which is the plan's additive-color invariant
// (docs/reference/cli-color.md, invariant 2). Driven through List over pack modules and a
// supersession claim, so every state listState can return comes from discovery itself.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// listColorFixture writes one loophole per state listState can return, plus the extras a row
// can carry (intercepts, a description holding a style tag, a supersession line, a setting),
// and returns List's output with the given color.
func listColorFixture(t *testing.T, color bool) string {
	t.Helper()
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Cleanup(ResetPackSupersessions)
	captureWarnings(t)

	root := onlyModules(t)
	writeManifest(t, mkdir(t, filepath.Join(root, "good")), map[string]any{
		"name": "good", "description": "reads [red] as text", "transport": "none",
		"settings": map[string]any{"quiet": map[string]any{"type": "bool"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(root, "icept")), map[string]any{
		"name": "icept", "description": "x", "transport": TransportLoopbackTLS,
		"intercepts": []any{map[string]any{"host": "example.test"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(root, "needs")), map[string]any{
		"name": "needs", "description": "x", "transport": "none",
		"requires": map[string]any{"command_on_path": "xyz-never-exists-abc"},
	})
	writeManifest(t, mkdir(t, filepath.Join(root, "off")), map[string]any{
		"name": "off", "description": "x", "transport": "none", "default_enabled": false,
	})
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})
	SetPackSupersessions([]PackSupersession{bedrock()})

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home, Color: color,
		LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil }}
	if rc := List(deps); rc != 0 {
		t.Fatalf("List rc = %d, err=%q", rc, errBuf.String())
	}
	return out.String()
}

// labelPad is the padding after a state label in its 36-column field: none for a label as long
// as the field or longer, which an unmet requirement's reason usually is.
func labelPad(label string) string { return strings.Repeat(" ", max(0, 36-len(label))) }

// row is one plain `loopholes list` row: the padded label, then the name, the tags and the
// metadata.
func row(label, name, tags, extra string) string {
	return "  " + label + labelPad(label) + "  " + name + "  (" + tags + ")  " + extra + "\n"
}

func TestListColorsItsStateVocabulary(t *testing.T) {
	plain, colored := listColorFixture(t, false), listColorFixture(t, true)

	if strings.Contains(plain, "\x1b[") || strings.Contains(plain, "[bold]") || strings.Contains(plain, "[/dim]") {
		t.Errorf("the plain report carries an escape or a style tag:\n%s", plain)
	}
	// The rows' plain bytes, the label column padded exactly as before.
	for _, want := range []string{
		row("active", "good", "pack/none/external", "transport=none"),
		row("active", "icept", "pack/"+TransportLoopbackTLS+"/external", "intercepts=[example.test]"),
		row("inactive ('xyz-never-exists-abc' not on PATH)", "needs", "pack/none/external", "transport=none"),
		row("disabled", "off", "pack/none/external", "transport=none"),
		row("inactive (superseded)", "broker-like", "pack/none/external", "transport=none"),
		"      reads [\u2060red] as text\n",
		"      settings.quiet: bool, user-scope, default false\n",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the plain report lacks %q:\n%s", want, plain)
		}
	}
	if got := ansiEscape.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("color is not additive: the colored report without its escapes differs from the "+
			"plain one\n--- plain\n%s\n--- colored, stripped\n%s", plain, got)
	}
	for _, want := range []string{
		"  \x1b[32mactive\x1b[0m" + labelPad("active") + "  \x1b[1mgood\x1b[0m  \x1b[2m(pack/none/external)  transport=none\x1b[0m\n",
		"\x1b[1micept\x1b[0m  \x1b[2m(pack/" + TransportLoopbackTLS + "/external)  intercepts=[example.test]\x1b[0m\n",
		// Longer than its field, so nothing pads it: the padding is the plain report's, never
		// 36 columns counted over the escapes.
		"  \x1b[33minactive ('xyz-never-exists-abc' not on PATH)\x1b[0m  \x1b[1mneeds\x1b[0m",
		"  \x1b[2mdisabled\x1b[0m" + labelPad("disabled") + "  \x1b[1moff\x1b[0m",
		// Superseded is off by the user's own pack selection, which `status` reports dim too.
		"  \x1b[2minactive (superseded)\x1b[0m" + labelPad("inactive (superseded)") + "  \x1b[1mbroker-like\x1b[0m",
		// A style tag in a description is the manifest's text, not yolo's markup.
		"      \x1b[2mreads [\u2060red] as text\x1b[0m\n",
	} {
		if !strings.Contains(colored, want) {
			t.Errorf("the colored report lacks %q:\n%q", want, colored)
		}
	}
}

// Every state listState can return has a color, and the same fact takes the color `status`
// gives it.
func TestEveryListStateHasAStyle(t *testing.T) {
	for _, tc := range []struct{ state, reason, want string }{
		{"active", "", "green"},
		{"inactive", "'x' not on PATH", "yellow"},
		{"inactive", "superseded", doctorStateStyle["superseded"]},
		{"disabled", "", doctorStateStyle["disabled"]},
	} {
		if got := listStateStyle(tc.state, tc.reason); got != tc.want {
			t.Errorf("listStateStyle(%q, %q) = %q, want %q", tc.state, tc.reason, got, tc.want)
		}
	}
}

// The empty list names where a loophole could come from, its two source labels bold.
func TestListEmptyStateColorsItsSourceLabels(t *testing.T) {
	isolateDirs(t)
	unsetJail(t)
	// One HOME for both renders: the two lines name paths under it.
	t.Setenv("HOME", t.TempDir())
	render := func(color bool) string {
		var out, errBuf bytes.Buffer
		deps := cmdDeps(t, &out, &errBuf, "", "")
		deps.Color = color
		if rc := List(deps); rc != 0 {
			t.Fatalf("List rc = %d, err=%q", rc, errBuf.String())
		}
		return out.String()
	}
	plain, colored := render(false), render(true)
	if !strings.HasPrefix(plain, "No loopholes installed.\n  • pack: a `loophole` contribution") ||
		!strings.Contains(plain, "\n  • config: loopholes: block in ") {
		t.Errorf("the plain empty state changed:\n%s", plain)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("the plain empty state carries an escape:\n%q", plain)
	}
	if got := ansiEscape.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("color is not additive in the empty state\n--- plain\n%s\n--- colored, stripped\n%s", plain, got)
	}
	for _, want := range []string{"  • \x1b[1mpack:\x1b[0m a `loophole`", "  • \x1b[1mconfig:\x1b[0m loopholes: block"} {
		if !strings.Contains(colored, want) {
			t.Errorf("the colored empty state lacks %q:\n%q", want, colored)
		}
	}
}

// Text yolo did not write reaches the list in four places a manifest or the machine fills: an
// unmet requirement's reason in the label, an intercept host, the description, and the two
// paths of the empty state. A style tag in any of them prints as text, and an opening it leaves
// unclosed does not swallow the closing tag yolo writes after it, which printed `[/dim]` as text
// and left a terminal dim.
func TestListEscapesTextYoloDidNotWrite(t *testing.T) {
	render := func(color bool) string {
		unsetJail(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		captureWarnings(t)
		root := onlyModules(t)
		writeManifest(t, mkdir(t, filepath.Join(root, "odd")), map[string]any{
			"name": "odd", "description": "see [docs for more", "transport": TransportLoopbackTLS,
			"intercepts": []any{map[string]any{"host": "[bold]h.test"}},
			"requires":   map[string]any{"command_on_path": "[bold]x"},
		})
		var out, errBuf bytes.Buffer
		deps := Deps{Out: &out, Err: &errBuf, Cwd: home, Color: color,
			LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
			LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil }}
		if rc := List(deps); rc != 0 {
			t.Fatalf("List rc = %d, err=%q", rc, errBuf.String())
		}
		return out.String()
	}
	plain, colored := render(false), render(true)
	const wj = "\u2060"
	// The pad is the unescaped label's: the joiner is zero-width.
	pad := labelPad("inactive ('[bold]x' not on PATH)")
	for _, want := range []string{
		"  inactive ('[" + wj + "bold]x' not on PATH)" + pad + "  odd  (pack/" + TransportLoopbackTLS + "/external)  intercepts=[[" + wj + "bold]h.test]\n",
		"      see [" + wj + "docs for more\n",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the plain report lacks %q:\n%q", want, plain)
		}
	}
	if got := ansiEscape.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("color is not additive\n--- plain\n%q\n--- colored, stripped\n%q", plain, got)
	}
	for _, want := range []string{
		"  \x1b[33minactive ('[" + wj + "bold]x' not on PATH)\x1b[0m" + pad + "  \x1b[1modd\x1b[0m",
		"intercepts=[[" + wj + "bold]h.test]\x1b[0m\n",
		"      \x1b[2msee [" + wj + "docs for more\x1b[0m\n",
	} {
		if !strings.Contains(colored, want) {
			t.Errorf("the colored report lacks %q:\n%q", want, colored)
		}
	}

	// The empty state's two paths are under a HOME whose name holds a style tag.
	isolateDirs(t)
	home := filepath.Join(t.TempDir(), "[dim]h")
	t.Setenv("HOME", home)
	for _, color := range []bool{false, true} {
		var out, errBuf bytes.Buffer
		deps := cmdDeps(t, &out, &errBuf, "", "")
		deps.Color = color
		if rc := List(deps); rc != 0 {
			t.Fatalf("List rc = %d, err=%q", rc, errBuf.String())
		}
		got := ansiEscape.ReplaceAllString(out.String(), "")
		for _, p := range []string{paths.LocalPackDir(), paths.UserConfigPath()} {
			if !strings.Contains(strings.ReplaceAll(got, wj, ""), " "+p+" ") {
				t.Errorf("color=%v: the empty state does not name %q:\n%q", color, p, out.String())
			}
		}
		if strings.Contains(out.String(), "\x1b[2m") {
			t.Errorf("color=%v: the HOME's [dim] styled the empty state:\n%q", color, out.String())
		}
	}
}
