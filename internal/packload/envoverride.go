package packload

// envoverride.go EVALUATES a pack's `overridden_by` declarations (packdecl.EnvOverride)
// against one launch, and words the refusal or warning. It is the whole of core's part in
// the rule: it knows what a launch delivers and from where, and nothing about what any
// variable means — that half is the pack's, down to the sentence the refusal quotes.
//
// # Why core may not know the variables
//
// The rule's first home was a core package that named the Bedrock bearer variable and the
// container-credentials pointer itself (deleted by OQ-SSO8), and config.ValidateConfig,
// which knew which home directory an AWS SDK reads. Both were facts about one ecosystem's
// resolution order living in the launch path, and OQ-SSO8 (docs/reference/agent-credentials.md, ruled
// 2026-09-25) moved them into the pack that owns the channel: *"this needs to be contained
// within the pack. So we can't like hard code anything into core to look for this stuff."*
// packs/aws-auth now declares all three overrides of its pointer; a made-up pack declaring
// a made-up variable is refused by exactly this code.
//
// # Two callers, one rule
//
// The launch pre-flight (internal/cli/run/envoverrides.go, at all three arms) and the
// `yolo check` prediction (internal/cli/check/envoverrides.go). Each assembles its own lookup
// and rendered-path list and asks EnvOverrideFindings, so the prediction and the refusal it
// predicts cannot word one problem two ways.
//
// # Fatal when certain, with no escape hatch; a warning when not
//
// OQ-SSO8 condition 2, and OQ-SSO5 before it: a hatch here would not let a user proceed
// with a known gap the way YOLO_ALLOW_MISSING_PROVIDERS does — it would let them proceed
// into the silent wrong answer the rule exists to make loud. The remedy is one line of
// config either way, and the refusal names both lines.
//
// That holds for a CERTAIN override (packdecl.EnvOverride.IsCertain). One the pack declares
// uncertain — its condition is met by jails the consumer still serves correctly — is a
// WARNING: the launch continues, and the finding is printed as a disclosure, which nothing
// suppresses. The maintainer's ruling of 2026-09-25 draws the line: fatal *"if we can know
// that it's just not going to launch or it's just not going to work as configured"*, and
// *"I really don't want false positives"*. The words of a warning are this file's too, so
// the launch and `yolo check` word it one way.

