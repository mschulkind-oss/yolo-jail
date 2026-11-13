package cli

// requestcheck_test.go pins that `yolo request`'s config-only post-write check reads the readable
// (TOML) config the launch reads: the user's config.toml and config.d fragment with `[allow]`,
// and the project's yolo-jail.toml with `[requests]` (docs/reference/config/rulings.md#RC-D12, RC-D17).

import (
	"strings"
	"testing"
)

func TestRequestsPostWriteCheckReadsTheTOMLConfigs(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	rc, out, errs := captureStd(t, func() int { return registry["request"]([]string{"request", "git", "push", "main"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	if !strings.Contains(out, "Config Check") || !strings.Contains(out, "Merged config is semantically valid") {
		t.Fatalf("request post-write check did not validate the merged config: %s", out)
	}
	for _, want := range []string{
		"Parsed user config: " + fx.userConfig,
		"Parsed workspace config: " + fx.project,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the post-write check lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{
		"No user config found",
		"No workspace",
		"config.requests: unknown key",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("the post-write check says %q:\n%s", bad, out)
		}
	}
}
