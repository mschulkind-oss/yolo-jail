package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// secretMoveAdvice is the copyable move a public provider key's refusal names for the place the
// launch runs (remedyCommand, internal/config/envsecretsrefusal.go): inside a jail the user-scope
// edit goes through the host-run line, on the host the direct command.
func secretMoveAdvice() string {
	if config.InJail() {
		return "Move it to the private set: yolo host-run -C " + config.JailWorkspace() +
			" --name config-secret -- yolo config secret ZAI_API_KEY --user"
	}
	return "Move it to the private set: yolo config secret ZAI_API_KEY --user"
}

// docs/reference/providers.md#the-two-env-sets-public-and-private rows 1, 3 and 4 end to end: a bare shell holds the public set only, the
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

// §3.5 (row 2) end to end: a selected provider's key in the public set refuses the launch, naming
// the move and never the value; the command never runs (SE-D7), and there is no hatch (SE-D10).
func TestAPublicProviderKeyRefusesTheLaunch(t *testing.T) {
	requireJail(t)
	writeUserTOML(t, "packs = [\"zai\"]\n\n[env]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\" }\n")
	dir := writeWorkspaceTOML(t, "")
	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
	if r.rc == 0 || strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
		t.Fatalf("a provider key in the public set must refuse the launch:\n%s", r.combined())
	}
	if !strings.Contains(r.stderr, secretMoveAdvice()) ||
		strings.Contains(r.combined(), "zai-probe") {
		t.Errorf("the refusal names the move for this place and never the value:\n%s", r.combined())
	}
}

// SE-D2 end to end: one name in both sets refuses the launch whatever claims it, naming both
// --drop commands.
func TestANameInBothSetsRefusesTheLaunch(t *testing.T) {
	requireJail(t)
	writeUserTOML(t, "packs = []\n\n[env]\nset = { FOO = \"a\" }\n\n[env.secrets]\nset = { FOO = \"b\" }\n")
	dir := writeWorkspaceTOML(t, "")
	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
	if r.rc == 0 || strings.Contains(r.stdout, "SHOULD-NOT-RUN") ||
		!strings.Contains(r.stderr, "yolo config secret FOO --drop --user") ||
		!strings.Contains(r.stderr, "yolo config unsecret FOO --drop --user") {
		t.Errorf("both sets is fatal, naming both --drop commands:\n%s", r.combined())
	}
}

// SE-D7's "an attach": a jail running on the private key, whose user config then moves the key
// to the public set, refuses the next session attaching to it, before that session runs.
func TestAnAttachRefusesAPublicProviderKey(t *testing.T) {
	requireJail(t)
	cfg := writeUserTOML(t, "packs = [\"zai\"]\n\n[env.secrets]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\" }\n")
	dir := writeWorkspaceTOML(t, "")
	const releaseName = "release-secret-attach"
	first := startYoloBackgroundWith(t, "first", dir, nil,
		`echo "FIRST-UP=[yes]"; for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644)
	})
	// The answer, not the boot's echo of the command (jailgrant_test.go's sync point).
	up := regexp.MustCompile(`FIRST-UP=\[yes\]`)
	deadline := time.Now().Add(jailTimeout())
	for !up.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("the first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first session never came up within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile(cfg, []byte("packs = [\"zai\"]\n\n[env]\nset = { ZAI_API_KEY = \"zai-probe-not-a-real-key\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
	if r.rc == 0 || strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
		t.Fatalf("an attach over a public provider key must refuse:\n%s", r.combined())
	}
	if !strings.Contains(r.stderr, secretMoveAdvice()) ||
		strings.Contains(r.combined(), "zai-probe") {
		t.Errorf("the attach's refusal names the move for this place and never the value:\n%s", r.combined())
	}
}
