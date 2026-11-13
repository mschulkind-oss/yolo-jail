package cli

import (
	"errors"
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

// inituserconfignext_test.go pins what `yolo init-user-config` WRITES and how it ENDS. It
// writes config.toml, the format every launch reads first, so a new user is not onboarded onto
// the legacy format the launch then offers to convert (LC-D12,
// docs/reference/config/rulings.md). It used to end at `Created <path>`, though the file
// it writes selects no packs, so the user's next jail has no coding agent and nothing on
// screen says what to do about it (docs/reference/happy-path-principle.md, rule 2). It now
// names the verb that chooses an agent, `yolo pack add`, never a `packs` line to paste, then
// the check and the launch.
//
// Everything runs through dispatchNative, the way `yolo init-user-config` is typed, so
// unwiring the command or dropping the lines fails here.

// nextStepAgent is the agent pack the steps name. It is the same example the empty-packs
// notice gives (config.NoPacksGuidance), so a new user hears one answer.
const nextStepAgent = "claude"

// initUserConfigHome points HOME at a fresh, symlink-resolved home outside any jail, so the
// config verbs edit it as they would on a host.
func initUserConfigHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	return home
}

func TestInitUserConfigWritesTOMLAndEndsWithTheNextStep(t *testing.T) {
	home := initUserConfigHome(t)
	rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
	if rc != 0 {
		t.Fatalf("`yolo init-user-config` exited %d:\n%s", rc, stdout)
	}
	p := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("`yolo init-user-config` wrote no %s: %v\n%s", p, err, stdout)
	}
	for _, legacy := range []string{paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(p), legacy)); err == nil {
			t.Errorf("`yolo init-user-config` wrote the legacy %s", legacy)
		}
	}
	for _, want := range []string{
		"Created " + p + "\nIt selects no packs",
		"yolo pack add " + nextStepAgent + "\n",
		"yolo check",
		"yolo -- " + nextStepAgent,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, `"packs"`) {
		t.Errorf("the next step spells a `packs` snippet to paste instead of naming the verb:\n%s", stdout)
	}
	// The file is one the loader reads without a warning: a commented template that selects
	// nothing, in the format the launch does not offer to convert.
	var problems []string
	if _, err := config.LoadPacks(func(s string) { problems = append(problems, s) }); err != nil || len(problems) > 0 {
		t.Fatalf("the written config does not load cleanly: %v %v", err, problems)
	}
}

// TestTheNamedStepSelectsTheAgent is rule 4 for the step above: following it must do what it
// says. It runs the verb exactly as printed against the file the command wrote, and asks the
// loader every launch asks; then checks that the pack it selects ships the program the launch
// line runs, and that the check it names is a command.
func TestTheNamedStepSelectsTheAgent(t *testing.T) {
	initUserConfigHome(t)
	if rc, out, _ := captureDispatchRC(t, []string{"init-user-config"}); rc != 0 {
		t.Fatalf("`yolo init-user-config` exited %d:\n%s", rc, out)
	}
	followPackAddStep(t)

	decode := packdecl.DecodeTOML
	raw, err := fs.ReadFile(packs.FS, nextStepAgent+"/"+packdecl.ManifestTOMLName)
	if errors.Is(err, fs.ErrNotExist) {
		decode = packdecl.Decode
		raw, err = fs.ReadFile(packs.FS, nextStepAgent+"/"+packdecl.ManifestLegacyJSONName)
	}
	if err != nil {
		t.Fatalf("no shipped pack named %q: %v", nextStepAgent, err)
	}
	m, probs := decode(raw)
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

// followPackAddStep runs `yolo pack add <agent>` as the step prints it and asks the loader
// whether the user config now selects a pack.
func followPackAddStep(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	if rc, out, errOut := captureDispatchRC(t, []string{"pack", "add", nextStepAgent}); rc != 0 {
		t.Fatalf("the step's `yolo pack add %s` exited %d:\n%s%s", nextStepAgent, rc, out, errOut)
	}
	var problems []string
	entries, err := config.LoadPacks(func(s string) { problems = append(problems, s) })
	if err != nil || len(problems) > 0 {
		t.Fatalf("the config the step edited does not load: %v %v", err, problems)
	}
	if !config.HasConfiguredPack(entries) {
		t.Fatal("the step `yolo pack add` named selects no pack")
	}
}

// TestInitUserConfigOnAnExistingFile: a second run used to stop at `<path> already
// exists.` — and the likeliest reason to run it again is a jail with no agent. With no
// pack selected it gives the same steps; with one selected it does not repeat them.
func TestInitUserConfigOnAnExistingFile(t *testing.T) {
	t.Run("no pack selected", func(t *testing.T) {
		initUserConfigHome(t)
		if rc, out, _ := captureDispatchRC(t, []string{"init-user-config"}); rc != 0 {
			t.Fatalf("first run exited %d:\n%s", rc, out)
		}
		rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
		if rc != 0 {
			t.Fatalf("second run exited %d:\n%s", rc, stdout)
		}
		for _, want := range []string{
			paths.UserConfigDisplayPath() + " already exists.\nIt selects no packs",
			"yolo pack add " + nextStepAgent + "\n",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("the output is missing %q:\n%s", want, stdout)
			}
		}
	})
	t.Run("a pack selected", func(t *testing.T) {
		home := initUserConfigHome(t)
		p := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
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

// TestInitUserConfigOnALegacyFileWithAnEmptyPacksList: an existing legacy file can already
// WRITE an empty `packs` list, where a second `packs` line pasted after the opening { would
// change nothing, the later key winning. The step is the verb, which appends to the list
// that is there, in the file's own format, and writes no config.toml beside it.
func TestInitUserConfigOnALegacyFileWithAnEmptyPacksList(t *testing.T) {
	home := initUserConfigHome(t)
	p := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"kvm\": true,\n  \"packs\": []\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, stdout, _ := captureDispatchRC(t, []string{"init-user-config"})
	if rc != 0 {
		t.Fatalf("exited %d:\n%s", rc, stdout)
	}
	if !strings.Contains(stdout, p+" already exists.\nIt selects no packs") ||
		!strings.Contains(stdout, "yolo pack add "+nextStepAgent+"\n") {
		t.Fatalf("the output does not name the existing file and the verb:\n%s", stdout)
	}
	followPackAddStep(t)
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), paths.UserConfigTOMLName)); err == nil {
		t.Error("following the step wrote a config.toml beside the legacy config.jsonc")
	}
}
