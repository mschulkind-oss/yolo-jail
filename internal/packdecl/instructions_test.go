package packdecl

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

const claudeInstructionsTOML = `[[instructions]]
agent = "claude"
bin = "claude"
reads = ["AGENTS.md", "CLAUDE.md"]
ignore = { list = { surface = "claude/settings", path = "/claudeMdExcludes", add = ["${workspace}/**/AGENTS.md", "${workspace}/**/CLAUDE.md"] }, host_launch = ["--settings", '{"claudeMdExcludes":${entries_json}}'] }
`

const piInstructionsTOML = `[[instructions]]
agent = "pi"
bin = "pi"
reads = ["AGENTS.md"]
ignore = { launch = { flags = ["--no-context-files"] }, still_read = [".pi/APPEND_SYSTEM.md"] }
redeliver = { flag = "--append-system-prompt", file = ".pi/agent/AGENTS.md" }
`

func mustInstructions(t *testing.T, data string) Instructions {
	t.Helper()
	m, problems := DecodeTOML([]byte(data))
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	got := m.InstructionsContributions()
	if len(got) != 1 {
		t.Fatalf("InstructionsContributions = %+v, want one", got)
	}
	return got[0]
}

func TestInstructionsListIsAnchoredAtTheWorkspaceAndInertWhenKept(t *testing.T) {
	d := mustInstructions(t, claudeInstructionsTOML)
	sel := &InstructionsSelection{Mode: ModeIgnore, Workspace: "/workspace", Home: "/home/agent"}
	surface, path, add, ok := d.ListFor(sel)
	if !ok || surface != "claude/settings" || path != "/claudeMdExcludes" {
		t.Fatalf("ListFor = %q %q %v", surface, path, ok)
	}
	if want := []string{"/workspace/**/AGENTS.md", "/workspace/**/CLAUDE.md"}; !reflect.DeepEqual(add, want) {
		t.Errorf("entries = %v, want %v", add, want)
	}
	for name, s := range map[string]*InstructionsSelection{
		"nil": nil, "keep": {Mode: ModeKeep, Workspace: "/workspace"}, "host": {Mode: ModeIgnore, Workspace: "/w", Host: true},
	} {
		if _, _, _, ok := d.ListFor(s); ok {
			t.Errorf("%s selection placed a list", name)
		}
	}
}

func TestInstructionsHostLaunchCarriesTheEntriesAsJSON(t *testing.T) {
	d := mustInstructions(t, claudeInstructionsTOML)
	bin, flags := d.LaunchFor(&InstructionsSelection{Mode: ModeReplace, Workspace: "/home/me/proj", Home: "/home/me", Host: true})
	want := []string{"--settings", `{"claudeMdExcludes":["/home/me/proj/**/AGENTS.md","/home/me/proj/**/CLAUDE.md"]}`}
	if bin != "claude" || !reflect.DeepEqual(flags, want) {
		t.Errorf("host launch = %q %q, want claude %q", bin, flags, want)
	}
	if _, flags := d.LaunchFor(&InstructionsSelection{Mode: ModeIgnore, Workspace: "/workspace"}); len(flags) != 0 {
		t.Errorf("a jail got host_launch flags %v; its list is rendered into the surface instead", flags)
	}
}

func TestInstructionsLaunchRedeliversTheBriefingFile(t *testing.T) {
	d := mustInstructions(t, piInstructionsTOML)
	bin, flags := d.LaunchFor(&InstructionsSelection{Mode: ModeIgnore, Workspace: "/workspace", Home: "/home/agent/"})
	want := []string{"--no-context-files", "--append-system-prompt", "/home/agent/.pi/agent/AGENTS.md"}
	if bin != "pi" || !reflect.DeepEqual(flags, want) {
		t.Errorf("launch = %q %q, want pi %q", bin, flags, want)
	}
	if _, flags := d.LaunchFor(nil); len(flags) != 0 {
		t.Errorf("a launch that keeps the files got flags %v", flags)
	}
	if m := d.Mechanism(nil); m != "--no-context-files" {
		t.Errorf("Mechanism = %q", m)
	}
}

