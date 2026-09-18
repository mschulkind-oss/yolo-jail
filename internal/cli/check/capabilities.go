package check

// capabilities.go predicts the launch's capability gate.
//
// The gate itself is `(*run.Options).refuseUnmetCapabilities`, the last step of the run
// config gate: a config that DECLARES it needs a capability and declares nothing that
// provides it stops the launch. It shipped there and only there, so until this file a
// config `yolo check` called clean was still refused at launch — the one thing `check`
// exists to prevent.
//
// WHY A SECOND COPY RATHER THAN AN IMPORT. `checkPresetNullConflicts` is already defined
// twice, once in `run/preflight.go` and once in this package, and this file follows that
// precedent deliberately. The run-side gate is an unexported METHOD on `run.Options` that
// prints through that type's own printer, so reaching it means either exporting a run
// internal and dragging its printer in, or hoisting the census into a third package that
// both import. Both are larger changes than the rule is, and the rule is twelve lines of
// map-building. The cost is the honest one: two copies can disagree, and the tests below
// are what notices.
//
// ⚠ AND THE COPY MUST NOT BE "BETTER" THAN THE ORIGINAL — this is the one way to get this
// file wrong. The launch's census reads the MERGED USER CONFIG only, because a pack's own
// `capabilities` reaches the launch through `packload.ComposeProviders`, which runs BELOW
// the gate. This package imports `internal/packload`, `internal/packreg` and
// `internal/packstage` directly, so it *could* consult declarations the launch cannot see
// — and would then pass a config the launch refuses, which is this file's own defect with
// the sign flipped. A prediction is only useful if it is wrong in the same places as the
// thing it predicts. Do not add pack data here; if the gate's input widens, widen it
// there and mirror it.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// allowUnmetCapabilitiesEnv mirrors `run.AllowUnmetCapabilitiesEnv`. Named here rather
// than imported for the same reason the census is copied.
const allowUnmetCapabilitiesEnv = "YOLO_ALLOW_UNMET_CAPABILITIES"

// capabilityBaseline mirrors the launch's `requiredCapabilityBaseline`: the two names
// every agent meets, which the config reference documents as writable, so a gate that
// refused them would refuse the documented spelling.
var capabilityBaseline = []string{"code_editing", "command_execution"}

// unmetCapabilities returns the `required_capabilities` entries nothing in cfg satisfies,
// in declaration order and de-duplicated.
//
// Two declaration surfaces, and they are the whole census of this config:
// `providers.<name>.capabilities`, and an `mcp_servers.<name>` entry whose `provides`
// names the capability. A provider satisfies whether or not a profile selects it — the
// same deliberate over-permission the launch has, because narrowing to the ACTIVE
// provider needs a channel composed below the gate.
//
// A null-valued provider or server declares NOTHING: the null removes the entry rather
// than running it, so a `"tavily": null` in the workspace must not keep satisfying
// `web_search` off the user-level entry it just deleted.
func unmetCapabilities(cfg *jsonx.OrderedMap) []string {
	have := map[string]bool{}
	for _, name := range capabilityBaseline {
		have[name] = true
	}
	if providers := subMap(cfg, "providers"); providers != nil {
		for _, name := range providers.Keys() {
			pm := subMap(providers, name)
			if pm == nil {
				continue
			}
			for _, capName := range strList(pm, "capabilities") {
				have[capName] = true
			}
		}
	}
	if servers := subMap(cfg, "mcp_servers"); servers != nil {
		for _, name := range servers.Keys() {
			sm := subMap(servers, name)
			if sm == nil {
				continue
			}
			if provides := str(sm, "provides"); provides != "" {
				have[provides] = true
			}
		}
	}

	seen := map[string]bool{}
	var missing []string
	for _, name := range strList(cfg, "required_capabilities") {
		if name == "" || have[name] || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	return missing
}

// capabilityGap reports what the Merged Configuration block should say about the
// capability gate, as (errors, warnings) in this section's own vocabulary.
//
// Three outcomes, and the middle one is the ruling worth reading:
//
//   - No gap → NOTHING, not a PASS line. It matches the launch, which never announces a
//     gate it did not trip, and it keeps `noRuntimeGolden` — which pins section ordering,
//     badge semantics AND the pass/warn/fail counts — from needing a bump for a line that
//     says nothing happened.
//   - A gap, with the hatch set → a WARN, not a FAIL. `check` exists to predict the
//     launch, and with the hatch set the launch PROCEEDS, so a FAIL would be a false
//     prediction. Silence would be worse: it would reintroduce exactly this file's defect
//     one layer up, a config that checks clean and then behaves in a way nobody was told
//     about. So it reports both halves — there is a gap, and the launch will run anyway.
//   - A gap, no hatch → a FAIL, appended to the section's errors, so `check`'s exit code
//     says what the launch will do. The launch refusal is fatal; a prediction of it that
//     exited 0 would be the defect again.
func capabilityGap(cfg *jsonx.OrderedMap, hatch string) (errs []string, warns []string) {
	missing := unmetCapabilities(cfg)
	if len(missing) == 0 {
		return nil, nil
	}
	named := "'" + strings.Join(missing, "', '") + "'"
	if hatch != "" {
		return nil, []string{"config.required_capabilities declares " + named +
			" that nothing in this config satisfies — the launch will CONTINUE because " +
			allowUnmetCapabilitiesEnv + " is set, with the capability still missing"}
	}
	return []string{"config.required_capabilities declares " + named +
		", and nothing this config declares satisfies it — this launch will be REFUSED. " +
		"Satisfy it with `providers.<name>.capabilities` naming it, or an " +
		"`mcp_servers.<name>` entry with \"provides\": \"<capability>\"; or drop the name; " +
		"or set " + allowUnmetCapabilitiesEnv + "=1 to launch anyway"}, nil
}

// --- small readers, so this file does not reach into run's cfgval helpers ---

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

func strList(m *jsonx.OrderedMap, key string) []string {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
