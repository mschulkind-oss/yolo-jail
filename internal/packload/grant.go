package packload

// grant.go is the --with-credentials GRANT's resolution and its per-provider account, read by
// every notch (docs/design/credential-sources-separation.md OQ-ES5): `yolo host` and a jail launch
// resolve one request the same way and report it in the same words, so one flag cannot mean two
// things at two notches. What differs is the vehicle: the host's grant is a recipient of the gate
// (ScopeInput.Grants), and a jail's is held by every process of the jail, outside the gate's
// per-agent deliveries (internal/cli/run's jailgrant.go, ES-D31).

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// GrantAll is the grant's keyword for every composed provider that claims a value env_sources
// holds (ClaimingProviders). A provider literally named `all` cannot be granted by name (ES-D14).
const GrantAll = "all"

// UnknownGrantError is a grant naming a provider the composed table does not hold. Known is every
// composed provider, sorted, which the refusal names, so a typo is never read as "the provider has
// no key".
type UnknownGrantError struct {
	Unknown []string
	Known   []string
}

func (e *UnknownGrantError) Error() string {
	quoted := make([]string, len(e.Unknown))
	for i, u := range e.Unknown {
		quoted[i] = fmt.Sprintf("%q", u)
	}
	if len(e.Known) == 0 {
		return fmt.Sprintf("--with-credentials names %s, and no provider is composed at this notch: no "+
			"selected pack ships one and %s declares none under `providers`",
			strings.Join(quoted, ", "), paths.UserConfigPath())
	}
	return fmt.Sprintf("--with-credentials names %s, which no composed provider is: the known providers "+
		"are %s (or `all`, every one that claims a value in env_sources)",
		strings.Join(quoted, ", "), strings.Join(e.Known, ", "))
}

// ResolveGrant resolves a --with-credentials request's names over the composed provider table and
// the hydrated env_sources: the providers it grants, sorted and deduplicated. A name the table does
// not hold refuses (*UnknownGrantError); `all` is every provider that claims a name env_sources
// holds, and may stand beside names.
func ResolveGrant(names []string, providers, envSources *jsonx.OrderedMap) ([]string, error) {
	var known []string
	if providers != nil {
		known = append(known, providers.Keys()...)
	}
	sort.Strings(known)
	var named, unknown []string
	all := false
	for _, n := range names {
		switch {
		case n == GrantAll:
			all = true
		case slices.Contains(known, n):
			named = append(named, n)
		default:
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return nil, &UnknownGrantError{Unknown: unknown, Known: known}
	}
	if all {
		named = append(named, ClaimingProviders(providers, envSources)...)
	}
	sort.Strings(named)
	return slices.Compact(named), nil
}

// GrantFor is what a grant of providers hands a process from this launch's composition: per
// provider, sorted, the names it claims that env_sources holds and every name it claims
// (GrantedProvider, names only), and the granted values themselves, in hydration order. The
// claims are the gate's own (credentialClaims), so the grant and the gate's lines cannot disagree
// about which names a provider owns.
func (s *CredentialScope) GrantFor(providers []string) ([]GrantedProvider, *jsonx.OrderedMap) {
	env := jsonx.NewOrderedMap()
	if s == nil {
		return nil, env
	}
	granted := sortedUnique(providers)
	out := make([]GrantedProvider, 0, len(granted))
	for _, p := range granted {
		g := GrantedProvider{Provider: p}
		for name, claimants := range s.claims {
			if slices.Contains(claimants, p) {
				g.Claims = append(g.Claims, name)
			}
		}
		sort.Strings(g.Claims)
		for _, k := range s.envSources.Keys() {
			if slices.Contains(s.claims[k], p) {
				g.Delivered = append(g.Delivered, k)
			}
		}
		out = append(out, g)
	}
	for _, k := range s.envSources.Keys() {
		for _, p := range granted {
			if slices.Contains(s.claims[k], p) {
				v, _ := s.envSources.Get(k)
				env.Set(k, v)
				break
			}
		}
	}
	return out, env
}

// GrantProviderLines is a grant's per-provider account, one line per granted provider: the names
// it delivered, or that it delivered nothing, which is reported rather than skipped (OQ-ES5: "a
// named provider with no value is reported"). Names only, never a value. Every notch's grant
// disclosure ends with these lines.
func GrantProviderLines(granted []GrantedProvider) []string {
	if len(granted) == 0 {
		return []string{"  nothing granted: no composed provider claims a value env_sources holds"}
	}
	var lines []string
	for _, g := range granted {
		switch {
		case len(g.Delivered) > 0:
			lines = append(lines, fmt.Sprintf("  %s: %s", g.Provider, strings.Join(g.Delivered, ", ")))
		case len(g.Claims) == 0:
			lines = append(lines, fmt.Sprintf("  %s: nothing granted — it claims no credential "+
				"name (no api_key_env_name), so there is no value to hand over", g.Provider))
		default:
			lines = append(lines, fmt.Sprintf("  %s: nothing granted — env_sources holds no value "+
				"for the names it claims (%s)", g.Provider, strings.Join(g.Claims, ", ")))
		}
	}
	return lines
}
