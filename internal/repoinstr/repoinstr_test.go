package repoinstr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func cfgWith(mode, with string) *jsonx.OrderedMap {
	table := jsonx.NewOrderedMap()
	if mode != "" {
		table.Set("mode", mode)
	}
	if with != "" {
		table.Set("replace_with", with)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("repo_instructions", table)
	return cfg
}

func workspaceWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func isolateUserScope(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func TestResolveKeepAndIgnoreReadNoFile(t *testing.T) {
	isolateUserScope(t)
	for _, cfg := range []*jsonx.OrderedMap{jsonx.NewOrderedMap(), cfgWith("keep", ""), cfgWith("ignore", "")} {
		res, err := Resolve(cfg, t.TempDir())
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if res.ReplaceText != "" || res.Section() != "" {
			t.Errorf("mode %q composed text %q", res.Mode, res.ReplaceText)
		}
	}
	if (&Resolved{Mode: "keep"}).Ignoring() || !(&Resolved{Mode: "ignore"}).Ignoring() || !(&Resolved{Mode: "replace"}).Ignoring() {
		t.Error("Ignoring disagrees with the mode vocabulary")
	}
}

func TestReplaceReadsAProjectFileInsideTheWorkspace(t *testing.T) {
	isolateUserScope(t)
	ws := workspaceWith(t, map[string]string{"rules/mine.md": "Use tabs.\n"})
	res, err := Resolve(cfgWith("replace", "rules/mine.md"), ws)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Mode != "replace" || res.ReplaceText != "Use tabs.\n" {
		t.Fatalf("resolved = %+v", res)
	}
	got := res.WithExtra("my extra\n")
	if !strings.HasPrefix(got, "my extra\n\n## Instructions that replace this repository's own") || !strings.Contains(got, "Use tabs.") {
		t.Errorf("extra with the section = %q", got)
	}
	if (&Resolved{Mode: "ignore"}).WithExtra("x") != "x" {
		t.Error("ignore changed the user's extra text")
	}
}

func TestReplaceRefusesWhatAProjectLayerMayNotRead(t *testing.T) {
	isolateUserScope(t)
	ws := workspaceWith(t, map[string]string{"real.md": "text", "sub/inner.md": "inner"})
	outside := workspaceWith(t, map[string]string{"secret.md": "secret"})
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "real.md"), filepath.Join(ws, "alias.md")); err != nil {
		t.Fatal(err)
	}
	for name, with := range map[string]string{
		"an escape":          "../secret.md",
		"an absolute path":   filepath.Join(outside, "secret.md"),
		"a home path":        "~/.ssh/id_ed25519",
		"a link to a file":   "alias.md",
		"a link on the way":  "link/secret.md",
		"a directory":        "sub",
		"a missing file":     "nope.md",
		"the workspace root": ".",
	} {
		_, err := Resolve(cfgWith("replace", with), ws)
		if err == nil {
			t.Errorf("%s: resolved, want a refusal", name)
			continue
		}
		if !strings.Contains(err.Error(), with) || !strings.Contains(err.Error(), "yolo config unset repo_instructions.replace_with") {
			t.Errorf("%s: %q must name the path and the command that removes it", name, err)
		}
	}
}

func TestReplaceWithoutAFileNamesTheNextStep(t *testing.T) {
	isolateUserScope(t)
	_, err := Resolve(cfgWith("replace", ""), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "yolo config set repo_instructions.replace_with") {
		t.Errorf("err = %v, want the command that names a file", err)
	}
}

