package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestConfigRefDocumentsAgentUpdatesTiming: `agent_updates` takes two TIMING values beside a
// boolean (docs/design/program-delivery.md OQ-PD30), and `yolo config-ref` is the authority for
// every config key, so its entry must name both, in the spellings the validator accepts, and say
// what the background one does and does not move, and where it writes.
func TestConfigRefDocumentsAgentUpdatesTiming(t *testing.T) {
	var buf bytes.Buffer
	configRefRun(&buf, false)
	text := buf.String()
	at := strings.Index(text, "agent_updates (boolean")
	if at < 0 {
		t.Fatal("config-ref has no agent_updates entry — this test has lost its subject")
	}
	block := text[at:]
	if end := strings.Index(block, "host_floor (boolean"); end >= 0 {
		block = block[:end]
	}
	for _, want := range []string{
		`"` + config.AgentUpdatesAtLaunch + `"`,
		`"` + config.AgentUpdatesNextLaunch + `"`,
		"in the\n    background",
		"~/.local/state/yolo/refresh/<agent>.log",
		"reported once, at the next launch",
		"still finish before the\n    agent starts",
		`"agent_updates": { "pi": "next-launch" }`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("config-ref's agent_updates entry does not say %q:\n%s", want, block)
		}
	}
}
