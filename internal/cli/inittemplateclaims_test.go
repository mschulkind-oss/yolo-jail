package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The workspace config `yolo init` writes is the third surface that states the blocked-tool
// default, after config_ref.txt (TestConfigRefAgreesWithTheBlockedToolDefault) and the init
// briefing (TestBriefingAgreesWithTheBlockedToolDefault). It kept saying "Defaults (no config
// needed): grep is blocked ... find is blocked unconditionally" after 2026-09-04, when blocking
// became opt-in through the guardrails pack, and the repository's own committed
// yolo-jail.jsonc, which started life as this template, carried the sentence with it.
//
// Both tests read the file Init WRITES rather than the embedded template, so they fail if the
// template stops reaching the output as surely as if its words go stale.

func initWorkspaceConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if code := Init(dir, nil, &bytes.Buffer{}, false); code != 0 {
		t.Fatalf("Init exited %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "yolo-jail.jsonc"))
	if err != nil {
		t.Fatalf("Init wrote no yolo-jail.jsonc: %v", err)
	}
	return string(b)
}

// commentBlockBefore returns the run of `//` comment lines immediately above the first line
// containing marker — the prose a reader sees next to that key — as one lowercased line, with
// the comment markers stripped and the wrapping undone, so a phrase can be matched wherever
// the text happens to break.
func commentBlockBefore(t *testing.T, body, marker string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("the config `yolo init` writes has no %q — this test has lost its subject", marker)
	}
	start := at
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
		start--
	}
	var words []string
	for _, l := range lines[start:at] {
		words = append(words, strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "//"))...)
	}
	return strings.ToLower(strings.Join(words, " "))
}

func TestInitTemplateAgreesWithTheBlockedToolDefault(t *testing.T) {
	block := commentBlockBefore(t, initWorkspaceConfig(t), `// "security": {`)

	defaults := config.DefaultBlockedTools()
	if len(defaults) == 0 {
		for _, stale := range []string{"defaults (no", "is blocked unconditionally", "the defaults are sane"} {
			if strings.Contains(block, stale) {
				t.Errorf("the config `yolo init` writes says %q, but the default blocked list is "+
					"empty — blocking is opt-in via the guardrails pack or security.blocked_tools:\n%s",
					stale, block)
			}
		}
		if !strings.Contains(block, "nothing is blocked by default") {
			t.Errorf("the default blocked list is empty and the config `yolo init` writes does "+
				"not say so — a reader who remembers the old default will assume it holds:\n%s", block)
		}
		if !strings.Contains(block, `"guardrails"`) {
			t.Errorf("the blocked-tools comment no longer names the guardrails pack, which is "+
				"where the old default went:\n%s", block)
		}
		return
	}
	for _, tool := range defaults {
		if !strings.Contains(block, strings.ToLower(tool)) {
			t.Errorf("%q is blocked by default and the config `yolo init` writes never names it:\n%s",
				tool, block)
		}
	}
}

// TestInitTemplateNamesEveryRuntime ties the runtime comment to the list validation accepts.
// It offered "podman" or "container" long after macos-user shipped, and once offered docker,
// which validation now refuses.
func TestInitTemplateNamesEveryRuntime(t *testing.T) {
	block := commentBlockBefore(t, initWorkspaceConfig(t), `// "runtime":`)
	for _, rt := range paths.AllRuntimes {
		if !strings.Contains(block, `"`+rt+`"`) {
			t.Errorf("the runtime comment in the config `yolo init` writes does not offer %q, a "+
				"value config validation accepts:\n%s", rt, block)
		}
	}
	if strings.Contains(block, `"docker"`) {
		t.Errorf("the runtime comment offers \"docker\", which config validation refuses:\n%s", block)
	}
}
