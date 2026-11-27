package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// `yolo config set|unset|why repo_instructions.*` is the path every piece of advice about the key
// names (the validator, the disclosure, the refusals), so it must exist for both keys and round-trip
// through the loader (docs/reference/workspace-instruction-files.md §3.1).
func TestRepoInstructionsSetUnsetRoundTripsThroughTheLoader(t *testing.T) {
	user, ws := genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "repo_instructions.mode", "replace", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	rc, out, errs = runConfigVerb(t, "set", "repo_instructions.replace_with", "~/rules/mine.md", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, "[repo_instructions]") || !strings.Contains(got, `mode = "replace"`) ||
		!strings.Contains(got, `replace_with = "~/rules/mine.md"`) {
		t.Fatalf("user config:\n%s", got)
	}
	cfg, _, err := config.LoadConfigWithSources(ws.Root, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if mode, with := config.RepoInstructions(cfg); mode != "replace" || with != "~/rules/mine.md" {
		t.Errorf("loaded table = %q %q", mode, with)
	}
	rc, out, errs = runConfigVerb(t, "why", "repo_instructions.mode")
	if rc != 0 || !strings.Contains(out+errs, "set in  ~/.config/yolo-jail/config.toml") {
		t.Errorf("why names no layer: rc=%d out=%q err=%q", rc, out, errs)
	}
	rc, out, errs = runConfigVerb(t, "unset", "repo_instructions.replace_with", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
}

func TestRepoInstructionsRefusesAModeThatDoesNotExist(t *testing.T) {
	genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	rc, out, errs := runConfigVerb(t, "set", "repo_instructions.mode", "mask", "--user")
	if rc == 0 || !strings.Contains(out+errs, "expected one of keep, ignore, replace") {
		t.Errorf("rc=%d out=%q err=%q, want the vocabulary named", rc, out, errs)
	}
}
