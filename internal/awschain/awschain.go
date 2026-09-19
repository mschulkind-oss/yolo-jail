// Package awschain holds the facts about the AWS SDK's credential chain that yolo has
// to act on, and the two configurations in which one channel silently beats another.
//
// # Why a package of its own, and why it names variables rather than a loophole
//
// The rules below are the design's two `Forbidden` clauses
// (docs/design/sso-backed-bedrock.md §8), and both are written about the CHAIN:
//
//   - "Never enable both arms in one jail. AWS_BEARER_TOKEN_BEDROCK takes precedence
//     over the credential chain in every client measured, so a jail with both
//     configured uses the frozen bearer and the pull channel never runs — a silent
//     wrong answer. Refuse the launch, naming which one to drop."
//   - "Never grant ~/.aws alongside this. fromIni sits AHEAD of the container provider
//     in the measured chain order, so a host_files mount of ~/.aws does not merely
//     duplicate this feature, it DISABLES it while looking like it works."
//
// Neither sentence is about `aws-auth`. Both are about what an AWS SDK resolves first,
// which is true whichever pack, loophole or hand-written config put each half there —
// so the predicates here key on the VARIABLE the chain reads and on the CAPABILITY a
// loophole declares it serves, never on a name.
//
// ⚠ THAT IS THE WHOLE REASON THIS PACKAGE EXISTS. `if lp.Name == "aws-auth"` is
// forbidden in the launch and boot paths — they render every pack in one loop with no
// switch on any tool name (AGENTS.md), and the plan opened a Blocker on exactly this
// (docs/design/sso-backed-bedrock-plan.md, Blockers 6 and 7). A rule keyed on
// AWS_CONTAINER_CREDENTIALS_FULL_URI fires for any pack that ships the pull channel and
// for a user who writes the pointer by hand; a rule keyed on "aws-auth" would fire for
// one pack and would have to be edited for the next.
//
// Nothing here reads a file, runs a command or knows about yolo's config shapes: the
// callers assemble the inputs and this package holds the rule and its wording, so the
// launch pre-flight (internal/cli/run), the `yolo check` prediction (internal/cli/check)
// and the config validator (internal/config) cannot word one problem three ways.
package awschain

import "strings"

const (
	// BearerTokenVar is the PUSH channel: a short-term Bedrock API key, resolved once
	// and frozen for the life of the jail. Design §4 option B / §6 N1.
	//
	// It is read by the Bedrock clients themselves rather than by the credential
	// chain, and in every client measured it WINS over the chain — which is what makes
	// it dangerous beside the pull channel rather than merely redundant.
	BearerTokenVar = "AWS_BEARER_TOKEN_BEDROCK"

	// PointerVar is the PULL channel: the loopback URL an AWS SDK's container-
	// credentials provider fetches from, re-resolving as the credential ages. Design
	// §5. Whoever sets it — packs/aws-auth's `kind: "env"` contribution today — is
	// asking for the chain to be used.
	PointerVar = "AWS_CONTAINER_CREDENTIALS_FULL_URI"

	// ContainerCredentialsCapability is the `serves` capability a loophole declares
	// when it answers the container-credentials protocol for the jail. It is a named
	// JOB, which is the whole point of the `serves` vocabulary
	// (internal/loopholedecl/capabilities.go): a replacement pack that claimed this
	// capability would inherit the grant conflict below without anything here
	// changing.
	ContainerCredentialsCapability = "aws-container-credentials"

	// SharedConfigDir is the jail-home-relative directory an AWS SDK's `fromIni`
	// provider reads, which sits AHEAD of the container-credentials provider in the
	// resolved chain.
	//
	// It is the JAIL destination rather than a host source on purpose: what decides
	// whether `fromIni` answers is what the SDK finds under $HOME inside the jail, so
	// an entry seeded from an inline `content` is exactly as disabling as one mounted
	// from the host's own ~/.aws.
	SharedConfigDir = ".aws"
)

// The DELIVERY CHANNELS a launch can put one of these variables on, spelled once because
// two callers report the same conflict: the launch pre-flight, which sees all five, and
// the `yolo check` prediction, which sees three (it assembles no container argv and does
// not run the env-derive runner). A caller that cannot see a channel simply never returns
// its phrase — but the phrases a caller DOES return have to be the same words, or the
// prediction and the thing predicted word one problem differently, which is the defect
// `check`'s awschannels.go exists to avoid rather than to introduce.
//
// They live here rather than at either caller for the same reason the refusal text does:
// this package holds the rule AND its wording, and an origin phrase is half of what makes
// the refusal actionable (R3, docs/design/protocol-resolution.md). They name yolo's own
// delivery mechanisms, never a config SHAPE — nothing here reads one.
const (
	// FromEnvSources is the hydrated `env_sources` secret channel: where a jail using
	// AWS_BEARER_TOKEN_BEDROCK today gets it (design §8, "Pre-existing state").
	FromEnvSources = "env_sources (the secret channel)"
	// FromContainerArgv is the assembled container argv's `-e` pairs. It carries what
	// the composed channel carries PLUS every pack-shipped loophole's jail_env, which
	// reaches the jail nowhere else — so it is the one channel only the launch sees.
	FromContainerArgv = "the assembled container argv (a `-e` pair)"
	// FromPackEnv is a selected pack's `kind: "env"` contribution, folded over the
	// profile table. It cannot name WHICH pack: the launch delivers the fold rather
	// than any one contribution, and `yolo pack footprint` is where a reader learns
	// which one.
	FromPackEnv = "a selected pack's `kind: \"env\"` contribution"
	// FromProfileEnv is the provider environment the active profiles compose. Only the
	// launch sees it: composing it runs the env-derive runner, which `yolo check` does
	// not do.
	FromProfileEnv = "the active profile's provider environment"
	// FromLaunchEnv is the environment yolo itself was launched from, which the relay
	// can draw on.
	FromLaunchEnv = "the environment yolo was launched from"
)

