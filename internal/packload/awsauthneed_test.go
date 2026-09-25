package packload

import (
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// awsauthneed_test.go pins sso-backed-bedrock plan step 5: packs/claude NEEDS aws-auth,
// unconditionally, the way it needs openai-auth. The `bedrock` profile is claude's, so a
// user who selects claude and enables the aws-auth loophole gets the credential service
// without listing a second pack. Selecting it changes nothing observable on its own: the
// pointer is gated on the `bedrock` profile and the loophole ships disabled.

// TestClaudeNeedsAWSAuth reads the shipped claude pack's declaration and runs the real
// closure over the shipped set, so it fails if the `needs` entry is removed, gains a
// condition, or names a pack that no longer ships.
func TestClaudeNeedsAWSAuth(t *testing.T) {
	shipped, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	byName := map[string]*Pack{}
	for _, p := range shipped {
		byName[p.Name] = p
	}
	claude := byName["claude"]
	if claude == nil {
		t.Fatal("no shipped claude pack")
	}
	found := false
	for _, n := range claude.Decl.DeclaredNeeds() {
		if n.Pack == "aws-auth" {
			found = true
			if len(n.WhenBins) != 0 {
				t.Errorf("claude's aws-auth need has when_bins %v; it is unconditional, since "+
					"the bedrock profile that consumes it is claude's own", n.WhenBins)
			}
		}
	}
	if !found {
		t.Fatalf("packs/claude declares no need on aws-auth (needs = %+v) — "+
			"docs/design/sso-backed-bedrock-plan.md step 5", claude.Decl.DeclaredNeeds())
	}
	added, causes, err := ResolveNeeds([]*Pack{claude}, func(name string) (*Pack, bool) {
		p, ok := byName[name]
		return p, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range added {
		names = append(names, p.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "aws-auth") {
		t.Errorf("a claude-only selection resolved needs %v, want aws-auth among them", names)
	}
	if !strings.Contains(strings.Join(causes, "\n"), "aws-auth") {
		t.Errorf("the closure's cause lines %q do not name aws-auth — the launch banner and "+
			"yolo check print exactly these", causes)
	}
}
