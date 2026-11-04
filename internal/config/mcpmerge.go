package config

import (
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// secureMCPServerMerge keeps remote headers bound to the destination they were written for.
// Config maps otherwise recursively merge, so a higher-scope URL override must not inherit a
// lower-scope credential. A transport transition is a new destination as well.
func secureMCPServerMerge(base, override, merged *jsonx.OrderedMap) {
	if base == nil || override == nil || merged == nil {
		return
	}
	for _, name := range override.Keys() {
		baseValue, basePresent := base.Get(name)
		overrideValue, overridePresent := override.Get(name)
		baseServer, baseOK := baseValue.(*jsonx.OrderedMap)
		overrideServer, overrideOK := overrideValue.(*jsonx.OrderedMap)
		mergedValue, mergedPresent := merged.Get(name)
		mergedServer, mergedOK := mergedValue.(*jsonx.OrderedMap)
		if !basePresent || !overridePresent || !baseOK || !overrideOK || !mergedPresent || !mergedOK {
			continue
		}

		baseURLValue, baseHasURL := baseServer.Get("url")
		baseRemote := baseHasURL && baseURLValue != nil
		baseCommand, baseHasCommand := baseServer.Get("command")
		baseLocal := baseHasCommand && baseCommand != nil
		effectiveURL, effectiveHasURL := mergedServer.Get("url")
		effectiveRemote := effectiveHasURL && effectiveURL != nil
		overrideCommand, overrideHasCommand := overrideServer.Get("command")
		overrideLocal := overrideHasCommand && overrideCommand != nil
		overrideURL, overrideHasURL := overrideServer.Get("url")
		overrideRemote := overrideHasURL && overrideURL != nil
		switch {
		case baseRemote && overrideLocal && !overrideRemote:
			for _, key := range []string{"url", "transport", "headers", "oauth"} {
				mergedServer.Delete(key)
			}
		case baseLocal && overrideRemote && !overrideLocal:
			for _, key := range []string{"command", "args", "env"} {
				mergedServer.Delete(key)
			}
		case baseRemote && effectiveRemote:
			baseTransport := mcpTransport(baseServer)
			effectiveTransport := mcpTransport(mergedServer)
			if !reflect.DeepEqual(baseURLValue, effectiveURL) || !reflect.DeepEqual(baseTransport, effectiveTransport) {
				if headers, explicit := overrideServer.Get("headers"); explicit && headers != nil {
					mergedServer.Set("headers", jsonx.DeepCopy(headers))
				} else {
					mergedServer.Delete("headers")
				}
			}
		}
	}
}

func mcpTransport(server *jsonx.OrderedMap) any {
	if value, ok := server.Get("transport"); ok {
		return value
	}
	return "http"
}
