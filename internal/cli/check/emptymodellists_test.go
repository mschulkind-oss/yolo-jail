package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func emptyModelCheckPack(t *testing.T, contribution string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"name":"emptycheckfixture","contributes":[` + contribution + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runEmptyModelCheck(t *testing.T, contribution string) (string, *reporter) {
	t.Helper()
	pack := emptyModelCheckPack(t, contribution)
	packsFixture(t, `{"packs":["claude","bedrock","file://`+pack+`"]}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("claude", "bedrock"))
	return buf.String(), r
}

// A supplied provider list containing only another maker leaves Claude's native Bedrock
// model selection empty. The warning must be produced through sectionPacks and must not
// mistake a viable native default for the supplied list's callable entries.
func TestCheckWarnsWhenClaudeHasNoCallableSuppliedBedrockModel(t *testing.T) {
	out, r := runEmptyModelCheck(t,
		`{"kind":"models","provider":"bedrock","add":[{"id":"other-maker-model","vendor":"openai"}]}`)
	for _, want := range []string{
		`Model list: claude's supplied list for provider "bedrock" contributes no callable model`,
		`Profile "bedrock" governs claude here; the program is declared by pack "claude"`,
		`providers.bedrock.models`, `callable`, `literal profile model cannot run`,
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("the check should include %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "contributes no callable model"); n != 1 {
		t.Errorf("want exactly one empty-list diagnostic, got %d:\n%s", n, out)
	}
	if r.failed != 0 {
		t.Errorf("an empty callable list must WARN, never fail:\n%s", out)
	}
}

func TestCheckClaudeNativeSourceEmptyExceptionStaysSilent(t *testing.T) {
	packsFixture(t, `{"packs":["claude","bedrock"]}`)
	merged := useProfiles("claude", "bedrock")
	provider := jsonx.NewOrderedMap()
	provider.Set("models", jsonx.NewOrderedMap())
	providers := jsonx.NewOrderedMap()
	providers.Set("bedrock", provider)
	merged.Set("providers", providers)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, merged)
	if out := buf.String(); strings.Contains(out, "contributes no callable model") || r.failed != 0 {
		t.Fatalf("the declared native source-empty exception should be silent, not a failure:\n%s", out)
	}
}

func TestCheckNativeMakerFilterAndViaAcceptanceUseTheConfiguredProfile(t *testing.T) {
	company := emptyModelCheckPack(t,
		`{"kind":"models","provider":"bedrock","add":[{"id":"other-maker-model","vendor":"openai"}]}`)
	packsFixture(t, `{"packs":["claude","bedrock","file://`+company+`"]}`)
	for _, tc := range []struct {
		profile string
		want    int
	}{
		{"bedrock", 1},
		{"bedrock-bridge", 0},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, useProfiles("claude", tc.profile))
			out := buf.String()
			if got := strings.Count(out, "contributes no callable model"); got != tc.want || r.failed != 0 {
				t.Fatalf("profile %q empty-list warnings = %d, want %d; failures=%d:\n%s",
					tc.profile, got, tc.want, r.failed, out)
			}
		})
	}
}

// A final user string alias supplies a vendorless row with no model_options map. Exercise
// sectionPacks so the report's actual evaluator cannot dereference missing alias facts.
func TestCheckAcceptsVendorlessUserAliasWithoutModelOptions(t *testing.T) {
	packsFixture(t, `{"packs":["pi","bedrock"]}`)
	merged := useProfiles("pi", "bedrock")
	models := jsonx.NewOrderedMap()
	models.Set("default", "vendorless-model")
	entry := jsonx.NewOrderedMap()
	entry.Set("models", models)
	providers := jsonx.NewOrderedMap()
	providers.Set("bedrock", entry)
	merged.Set("providers", providers)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, merged)
	if out := buf.String(); strings.Contains(out, "contributes no callable model") || r.failed != 0 {
		t.Fatalf("vendorless rows must stay callable, without a refusal:\n%s", out)
	}
}
