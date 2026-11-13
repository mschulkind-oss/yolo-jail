package config

// envprivateredact.go redacts the values of the private env set's inline table, [env.secrets]
// set, wherever a copy of the merged config is written or printed outside the launch itself
// (docs/reference/providers.md#the-two-env-sets-public-and-private: a private value reaches only the agents that claim it, and
// never a file or a line a jail or a log reader can see). Names stay, so a reader still sees
// which names the private set holds; values never do.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// RedactedEnvValue stands in for a private inline value.
const RedactedEnvValue = "<redacted>"

// RedactPrivateEnvValues is cfg with every value under env.secrets.set replaced by
// RedactedEnvValue. cfg is not changed; with no such table it is returned as is.
func RedactPrivateEnvValues(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	set := privateSetOf(cfg)
	if set == nil || set.Len() == 0 {
		return cfg
	}
	out := jsonx.DeepCopyMap(cfg)
	redacted := privateSetOf(out)
	for _, k := range redacted.Keys() {
		redacted.Set(k, RedactedEnvValue)
	}
	return out
}

func privateSetOf(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	env, _ := cfg.Get("env")
	envMap, _ := env.(*jsonx.OrderedMap)
	if envMap == nil {
		return nil
	}
	secrets, _ := envMap.Get("secrets")
	secretsMap, _ := secrets.(*jsonx.OrderedMap)
	if secretsMap == nil {
		return nil
	}
	set, _ := secretsMap.Get("set")
	setMap, _ := set.(*jsonx.OrderedMap)
	return setMap
}

// RedactPrivateEnvSnapshot is the canonical snapshot text of a config with its private inline
// values redacted. Text that is not a JSON object is returned unchanged.
func RedactPrivateEnvSnapshot(text string) string {
	v, err := jsonx.Decode([]byte(text))
	m, ok := v.(*jsonx.OrderedMap)
	if err != nil || !ok {
		return text
	}
	if privateSetOf(m) == nil {
		return text
	}
	out, err := SnapshotJSON(RedactPrivateEnvValues(m))
	if err != nil {
		return text
	}
	return out
}

// RedactPrivateEnvAt is value, found at the config path steps, with anything under
// env.secrets.set replaced by RedactedEnvValue: a value at or below a name of the table, or a
// table above it (env, env.secrets, env.secrets.set) holding one.
func RedactPrivateEnvAt(steps []configdoc.Step, value any) any {
	var keys []string
	for _, s := range steps {
		if s.IsIndex {
			return value
		}
		keys = append(keys, s.Key)
	}
	private := []string{"env", "secrets", "set"}
	if len(keys) > len(private) {
		if keys[0] == "env" && keys[1] == "secrets" && keys[2] == "set" {
			return RedactedEnvValue
		}
		return value
	}
	// Wrap value in the path's tables, redact the whole, and unwrap.
	root := jsonx.NewOrderedMap()
	cur := root
	for i, k := range keys {
		if i == len(keys)-1 {
			cur.Set(k, value)
			break
		}
		next := jsonx.NewOrderedMap()
		cur.Set(k, next)
		cur = next
	}
	if len(keys) == 0 {
		m, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return value
		}
		return RedactPrivateEnvValues(m)
	}
	redacted := RedactPrivateEnvValues(root)
	var got any = redacted
	for _, k := range keys {
		m, _ := got.(*jsonx.OrderedMap)
		if m == nil {
			return value
		}
		got, _ = m.Get(k)
	}
	return got
}
