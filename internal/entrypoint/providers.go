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
