package run

// provisioningbanner_test.go holds the two claims command.go's comments make about
// what BINDS those frozen script constants — because a comment naming a binder is only
// worth reading if the binder exists, and both claims were wrong in a way no test could
// see.
//
// The first was an over-attribution: setupScript's comment claimed the literal
// "PROVISIONING FAILED" as one of exactly two things binding ITS bytes. The literal is
// not in setupScript at all (provisionScript wraps it and emits the banner), and the
// comment named one reader when there are two — the second being prose shipped INTO
// every jail, which no Go tool can follow.
//
// The second was an unattributed claim: three constants each said "Frozen contract
// (must not drift — the exact bytes matter)" with nothing named as the freezer. One
// golden closes over all three, and that is a checkable statement, so it is checked.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// provisioningFailedBanner is the cross-process literal. Spelled here on purpose: this
// is the fourth copy, and it is the one that FAILS when any of the other three is
// renamed alone, which is the only way a string agreed on by a shell script, a Go
// grep and an agent-facing skill can be held together.
const provisioningFailedBanner = "PROVISIONING FAILED"

// TestProvisioningFailedBannerBindsItsThreeSites: the emitter and both readers, in one
// test, so a rename cannot land in one of them.
func TestProvisioningFailedBannerBindsItsThreeSites(t *testing.T) {
	// EMITTER. provisionScript writes the banner into startup.log; setupScript, which it
	// wraps, does not contain the string at all — the attribution the old comment got
	// backwards.
	if !strings.Contains(provisionScript, provisioningFailedBanner) {
		t.Errorf("provisionScript no longer emits %q — nothing else writes it, so a failed "+
			"provision leaves a log its two readers cannot recognize", provisioningFailedBanner)
	}
	if strings.Contains(setupScript, provisioningFailedBanner) {
		t.Errorf("setupScript now contains %q; the banner belongs to provisionScript, and "+
			"setupScript's comment says so", provisioningFailedBanner)
	}

	// READER 1 (code): jailcontent.ReadProvisioningFailed greps the log for it. Exercised
	// through the real function on a real file — asserting on the literal alone would
	// prove nothing about the grep that consumes it.
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(ws, ".yolo", "startup.log")
	if err := os.WriteFile(log, []byte("=== yolo provisioning 2026-01-01T00:00:00+0000 ===\n"+
		provisioningFailedBanner+" (exit 1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !jailcontent.ReadProvisioningFailed(ws) {
		t.Errorf("jailcontent.ReadProvisioningFailed did not recognize a log carrying %q — "+
			"the briefing then shows no banner and a failed provision looks like a healthy jail",
			provisioningFailedBanner)
	}

	// READER 2 (prose shipped to agents): the built-in diagnosing-the-jail skill tells the
	// agent to look for this exact string in the log. It is not code, so nothing else in
	// this repo would notice it going stale.
	skill, err := builtinskills.FS.ReadFile("diagnosing-the-jail/SKILL.md")
	if err != nil {
		t.Fatalf("reading the built-in diagnosing-the-jail skill: %v", err)
	}
	if !strings.Contains(string(skill), provisioningFailedBanner) {
		t.Errorf("the diagnosing-the-jail skill no longer names %q — every jail ships an "+
			"instruction to grep for a string that is no longer written", provisioningFailedBanner)
	}
}

// TestFinalInternalCmdClosesOverEveryFrozenConstant checks the claim the three
// "frozen bytes" comments now make: ONE golden freezes all of them, because
// buildFinalInternalCmd composes all of them. If a constant stopped being composed, its
// comment would be promising a pin that no longer covers it — and the golden would keep
// passing, since the golden only ever sees this function's output.
func TestFinalInternalCmdClosesOverEveryFrozenConstant(t *testing.T) {
	got := buildFinalInternalCmd("bash", false)
	for name, part := range map[string]string{
		"setupScript":     setupScript,
		"provisionScript": provisionScript,
		"miseActivate":    miseActivate,
	} {
		if !strings.Contains(got, part) {
			t.Errorf("final_internal_cmd no longer contains %s verbatim, so "+
				"testdata/final_cmd_bash.txt does not pin it: %q", name, got)
		}
	}
}
