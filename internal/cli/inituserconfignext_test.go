package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// inituserconfignext_test.go pins how `yolo init-user-config` ENDS. It used to end at
// `Created <path>`, though the file it writes selects no packs, so the user's next jail
// has no coding agent and nothing on screen says what to do about it
// (docs/reference/happy-path-principle.md, rule 2: a success points forward too).
// `yolo init` names its next command; this one now names the edit that chooses an
// agent, the check, and the launch.
//
// Everything runs through dispatchNative, the way `yolo init-user-config` is typed, so
// unwiring the command or dropping the lines fails here.

// nextStepAgent is the agent pack the steps name. It is the same example the empty-packs
// notice gives (config.NoPacksGuidance), so a new user hears one answer.
const nextStepAgent = "claude"

func TestInitUserConfigEndsWithTheNextStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
	if rc != 0 {
		t.Fatalf("`yolo init-user-config` exited %d:\n%s", rc, stdout)
	}
	p := paths.UserConfigPath()
	for _, want := range []string{
		"Created " + p + "\n",
		`"packs": ["` + nextStepAgent + `"],`,
		"yolo check",
		"yolo -- " + nextStepAgent,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output is missing %q:\n%s", want, stdout)
		}
	}
	// The edit says "that file", so the line just before the steps must be the one that
	// names it: nothing may come between them.
	if !strings.Contains(stdout, "Created "+p+"\nIt selects no packs") {
		t.Errorf("the steps do not follow the line naming the file to edit (%s):\n%s", p, stdout)
	}
}

// TestTheNamedEditSelectsTheAgent is rule 4 for the step above: following it must do what
// it says. It makes the edit exactly as printed, in the file the command wrote, and asks
// the loader every launch asks; then checks that the pack it selects ships the program
// the launch line runs, and that the check it names is a command.
func TestTheNamedEditSelectsTheAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if rc, out, _ := captureDispatchRC(t, []string{"init-user-config"}); rc != 0 {
		t.Fatalf("`yolo init-user-config` exited %d:\n%s", rc, out)
	}
	p := paths.UserConfigPath()
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// "right after the opening {", as the step says.
	edited := strings.Replace(string(body), "{", "{\n  "+`"packs": ["`+nextStepAgent+`"],`, 1)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	var problems []string
	entries, err := config.LoadPacks(func(s string) { problems = append(problems, s) })
	if err != nil || len(problems) > 0 {
		t.Fatalf("the edited config does not load: %v %v\n%s", err, problems, edited)
	}
	if !config.HasConfiguredPack(entries) {
		t.Fatalf("the edit the step names selects no pack:\n%s", edited)
	}

	raw, err := fs.ReadFile(packs.FS, nextStepAgent+"/pack.json")
	if err != nil {
		t.Fatalf("no shipped pack named %q: %v", nextStepAgent, err)
	}
	m, probs := packdecl.Decode(raw)
	if len(probs) > 0 {
		t.Fatalf("pack %q: %v", nextStepAgent, probs)
	}
	runs := false
	for _, c := range m.Contributes {
		if c.Kind == packdecl.KindProgram && c.Bin == nextStepAgent {
			runs = true
		}
	}
	if !runs {
		t.Errorf("pack %q installs no program %q, so `yolo -- %s` runs nothing it delivered",
			nextStepAgent, nextStepAgent, nextStepAgent)
	}
	if _, ok := registry["check"]; !ok {
		t.Error("the step names `yolo check`, which is not a command")
	}
}

// TestInitUserConfigOnAnExistingFile: a second run used to stop at `<path> already
// exists.` — and the likeliest reason to run it again is a jail with no agent. With no
// pack selected it gives the same steps; with one selected it does not repeat them.
func TestInitUserConfigOnAnExistingFile(t *testing.T) {
	t.Run("no pack selected", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if rc, out, _ := captureDispatchRC(t, []string{"init-user-config"}); rc != 0 {
			t.Fatalf("first run exited %d:\n%s", rc, out)
		}
		rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
		if rc != 0 {
			t.Fatalf("second run exited %d:\n%s", rc, stdout)
		}
		for _, want := range []string{
			paths.UserConfigPath() + " already exists.\nIt selects no packs",
			`"packs": ["` + nextStepAgent + `"],`,
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("the output is missing %q:\n%s", want, stdout)
			}
		}
	})
	t.Run("a pack selected", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		p := paths.UserConfigPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"packs": ["`+nextStepAgent+`"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
		if rc != 0 {
			t.Fatalf("exited %d:\n%s", rc, stdout)
		}
		if strings.Contains(stdout, "selects no packs") {
			t.Errorf("a config that selects a pack was told it selects none:\n%s", stdout)
		}
	})
}

// TestInitUserConfigOnAFileWithAnEmptyPacksList: an existing file can already WRITE an
// empty `packs` list. Adding a second `packs` line after the opening { then changes
// nothing, because the later key wins, so the step must point at the list that is there.
// The test follows the step as printed: it reads the location off the output, fills the
// list it names, and asks the loader whether that selects the pack.
func TestInitUserConfigOnAFileWithAnEmptyPacksList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{\n  \"kvm\": true,\n  \"packs\": []\n}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
	if rc != 0 {
		t.Fatalf("exited %d:\n%s", rc, stdout)
	}
	if strings.Contains(stdout, "right after the opening {") {
		t.Errorf("told to add a second `packs` line, which the existing one overrides:\n%s", stdout)
	}
	const at = "~/.config/yolo-jail/config.jsonc:3:12"
	if !strings.Contains(stdout, `"packs" list at `+at) || !strings.Contains(stdout, `["`+nextStepAgent+`"]`) {
		t.Fatalf("the step does not name the list at %s and what to make it:\n%s", at, stdout)
	}

	// Follow it: line 3, column 12 is where the list starts.
	lines := strings.SplitAfter(body, "\n")
	if !strings.HasPrefix(lines[2][11:], "[]") {
		t.Fatalf("%s does not hold the empty list in %q", at, body)
	}
	lines[2] = lines[2][:11] + `["` + nextStepAgent + `"]` + lines[2][13:]
	edited := strings.Join(lines, "")
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := config.LoadPacks(nil)
	if err != nil || !config.HasConfiguredPack(entries) {
		t.Fatalf("the edit the step names selects no pack (%v):\n%s", err, edited)
	}
}
