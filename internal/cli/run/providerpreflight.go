package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// checkProviderCredentials is the SEVENTH bespoke launch pre-flight
// (docs/reference/providers.md#the-credential-preflight, #pv-oq-13): a SELECTED pack that requires
// a provider — by shipping one, or by a variant naming one — is refused when the composed
// providers table has no such entry or when the credential variable that entry points at
// does not reach every agent that selected it (provider-credential-scope.md CN-D25). It
// returns the lines to print and whether
// the caller must stop: lines alone cannot carry that, because the escape hatch turns a
// refusal into a LOUD CONTINUATION, and a caller that only looked at len(lines) would exit
// on the notice (measured — this is the bug the first nested launch caught).
//
// Scoped to the SELECTED PROVIDERS since the credential gate (OQ-CN3, ruled 2026-09-26,
// docs/design/provider-credential-scope.md): a cataloged provider no agent's profile
// selects is no requirement, because the gate delivers its key to nobody, and refusing a
// launch over a key nobody will deliver was the gate's defect one layer up. That reopens
// providers.md#pv-oq-13's pack scoping deliberately. What still refuses is a provider some
// agent selected whose key never arrived — the mysterious first-request failure
// providers.md#the-credential-preflight records. The packs argument IS the selected pack
// set — the same slice staging produced — so an unselected pack cannot reach this.
//
// WHY NOT in stagePacks beside the other bespoke pre-flights: the question it answers is
// "does the environment this launch composes carry the key", so it belongs with the
// composition — the channel (profilechannel.go) — rather than with the pack set. WHY ONE
// CALLER PER ARM and not one call above the dispatch: both arms check the SAME channel,
// but the container arm gates the FRESH-LAUNCH path only (attaching to a running jail
// delivers no environment, so the question has no subject there), while on macos-user
// every invocation is fresh. That is the config-change approval's split, and for the same
// reason; the dispatch-level tests in profilechanneldispatch_test.go fail if either arm
// stops calling this.
//
// argvPairs is the `-e K=V` map of the assembled container argv, or nil off the container.
// It is folded into the delivery lookup because a pack-shipped loophole's jail_env can put
// a credential on the argv that the channel alone does not know about.
//
// The escape hatch is consulted only where it suppresses something — a launch with no gap
// never announces it, which is the same rule the reachability witness's override notice
// follows. When it DOES suppress, the notice says what it is suppressing rather than going
// quiet: nothing was repaired, and the agent's first request against that provider still
// fails.
//
// IT ALSO RUNS THE REGION PRE-FLIGHT (checkProviderRegions, OQ-BR6) and returns both halves'
// lines, so the three call sites — the fresh container launch, the attach delivery and every
// macos-user invocation — each ask about a selected provider's region without a fourth call
// to keep in step with this one.
func (o *Options) checkProviderCredentials(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	channel *packChannel, argvPairs map[string]string) (lines []string, refuse bool) {
	consulted := config.DescribeEnvSources(o.Workspace, cfg)
	consulted = append(consulted, packload.FromLaunchEnv)
	// NARROWED WITH THE GATE (OQ-CN3): the providers some agent selected are the only ones
	// whose key this launch delivers to anybody, so they are the only ones it may demand.
	// The SAME scope the vehicles deliver from, so the check and the delivery cannot
	// disagree about who gets a credential.
	//
	// PER AGENT, and the environment yolo was launched from only through a RELAY: no jail
	// backend hands that environment to the jail's processes (bedrock-plumbing.md BR-D2), so a
	// key found only there reaches an agent when its env derive copied the value into the
	// agent's own environment, as claude's does into ANTHROPIC_AUTH_TOKEN, and for no other.
	// opencode, pi and codex read the variable itself, and counting the shell for them started
	// them with no key. Every other channel is asked of the agent, as the region half below asks
	// (CredentialScope.DeliveredTo), and the argv's `-e` pairs reach every process of a
	// container.
	reaches := func(agent, name string) bool {
		if v, found := argvPairs[name]; found && v != "" {
			return true
		}
		if _, ok := channel.scope.DeliveredTo(agent, name); ok {
			return true
		}
		return channel.scope.Relays(agent, o.Getenv(name))
	}
	facts := packload.ProviderCredentialGapsTo(packs, channel.providers, channel.scope, reaches,
		func(name string) bool { return o.Getenv(name) != "" }, consulted)
	held := o.Getenv(paths.AllowMissingProvidersEnv) != ""
	// The refusal's wording is packload's, the host notch's too (notch-convergence.md item 14).
	lines, refuse = packload.ProviderCredentialRefusal(facts, held)
	// THE REGION HALF (OQ-BR6), in this function rather than beside it at the three call
	// sites, so every arm that asks about the provider a launch delivers asks both questions:
	// is its key here, and is its region. Same hatch, same renderer, a verdict of its own.
	regionLines, regionRefuse := o.checkProviderRegions(cfg, packs, channel, argvPairs, held)
	return append(lines, regionLines...), refuse || regionRefuse
}

