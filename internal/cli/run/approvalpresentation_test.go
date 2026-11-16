package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestApprovalPresentationIncludesEveryGroupInBothViews(t *testing.T) {
	previous := presentationTree(t, `{"install":{"nix":["jq"]},"example":{"gone":true}}`)
	current := presentationTree(t, `{"install":{"nix":["jq","ripgrep"]},"example":{"added":{"z":1,"a":2}}}`)
	changes := config.BuildApprovalChangeSet(previous, current, false)
	lines := strings.Join(approvalPresentationLines(&changes, false, 0), "\n")
	for _, want := range []string{"Workspace config: 3 changes in 3 areas", "Interpreted", "Exact", "install: Nix packages and mise tools", "[1]", "[2]", "[3]", "adds 1 item: \"ripgrep\"", "gone: stops setting it", "example.added.a = 2", "example.added.z = 1"} {
		if !strings.Contains(lines, want) {
			t.Errorf("presentation missing %q:\n%s", want, lines)
		}
	}
	if strings.Count(lines, "[1]") != 2 || strings.Count(lines, "[2]") != 2 || strings.Count(lines, "[3]") != 2 {
		t.Fatalf("each group number must appear exactly once in each block:\n%s", lines)
	}
}

func TestApprovalPresentationUsesTOMLValidASCIIForValuesAndPaths(t *testing.T) {
	previous := jsonx.NewOrderedMap()
	current := jsonx.NewOrderedMap()
	current.Set("a.b", "emoji 😀 control \x1b bidi \u202e")
	changes := config.BuildApprovalChangeSet(previous, current, false)
	lines := strings.Join(approvalPresentationLines(&changes, false, 0), "\n")
	for _, want := range []string{`"a.b"`, `\U0001F600`, `\u001B`, `\u202E`} {
		if !strings.Contains(lines, want) {
			t.Errorf("presentation missing escaped %q:\n%s", want, lines)
		}
	}
	for _, forbidden := range []string{"😀", "\x1b", "\u202e", `\uD83D\uDE00`} {
		if strings.Contains(lines, forbidden) {
			t.Errorf("presentation contains unsafe/raw %q:\n%s", forbidden, lines)
		}
	}
	if !changes.ReplayVerified {
		t.Fatal("presentation fixture does not replay")
	}
}

func TestApprovalPresentationWrapsWithoutTruncatingOrSplittingEscapes(t *testing.T) {
	previous := jsonx.NewOrderedMap()
	current := jsonx.NewOrderedMap()
	current.Set("banner", strings.Repeat("!", 50)+"😀"+strings.Repeat("^", 50))
	changes := config.BuildApprovalChangeSet(previous, current, false)
	wrapped := strings.Join(approvalPresentationLines(&changes, false, 40), "\n")
	if !strings.Contains(wrapped, `\U0001F600`) || strings.Count(wrapped, "!") != 50 || strings.Count(wrapped, "^") != 50 {
		t.Fatalf("wrap truncated or lost value characters:\n%s", wrapped)
	}
	if strings.Contains(wrapped, `\U0001F6\n`) || strings.Contains(wrapped, `\U0001F60\n`) {
		t.Fatalf("wrap split a TOML escape:\n%s", wrapped)
	}
	if plain := strings.Join(approvalPresentationLines(&changes, false, 0), "\n"); !strings.Contains(plain, strings.Repeat("!", 50)) || !strings.Contains(plain, strings.Repeat("^", 50)) {
		t.Fatalf("non-TTY rendering hard-wrapped a value:\n%s", plain)
	}
}

func TestApprovalPresentationLabelsEveryWorkspaceSettableReadableRoot(t *testing.T) {
	missing := make([]string, 0)
	seen := make(map[string]bool)
	for _, key := range config.ReadableSchemaKeys() {
		if strings.ContainsAny(key.Path, ".*") || key.Scope == config.SchemaScopeUserOnly {
			continue
		}
		if seen[key.Path] {
			continue
		}
		seen[key.Path] = true
		if _, ok := approvalAreaLabels[key.Path]; !ok {
			missing = append(missing, key.Path)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("workspace-settable roots lack an accurate label or deliberate fallback: %v", missing)
	}
}

func presentationTree(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	value, err := jsonx.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	mapped, ok := value.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decoded %T, want ordered map", value)
	}
	return mapped
}
