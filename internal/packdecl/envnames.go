package packdecl

// envnames.go is the TYPE of a provider's `api_key_env_name`, which grew from one variable
// name into a list of them (docs/design/provider-credential-scope.md, OQ-CN1).
//
// WHY A LIST. The field is the association between a credential variable and the provider
// it authenticates, and the credential gate reads it to deliver each variable only to an
// agent that selected that provider. One name cannot express Bedrock: its three ruled
// credential routes (a bearer, a static key pair, an SSO pointer) arrive in different
// variables, and the provider that caused the leak the gate closes is the one a
// single-valued field could not describe.
//
// WHY STILL `api_key_env_name`, and a string when there is one. Every shipped manifest and
// every user config spells the one-variable form, and each consumer that points an agent at
// ONE variable (a derive writing `apiKey: "${ZAI_API_KEY}"`, the credential pre-flight, the
// wire bridge) keeps reading exactly that: KeyPointer is the one name when the list holds
// one, and nothing when it holds several — a multi-route provider has no single key an
// agent could be pointed at, and picking the first would, for Bedrock, compose a bearer
// token into claude's ANTHROPIC_AUTH_TOKEN.

import (
	"encoding/json"
	"fmt"
)

// EnvNames is a provider's credential variable NAMES — never values. It decodes from a JSON
// string (the one-variable spelling) or a non-empty JSON array of strings, and encodes back
// to a string when it holds exactly one name, so a one-variable manifest round-trips
// byte-for-byte.
type EnvNames []string

// UnmarshalJSON accepts a string or an array of strings, and refuses everything else. An
// EMPTY array is refused rather than read as "none": an author who wrote the key meant to
// name something, and a list that silently scopes nothing is the failure the gate exists to
// end.
func (n *EnvNames) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*n = EnvNames{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("api_key_env_name: want a variable name or a list of them")
	}
	if len(many) == 0 {
		return fmt.Errorf("api_key_env_name: an empty list names no variable — omit the key instead")
	}
	*n = EnvNames(many)
	return nil
}

// MarshalJSON writes one name as a string and several as an array — the inverse of
// UnmarshalJSON, so the one-variable form stays the one every reader already knows.
func (n EnvNames) MarshalJSON() ([]byte, error) {
	if len(n) == 1 {
		return json.Marshal(n[0])
	}
	return json.Marshal([]string(n))
}

// KeyPointer is the ONE variable an agent is pointed at, "" when there is none or several.
func (n EnvNames) KeyPointer() string {
	if len(n) == 1 {
		return n[0]
	}
	return ""
}

// EnvNamesFromValue lowers an ALREADY-DECODED JSON value into EnvNames — the same rule
// UnmarshalJSON applies, for the user's `providers` entry, which internal/config holds as an
// OrderedMap rather than decoding through this package. OptionDefaultFromValue is the
// precedent and the reason: a pack's manifest and a user's config entry are the same entry
// of one composed table, so what the field may hold is stated once. ok=false refuses the
// value; the caller words the refusal.
func EnvNamesFromValue(v any) (EnvNames, bool) {
	switch t := v.(type) {
	case string:
		return EnvNames{t}, true
	case []any:
		if len(t) == 0 {
			return nil, false
		}
		out := make(EnvNames, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		if len(t) == 0 {
			return nil, false
		}
		return EnvNames(append([]string(nil), t...)), true
	}
	return nil, false
}

// Problems reports each name that is not a usable environment variable name, and each
// name listed twice. label prefixes every line.
func (n EnvNames) Problems(label string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range n {
		if !ValidEnvName(name) {
			out = append(out, fmt.Sprintf("%s.api_key_env_name: invalid env var name %q "+
				"(must match [A-Za-z_][A-Za-z0-9_]*)", label, name))
			continue
		}
		if seen[name] {
			out = append(out, fmt.Sprintf("%s.api_key_env_name: %s is listed twice", label, name))
		}
		seen[name] = true
	}
	return out
}

// ValidEnvName reports whether s is a portable environment variable name:
// [A-Za-z_][A-Za-z0-9_]*.
func ValidEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