import (
	"fmt"
	"slices"
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
	// FromEnvSources is the composition marker for its configured environment channel. Override
	// callers refine it from hydrated provenance when that is available.
	FromEnvSources = "env_sources (the secret channel)"
	// FromConfiguredEnv is a configured environment source whose exact spelling is not
	// available to the override caller.
	FromConfiguredEnv = "the configured environment"
	// FromLegacyEnvSources names the genuinely legacy env_sources input when its provenance is
	// available. Keep it distinct from TOML's [env.secrets] private set.
	FromLegacyEnvSources = "legacy env_sources"
	// FromEnvSecrets is a TOML private environment source with no finer provenance.
	FromEnvSecrets = "TOML [env.secrets]"
	// FromEnvSecretsFiles and FromEnvSecretsSet identify the two private TOML mechanisms so
	// a refusal can offer only the matching removal path.
	FromEnvSecretsFiles = "TOML [env.secrets].files"
	FromEnvSecretsSet   = "TOML [env.secrets].set"
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
	// pre-flight needs it. It is NOT a delivery into a JAIL: no jail backend forwards that
	// environment under the variable's own name, and a relayed value is FromProfileEnv. So a
	// jail's OriginLookup handed to EnvOverrideRefusal must answer "not delivered" rather than
	// this phrase — a variable only in the shell overrides nothing (OQ-SSO8 condition 3).
	// At the HOST notch it is a delivery, and the host's lookup answers it: the agent `yolo
	// host` execs inherits that environment whole (cli's hostComposition.envOverrideFindings).
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

// EnvOverrideFinding is one tripped `overridden_by` entry: whether it refuses the launch, and
// what to print. One finding is one problem, so a report that grades problems (`yolo check`)
// gives each finding one line of its own and carries the rest as detail.
type EnvOverrideFinding struct {
	// Certain is the entry's certainty (packdecl.EnvOverride.IsCertain): true refuses the
	// launch, false warns and lets it continue.
	Certain bool
	// Lines are the finding as printed: the verdict first, then the launch's facts (one per
	// delivered thing, indented), the pack's own reason, and the remedy.
	Lines []string
}

// EnvOverrideRefusal reports every CERTAIN `overridden_by` entry this launch trips, as the
// lines of one refusal (the first the verdict), or nil when none is tripped. It is
// EnvOverrideFindings narrowed to what refuses, for a caller that has nothing to do with a
// warning; see there for when an entry is evaluated at all.
func EnvOverrideRefusal(packs []*Pack, sel GateSelection, look OriginLookup,
	renderedHostFiles []string) []string {
	var out []string
	for _, f := range EnvOverrideFindings(packs, sel, look, renderedHostFiles, nil) {
		if f.Certain {
			out = append(out, f.Lines...)
		}
	}
	return out
}

// EnvOverrideFindings reports every `overridden_by` entry this launch trips, certain and
// uncertain alike, in pack and declaration order, or nil when none is tripped.
//
// A declaration is evaluated only when the contribution carrying it is DELIVERED — the pack
// is in packs, and the contribution is unconditional or its gate (a `profile` name or a
// `platform`) fires for some agent of the launch by the env fold's own per-agent rule
// (gateDelivered over gateFiresFor). An undelivered contribution has nothing to be
// overridden, and refusing over it is the false positive OQ-SSO8 forbids: a user who selects
// packs/aws-auth with no agent on a Bedrock provider has no pointer in the jail.
//
// sel is the gate's view of the launch's selection (CredentialScope.Selection, which the
// notch's credential gate built). look is the caller's delivery lookup. renderedHostFiles is the home-relative
// jail destinations the launch's `host_files` would actually render on its backend
// (config.RenderedHostFilePaths); a caller that cannot tell passes nil, which only ever
// costs a false negative.
//
// served is the notch's served set (ScopeInput.Served). A contribution declared `served_by` a
// daemon this notch does not serve is not delivered here — the gate withholds it — so it has
// nothing to be overridden either, and refusing over it would be the same false positive.
// Nil evaluates every contribution as declared.
func EnvOverrideFindings(packs []*Pack, sel GateSelection, look OriginLookup,
	renderedHostFiles []string, served *ServedDaemons) []EnvOverrideFinding {
	if look == nil {
		look = func(string) (string, bool) { return "", false }
	}
	rendered := append([]string(nil), renderedHostFiles...)
	sort.Strings(rendered)

	var out []EnvOverrideFinding
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, d := range p.Decl.EnvOverrideContributions() {
			if (d.Profile != "" || d.Platform != "") &&
				!gateDelivered(packs, p, envGate{profile: d.Profile, platform: d.Platform,
					servedBy: d.ServedBy, sets: d.Sets}, sel) {
				continue
			}
			if served != nil && d.ServedBy != "" && !served.Serves(d.ServedBy) {
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
				lines := overrideFindingLines(p.Name, d, o, facts, removals)
				// ONE LAUNCH, ONE ROUTE (docs/design/bedrock-key-provider.md OQ-BK5): when the
				// contribution and what overrides it reach DIFFERENT agents, the launch mixes two
				// providers, and emptying the login's profile would break the agent that uses it.
				// The remedy names a launch on either alone instead.
				if mix := mixedProviderRemedy(packs, p, d, o, sel); mix != "" {
					lines[len(lines)-1] = mix
					out = append(out, EnvOverrideFinding{Certain: o.IsCertain(), Lines: lines})
					continue
				}
				// A pointer at a LOGIN LOOPHOLE has a third way out, the one a layer that
				// keeps its own credential takes: that layer's profile set to "", so the
				// service serves nothing and the pointer is withheld (UnconfiguredLogins).
				if l, ok := LoginLoopholes(packs)[d.ServedBy]; ok && d.ServedBy != "" {
					last := len(lines) - 1
					lines[last] = strings.TrimSuffix(lines[last], ".") + "; or, to keep that credential, " +
						l.EmptyProfileStep() + "."
				}
				out = append(out, EnvOverrideFinding{Certain: o.IsCertain(), Lines: lines})
			}
		}
	}
	return out
}

