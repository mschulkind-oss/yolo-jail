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
// WHY HERE and not in stagePacks beside the other bespoke pre-flights: the question it
// answers is "does the assembled launch environment carry the key", and until env_sources
// is hydrated and the argv built, there is no assembled launch environment. WHY NOT LATER:
// past this point the pipeline spawns host daemons and takes the terminal, and a refusal
// that arrives after a loophole daemon is already running cleans up badly.
//
// The escape hatch is consulted only where it suppresses something — a launch with no gap
// never announces it, which is the same rule the reachability witness's override notice
// follows. When it DOES suppress, the notice says what it is suppressing rather than going
// quiet: nothing was repaired, and the agent's first request against that provider still
// fails.
//
// The container arm only, as a consequence of living in runContainer: macos-user returns
// above the dispatch and composes no env_sources table, so this notch says nothing there.
func (o *Options) checkProviderCredentials(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	userEnv *jsonx.OrderedMap, runCmd []string) (lines []string, refuse bool) {
	consulted := config.DescribeEnvSources(o.Workspace, cfg)
	consulted = append(consulted, "the environment yolo was launched from")
	facts := packload.ProviderCredentialGaps(packs, composedProviders(cfg, packs),
		o.launchEnvLookup(userEnv, runCmd), consulted)
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

// launchEnvLookup is what "set in this launch's environment" means at the jail notch, in
// the order the launch would have used the value: the env_sources hydration, then the -e
// pairs of the assembled argv (pack env, a variant's env, and the env_shape aliases the
// composition already relayed from somewhere), then the environment yolo itself was
// launched from — which the relay can draw on, so a key exported in the invoking shell
// counts as delivered even though no -e pair carries it verbatim.
//
// An EMPTY value is unset, at every step: agentenv drops an empty placeholder result
// rather than composing an empty token, and an empty credential is the failure this
// pre-flight exists to name, not an escape from it.
func (o *Options) launchEnvLookup(userEnv *jsonx.OrderedMap, runCmd []string) func(string) (string, bool) {
	envArgs := envPairs(runCmd)
	return func(name string) (string, bool) {
		if s := mapStr(userEnv, name); s != "" {
			return s, true
		}
		if v := envArgs[name]; v != "" {
			return v, true
		}
		if v := o.Getenv(name); v != "" {
			return v, true
		}
		return "", false
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