// OriginLookup answers "would this launch deliver that variable, and from where".
// A caller assembles it from whatever it can see; ok is false when the launch would
// not deliver the variable at all.
//
// `where` is a human phrase naming the DECLARATION SITE ("env_sources (the secret
// channel)", "a selected pack's `kind: \"env\"` contribution", …) — the half of a
// refusal that turns "you configured two things" into "you configured THESE two, here
// and here". R3 of docs/design/protocol-resolution.md is the standing rule: the wrong
// declaration has to be visible in the refusal rather than in a later support request.
//
// ⚠ IT IS NEVER THE VALUE, and the signature is what enforces that: these two variables
// are a credential and a pointer at one, and this package's whole output is text a
// launch prints. A lookup that returned the value would put a bearer token on the
// terminal of every jail that tripped the rule.
//
// An EMPTY value must read as unset, exactly as the launch's own delivery lookup has
// it: an empty credential is the failure a pre-flight exists to name, not an escape
// from one.
type OriginLookup func(name string) (where string, ok bool)

// ExclusivityRefusal reports the launch-refusing conflict between the two arms: the
// frozen bearer and the refreshing pointer, both delivered into one jail.
//
// It returns the refusal as lines (the first the verdict, the rest its facts), or nil
// when at most one arm is delivered. There is DELIBERATELY NO ESCAPE HATCH, and it is
// the one pre-flight in this repo that should not grow one: a hatch here does not let a
// user proceed with a known gap the way YOLO_ALLOW_MISSING_PROVIDERS does — it lets
// them proceed into the silent wrong answer the rule exists to make loud. The remedy is
// one line of config either way, and the refusal names both.
func ExclusivityRefusal(look OriginLookup) []string {
	if look == nil {
		return nil
	}
	bearerWhere, bearer := look(BearerTokenVar)
	pointerWhere, pointer := look(PointerVar)
	if !bearer || !pointer {
		return nil
	}
	return []string{
		"Refusing to launch: this jail would carry TWO AWS credential channels, and the " +
			"agent's Bedrock traffic would authenticate with whichever the SDK prefers.",
		"  " + BearerTokenVar + " — the frozen bearer — is delivered by " +
			describe(bearerWhere) + ".",
		"  " + PointerVar + " — the pointer at the refreshing credential " +
			"service — is delivered by " + describe(pointerWhere) + ".",
		"  In every client measured the bearer WINS over the credential chain, so the " +
			"service behind the pointer would never be asked: the jail would " +
			"authenticate with a credential frozen at launch while the half that " +
			"survives an `aws sso login` sat idle.",
		"  Drop one. Remove " + BearerTokenVar + " from " + describe(bearerWhere) +
			", or stop delivering " + PointerVar + " from " + describe(pointerWhere) + ".",
	}
}

// GrantConflict reports the other half of the same rule: a jail that both mounts an
// AWS shared-config directory and runs a loophole serving container credentials has two
// answers to "who am I", and the SDK takes the mount.
//
// jailPath is the host_files entry's JAIL destination, home-relative with the leading
// "~/" already stripped — the form config.HostFileEntry.Path carries.
func GrantConflict(jailPath string) bool {
	p := strings.TrimPrefix(strings.TrimPrefix(jailPath, "~/"), "./")
	return p == SharedConfigDir || strings.HasPrefix(p, SharedConfigDir+"/")
}

// GrantRefusal is GrantConflict's message: both sides, and where each was declared.
//
// grantWhere is the config location of the offending host_files entry
// ("config.host_files[2]"); loopholeWhere is how the loophole came to be on
// ("config.loopholes.aws-auth.enabled", or its manifest's own default).
func GrantRefusal(grantWhere, jailPath, loophole, loopholeWhere string) string {
	return grantWhere + " renders " + home(jailPath) + " into the jail, while the " + loophole +
		" loophole — " + loopholeWhere + " — serves the jail's AWS credentials over the " +
		"container-credentials protocol (`serves: [\"" + ContainerCredentialsCapability +
		"\"]`). An AWS SDK reads ~/" + SharedConfigDir + " AHEAD of that endpoint, so this " +
		"entry does not duplicate the loophole, it DISABLES it while looking like it " +
		"works. Drop the " + grantWhere + " entry, or turn the loophole off."
}

// describe keeps an empty origin from rendering as an empty phrase: a caller that knows
// a variable is delivered but not from where still has to produce a readable sentence.
func describe(where string) string {
	if strings.TrimSpace(where) == "" {
		return "this launch's environment"
	}
	return where
}

// home re-attaches the "~/" a host_files destination carries stripped, so the refusal
// spells the path the way the config that declared it does. The INPUT stays stripped
// (GrantConflict and GrantRefusal both take HostFileEntry.Path's own form); only the
// rendering differs, because ".aws" on a terminal reads as a relative path and "~/.aws"
// reads as the directory an AWS SDK actually opens.
func home(jailPath string) string {
	return "~/" + strings.TrimPrefix(strings.TrimPrefix(jailPath, "~/"), "./")
}