// mixedProviderRemedy is the remedy line for a launch that delivers d to some agents and o's
// variables to OTHER agents, "" when it does not: each side is named by its agents' selected
// profiles, and the way out is a launch on one of them alone. o's variables reach an agent when
// one of its set's providers claims them all (GateSelection.claimingEntry); a variable no agent's
// provider claims reaches every process, so nothing is mixed. Names only, never a value.
func mixedProviderRemedy(packs []*Pack, p *Pack, d packdecl.EnvOverrideDecl, o packdecl.EnvOverride,
	sel GateSelection) string {
	if len(o.Vars) == 0 || d.Platform == "" {
		return ""
	}
	g := envGate{profile: d.Profile, platform: d.Platform, servedBy: d.ServedBy, sets: d.Sets}
	// Each side's agents, and the set entry that puts each agent on that side: the profile a
	// launch on that side alone selects, which in a set need not be the agent's primary.
	var gated, overriding, gatedProfiles, overridingProfiles []string
	for _, agent := range sortedMapKeys(sel.Profiles) {
		fires := gateFiresFor(packs, p, g, sel, agent)
		// Only an agent some selected pack installs is a process at all, as gateFiresFor asks.
		claiming := -1
		if agentInstalled(packs, agent) {
			claiming = sel.claimingEntry(agent, o.Vars)
		}
		if fires && claiming >= 0 {
			return ""
		}
		if fires {
			gated = append(gated, agent)
			gatedProfiles = appendUnique(gatedProfiles, sel.entryName(agent, sel.gatedEntry(agent, g)))
		}
		if claiming >= 0 {
			overriding = append(overriding, agent)
			overridingProfiles = appendUnique(overridingProfiles, sel.entryName(agent, claiming))
		}
	}
	if len(gated) == 0 || len(overriding) == 0 {
		return ""
	}
	var launches []string
	for _, profile := range append(append([]string(nil), overridingProfiles...), gatedProfiles...) {
		launches = appendUnique(launches, "`yolo -p "+profile+"`")
	}
	return fmt.Sprintf("  One launch cannot mix them: %s on %s receive%s pack %s's %s, and %s on %s "+
		"receive%s %s. Launch on one provider alone, %s; separate launches in one workspace may "+
		"each use a different one.",
		strings.Join(gated, ", "), quotedList(gatedProfiles), verbS(gated), p.Name, strings.Join(d.Sets, ", "),
		strings.Join(overriding, ", "), quotedList(overridingProfiles), verbS(overriding),
		strings.Join(o.Vars, ", "), strings.Join(launches, " or "))
}

// agentInstalled reports whether some pack of packs installs agent's CLI.
func agentInstalled(packs []*Pack, agent string) bool {
	for _, p := range packs {
		if slices.Contains(p.InstallBins(), agent) {
			return true
		}
	}
	return false
}

// gatedEntry is the index of the entry of agent's set the platform gate g fires on: for a served
// contribution the one the served-pointer rule lets through, else the first on g's platform; -1
// (the primary) when no entry says.
func (sel GateSelection) gatedEntry(agent string, g envGate) int {
	if g.servedBy != "" {
		i, _ := sel.servedEntry(agent, g.platform, g.sets)
		return i
	}
	for i, platform := range sel.entryPlatforms(agent) {
		if platform == g.platform {
			return i
		}
	}
	return -1
}