func TestAnEmptyReplacementBehavesAsIgnore(t *testing.T) {
	isolateUserScope(t)
	ws := workspaceWith(t, map[string]string{"blank.md": " \n\t\n"})
	res, err := Resolve(cfgWith("replace", "blank.md"), ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "ignore" || res.Section() != "" {
		t.Errorf("resolved = %+v, want ignore with no section", res)
	}
}

func TestAUserScopePathIsReadAsTheUserSpelledIt(t *testing.T) {
	isolateUserScope(t)
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(home, "rules.md")
	if err := os.WriteFile(rules, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	user := "[repo_instructions]\nmode = \"replace\"\nreplace_with = \"~/rules.md\"\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(cfgWith("replace", "~/rules.md"), t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.ReplaceText != "mine\n" {
		t.Errorf("text = %q", res.ReplaceText)
	}
	// The same spelling from a project layer is not the user's, and is refused.
	if _, err := Resolve(cfgWith("replace", "~/other.md"), t.TempDir()); err == nil {
		t.Error("a project layer's ~/ path was read")
	}
}

func TestTheWireCarriesTheModeAndRebuildsTheSelection(t *testing.T) {
	for _, mode := range []string{"ignore", "replace"} {
		wire := WireFromConfig(cfgWith(mode, "x.md"))
		sel := FromWire(wire, "/workspace", "/home/agent")
		if sel == nil || sel.Mode != mode || sel.Workspace != "/workspace" || sel.Home != "/home/agent" || sel.Host {
			t.Errorf("%s: wire %q gave %+v", mode, wire, sel)
		}
	}
	for name, cfg := range map[string]*jsonx.OrderedMap{"unset": jsonx.NewOrderedMap(), "keep": cfgWith("keep", "")} {
		if wire := WireFromConfig(cfg); wire != "" {
			t.Errorf("%s: wire = %q, want empty so nothing crosses", name, wire)
		}
	}
	for _, bad := range []string{"", "not json", `{"mode":"keep"}`, `{"mode":"surprise"}`} {
		if sel := FromWire(bad, "/w", "/h"); sel != nil {
			t.Errorf("FromWire(%q) = %+v, want nil", bad, sel)
		}
	}
}

func TestScanFindsNestedFilesAndSkipsGitAndNodeModules(t *testing.T) {
	ws := workspaceWith(t, map[string]string{
		"AGENTS.md":                       "a",
		"pkg/CLAUDE.md":                   "b",
		"pkg/deep/.claude/rules/x.md":     "c",
		".git/AGENTS.md":                  "no",
		"node_modules/dep/AGENTS.md":      "no",
		"src/README.md":                   "no",
		".github/copilot-instructions.md": "d",
	})
	scan := ScanWorkspace(ws, map[string][]string{
		"claude":  {"AGENTS.md", "CLAUDE.md", ".claude/rules/**"},
		"copilot": {".github/copilot-instructions.md"},
	})
	if got := strings.Join(scan.Files["claude"], ","); got != "AGENTS.md,pkg/CLAUDE.md,pkg/deep/.claude/rules/x.md" {
		t.Errorf("claude files = %s", got)
	}
	if got := strings.Join(scan.Files["copilot"], ","); got != ".github/copilot-instructions.md" {
		t.Errorf("copilot files = %s", got)
	}
	if scan.Incomplete {
		t.Error("a small workspace reported an incomplete scan")
	}
}

// The workspace's own .yolo state (a home with thousands of directories) is yolo's, not the repository's.
func TestScanSkipsTheWorkspacesOwnYoloState(t *testing.T) {
	ws := workspaceWith(t, map[string]string{"AGENTS.md": "a", ".yolo/home/a/b/c/d/e/f/AGENTS.md": "state"})
	scan := ScanWorkspace(ws, map[string][]string{"claude": {"AGENTS.md"}})
	if scan.Incomplete || strings.Join(scan.Files["claude"], ",") != "AGENTS.md" {
		t.Errorf("scan = %+v, want only the repository's file and no bound reached", scan)
	}
}

func TestScanStopsAtItsDepthBoundAndSaysSo(t *testing.T) {
	ws := workspaceWith(t, map[string]string{"a/b/c/d/e/f/g/AGENTS.md": "too deep", "AGENTS.md": "near"})
	scan := ScanWorkspace(ws, map[string][]string{"claude": {"AGENTS.md"}})
	if !scan.Incomplete {
		t.Error("a scan that skipped a deeper directory did not say it was incomplete")
	}
	if got := strings.Join(scan.Files["claude"], ","); got != "AGENTS.md" {
		t.Errorf("files = %s, want only the one inside the bound", got)
	}
}

// shippedFor loads the real shipped packs a disclosure test needs, selected by name.
func shippedFor(t *testing.T, names ...string) (selected, all []*packload.Pack) {
	t.Helper()
	all = packload.Embedded()
	for _, n := range names {
		found := false
		for _, p := range all {
			if p.Name == n {
				selected = append(selected, p)
				found = true
			}
		}
		if !found {
			t.Fatalf("no shipped pack %q", n)
		}
	}
	return selected, all
}

func TestDisclosureNamesWhatEachAgentDidAndTheNextStepWhenItCannot(t *testing.T) {
	ws := workspaceWith(t, map[string]string{"AGENTS.md": "a", "CLAUDE.md": "b", "pkg/AGENTS.md": "c"})
	selected, all := shippedFor(t, "claude", "pi", "agy")
	lines := Disclosure(selected, all, &Resolved{Mode: "ignore"}, ws, false, "")
	if len(lines) != 3 {
		t.Fatalf("lines = %q, want one per selected agent", lines)
	}
	byAgent := map[string]string{}
	for _, l := range lines {
		if !strings.HasPrefix(l, "repo instructions: ") {
			t.Errorf("line %q lacks the lead", l)
		}
		byAgent[strings.Fields(strings.TrimPrefix(l, "repo instructions: "))[0]] = l
	}
	if l := byAgent["claude:"]; !strings.Contains(l, "ignoring 3 files (AGENTS.md, CLAUDE.md, +1 more) through claudeMdExcludes") {
		t.Errorf("claude: %q", l)
	}
	if l := byAgent["pi:"]; !strings.Contains(l, "through --no-context-files") || !strings.Contains(l, "still read: .pi/SYSTEM.md, .pi/APPEND_SYSTEM.md") {
		t.Errorf("pi: %q", l)
	}
	agy := byAgent["agy:"]
	for _, want := range []string{"NOT ignored", "still reads", "agents that can ignore them: claude, codex, pi", "yolo config set repo_instructions.mode keep"} {
		if !strings.Contains(agy, want) {
			t.Errorf("agy: %q lacks %q", agy, want)
		}
	}
}

func TestDisclosureForReplaceNamesTheReplacementAndStaysSilentForKeep(t *testing.T) {
	ws := workspaceWith(t, map[string]string{"AGENTS.md": "a"})
	selected, all := shippedFor(t, "claude", "copilot")
	lines := Disclosure(selected, all, &Resolved{Mode: "replace", ReplaceWith: "~/rules.md", ReplaceText: "x"}, ws, false, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "replaced by ~/rules.md") || !strings.Contains(joined, "the replacement text still joins its briefing") {
		t.Errorf("lines = %s", joined)
	}
	if got := Disclosure(selected, all, &Resolved{Mode: "keep"}, ws, false, ""); len(got) != 0 {
		t.Errorf("keep disclosed %q", got)
	}
	if got := Disclosure(selected, all, nil, ws, false, ""); len(got) != 0 {
		t.Errorf("an unresolved launch disclosed %q", got)
	}
}

func TestDisclosureAtTheHostIsLimitedToTheLaunchedProgram(t *testing.T) {
	ws := workspaceWith(t, map[string]string{"AGENTS.md": "a"})
	selected, all := shippedFor(t, "claude", "pi")
	lines := Disclosure(selected, all, &Resolved{Mode: "ignore"}, ws, true, "claude")
	if len(lines) != 1 || !strings.Contains(lines[0], "claude:") || !strings.Contains(lines[0], "--settings") {
		t.Errorf("lines = %q, want claude's alone, naming its host flag", lines)
	}
	if got := Disclosure(selected, all, &Resolved{Mode: "ignore"}, ws, true, "bash"); len(got) != 0 {
		t.Errorf("a command that is no agent got %q", got)
	}
}

// The selection a launch hands is nil for keep, so a launch that keeps the files composes as it did
// before the key existed.
func TestSelectionIsNilForKeep(t *testing.T) {
	if (&Resolved{Mode: "keep"}).Selection("/w", "/h", false) != nil {
		t.Error("keep produced a selection")
	}
	sel := (&Resolved{Mode: "ignore"}).Selection("/w", "/h", true)
	if sel == nil || !sel.Host || sel.Mode != packdecl.ModeIgnore {
		t.Errorf("selection = %+v", sel)
	}
}
