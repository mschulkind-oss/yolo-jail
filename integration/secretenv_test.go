package integration

import (
	"strings"
	"testing"
)

// secret-env-scoping.md §3.4 rows 1, 3 and 4 end to end: a bare shell holds the public set only, the
// agent whose selected provider claims a private key receives it in its agent env file, and a
// private name nothing claims is named on stderr and reaches nobody.
func TestAPrivateKeyReachesOnlyItsClaimingAgent(t *testing.T) {
	requireJail(t)
	writeUserTOML(t, "packs = [\"claude\", \"zai\"]\n\n[active_provider]\nclaude = \"zai\"\n\n"+
		"[env]\nset = { GH_TOKEN = \"gh-probe\" }\n\n"+
		"[env.secrets]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\", STRIPE_KEY = \"stripe-probe\" }\n")
	dir := writeWorkspaceTOML(t, "")
	r := runYolo(t, dir, `echo "SHELL=[${ZAI_API_KEY-}][${STRIPE_KEY-}][${GH_TOKEN-}]"; `+
		`grep -c ZAI_API_KEY ~/.config/yolo-agent-env/claude.sh`)
	if r.rc != 0 {
		t.Fatalf("rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "SHELL=[][][gh-probe]") {
		t.Errorf("a bare shell holds the public set only:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "\n1\n") && !strings.HasSuffix(strings.TrimSpace(r.stdout), "1") {
		t.Errorf("claude's agent env file carries the private key:\n%s", r.combined())
	}
	if !strings.Contains(r.stderr, "STRIPE_KEY: withheld from every process — nothing in this launch claims it.") ||
		strings.Contains(r.combined(), "stripe-probe") {
		t.Errorf("row 4's line, never the value:\n%s", r.combined())
	}
}
