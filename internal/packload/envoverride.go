package packload

// envoverride.go EVALUATES a pack's `overridden_by` declarations (packdecl.EnvOverride)
// against one launch, and words the refusal. It is the whole of core's part in the rule:
// it knows what a launch delivers and from where, and nothing about what any variable
// means — that half is the pack's, down to the sentence the refusal quotes.
//
// # Why core may not know the variables
//
// The rule's first home was internal/awschain, which named the Bedrock bearer variable
// and the container-credentials pointer itself, and config.ValidateConfig, which knew
// which home directory an AWS SDK reads. Both were facts about one ecosystem's resolution
// order living in the launch path, and OQ-SSO8 (docs/design/sso-backed-bedrock.md, ruled
// 2026-09-25) moved them into the pack that owns the channel: *"this needs to be contained
// within the pack. So we can't like hard code anything into core to look for this stuff."*
// packs/aws-auth now declares all three overrides of its pointer; a made-up pack declaring
// a made-up variable is refused by exactly this code.
//
// # Two callers, one rule
//
// The launch pre-flight (internal/cli/run/envoverrides.go, at all three arms) and the
// `yolo check` prediction (internal/cli/check/envoverrides.go). Each assembles its own lookup
// and rendered-path list and asks this function, so the prediction and the refusal it
// predicts cannot word one problem two ways.
//
// # Fatal, with no escape hatch
//
// OQ-SSO8 condition 2, and OQ-SSO5 before it: a hatch here would not let a user proceed
// with a known gap the way YOLO_ALLOW_MISSING_PROVIDERS does — it would let them proceed
// into the silent wrong answer the rule exists to make loud. The remedy is one line of
// config either way, and the refusal names both lines.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The DELIVERY CHANNELS a launch can put a variable on, spelled once because two callers
// report the same refusal: the launch pre-flight, which sees the first four, and the
// `yolo check` prediction, which sees two (it assembles no container argv and does not run
// the env-derive runner). A caller that cannot see a channel never returns its phrase — but
// the phrases a caller DOES return have to be the same words, or the prediction and the
// thing predicted word one problem differently.
//
// The fifth, FromLaunchEnv, is a SOURCE rather than a delivery into the jail, and no
// override refusal carries it (see its own comment).
//
// They name yolo's own delivery mechanisms, never a variable, and they are half of what
// makes a refusal actionable: "you configured two things" sends a reader hunting, "env_sources,
// and a selected pack's env contribution" does not
// (docs/reference/protocol-resolution.md#the-four-outcomes).
const (
	// FromEnvSources is the hydrated `env_sources` secret channel.
	FromEnvSources = "env_sources (the secret channel)"
	// FromContainerArgv is the assembled container argv's `-e` pairs. It carries what the
	// composed channel carries PLUS every pack-shipped loophole's jail_env, which reaches
	// the jail nowhere else — so it is the one channel only the container launch sees.
	FromContainerArgv = "the assembled container argv (a `-e` pair)"
	// FromPackEnv is a selected pack's `kind: "env"` contribution, folded over the profile
	// table. It cannot name WHICH pack: the launch delivers the fold rather than any one
	// contribution, and `yolo pack footprint` is where a reader learns which one.
	FromPackEnv = "a selected pack's `kind: \"env\"` contribution"
	// FromProfileEnv is the provider environment the active profiles compose. Only the
	// launch sees it: composing it runs the env-derive runner, which `yolo check` does not.
	FromProfileEnv = "the active profile's provider environment"
	// FromLaunchEnv is the environment yolo itself was launched from, which the relay can
	// draw on. The launch's delivery lookup answers it last because the credential
	// pre-flight needs it. It is NOT a delivery into the jail: no backend forwards that
	// environment under the variable's own name, and a relayed value is FromProfileEnv. So
	// an OriginLookup handed to EnvOverrideRefusal must answer "not delivered" rather than
	// this phrase — a variable only in the shell overrides nothing (OQ-SSO8 condition 3).
	FromLaunchEnv = "the environment yolo was launched from"
)

// OriginLookup answers "would this launch deliver that variable INTO THE JAIL, and from
// where". A caller assembles it from whatever it can see; ok is false when the jail would not
// receive the variable under that name at all — including when it exists only in the
// environment yolo was launched from (FromLaunchEnv).
//
// ⚠ IT RETURNS THE ORIGIN, NEVER THE VALUE, and the signature is what enforces that: the
// variables an override names are typically credentials, and this package's whole output is
// text a launch prints. An EMPTY value must read as unset, exactly as the launch's own
// delivery lookup has it.
type OriginLookup func(name string) (where string, ok bool)

