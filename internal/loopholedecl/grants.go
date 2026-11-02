package loopholedecl

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// grants.go is the `[[grants]]` manifest block: the GRANT KINDS a loophole declares, the
// decisions a project can be granted at the config-change gate
// (docs/reference/loophole-grants.md §4.2; the spelling is the plan's PD-1).
//
// A loophole declaring none is UNGATED: it runs on selection and whatever it serves is served,
// approved by its own means (a login, a setting) or by having nothing of the user's to approve.
// A broker's grant kinds stay in `brokered.grants`, so a manifest declares one block or the
// other, never both.
//
// Like `brokered`, the block is a FENCE, never a grant: nothing in it widens what a jail can
// reach, because a kind only names what the user's ceiling and the gate may later grant. So a
// pack-shipped manifest may carry any value in it, and its census has one voice, refusal, in
// both decoders: a kind this build cannot read is a kind no gate could show.

// Grant is one declared grant kind.
type Grant struct {
	// Kind names the decision, as `requests.<pack>.<kind>` and `allow.<pack>.on_request` spell
	// it: lowercase snake case, e.g. "read", "read_all".
	Kind string
	// Text is the kind's one line, as the gate and the refusals print it: "read your user
	// journal".
	Text string
	// Valued marks a kind whose grant carries values the project names (process names, device
	// globs). A valued kind is granted only on request, never under `always`.
	Valued bool
	// RequestableByDefault puts the kind in the user's ceiling `on_request` when the user wrote
	// no `on_request` list. Never on a valued kind.
	RequestableByDefault bool
	// ValueNoun names a valued kind's values in refusals ("process names"). Valued kinds only.
	ValueNoun string
	// LegacyEnable marks the kind a legacy `loopholes.<name>.enabled: true` becomes. At most one
	// per manifest.
	LegacyEnable bool
	// LegacySetting is the legacy setting whose `true` becomes this kind ("full" → read_all).
	LegacySetting string
	// LegacyValues is the legacy setting whose list becomes this kind's values ("visible").
	// Valued kinds only.
	LegacyValues string
	// LegacyDefaultValues is the list the legacy setting named by LegacyValues defaulted to: a
	// legacy config that switched the loophole on and wrote no list ran on these values, so the
	// legacy projection reads them as the request's values in that case (serial's five device
	// globs). Declared here, beside LegacyValues, rather than kept as a list in the projection,
	// because the manifest that retired the setting is the one place that knew its default.
	// Valued kinds with LegacyValues only.
	LegacyDefaultValues []string
}

const (
	keyAccess               = "access"
	keyKind                 = "kind"
	keyText                 = "text"
	keyValued               = "valued"
	keyRequestableByDefault = "requestable_by_default"
	keyValueNoun            = "value_noun"
	keyLegacyEnable         = "legacy_enable"
	keyLegacySetting        = "legacy_setting"
	keyLegacyValues         = "legacy_values"
	keyLegacyDefaultValues  = "legacy_default_values"
)

// grantKeys is one `[[grants]]` entry's census.
var grantKeys = []string{keyKind, keyText, keyValued, keyRequestableByDefault, keyValueNoun,
	keyLegacyEnable, keyLegacySetting, keyLegacyValues, keyLegacyDefaultValues}

// loopholeGrantKindRE spells a loophole grant kind. Snake case rather than ValidGrantKind's
// hyphens, because the kind is a key of `[requests.<pack>]` and a value of the readable config's
// `on_request`, whose names are snake case (`read_all`, the design's own kind).
var loopholeGrantKindRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ReservedGrantsSettingName is the settings-file key the grant file reserves for the grants
// (plan PD-2), so no loophole may declare a setting of that name.
const ReservedGrantsSettingName = keyGrants

