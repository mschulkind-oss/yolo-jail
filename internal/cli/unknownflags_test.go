package cli

// unknownflags_test.go pins requirement 3's unmet half
// (docs/reference/self-documenting-cli.md): a flag the command does not define is MISUSE — stderr,
// exit 2 — not something to drop while returning 0.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

func TestUnknownFlagIsRefusedWithTheNameAndAPointer(t *testing.T) {
	var errw bytes.Buffer
	if !refuseUnknownFlags("check", []string{"check", "--no-buld"}, checkKnownFlags, &errw) {
		t.Fatal("a typo'd flag must be refused — dropping it and returning 0 tells the operator " +
			"their request was honored")
	}
	got := errw.String()
	if !strings.Contains(got, "--no-buld") {
		t.Errorf("the refusal must name the flag it did not understand; got %q", got)
	}
	if !strings.Contains(got, "--help") {
		t.Errorf("the refusal must point at the authority for the real list; got %q", got)
	}
}

// ⚠ THE CASE THAT WOULD BREAK THE TOOL. Everything after a bare "--" is the WRAPPED program's argv,
// so scanning it would refuse the launch yolo exists for — `yolo -- claude
// --dangerously-skip-permissions` is the single most common invocation there is.
func TestFlagsAfterTheSeparatorAreNeverYolos(t *testing.T) {
	var errw bytes.Buffer
	args := []string{"run", "--profile", "zai", "--", "claude", "--dangerously-skip-permissions",
		"--some-future-flag=1"}
	if refuseUnknownFlags("run", args, runKnownFlags(), &errw) {
		t.Fatalf("refused the wrapped program's own flags — this breaks every launch: %s",
			errw.String())
	}
}

// ⚠ THE SECOND WAY YOLO'S TOKENS END, and the one that shipped broken. `run` takes its command
// positionally too — `yolo run claude --resume` has no `--` at all — so a scan that stopped only at
// the separator read the INNER program's flags as yolo's and exited 2 on the form
// parseRunArgs is pinned to support (TestRunPassesHelpThroughToInnerCommand's `run claude --help`).
//
// It composes the two halves the way runRun does, deliberately: each half is already covered alone,
// and the defect was in the JOIN — the boundary existed in the parser and the caller ignored it. So
// this fails if runRun's slice goes back to the whole argv, which is the regression to catch.
func TestAnImplicitCommandsOwnFlagsAreNeverYolos(t *testing.T) {
	for _, argv := range []string{
		"run claude --resume",
		"run bash -c echo hi",
		"run bash --login",
		"run --network host claude --resume",
		"run --profile zai bash -c echo",
	} {
		var errw bytes.Buffer
		var opts run.Options
		args := strings.Fields(argv)
		n := parseRunArgs(args, &opts)
		if refuseUnknownFlags("run", args[:n], runKnownFlags(), &errw) {
			t.Errorf("`yolo %s` refused the inner command's own flags: %s", argv, errw.String())
		}
	}
}

// The boundary must not cost the refusal its whole point: a flag yolo does not define, in the
// position where it IS yolo's, still has to be caught.
func TestATypoBeforeTheCommandIsStillRefused(t *testing.T) {
	for _, argv := range []string{
		"--dry-runn run -- claude",
		"run --netwrok host -- claude",
		"run --timng claude",
	} {
		var errw bytes.Buffer
		var opts run.Options
		args := strings.Fields(argv)
		n := parseRunArgs(args, &opts)
		if !refuseUnknownFlags("run", args[:n], runKnownFlags(), &errw) {
			t.Errorf("`yolo %s` carries a flag run does not define and was not refused", argv)
		}
	}
}

// A value that follows a flag is a positional, not a flag, and a bare "-" is a positional by
// convention.
func TestValuesAndBareDashAreNotFlags(t *testing.T) {
	var errw bytes.Buffer
	if refuseUnknownFlags("run", []string{"run", "--at", "host", "-"}, runKnownFlags(), &errw) {
		t.Fatalf("a flag VALUE must not read as a flag: %s", errw.String())
	}
}

// `--flag=value` is matched on the NAME, or every `=`-spelled flag would be refused.
func TestEqualsSpellingMatchesOnTheName(t *testing.T) {
	var errw bytes.Buffer
	if refuseUnknownFlags("run", []string{"run", "--profile=zai"}, runKnownFlags(), &errw) {
		t.Fatalf("--flag=value must match on the name: %s", errw.String())
	}
	errw.Reset()
	if !refuseUnknownFlags("run", []string{"run", "--profil=zai"}, runKnownFlags(), &errw) {
		t.Error("a typo in the =-spelling must still be refused")
	}
}

// runKnownFlags is DERIVED from runFlags, so a flag added to the policy list cannot be refused by
// the scan meant to accept it. Pinned because retyping the list is the obvious thing a later author
// does, and the failure is that a brand-new flag stops working.
func TestRunKnownFlagsCoversEveryPolicyFlag(t *testing.T) {
	known := map[string]bool{}
	for _, k := range runKnownFlags() {
		known[k] = true
	}
	for _, f := range runFlags {
		if !known[f] {
			t.Errorf("runFlags has %q and runKnownFlags does not — the refusal would reject a flag "+
				"run itself defines", f)
		}
	}
}

// Every command's real flags survive its own allowlist. A false refusal here is worse than the
// silence this change replaced, so each list is exercised against the flags it must accept.
func TestEachCommandAcceptsItsOwnFlags(t *testing.T) {
	for _, tc := range []struct {
		cmd   string
		known []string
		args  []string
	}{
		{"check", checkKnownFlags, []string{"check", "--no-build", "--accept-config-changes"}},
		{"prune", pruneKnownFlags, []string{"prune", "--apply", "--no-images", "--cache-age", "7"}},
		{"run", runKnownFlags(), []string{"run", "--timing", "--dry-run", "-p", "zai"}},
	} {
		var errw bytes.Buffer
		if refuseUnknownFlags(tc.cmd, tc.args, tc.known, &errw) {
			t.Errorf("%s refused its own documented flags: %s", tc.cmd, errw.String())
		}
	}
}
