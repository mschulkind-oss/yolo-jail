package awschain

// awschain_test.go pins the RULE, which is all this package holds. Every caller —
// the launch pre-flight (internal/cli/run/awschannels.go), the `yolo check`
// prediction (internal/cli/check/awschannels.go) and the config validator
// (internal/config/validate_loopholes.go) — assembles its own inputs and then asks
// these three functions, so a wrong answer here is wrong in three places at once.
//
// The negative cases carry as much weight as the positive one and are the reason
// this file is longer than the package. A rule that refuses a jail carrying ONE arm
// would refuse every jail that uses `AWS_BEARER_TOKEN_BEDROCK` through env_sources
// today — §8 of docs/design/sso-backed-bedrock.md says those keep working unchanged
// — and a `GrantConflict` that matched on a bare prefix would refuse `~/.awsfoo`,
// a directory no AWS SDK has ever read.

import (
	"strings"
	"testing"
)

// lookupOf builds an OriginLookup from a var->origin map. An absent key is undelivered,
// which is the "ok is false" half of the contract.
func lookupOf(m map[string]string) OriginLookup {
	return func(name string) (string, bool) {
		where, ok := m[name]
		return where, ok
	}
}

// TestExclusivityRefusalNamesBothArmsAndBothOrigins is the positive case: both
// channels delivered, so the refusal has to say WHICH two and WHERE each came from.
// Naming only the variables would send a reader hunting through four files for the
// declaration — R3 of docs/design/protocol-resolution.md, the rule the origin phrase
// exists for.
func TestExclusivityRefusalNamesBothArmsAndBothOrigins(t *testing.T) {
	lines := ExclusivityRefusal(lookupOf(map[string]string{
		BearerTokenVar: "env_sources (the secret channel)",
		PointerVar:     "a selected pack's `kind: \"env\"` contribution",
	}))
	if len(lines) == 0 {
		t.Fatal("both arms delivered and nothing was refused — this is the silent wrong " +
			"answer the rule exists to make loud")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		BearerTokenVar,
		PointerVar,
		"env_sources (the secret channel)",
		"a selected pack's `kind: \"env\"` contribution",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, joined)
		}
	}
	// The remedy is the actionable half: a reader who is told only that two things
	// conflict still has to guess which one to drop.
	if !strings.Contains(joined, "Drop one.") {
		t.Errorf("the refusal never says what to do about it:\n%s", joined)
	}
}

// TestExclusivityRefusalIsSilentOnOneArm: the two configurations that are not a
// conflict, and the one this repo already ships. A jail carrying the bearer alone is
// the pre-existing `env_sources` setup §8 promises keeps working; a jail carrying the
// pointer alone is the pack's own shape.
func TestExclusivityRefusalIsSilentOnOneArm(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"the frozen bearer alone — the pre-existing env_sources jail", map[string]string{
			BearerTokenVar: "env_sources (the secret channel)",
		}},
		{"the pointer alone — packs/aws-auth's own shape", map[string]string{
			PointerVar: "a selected pack's `kind: \"env\"` contribution",
		}},
		{"neither", map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if lines := ExclusivityRefusal(lookupOf(tc.env)); lines != nil {
				t.Errorf("refused a jail with no conflict:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
}

// TestExclusivityRefusalTakesANilLookup: a caller with nothing composed — the launch
// arm whose channel is nil — must get silence rather than a panic. It is the one
// input a pre-flight is guaranteed to be handed at least once (`checkAWSCredentialChannels`
// returns early on a nil channel, and this is the contract it relies on).
func TestExclusivityRefusalTakesANilLookup(t *testing.T) {
	if lines := ExclusivityRefusal(nil); lines != nil {
		t.Errorf("a nil lookup produced a refusal: %v", lines)
	}
}

// TestExclusivityRefusalDescribesAnUnknownOrigin: a caller that knows a variable is
// delivered but not from where still has to produce a readable sentence. Without the
// fallback the line reads "… is delivered by ." — which names nothing and looks like
// a rendering bug rather than a limit of what the caller could see.
func TestExclusivityRefusalDescribesAnUnknownOrigin(t *testing.T) {
	lines := ExclusivityRefusal(lookupOf(map[string]string{
		BearerTokenVar: "",
		PointerVar:     "   ",
	}))
	joined := strings.Join(lines, "\n")
	if joined == "" {
		t.Fatal("an origin-less pair must still refuse — the conflict is the delivery, " +
			"not the knowing where from")
	}
	if strings.Contains(joined, "delivered by .") || strings.Contains(joined, "from .") {
		t.Errorf("an empty origin rendered as an empty phrase:\n%s", joined)
	}
	if strings.Count(joined, "this launch's environment") < 2 {
		t.Errorf("both unknown origins must fall back to a readable phrase:\n%s", joined)
	}
}

// TestGrantConflictMatchesTheDirectoryAndItsContents, and NOTHING that merely starts
// with the same letters. `.awsfoo` is the case a `strings.HasPrefix(p, ".aws")`
// would get wrong, and getting it wrong means refusing a launch over a directory no
// AWS SDK has ever opened.
func TestGrantConflictMatchesTheDirectoryAndItsContents(t *testing.T) {
	conflicting := []string{
		".aws",
		".aws/config",
		".aws/credentials",
		".aws/cli/cache/x.json",
		"~/.aws",
		"~/.aws/config",
		"./.aws",
	}
	for _, p := range conflicting {
		if !GrantConflict(p) {
			t.Errorf("GrantConflict(%q) = false — an SDK's fromIni reads this ahead of the "+
				"container provider, so the grant disables the loophole", p)
		}
	}

	clear := []string{
		"",
		".awsfoo",
		".aws-backup",
		".awsrc",
		"aws",
		".config/aws/config",
		"code/.aws",
		".config/.aws",
	}
	for _, p := range clear {
		if GrantConflict(p) {
			t.Errorf("GrantConflict(%q) = true — nothing reads this as the AWS shared "+
				"config, so refusing a launch over it is a false positive", p)
		}
	}
}

// TestGrantRefusalNamesBothSidesAndTheCapability. The capability spelling is in the
// message on purpose: it is the `serves` value a replacement pack would have to claim,
// so a reader who is shipping one learns the key rather than the loophole's name.
func TestGrantRefusalNamesBothSidesAndTheCapability(t *testing.T) {
	msg := GrantRefusal("config.host_files[2]", ".aws/config", "aws-auth",
		"config.loopholes.aws-auth.enabled")
	for _, want := range []string{
		"config.host_files[2]",
		"~/.aws/config",
		"aws-auth",
		"config.loopholes.aws-auth.enabled",
		ContainerCredentialsCapability,
		"DISABLES",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, msg)
		}
	}
}

// TestGrantRefusalSpellsThePathTheWayTheConfigDoes. The entry's jail destination
// arrives home-relative with the "~/" stripped (HostFileEntry.Path's form), and a bare
// ".aws" on a terminal reads as a path relative to the cwd. The reader has to be able
// to match the message against what they wrote.
func TestGrantRefusalSpellsThePathTheWayTheConfigDoes(t *testing.T) {
	for _, in := range []string{".aws", "~/.aws", "./.aws"} {
		msg := GrantRefusal("config.host_files[0]", in, "aws-auth", "its manifest default")
		if !strings.Contains(msg, "~/.aws") {
			t.Errorf("GrantRefusal(%q) does not spell the destination as ~/.aws:\n%s", in, msg)
		}
		if strings.Contains(msg, "~/~/") || strings.Contains(msg, "~/./") {
			t.Errorf("GrantRefusal(%q) double-prefixed the destination:\n%s", in, msg)
		}
	}
}