// quotedList is names in backticks, joined with " or ".
func quotedList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, " or ")
}

// verbS is the present-tense "s" for a subject of agents: one agent receives, two receive.
func verbS(agents []string) string {
	if len(agents) == 1 {
		return "s"
	}
	return ""
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

// overrideFindingLines words one tripped declaration: the verdict naming the pack and what
// its contribution sets, the launch's facts, the pack's own reason, and the two remedies.
//
// A certain entry's verdict is a refusal; an uncertain one's says MAY and that the launch
// continues, and its remedy is conditional on the override being real — the warning's
// whole point is that yolo cannot tell, so it must not tell the reader to act as if it
// could.
func overrideFindingLines(pack string, d packdecl.EnvOverrideDecl, o packdecl.EnvOverride,
	facts, removals []string) []string {
	verdict := fmt.Sprintf("Refusing to launch: pack %s's `kind: \"env\"` contribution "+
		"sets %s, and this launch also delivers what the pack declares OVERRIDES it — the "+
		"agent would silently use that instead.", pack, strings.Join(d.Sets, ", "))
	remedy := "  Drop one. "
	if !o.IsCertain() {
		verdict = fmt.Sprintf("Warning: pack %s's `kind: \"env\"` contribution sets %s, and "+
			"this launch also delivers what the pack declares MAY override it — if it does, "+
			"the agent silently uses that instead. The launch continues.",
			pack, strings.Join(d.Sets, ", "))
		remedy = "  If it does, drop one. "
	}
	lines := []string{verdict}
	lines = append(lines, facts...)
	lines = append(lines, "  The pack says: "+strings.TrimSpace(o.Because))

	drop := "Remove " + removals[0]
	if len(removals) > 1 {
		// A conjunction is broken by removing ANY one half, so the remedy says so rather
		// than implying the user must remove all of them.
		drop = "Remove any one of: " + strings.Join(removals, "; ")
	}
	stop := "or deselect pack " + pack
	switch {
	case d.Platform != "":
		stop = "or stop delivering pack " + pack + "'s contribution — it is delivered only to an " +
			"agent whose selected provider's platform is `" + d.Platform + "`, so select another " +
			"provider for that agent or deselect the pack"
	case d.Profile != "":
		stop = "or stop delivering pack " + pack + "'s contribution — it is delivered only " +
			"while the `" + d.Profile + "` profile is active, so deselect that profile or the pack"
	}
	last := remedy + drop + ", " + stop + "."
	switch {
	case hasEnvSource(removals, FromEnvSecretsFiles):
		last += " This is a private dotenv value from `[env.secrets].files`; `yolo config unset` " +
			"edits inline values only and does not change dotenv bytes. Remove only this name's line from its source file."
	case hasEnvSource(removals, FromEnvSecretsSet):
		last += " For this inline private value, run `yolo config unset env.secrets.set.<NAME>` " +
			"with its scope (`--user`, `--project`, or `--local`), or select the declaring included config with `--file <path>`."
	case hasPrivateEnvSource(removals):
		last += " This is a private TOML value; verify whether it is inline `[env.secrets].set` or " +
			"dotenv-backed `[env.secrets].files` before removing it. `yolo config unset` edits inline values only, not dotenv bytes."
	}
	return append(lines, last)
}

func hasEnvSource(removals []string, source string) bool {
	for _, removal := range removals {
		if strings.Contains(removal, " from "+source) {
			return true
		}
	}
	return false
}

func hasPrivateEnvSource(removals []string) bool {
	return hasEnvSource(removals, FromEnvSecrets)
}

// describeOrigin keeps an empty origin from rendering as an empty phrase: a caller that knows
// a variable is delivered but not from where still has to produce a readable sentence.
func describeOrigin(where string) string {
	if strings.TrimSpace(where) == "" {
		return "this launch's environment"
	}
	return where
}
