package run

// repoinstructions_test.go pins the launch half of `repo_instructions`
// (docs/design/workspace-instruction-files.md): the mode is resolved once and handed to the staged
// packs, the replacement joins the briefing's user block, the mode crosses to the jail on the
// session-input channel, and the disclosure reaches stderr whatever YOLO_NO_BANNER says, naming the
// way back for an agent that cannot honor it. There is no test for a mount, because nothing builds
// one: yolo never masks a workspace file (IF-D14).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/repoinstr"
)

func repoInstrConfig(mode, with string) *jsonx.OrderedMap {
	table := jsonx.NewOrderedMap()
	table.Set("mode", mode)
	if with != "" {
		table.Set("replace_with", with)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("repo_instructions", table)
	return cfg
}

func shippedPacksNamed(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	var out []*packload.Pack
	for _, want := range names {
		found := false
		for _, p := range packload.Embedded() {
			if p.Name == want {
				c := *p
				out = append(out, &c)
				found = true
			}
		}
		if !found {
			t.Fatalf("no shipped pack %q", want)
		}
	}
	return out
}

func repoInstrWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(ws, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func TestApplyRepoInstructionsHandsTheStagedPacksTheirSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	ws := repoInstrWorkspace(t, map[string]string{"AGENTS.md": "x"})
	var errw bytes.Buffer
	o := &Options{Workspace: ws, Stderr: &errw}
	packs := shippedPacksNamed(t, "claude", "pi")
	if !o.applyRepoInstructions(repoInstrConfig("ignore", ""), "podman", packs) {
		t.Fatalf("refused: %s", errw.String())
	}
	for _, p := range packs {
		if p.Instructions == nil || p.Instructions.Mode != "ignore" || p.Instructions.Workspace != "/workspace" ||
			p.Instructions.Home != "/home/agent" || p.Instructions.Host {
			t.Errorf("%s was handed %+v", p.Name, p.Instructions)
		}
	}
	// The one fold every spelling of the launch flags reads now carries pi's flag.
	found := false
	for _, f := range packload.LaunchFlagsFor(packs, true)["pi"] {
		found = found || f == "--no-context-files"
	}
	if !found {
		t.Errorf("pi's launch flags = %v, want --no-context-files among them", packload.LaunchFlagsFor(packs, true)["pi"])
	}
}

func TestApplyRepoInstructionsLeavesAKeepLaunchAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var errw bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &errw}
	packs := shippedPacksNamed(t, "claude")
	if !o.applyRepoInstructions(jsonx.NewOrderedMap(), "podman", packs) || packs[0].Instructions != nil {
		t.Fatalf("a launch with no table was handed %+v", packs[0].Instructions)
	}
	o.discloseRepoInstructions(packs)
	if errw.Len() != 0 {
		t.Errorf("a launch that keeps the files said %q", errw.String())
	}
}

func TestAReplacementThatCannotBeReadRefusesTheLaunchNamingTheNextStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var errw bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &errw}
	if o.applyRepoInstructions(repoInstrConfig("replace", "rules/missing.md"), "podman", nil) {
		t.Fatal("a launch whose replacement is missing went ahead")
	}
	out := errw.String()
	if !strings.Contains(out, "Refusing to launch") || !strings.Contains(out, "rules/missing.md") ||
		!strings.Contains(out, "yolo config unset repo_instructions.replace_with") {
		t.Errorf("refusal = %q", out)
	}
}

func TestTheReplacementJoinsTheBriefingsUserBlock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := repoInstrWorkspace(t, map[string]string{"rules.md": "Prefer small commits.\n"})
	o := &Options{Workspace: ws, Stderr: &bytes.Buffer{}}
	if !o.applyRepoInstructions(repoInstrConfig("replace", "rules.md"), "podman", nil) {
		t.Fatal("refused")
	}
	got := o.repoInstr.WithExtra("my extra\n")
	if !strings.Contains(got, "my extra") || !strings.Contains(got, "Prefer small commits.") {
		t.Errorf("extra = %q", got)
	}
	// And a sealed build carries none of the user's own text.
	sealed := &Options{Workspace: ws, Stderr: &bytes.Buffer{}, Sealed: true}
	sealed.applyRepoInstructions(repoInstrConfig("replace", "rules.md"), "podman", nil)
	if sealed.repoInstr.Ignoring() || sealed.repoInstr.Section() != "" {
		t.Errorf("a sealed build resolved %+v", sealed.repoInstr)
	}
}

func TestTheModeCrossesOnTheSessionInputChannel(t *testing.T) {
	has := func(cfg *jsonx.OrderedMap, sealed bool) string {
		for _, in := range composeSessionInputs(cfg, nil, sealed, false, nil, toolSkillInputs{}) {
			if in.name == repoinstr.EnvName {
				return in.value
			}
		}
		t.Fatalf("%s is not a session input", repoinstr.EnvName)
		return ""
	}
	if got := has(repoInstrConfig("ignore", ""), false); got != `{"mode":"ignore"}` {
		t.Errorf("ignore crossed as %q", got)
	}
	if got := has(jsonx.NewOrderedMap(), false); got != "" {
		t.Errorf("an unset table crossed as %q", got)
	}
	if got := has(repoInstrConfig("ignore", ""), true); got != "" {
		t.Errorf("a sealed build carried the user's mode: %q", got)
	}
}

func TestTheDisclosureReachesStderrAndNamesTheWayBackForAnAgentThatCannotIgnore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_NO_BANNER", "1")
	ws := repoInstrWorkspace(t, map[string]string{"AGENTS.md": "x", "CLAUDE.md": "y"})
	var errw bytes.Buffer
	o := &Options{Workspace: ws, Stderr: &errw}
	packs := shippedPacksNamed(t, "claude", "copilot")
	if !o.applyRepoInstructions(repoInstrConfig("ignore", ""), "podman", packs) {
		t.Fatalf("refused: %s", errw.String())
	}
	o.discloseRepoInstructions(packs)
	out := errw.String()
	for _, want := range []string{
		"repo instructions: claude: ignoring 2 files",
		"through claudeMdExcludes",
		"repo instructions: copilot: NOT ignored",
		"agents that can ignore them: claude, codex, pi",
		"yolo config set repo_instructions.mode keep",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
}
