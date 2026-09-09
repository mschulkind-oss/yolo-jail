package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestConfigRefAgreesWithTheBlockedToolDefault ties one prose claim to the
// function that decides it.
//
// `config_ref.txt` said "By default, grep is replaced by rg and find by fd" for
// five days after 2026-09-04, when blocking became opt-in through the guardrails
// pack and config.DefaultBlockedTools() went empty. That is the CLI's own concept
// surface — the thing self-documenting-cli.md exists to keep true — telling a
// user something false about their jail.
//
// The general class (every sentence in config_ref.txt vs. the code) is not
// mechanically checkable. This one is, because the default is a function: if the
// list is empty, the prose may not promise a block; if it is ever non-empty
// again, the prose must name what it blocks. Both directions fail here.
func TestConfigRefAgreesWithTheBlockedToolDefault(t *testing.T) {
	b, err := os.ReadFile("config_ref.txt")
	if err != nil {
		t.Fatalf("read config_ref.txt: %v", err)
	}
	ref := string(b)

	const heading = "[bold]Blocked Tools[/bold]"
	i := strings.Index(ref, heading)
	if i < 0 {
		t.Fatalf("no %q section in config_ref.txt — this test has lost its subject "+
			"and is now vacuous; repoint it at wherever blocking is explained", heading)
	}
	rest := ref[i+len(heading):]
	if j := strings.Index(rest, "[bold]"); j >= 0 {
		rest = rest[:j]
	}
	section := strings.ToLower(rest)

	defaults := config.DefaultBlockedTools()

	if len(defaults) == 0 {
		// The prose must not promise a block that does not happen.
		if strings.Contains(section, "by default, grep is replaced") {
			t.Error("config_ref.txt promises grep/find are replaced BY DEFAULT, but " +
				"the default blocked list is empty — blocking is opt-in via the " +
				"guardrails pack or security.blocked_tools since 2026-09-04")
		}
		// And it must say so, or a reader assumes the old behavior from silence.
		if !strings.Contains(section, "nothing is blocked by default") {
			t.Error("the default blocked list is empty and config_ref.txt does not say " +
				"so plainly — a reader who remembers the old default will assume it " +
				"still holds")
		}
		return
	}

	// The other direction: a non-empty default must be named where users read.
	if strings.Contains(section, "nothing is blocked by default") {
		t.Errorf("config_ref.txt says nothing is blocked by default, but the default "+
			"list is %v", defaults)
	}
	for _, tool := range defaults {
		if !strings.Contains(section, strings.ToLower(tool)) {
			t.Errorf("%q is blocked by default and config_ref.txt's Blocked Tools "+
				"section never names it", tool)
		}
	}
}