// EnvOverrideRefusal reports every `overridden_by` declaration this launch trips, as the
// lines of one refusal (the first the verdict), or nil when none is tripped.
//
// A declaration is evaluated only when the contribution carrying it is DELIVERED — the pack
// is in packs, and the contribution is unconditional or its `profile` gate is active in
// profiles by the env fold's own rule (profileActive). An undelivered contribution has
// nothing to be overridden, and refusing over it is the false positive OQ-SSO8 forbids: a
// user who selects packs/aws-auth without the `bedrock` profile has no pointer in the jail.
//
// profiles is the launch's CLI-keyed profile table (ProfileTable of the effective
// selection). look is the caller's delivery lookup. renderedHostFiles is the home-relative
// jail destinations the launch's `host_files` would actually render on its backend
// (config.RenderedHostFilePaths); a caller that cannot tell passes nil, which only ever
// costs a false negative.
func EnvOverrideRefusal(packs []*Pack, profiles map[string]string, look OriginLookup,
	renderedHostFiles []string) []string {
	if look == nil {
		look = func(string) (string, bool) { return "", false }
	}
	rendered := append([]string(nil), renderedHostFiles...)
	sort.Strings(rendered)

	var out []string
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, d := range p.Decl.EnvOverrideContributions() {
			if d.Profile != "" && !profileActive(packs, p, d.Profile, profiles) {
				continue
			}
			for _, o := range d.Overrides {
				var facts, removals []string
				switch {
				case len(o.Vars) > 0:
					facts, removals = trippedVars(o, look)
				case o.HostFile != "":
					facts, removals = trippedHostFiles(o, rendered)
				}
				if len(facts) == 0 {
					continue
				}
				out = append(out, overrideRefusalLines(p.Name, d, o, facts, removals)...)
			}
		}
	}
	return out
}

// trippedVars returns, when every one of o.Vars is delivered and none of o.Unless is, one
// fact line per variable naming its origin, plus the removal phrase for each. Nil otherwise.
func trippedVars(o packdecl.EnvOverride, look OriginLookup) (facts, removals []string) {
	for _, u := range o.Unless {
		if _, ok := look(u); ok {
			return nil, nil
		}
	}
	for _, v := range o.Vars {
		where, ok := look(v)
		if !ok {
			return nil, nil
		}
		facts = append(facts, "  "+v+" is delivered by "+describeOrigin(where)+".")
		removals = append(removals, v+" from "+describeOrigin(where))
	}
	return facts, removals
}

// trippedHostFiles returns one fact line per rendered destination under o.HostFile, plus
// the removal phrase for each. Nil when none is covered.
func trippedHostFiles(o packdecl.EnvOverride, rendered []string) (facts, removals []string) {
	for _, dest := range rendered {
		if !o.Covers(dest) {
			continue
		}
		facts = append(facts, "  A host_files entry renders ~/"+dest+" into the jail.")
		removals = append(removals, "the host_files entry for ~/"+dest)
	}
	return facts, removals
}

// overrideRefusalLines words one tripped declaration: the verdict naming the pack and what
// its contribution sets, the launch's facts, the pack's own reason, and the two remedies.
func overrideRefusalLines(pack string, d packdecl.EnvOverrideDecl, o packdecl.EnvOverride,
	facts, removals []string) []string {
	lines := []string{fmt.Sprintf("Refusing to launch: pack %s's `kind: \"env\"` contribution "+
		"sets %s, and this launch also delivers what the pack declares OVERRIDES it — the "+
		"agent would silently use that instead.", pack, strings.Join(d.Sets, ", "))}
	lines = append(lines, facts...)
	lines = append(lines, "  The pack says: "+strings.TrimSpace(o.Because))

	drop := "Remove " + removals[0]
	if len(removals) > 1 {
		// A conjunction is broken by removing ANY one half, so the remedy says so rather
		// than implying the user must remove all of them.
		drop = "Remove any one of: " + strings.Join(removals, "; ")
	}
	stop := "or deselect pack " + pack
	if d.Profile != "" {
		stop = "or stop delivering pack " + pack + "'s contribution — it is delivered only " +
			"while the `" + d.Profile + "` profile is active, so deselect that profile or the pack"
	}
	return append(lines, "  Drop one. "+drop+", "+stop+".")
}

// describeOrigin keeps an empty origin from rendering as an empty phrase: a caller that knows
// a variable is delivered but not from where still has to produce a readable sentence.
func describeOrigin(where string) string {
	if strings.TrimSpace(where) == "" {
		return "this launch's environment"
	}
	return where
}
