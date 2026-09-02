package entrypoint

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LoadProviders reads the YOLO_PROVIDERS JSON object passed into the jail environment.
// Returns an OrderedMap whose key order follows declaration order.
func (e *Env) LoadProviders() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	raw := e.Getenv("YOLO_PROVIDERS")
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		return m
	}
	return out
}

// LoadUseProfiles reads the YOLO_USE_PROFILES JSON object passed into the jail environment.
// Returns an OrderedMap mapping agent names to active profile names.
func (e *Env) LoadUseProfiles() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	raw := e.Getenv("YOLO_USE_PROFILES")
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		return m
	}
	return out
}

// Profile is one resolved profile as the launch delivered it in YOLO_PROFILES: the
// provider it selects and the option map active under it — the provider-declared
// defaults with the profile's own values over them (provider-catalog-and-selection.md
// §5.2). Resolved HOST-side, by packload.ResolveProfiles; this is the read half, and it
// never re-derives, or the jail would hold a second implementation of the merge.
type Profile struct {
	Provider string
	Options  map[string]string
}

// LoadProfiles reads the YOLO_PROFILES JSON object passed into the jail environment —
// the resolved table for every profile name this launch could activate, keyed by profile
// NAME (not by CLI: the same name can be active for several agents, which is the point of
// it being a selection rather than a per-agent setting).
//
// Absent, empty, or undecodable is an EMPTY map, the same answer LoadProviders gives for
// its table: a launch that composed no profiles (or an older launcher that emitted no
// variable) has no profiles, and that is not an error to recover from here — the
// launcher's composition is the one this side reads.
func (e *Env) LoadProfiles() map[string]Profile {
	out := map[string]Profile{}
	raw := e.Getenv("YOLO_PROFILES")
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return out
	}
	for _, name := range m.Keys() {
		v, _ := m.Get(name)
		entry, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			continue
		}
		p := Profile{Options: map[string]string{}}
		for _, key := range entry.Keys() {
			val, _ := entry.Get(key)
			s, isStr := val.(string)
			if !isStr {
				continue
			}
			if key == "provider" {
				p.Provider = s
				continue
			}
			p.Options[key] = s
		}
		out[name] = p
	}
	return out
}
