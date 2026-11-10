package packload

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"os"
	"path/filepath"
)

// shippedManifestPath is the manifest a materialized shipped pack carries at root: pack.toml
// once the pack is converted, pack.json while it stays on its legacy manifest.
func shippedManifestPath(root string) string {
	toml := filepath.Join(root, "pack.toml")
	if _, err := os.Stat(toml); err == nil {
		return toml
	}
	return filepath.Join(root, "pack.json")
}

// shippedProviderNamedProfiles are the profiles the shipped packs used to declare over a provider
// of the same name, which a provider is now selectable by (OQ-RC3). The config layer's
// SelectableProfiles supplies them to a real launch; a test inside this package cannot import it,
// so withShippedProviderProfiles hands ResolveProfiles the same selection for each provider the
// composed table holds.
var shippedProviderNamedProfiles = []string{"zai", "openrouter", "llamacpp", "kilo", "cerebras", "bedrock", "bedrock-key", "meta"}

func withShippedProviderProfiles(user map[string]UserProfile, providers *jsonx.OrderedMap) map[string]UserProfile {
	out := map[string]UserProfile{}
	for name, profile := range user {
		out[name] = profile
	}
	if providers == nil {
		return out
	}
	for _, name := range shippedProviderNamedProfiles {
		if _, declared := providers.Get(name); !declared {
			continue
		}
		if _, taken := out[name]; !taken {
			out[name] = UserProfile{Provider: name}
		}
	}
	return out
}