// checkProviderRegions is the region pre-flight at the jail notch (packload.ProviderRegionGaps;
// docs/design/bedrock-plumbing.md §8, OQ-BR6): a provider some agent selected whose pack says it
// is reached through a region, whose composed entry has no `region`, and none of whose region
// variables reaches the jail, refuses the launch.
//
// PER AGENT (the review's finding on the first build): each agent whose profile selects a
// provider is asked about what reaches IT — the env_sources the gate delivers to it, the shared
// pack env, its own gated pack env and shape vars (CredentialScope.DeliveredTo), and on a
// container the argv's `-e` pairs, which every process of the jail inherits. Asked through the
// launch-wide deliverySource instead, a region only codex receives satisfied claude's bedrock,
// and claude started with none.
//
// "REACHES THE AGENT" EXCLUDES THE ENVIRONMENT YOLO WAS LAUNCHED FROM: the credential half
// counts it, because a derive can relay a credential out of it, but nothing relays a region —
// the claude derive composes AWS_REGION from the provider's `region` only — and no backend
// forwards that environment under its own name. So a region exported only in the invoking shell
// is not counted, and the refusal names it as stranded there, which is the one form of this
// mistake a user can see from their own terminal.
//
// A REGION FROM THE HOST'S REGION FILE IS DELIVERED, NOT COUNTED HERE (BR-DIR1): the gate's
// region fill put it in the agent's shape vars (packload's regionfill.go), which DeliveredTo
// reads, and the ask carries what the fill read so a refusal names the file and profile.
func (o *Options) checkProviderRegions(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	channel *packChannel, argvPairs map[string]string, held bool) ([]string, bool) {
	var asks []packload.RegionAsk
	for _, agent := range channel.scope.Agents() {
		d := channel.scope.Agent(agent)
		if d == nil || d.Provider == "" {
			continue // a grant-only process selects no provider
		}
		// EACH ENTRY OF THE AGENT'S ACTIVE SET (docs/design/active-provider-sets.md AP-P1): a
		// regional provider anywhere in pi's set needs its region delivered to pi. The region
		// file's lookup is the one the fill read for that provider (AgentDelivery.RegionFile).
		for _, provider := range packload.SetProvidersOf(d) {
			asks = append(asks, packload.RegionAsk{Agent: agent, Provider: provider,
				File: d.RegionFileFor(provider),
				Lookup: func(name string) (string, bool) {
					if v, found := argvPairs[name]; found && v != "" {
						return v, true
					}
					return channel.scope.DeliveredTo(agent, name)
				}})
		}
	}
	stranded := func(name string) bool { return o.Getenv(name) != "" }
	channels := []string{packload.FromPackEnv, packload.FromProfileEnv}
	if argvPairs != nil {
		channels = append(channels, packload.FromContainerArgv)
	}
	facts := packload.ProviderRegionGaps(packs, channel.providers, asks, stranded,
		packload.RegionConsulted(config.DescribeEnvSources(o.Workspace, cfg), channels...))
	return packload.ProviderRegionRefusal(facts, held)
}

// notePlatformSwitchConflicts is PP-D1 at the jail notch (docs/design/providers-and-profiles-
// redesign.md, ruled 2026-09-29): one line per agent whose own config switches it onto a provider
// platform its selection does not serve — CLAUDE_CODE_USE_BEDROCK in the user's own
// ~/.claude/settings.json, which reaches the jail as claude/settings' host layer, while claude's
// selected provider is not Bedrock — naming the conflict and both fixes. yolo deletes nothing it
// did not write, so the key reaches the jail as the user wrote it, and the credential gate, which
// is yolo's, sends claude no Bedrock credential: the failure is yolo's to name
// (packload.PlatformSwitchConflicts). A disclosure, not a refusal, so no quiet switch
// (docs/reference/report-tiers.md, OQ-RO3).
//
// Called beside the provider pre-flight at each of its three call sites — the fresh container
// launch, the attach delivery and every macos-user invocation — before it, so a launch that
// pre-flight then refuses still says so.
func (o *Options) notePlatformSwitchConflicts(packs []*packload.Pack, channel *packChannel) {
	if channel == nil {
		return
	}
	// The host's computed-leaf record says which switch `yolo host apply` wrote into the user's
	// file for the HOST selection (HC-D25): its line names yolo's write, not the user's.
	home := paths.Home()
	for _, c := range packload.PlatformSwitchConflicts(packs, channel.scope.Selection(),
		channel.resolvedProfiles, channel.providers, home, "", render.HostLeafWrote(home)) {
		o.pr(o.Stderr).print("[yellow]" + c.Line() + "[/yellow]")
	}
}

// printProviderRefusal renders a pre-flight's output: every VERDICT line in bold red, the
// facts under it plain. One renderer for both arms, so the same refusal reads the same way
// on a container and on a native sandbox.
//
// It renders the env-override refusal beside it (envoverrides.go) too — same shape, same
// three call sites, and a second renderer would be a second way for one class of
// pre-flight to look on the terminal.
//
// A VERDICT IS AN UNINDENTED LINE, not the first line. The lines can hold SEVERAL findings:
// checkEnvOverrides concatenates one block per tripped certain `overridden_by` entry, each
// opening with its own "Refusing to launch: …" verdict, and bolding only lines[0] left
// every later finding's verdict looking like one more fact under the first. Both
// producers already follow the convention this reads — packload's finding lines and
// ProviderCredentialGaps' facts are indented, verdicts are not —
// and TestPrintProviderRefusalBoldsEveryFindingsVerdict renders a real two-finding refusal
// through this, so a producer that broke it would fail there.
func (o *Options) printProviderRefusal(lines []string) {
	out := o.pr(o.Stderr)
	for _, line := range lines {
		if isVerdictLine(line) {
			out.printf("[bold red]%s[/bold red]", line)
			continue
		}
		out.print(line)
	}
}

// isVerdictLine reports whether a pre-flight line opens a finding: it is non-empty and not
// indented. See printProviderRefusal.
func isVerdictLine(line string) bool {
	return line != "" && line[0] != ' ' && line[0] != '\t'
}

// envPairs lifts every `-e K=V` pair out of an assembled container argv, keyed by K.
// Read off the argv rather than recomputed from the folds that produced it, so the
// pre-flight answers for the environment the container will actually start with and
// cannot drift from an assembly change.
func envPairs(argv []string) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-e" {
			continue
		}
		kv := argv[i+1]
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			out[kv[:eq]] = kv[eq+1:]
		}
	}
	return out
}
