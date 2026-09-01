package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// checkProviderCredentials is the SEVENTH bespoke launch pre-flight
// (profiles-as-pack-variants.md §6.2 as rescoped by OQ-13): a SELECTED pack that requires
// a provider — by shipping one, or by a variant naming one — is refused when the composed
// providers table has no such entry or when the credential variable that entry points at
// is not set in what this launch would deliver. It returns the lines to print and whether
// the caller must stop: lines alone cannot carry that, because the escape hatch turns a
// refusal into a LOUD CONTINUATION, and a caller that only looked at len(lines) would exit
// on the notice (measured — this is the bug the first nested launch caught).
//
// Scoped to the SELECTED set, not to the active profile, per OQ-13: "configured but never
// selected stays inert" is withdrawn, because a variant that resolved to nothing has
// already written a config pointing at a provider the launch never delivered, and the
// symptom is the mysterious auth failure §6.1 records rather than anything yolo says.
// The packs argument IS the selected set — the same slice staging produced — so an
// unselected pack cannot reach this.
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
func (o *Options) checkProviderCredentials(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	channel *packChannel, argvPairs map[string]string) (lines []string, refuse bool) {
	consulted := config.DescribeEnvSources(o.Workspace, cfg)
	consulted = append(consulted, "the environment yolo was launched from")
	facts := packload.ProviderCredentialGaps(packs, channel.providers,
		channel.deliveryLookup(o, argvPairs), consulted)
	if len(facts) == 0 {
		return nil, false
	}
	if o.Getenv(paths.AllowMissingProvidersEnv) != "" {
		return append([]string{"Warning: " + paths.AllowMissingProvidersEnv +
			" is set — CONTINUING, with a selected pack's provider credential still missing. " +
			"Nothing was repaired: the agent's first request against that provider will " +
			"still fail."}, facts...), false
	}
	return append([]string{
		"Refusing to launch: a selected pack needs a provider this launch cannot deliver.",
	}, append(facts,
		"  Put the variable in one of the consulted channels, or launch anyway with "+
			paths.AllowMissingProvidersEnv+"=1.")...), true
}

// printProviderRefusal renders the check's output: the first line as the bold verdict,
// the rest as its facts. One renderer for both arms, so the same refusal reads the same
// way on a container and on a native sandbox.
func (o *Options) printProviderRefusal(lines []string) {
	out := o.pr(o.Stderr)
	for i, line := range lines {
		if i == 0 {
			out.printf("[bold red]%s[/bold red]", line)
			continue
		}
		out.print(line)
	}
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
