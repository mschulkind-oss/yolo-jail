package capture

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/BurntSushi/toml"
)

// gatetoolchain_pin_test.go pins the toolchain half of `just check-ci`, the gate the
// pre-commit hook and CI's check-go job both run.
//
// A fresh clone gets its tools from `just setup`, which is `mise install` over mise.toml.
// CI installs the same tools its own way (setup-go, setup-uv, a staticcheck `go install`),
// so a tool the gate calls and mise.toml forgets is invisible there and in any jail whose
// owner has it globally — and breaks only for the next contributor, on their first
// `just check-ci`. That happened with uv: lint-ci ran `uvx vantage-check` for weeks while
// mise.toml declared no uv, and it kept working on the maintainer's machines only because uv
// was already installed there some other way.

// gateCommands maps a command the gate recipes run to the mise.toml tool that provides it.
// python3 and sh are absent on purpose: they are system prerequisites the README names, not
// mise tools.
var gateCommands = map[string]string{
	"go":          "go",
	"staticcheck": "go:honnef.co/go/tools/cmd/staticcheck",
	"uvx":         "uv",
}

// gateRecipes are the recipes `just check-ci` reaches: check-ci -> lint-ci (-> lint) and
// test-fast.
var gateRecipes = []string{"lint", "lint-ci", "test-fast"}

func TestEveryToolTheGateRunsIsDeclaredInMiseToml(t *testing.T) {
	var mise struct {
		Tools map[string]any `toml:"tools"`
	}
	raw, err := os.ReadFile(filepath.Join(repoRootDir(t), "mise.toml"))
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}
	if err := toml.Unmarshal(raw, &mise); err != nil {
		t.Fatalf("parse mise.toml: %v", err)
	}

	seen := 0
	for _, recipe := range gateRecipes {
		body := justRecipeBody(t, recipe)
		for cmd, tool := range gateCommands {
			// The command at the start of a recipe line, after any VAR=value assignments.
			re := regexp.MustCompile(`(?m)^\s*(?:[A-Z_]+=\S+\s+)*` + regexp.QuoteMeta(cmd) + `\s`)
			if !re.MatchString(body) {
				continue
			}
			seen++
			if _, ok := mise.Tools[tool]; !ok {
				t.Errorf("the %q recipe runs %s, and mise.toml declares no %q tool, so `just setup` "+
					"on a fresh clone leaves the gate unable to run it", recipe, cmd, tool)
			}
		}
	}
	if seen == 0 {
		t.Fatal("none of the gate recipes runs any command in gateCommands — the recipes were " +
			"restructured and this pin now checks nothing; point it at what they run instead")
	}
}
