package naming

import (
	"regexp"
	"strings"
	"testing"
)

// goldenNames pins the container name for a corpus of already-resolved paths.
// Values are the observed output of the Python
// cli.runtime.container_name_for_workspace's sanitize+hash tail.
var goldenNames = map[string]string{
	"/home/matt/code/system/yolo-jail": "yolo-yolo-jail-a4bcac6e",
	"/srv/App":                         "yolo-app-2fe44894",
	"/srv/---":                         "yolo-jail-fbe71d46",
	"/":                                "yolo-jail-8a5edab2",
	// U+0130 (Turkish İ): Python .lower() expands to "i"+combining-dot, which
	// sanitizes to "i-"; a naive strings.ToLower would give "i". This is the
	// audit-confirmed frozen-contract divergence pyLower fixes.
	"/home/matt/aİb": "yolo-ai-b-4937a232",
}

func TestFromResolvedGolden(t *testing.T) {
	for in, want := range goldenNames {
		if got := FromResolved(in); got != want {
			t.Errorf("FromResolved(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPyLowerIsOnlyDivergence documents (and guards) the claim that U+0130 is
// the ONLY code point whose Python .lower() differs from what pyLower produces
// in a way that survives the [^a-z0-9-] sanitize. We can't run Python here, so
// we assert the specific known case and that pyLower agrees with strings.ToLower
// everywhere else that matters is covered by the golden tests's cross-language
// diff. This test pins the special-case itself.
func TestPyLowerU0130(t *testing.T) {
	san := regexp.MustCompile(`[^a-z0-9-]`)
	sanitize := func(s string) string { return san.ReplaceAllString(pyLower(s), "-") }
	if got := sanitize("aİb"); got != "ai-b" {
		t.Errorf("sanitize(aİb) = %q, want ai-b (Python .lower() expansion)", got)
	}
	// Non-U+0130 input must be untouched relative to plain ToLower.
	for _, s := range []string{"App", "café", "MixedCase", "日本語", "ABC-123"} {
		if pyLower(s) != strings.ToLower(s) {
			t.Errorf("pyLower(%q)=%q diverges from ToLower=%q (only U+0130 should)", s, pyLower(s), strings.ToLower(s))
		}
	}
}

func TestFromResolvedEmptyBasenameFallback(t *testing.T) {
	// All-punctuation basename -> "jail" fallback.
	if got := FromResolved("/srv/!!!"); !strings.HasPrefix(got, "yolo-jail-") {
		t.Errorf("all-punct basename got %q, want yolo-jail-<hash>", got)
	}
}

func TestFromResolvedTruncatesByRune(t *testing.T) {
	// 60 'x' -> truncated to 40 runes.
	got := FromResolved("/srv/" + strings.Repeat("x", 60))
	// yolo-<40 x's>-<hash>
	body := strings.TrimPrefix(got, "yolo-")
	seg := strings.SplitN(body, "-", 2)[0]
	if len([]rune(seg)) != 40 {
		t.Errorf("safe segment = %d runes, want 40", len([]rune(seg)))
	}
}