// parseGrants reads the top-level `grants` list. nil when absent or empty.
func parseGrants(manifestPath string, raw any) ([]Grant, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'grants' must be a list of [[grants]] tables, each with a 'kind' and a 'text'", manifestPath)
	}
	var out []Grant
	legacyEnable := ""
	for i, entry := range list {
		at := fmt.Sprintf("grants[%d]", i)
		m, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return nil, Errorf("%s: %s must be a table with a 'kind' and a 'text'", manifestPath, at)
		}
		for _, k := range m.Keys() {
			if !inList(k, grantKeys) {
				return nil, Errorf("%s: unknown key %q (known: %s) — a grant kind this build cannot "+
					"read is one no gate could show, so it is refused rather than ignored",
					manifestPath, at+"."+k, strings.Join(sortedCopy(grantKeys), ", "))
			}
		}
		var g Grant
		g.Kind, _ = getOrNil(m, keyKind).(string)
		if !loopholeGrantKindRE.MatchString(g.Kind) {
			return nil, Errorf("%s: %s.kind must name the grant in lowercase snake case, such as "+
				"\"read\" or \"read_all\" (it is the key a project writes under [requests.<pack>])",
				manifestPath, at)
		}
		at = fmt.Sprintf("grants[%d] (kind %q)", i, g.Kind)
		for _, prev := range out {
			if prev.Kind == g.Kind {
				return nil, Errorf("%s: 'grants' declares kind %q twice; declare each kind once", manifestPath, g.Kind)
			}
		}
		text, isStr := getOrNil(m, keyText).(string)
		if !isStr || strings.TrimSpace(text) == "" {
			return nil, Errorf("%s: %s needs a 'text', the one line the gate and every refusal print, "+
				"such as \"read your user journal\"", manifestPath, at)
		}
		if err := refuseControlChars(manifestPath, at+".text", text); err != nil {
			return nil, err
		}
		g.Text = text
		for _, f := range []struct {
			key string
			dst *bool
		}{{keyValued, &g.Valued}, {keyRequestableByDefault, &g.RequestableByDefault}, {keyLegacyEnable, &g.LegacyEnable}} {
			key, dst := f.key, f.dst
			if v, ok := m.Get(key); ok {
				b, isBool := v.(bool)
				if !isBool {
					return nil, Errorf("%s: %s.%s must be a boolean — write true or false", manifestPath, at, key)
				}
				*dst = b
			}
		}
		for _, f := range []struct {
			key string
			dst *string
		}{{keyValueNoun, &g.ValueNoun}, {keyLegacySetting, &g.LegacySetting}, {keyLegacyValues, &g.LegacyValues}} {
			key, dst := f.key, f.dst
			if v, ok := m.Get(key); ok {
				s, isStr := v.(string)
				if !isStr || s == "" {
					return nil, Errorf("%s: %s.%s must be a non-empty string", manifestPath, at, key)
				}
				if err := refuseControlChars(manifestPath, at+"."+key, s); err != nil {
					return nil, err
				}
				*dst = s
			}
		}
		for _, f := range [][2]string{{keyLegacySetting, g.LegacySetting}, {keyLegacyValues, g.LegacyValues}} {
			if f[1] != "" && !settingKeyRe.MatchString(f[1]) {
				return nil, Errorf("%s: %s.%s = %q must name a legacy setting, spelled as a setting name is",
					manifestPath, at, f[0], f[1])
			}
		}
		if v, ok := m.Get(keyLegacyDefaultValues); ok {
			list, isList := v.([]any)
			if !isList || len(list) == 0 {
				return nil, Errorf("%s: %s.%s must be a non-empty list of strings, the values the legacy "+
					"setting defaulted to", manifestPath, at, keyLegacyDefaultValues)
			}
			for j, e := range list {
				s, isStr := e.(string)
				if !isStr || s == "" {
					return nil, Errorf("%s: %s.%s[%d] must be a non-empty string", manifestPath, at, keyLegacyDefaultValues, j)
				}
				if err := refuseControlChars(manifestPath, fmt.Sprintf("%s.%s[%d]", at, keyLegacyDefaultValues, j), s); err != nil {
					return nil, err
				}
				g.LegacyDefaultValues = append(g.LegacyDefaultValues, s)
			}
		}
		if g.Valued && g.RequestableByDefault {
			return nil, Errorf("%s: %s is valued, so it cannot be requestable_by_default: its values are "+
				"the project's to name, and there is no default value for the ceiling to offer; drop "+
				"requestable_by_default", manifestPath, at)
		}
		if !g.Valued && g.ValueNoun != "" {
			return nil, Errorf("%s: %s sets value_noun but is not valued; value_noun names a valued "+
				"kind's values, so set valued = true or drop it", manifestPath, at)
		}
		if !g.Valued && g.LegacyValues != "" {
			return nil, Errorf("%s: %s sets legacy_values but is not valued; a legacy list becomes a "+
				"valued kind's values, so set valued = true or drop it", manifestPath, at)
		}
		if len(g.LegacyDefaultValues) > 0 && g.LegacyValues == "" {
			return nil, Errorf("%s: %s sets legacy_default_values but no legacy_values; the defaults "+
				"are the legacy setting's, so name that setting in legacy_values or drop them", manifestPath, at)
		}
		if g.LegacyEnable {
			if legacyEnable != "" {
				return nil, Errorf("%s: legacy_enable is set on both %q and %q; a legacy "+
					"`enabled: true` becomes exactly one kind, so set it on one", manifestPath, legacyEnable, g.Kind)
			}
			legacyEnable = g.Kind
		}
		out = append(out, g)
	}
	return out, nil
}

// parseAccess reads the top-level `access` line and holds it and the grants together: every
// refusal prints the access line, so grants without one are refused, and an access line with no
// grant kinds would describe a gate that does not exist.
func parseAccess(manifestPath string, raw any, grants []Grant) (string, error) {
	if raw == nil {
		if len(grants) > 0 {
			return "", Errorf("%s: this manifest declares [[grants]] and no 'access', the one line every "+
				"refusal prints for what the loophole reaches, such as \"read the host's journal\"; add it",
				manifestPath)
		}
		return "", nil
	}
	access, isStr := raw.(string)
	if !isStr || strings.TrimSpace(access) == "" {
		return "", Errorf("%s: 'access' must be a non-empty string, such as \"read the host's journal\"", manifestPath)
	}
	if err := refuseControlChars(manifestPath, "'access'", access); err != nil {
		return "", err
	}
	if len(grants) == 0 {
		return "", Errorf("%s: 'access' is set but the manifest declares no [[grants]], so no gate or "+
			"refusal would print it; declare the grant kinds or drop 'access'", manifestPath)
	}
	return access, nil
}
