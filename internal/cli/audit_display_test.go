package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// A refused call is audited with the argv the jail sent, and a standing read keeps its
// positionals verbatim, so an agent chooses bytes `yolo audit` prints on the host's
// terminal. They are printed as text: no clearing the screen, no rewriting the lines above,
// no retitling the window.
func TestAuditPrintsTheJailsArgvAsText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	l := brokeraudit.Open(path, nil)
	code := 64
	l.Append(brokeraudit.Event{Time: "2026-09-29T10:00:00Z", Event: "call", Service: "github", Jail: "aaa",
		Argv: []string{"\x1b[2J\x1b[H\x1b]0;pwned\afake-clean-line\r"}, Set: "refused", Outcome: "refused",
		Exit: &code})
	rc, out, _ := runAuditCase(t, path)
	if rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if termsafe.HasUnsafe(strings.TrimSuffix(out, "\n")) {
		t.Fatalf("yolo audit printed a control character from the jail's argv: %q", out)
	}
	if !strings.Contains(out, `github: $'\x1b[2J\x1b[H\x1b]0;pwned\x07fake-clean-line\x0d'`) {
		t.Fatalf("the argv is not shown as its escaped word: %q", out)
	}
}