// At the host the briefing file exists only after `yolo host apply` wrote it, and a program that takes
// "text or a file" would read a path naming nothing as the text itself: not handed when missing.
func TestHostRedeliveryNamesOnlyAFileThatExists(t *testing.T) {
	d := mustInstructions(t, piInstructionsTOML)
	home := t.TempDir()
	sel := &InstructionsSelection{Mode: ModeIgnore, Workspace: "/w", Home: home, Host: true}
	if _, flags := d.LaunchFor(sel); !reflect.DeepEqual(flags, []string{"--no-context-files"}) {
		t.Errorf("with no briefing file: %v", flags)
	}
	if err := os.MkdirAll(home+"/.pi/agent", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/.pi/agent/AGENTS.md", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"--no-context-files", "--append-system-prompt", home + "/.pi/agent/AGENTS.md"}
	if _, flags := d.LaunchFor(sel); !reflect.DeepEqual(flags, want) {
		t.Errorf("with the briefing file: %v, want %v", flags, want)
	}
}

func TestInstructionsSetBodyIsTheManagedLayer(t *testing.T) {
	d := mustInstructions(t, `[[instructions]]
agent = "codex"
bin = "codex"
reads = ["AGENTS.md"]
ignore = { set = { surface = "codex/config", config = { project_doc_max_bytes = 0 } }, host_launch = ["-c", "project_doc_max_bytes=0"] }
`)
	surface, body, ok := d.SetFor(&InstructionsSelection{Mode: ModeIgnore})
	if !ok || surface != "codex/config" || string(body) != `{"managed":{"project_doc_max_bytes":0}}` {
		t.Errorf("SetFor = %q %s %v", surface, body, ok)
	}
	if _, flags := d.LaunchFor(&InstructionsSelection{Mode: ModeIgnore, Host: true}); !reflect.DeepEqual(flags, []string{"-c", "project_doc_max_bytes=0"}) {
		t.Errorf("host flags = %v", flags)
	}
}

func TestAnAgentWithNoMechanismIsUnsupportedAndHandsNothing(t *testing.T) {
	d := mustInstructions(t, `[[instructions]]
agent = "agy"
bin = "agy"
reads = ["GEMINI.md", "AGENTS.md"]
`)
	if d.Supported() {
		t.Fatal("a declaration with no ignore reads as supported")
	}
	sel := &InstructionsSelection{Mode: ModeIgnore, Workspace: "/workspace", Home: "/home/agent"}
	if _, _, _, ok := d.ListFor(sel); ok {
		t.Error("an unsupported agent placed a list")
	}
	if _, _, ok := d.SetFor(sel); ok {
		t.Error("an unsupported agent placed a set")
	}
	if _, flags := d.LaunchFor(sel); len(flags) != 0 {
		t.Errorf("an unsupported agent was handed flags %v", flags)
	}
}

func TestInstructionsProblems(t *testing.T) {
	for name, tc := range map[string]struct{ data, want string }{
		"no reads": {`[[instructions]]
agent = "x"
`, `non-empty "reads"`},
		"no agent": {`[[instructions]]
reads = ["A.md"]
`, `needs "agent"`},
		"absolute read": {`[[instructions]]
agent = "x"
reads = ["/etc/passwd"]
`, "workspace-relative"},
		"two mechanisms": {`[[instructions]]
agent = "x"
bin = "x"
reads = ["A.md"]
ignore = { launch = { flags = ["-n"] }, set = { surface = "x/y", config = { k = 1 } } }
`, "at most one"},
		"launch without a bin": {`[[instructions]]
agent = "x"
reads = ["A.md"]
ignore = { launch = { flags = ["-n"] } }
`, `"bin"`},
		"redeliver without a launch": {`[[instructions]]
agent = "x"
bin = "x"
reads = ["A.md"]
ignore = { set = { surface = "x/y", config = { k = 1 } } }
redeliver = { flag = "--f", file = ".x/A.md" }
`, `needs "ignore.launch"`},
		"host_launch alone": {`[[instructions]]
agent = "x"
bin = "x"
reads = ["A.md"]
ignore = { host_launch = ["-n"] }
`, "needs one of them"},
		"declared twice": {`[[instructions]]
agent = "x"
reads = ["A.md"]

[[instructions]]
agent = "x"
reads = ["B.md"]
`, "declared twice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := DecodeTOML([]byte(tc.data))
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

func TestInstructionsFieldsAreRefusedOnAnotherKind(t *testing.T) {
	_, problems := DecodeTOML([]byte("[[env]]\nreads = [\"AGENTS.md\"]\n[env.vars]\nA = \"1\"\n"))
	if !strings.Contains(strings.Join(problems, "\n"), `does not take "reads"`) {
		t.Errorf("problems = %v, want the stray reads refused", problems)
	}
}
