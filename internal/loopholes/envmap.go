package loopholes

// EnvMap is an insertion-ordered string->string map built from a JSON object
// (jail_env, host_daemon.env). Order matters: iteration order is the manifest's
// key order, and runtime_args_for emits `-e K=V` in that order, so argv
// byte-stability depends on preserving it.
type EnvMap struct {
	keys   []string
	values map[string]string
}

// NewEnvMap returns an empty EnvMap.
func NewEnvMap() *EnvMap {
	return &EnvMap{values: map[string]string{}}
}

// Set inserts or updates key. A new key is appended; updating an existing key
// keeps its position.
func (m *EnvMap) Set(key, value string) {
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

// Get returns the value for key and whether it is present.
func (m *EnvMap) Get(key string) (string, bool) {
	v, ok := m.values[key]
	return v, ok
}

// Keys returns the keys in insertion order (do not mutate).
func (m *EnvMap) Keys() []string { return m.keys }

// Len returns the number of entries.
func (m *EnvMap) Len() int { return len(m.keys) }

// Clone returns a shallow copy preserving order.
func (m *EnvMap) Clone() *EnvMap {
	out := NewEnvMap()
	for _, k := range m.keys {
		out.Set(k, m.values[k])
	}
	return out
}

// MergedWith returns the merge of m and other: m's entries in order, then
// other's entries (new keys appended, existing keys updated in place — the
// left operand's key order wins for shared keys).
func (m *EnvMap) MergedWith(other *EnvMap) *EnvMap {
	out := m.Clone()
	if other != nil {
		for _, k := range other.keys {
			out.Set(k, other.values[k])
		}
	}
	return out
}
