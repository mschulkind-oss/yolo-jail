package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// undeclaredbuiltinprofile_test.go pins that the undeclared-profile refusal for a name an agent
// has built in (packload.UndeclaredProfileHelp) gives commands that WORK: run exactly as printed,
// through `yolo config set`, against a TOML user config, they leave the name declared, so the
// launch's declaration check (config.UndeclaredProfileProblems, which `yolo check` predicts) no
// longer refuses it.

// undeclaredProblems is the launch's undeclared-profile check over the config in the current
// home, as `yolo check` asks it.
func undeclaredProblems(t *testing.T, workspace string) []string {
	t.Helper()
	cfg, _, err := config.LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	selected, complete := config.UserScopeSelectedPacks()
	if !complete {
		t.Fatal("the selected packs could not all be read")
	}
	profiles, err := config.LoadSelectableProfiles(nil, cfg, selected)
	if err != nil {
		t.Fatalf("selectable profiles: %v", err)
	}
	return config.UndeclaredProfileProblems(config.ConfigProfileFold(cfg, selected), 0, profiles,
		config.ReplacedNameRefusals(cfg, selected, profiles), selected, packload.Embedded())
}

func TestTheUndeclaredBuiltInNameCommandsDeclareIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("YOLO_VERSION", "") // user-scope edits run on the host, which is what this test is
	workspace := t.TempDir()
	t.Chdir(workspace)
	userConfig := filepath.Join(home, ".config", "yolo-jail", "config.toml")
	writeFile(t, userConfig, "packs = [\"pi\"]\n\n[active_provider]\npi = \"groq\"\n")

	problems := undeclaredProblems(t, workspace)
	if len(problems) != 1 || !strings.Contains(problems[0], `no profile named "groq" is declared`) {
		t.Fatalf("the fixture must be refused as undeclared, got %q", problems)
	}
	var commands [][]string
	for _, line := range strings.Split(problems[0], "\n") {
		if rest, ok := strings.CutPrefix(line, "  yolo config "); ok {
			commands = append(commands, strings.Fields(rest))
		}
	}
	if len(commands) == 0 {
		t.Fatalf("the refusal names no `yolo config` command:\n%s", problems[0])
	}
	for _, args := range commands {
		var out, errw bytes.Buffer
		if rc := runConfigEditWith(args, &out, &errw, workspace); rc != 0 {
			t.Fatalf("yolo config %s: rc=%d\n%s%s", strings.Join(args, " "), rc, out.String(), errw.String())
		}
	}
	if problems := undeclaredProblems(t, workspace); len(problems) != 0 {
		data, _ := os.ReadFile(userConfig)
		t.Fatalf("after the printed commands groq is still refused: %q\nuser config:\n%s", problems, data)
	}
}
