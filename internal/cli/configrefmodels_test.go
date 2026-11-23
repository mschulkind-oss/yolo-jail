package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const scopedModelsTOMLExample = `[[models]]
provider = "bedrock"
only = ["example.fable", "example.sonnet"]
agents = ["claude"]`

func modelsDocumentationEntry(t *testing.T, text, start, next string) string {
	t.Helper()
	at := strings.Index(text, start)
	if at < 0 {
		t.Fatalf("documentation has no models entry beginning %q", start)
	}
	entry := text[at:]
	end := strings.Index(entry, next)
	if end < 0 {
		t.Fatalf("documentation models entry has no following boundary %q", next)
	}
	return entry[:end]
}

func normalizedDocumentation(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func TestConfigRefDocumentsScopedModelsOnlyAudience(t *testing.T) {
	var rc int
	out := captureStdout(t, func() { rc = runConfigRef([]string{"config-ref"}) })
	if rc != 0 {
		t.Fatalf("config-ref rc = %d", rc)
	}
	entry := modelsDocumentationEntry(t, out, "\n      models ", "\n      adapter ")
	plain := normalizedDocumentation(entry)
	for _, want := range []string{
		"agents:[bin...]",
		"agents = [\"claude\"]",
		"agents is optional and may appear only with only",
		"without agents, only keeps the provider-wide list",
		"matches a program's bin and derive identity exactly, not its pack name",
		"changes a consumer menu only; it does not change shared provider routes",
		"only keeps IDs; it never adds them",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("config-ref models entry is missing %q:\n%s", want, entry)
		}
	}
	if !strings.Contains(plain, scopedModelsTOMLExample) {
		t.Fatalf("config-ref models entry does not contain the documented TOML example:\n%s", entry)
	}

	manifest, problems := packdecl.DecodeTOML([]byte(scopedModelsTOMLExample))
	if len(problems) != 0 || manifest == nil {
		t.Fatalf("documented models TOML example is refused by packdecl: %v", problems)
	}
	got := manifest.ModelsContributions()
	if len(got) != 1 || got[0].Provider != "bedrock" ||
		len(got[0].Only) != 2 || got[0].Only[0] != "example.fable" || got[0].Only[1] != "example.sonnet" ||
		len(got[0].Agents) != 1 || got[0].Agents[0] != "claude" {
		t.Fatalf("documented models TOML decoded to %+v, want its provider, only IDs, and claude audience", got)
	}
}

func TestPackHelpDocumentsScopedModelsOnlyAudience(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"--help"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack --help rc=%d: %s", rc, errw.String())
	}
	entry := modelsDocumentationEntry(t, out.String(), "\n  models ", "\n  adapter ")
	plain := normalizedDocumentation(entry)
	for _, want := range []string{
		"agents",
		"only",
		"without agents, the existing provider-wide list is used",
		"yolo config-ref",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("pack --help models entry is missing %q:\n%s", want, entry)
		}
	}
}
