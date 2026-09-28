package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfigRefDocumentsResourcesIo: `resources.io` is a key the validator accepts, so
// `yolo config-ref`, the authority for every config key, must document it inside the
// `resources` block. And that block's lead said the limits "are enforced by the kernel —
// the jail cannot exceed them", which is false of a priority any process can raise, so the
// entry must say it is advisory and must not sit under an unscoped enforcement claim.
func TestConfigRefDocumentsResourcesIo(t *testing.T) {
	var buf bytes.Buffer
	configRefRun(&buf, false)
	text := buf.String()
	at := strings.Index(text, "resources (object)")
	if at < 0 {
		t.Fatal("config-ref has no resources block — this test has lost its subject")
	}
	block := text[at:]
	if end := strings.Index(block, "In-jail sub-process limits"); end >= 0 {
		block = block[:end]
	}
	if !strings.Contains(block, "• io (string|object)") {
		t.Fatalf("config-ref's resources block does not document io:\n%s", block)
	}
	for _, want := range []string{`"low"`, `"idle"`, `"normal"`, "ADVISORY", "writeback", "kyber", "VirtioFS", "yolo check"} {
		if !strings.Contains(block, want) {
			t.Errorf("config-ref's io entry does not mention %s:\n%s", want, block)
		}
	}
	if strings.Contains(block, "These limits are enforced by the kernel") {
		t.Errorf("the resources lead still claims every key is kernel-enforced, io included:\n%s", block)
	}
}
