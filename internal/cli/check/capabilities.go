package check

// capabilities.go predicts the launch's capability gate.
//
// The gate itself is `(*run.Options).refuseUnmetCapabilities`, the last step of the run
// config gate: a config that DECLARES it needs a capability, with nothing in the config or its
// selected packs that provides it, stops the launch. It shipped there and only there, so until
// this file a config `yolo check` called clean was still refused at launch — the one thing
// `check` exists to prevent.
//
// ONE CENSUS, NOT A COPY. This file used to restate the launch's census, on the argument that
// it was twelve lines of map-building and reaching the launch's would drag run.Options' printer
// along. The census stopped being twelve lines when the selected packs' own declarations began
// to count (agent-auth-modes.md §6.1 clause 1: each installed agent's active source, the
// provider its profile selects or its built-in login), so it moved to config.UnmetCapabilities,
// which both call. What is still this file's own is the LAUNCH handed to it: the launch hands
// its own (its `-p` picks profiles and can add a `via` pack), and `check` hands the one its
// config describes (config.ConfigCapabilityLaunch: the user scope's selection under the
// `profile` key), because a `-p` is an argument to a launch that has not happened — the same
// limit protocols.go's ⚠ states for its gate.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// allowUnmetCapabilitiesEnv is the launch's hatch, config.AllowUnmetCapabilitiesEnv.
const allowUnmetCapabilitiesEnv = config.AllowUnmetCapabilitiesEnv

// capabilityGap reports what the Merged Configuration block should say about the
// capability gate, as (errors, warnings) in this section's own vocabulary. launch is the
// selection and profiles the census counts once the config's own declarations leave a name
// unmet; nil counts no pack.
//
// Four outcomes, and the middle two are the rulings worth reading:
//
//   - No gap → NOTHING, not a PASS line. It matches the launch, which never announces a
//     gate it did not trip, and it keeps `noRuntimeGolden` — which pins section ordering,
//     badge semantics AND the pass/warn/fail counts — from needing a bump for a line that
//     says nothing happened.
//   - A gap the census could not prove (a selected pack it could not read, a provider table
//     that did not compose, profiles that did not resolve) → a WARN naming why, never silence and never a FAIL: the launch
//     does not refuse it either, and reports that fault itself (protocols.go's "I could not
//     look" rule).
//   - A gap, with the hatch set → a WARN, not a FAIL. `check` exists to predict the
//     launch, and with the hatch set the launch PROCEEDS, so a FAIL would be a false
//     prediction. Silence would be worse: it would reintroduce exactly this file's defect
//     one layer up, a config that checks clean and then behaves in a way nobody was told
//     about. So it reports both halves — there is a gap, and the launch will run anyway.
//   - A gap, no hatch → a FAIL, appended to the section's errors, so `check`'s exit code
//     says what the launch will do. The launch refusal is fatal; a prediction of it that
//     exited 0 would be the defect again.
func capabilityGap(cfg *jsonx.OrderedMap, hatch string, launch *config.CapabilityLaunch) (errs []string, warns []string) {
	missing, err := config.UnmetCapabilities(cfg, launch)
	if len(missing) == 0 {
		return nil, nil
	}
	named := "'" + strings.Join(missing, "', '") + "'"
	if err != nil {
		return nil, []string{"Could not predict the capability gate for config.required_capabilities " +
			named + ": " + err.Error() + ". The launch does not refuse over it, and reports " +
			"that problem itself; the capability is unchecked until it is fixed"}
	}
	if hatch != "" {
		return nil, []string{"config.required_capabilities declares " + named +
			" that nothing in this launch satisfies — the launch will CONTINUE because " +
			allowUnmetCapabilitiesEnv + " is set, with the capability still missing"}
	}
	return []string{"config.required_capabilities declares " + named +
		", and nothing this config or its selected packs declare satisfies it — this launch " +
		"will be REFUSED. Satisfy it with `providers.<name>.capabilities` naming it, an " +
		"`mcp_servers.<name>` entry with \"provides\": \"<capability>\", or a selected agent " +
		"whose pack declares it for the source the agent runs on (its built-in login, or the " +
		"provider its profile selects); or drop the name; or set " +
		allowUnmetCapabilitiesEnv + "=1 to launch anyway"}, nil
}

// --- small readers, so this package does not reach into run's cfgval helpers ---

func subMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	om, _ := v.(*jsonx.OrderedMap)
	return om
}

func str(m *jsonx.OrderedMap, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m.Get(key)
	s, _ := v.(string)
	return s
}
