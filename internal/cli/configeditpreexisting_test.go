package cli

import (
	"strings"
	"testing"
)

// manyGapsFragment declares four bedrock models with no price facts and two models whose maker
// is ambiguous: six problems of two kinds.
func manyGapsFragment() string {
	frag := pricelessBedrockFragment
	for _, id := range []string{"global.anthropic.claude-sonnet-5-5", "global.anthropic.claude-opus-5-5", "global.anthropic.claude-haiku-4-5"} {
		frag += "\n[providers.bedrock.models.\"" + id + "\"]\nname = \"" + id + "\"\ncontext = 1000000\n"
	}
	for _, id := range []string{"local-alpha", "local-beta"} {
		frag += "\n[providers.bedrock.models.\"" + id + "\"]\nname = \"" + id + "\"\ncontext = 8000\n" +
			"[providers.bedrock.models.\"" + id + "\".price]\ninput = 1.0\noutput = 2.0\ncache_read = 0.1\ncache_write = 1.2\n"
	}
	return frag
}

// Several pre-existing problems print one header, each kind's explanation and fix once, and every
// affected model row. Going through the verb pins writeConfigEditPreexisting's call site.
func TestConfigSetGroupsPreexistingProblemsByKind(t *testing.T) {
	gapModelFixture(t, manyGapsFragment())
	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "set", "--user", "resource_groups.default", `"batch-yolo.slice"`})
	})
	if rc != 0 {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	t.Logf("%s", errs)
	if n := strings.Count(errs, "warning:"); n != 1 {
		t.Fatalf("want exactly one warning header, got %d: %q", n, errs)
	}
	if !strings.Contains(errs, "warning: your config already has 6 problems that this edit left as they are:") {
		t.Fatalf("header missing: %q", errs)
	}
	for _, once := range []string{
		"add price with input, output, cache_read and cache_write", "maker is ambiguous; author vendor",
	} {
		if n := strings.Count(errs, once); n != 1 {
			t.Fatalf("%q appears %d times, want once: %q", once, n, errs)
		}
	}
	if n := strings.Count(errs, "a launch runs without it"); n != 2 {
		t.Fatalf("each kind's fix text should print once (2 kinds), got %d: %q", n, errs)
	}
	for _, id := range []string{"global.anthropic.claude-haiku-5-5", "global.anthropic.claude-sonnet-5-5",
		"global.anthropic.claude-opus-5-5", "global.anthropic.claude-haiku-4-5", "local-alpha", "local-beta"} {
		if n := strings.Count(errs, `model "`+id+`"`); n != 1 {
			t.Fatalf("model %s appears %d times, want once: %q", id, n, errs)
		}
	}
	if !strings.Contains(errs, "config.d/work.toml:") {
		t.Fatalf("locations missing: %q", errs)
	}
}

// A problem of no recognized shape is printed whole and never merged with another.
func TestGroupPreexistingProblemsNeverManglesAnUngroupableMessage(t *testing.T) {
	odd := []string{"config.network.mode: expected 'bridge' or 'host'", "config.network.mode: expected 'bridge' or 'host'"}
	groups := groupPreexistingProblems(odd)
	if len(groups) != 2 || groups[0].original[0] != odd[0] || groups[0].places != nil {
		t.Fatalf("ungroupable messages were merged or changed: %+v", groups)
	}
}
