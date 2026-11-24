package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func repoInstrTable(pairs ...any) *jsonx.OrderedMap {
	table := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		table.Set(pairs[i].(string), pairs[i+1])
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("repo_instructions", table)
	return cfg
}

func TestRepoInstructionsDefaultsToKeep(t *testing.T) {
	for name, cfg := range map[string]*jsonx.OrderedMap{"nil": nil, "empty": jsonx.NewOrderedMap(), "no mode": repoInstrTable()} {
		if mode, with := RepoInstructions(cfg); mode != RepoInstructionsModeKeep || with != "" {
			t.Errorf("%s: %q %q", name, mode, with)
		}
	}
	if mode, with := RepoInstructions(repoInstrTable("mode", "replace", "replace_with", "a.md")); mode != "replace" || with != "a.md" {
		t.Errorf("read %q %q", mode, with)
	}
}

func TestValidateRepoInstructions(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *jsonx.OrderedMap
		want string // "" for valid
	}{
		"keep":                        {repoInstrTable("mode", "keep"), ""},
		"ignore":                      {repoInstrTable("mode", "ignore"), ""},
		"replace with a file":         {repoInstrTable("mode", "replace", "replace_with", "~/r.md"), ""},
		"replace_with alone":          {repoInstrTable("replace_with", "r.md"), ""},
		"a mode that does not exist":  {repoInstrTable("mode", "mask"), "expected one of keep, ignore, replace"},
		"a mode that is not a string": {repoInstrTable("mode", true), "expected one of keep, ignore, replace"},
		"replace_with under ignore":   {repoInstrTable("mode", "ignore", "replace_with", "r.md"), "yolo config unset repo_instructions.replace_with"},
		"replace_with under keep":     {repoInstrTable("mode", "keep", "replace_with", "r.md"), "only the `replace` mode reads it"},
		"an empty replace_with":       {repoInstrTable("mode", "replace", "replace_with", " "), "expected the path of a markdown file"},
		"an unknown key":              {repoInstrTable("mod", "ignore"), "mod"},
	} {
		var errs []string
		validateRepoInstructions(tc.cfg, &errs)
		joined := strings.Join(errs, "\n")
		if tc.want == "" && joined != "" {
			t.Errorf("%s: unexpected %q", name, joined)
		}
		if tc.want != "" && !strings.Contains(joined, tc.want) {
			t.Errorf("%s: %q lacks %q", name, joined, tc.want)
		}
	}
	var errs []string
	notATable := jsonx.NewOrderedMap()
	notATable.Set("repo_instructions", "ignore")
	validateRepoInstructions(notATable, &errs)
	if len(errs) != 1 || !strings.Contains(errs[0], "expected a table") {
		t.Errorf("a scalar table = %v", errs)
	}
}

// The validator is wired into the whole-config pass, not only callable.
func TestTheWholeConfigValidatorRunsTheRepoInstructionsCheck(t *testing.T) {
	errs, _ := ValidateConfig(repoInstrTable("mode", "mask"), t.TempDir(), nil)
	if !strings.Contains(strings.Join(errs, "\n"), "repo_instructions.mode") {
		t.Errorf("ValidateConfig missed a bad mode: %v", errs)
	}
}
