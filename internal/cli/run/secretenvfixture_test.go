package run

import (
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// testOnlyProviderKeys are the credential names the providers these tests declare inline claim
// (no shipped pack does): a fixture's value of one is a provider's key, so it lives in the
// private set (docs/design/secret-env-scoping.md §3.4).
var testOnlyProviderKeys = map[string]bool{
	"ROUTE_BEARER":  true,
	"ROUTE_PAIR_ID": true,
	"WIDGET_TOKEN":  true,
	"FXP_KEY":       true,
	"ACME_API_KEY":  true,
}

var (
	shippedClaimsOnce sync.Once
	shippedClaims     map[string][]packload.ShippedClaim
)

// isFixtureProviderKey reports whether a fixture's name is a provider's key: one a shipped
// provider claims, or one a provider these tests declare claims.
func isFixtureProviderKey(name string) bool {
	shippedClaimsOnce.Do(func() { shippedClaims = packload.UnselectedProviderClaims(nil) })
	return testOnlyProviderKeys[name] || len(shippedClaims[name]) > 0
}

// hydrate is a fixture's flat env map as the launch hydrates it: every provider key in the
// private set, every other name in the public set, and a nil value a removal. A nil map stays nil,
// which tells composePackChannel to hydrate from the config itself.
func hydrate(m *jsonx.OrderedMap) *config.HydratedEnv {
	if m == nil {
		return nil
	}
	h := config.NewHydratedEnv()
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, ok := v.(string)
		switch {
		case v == nil:
			h.Remove(k)
		case !ok:
		case isFixtureProviderKey(k):
			h.SetPrivate(k, s, config.EnvPlace{Private: true, Key: "env.secrets.set"})
		default:
			h.SetPublic(k, s, config.EnvPlace{Key: "env.set"})
		}
	}
	return h
}
