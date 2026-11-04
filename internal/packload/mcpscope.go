package packload

import (
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Scope says whether a composed MCP server is supplied by user configuration alone or is
// specific to this workspace, and why.
type Scope struct {
	Level  string
	Reason string
}

// MCPServerScopes attributes each live effective server to the user layer only when the
// same-name server has the same sign-in identity there. Removal entries are absent.
func MCPServerScopes(effective, userOnly *jsonx.OrderedMap) map[string]Scope {
	out := map[string]Scope{}
	if effective == nil {
		return out
	}
	for _, name := range effective.Keys() {
		raw, _ := effective.Get(name)
		server, ok := mcpObject(raw)
		if !ok {
			continue
		}
		userRaw, exists := any(nil), false
		if userOnly != nil {
			userRaw, exists = userOnly.Get(name)
		}
		user, userOK := mcpObject(userRaw)
		if !exists || !userOK {
			out[name] = Scope{Level: "workspace", Reason: "not present in your user-level config"}
			continue
		}
		if sameMCPSignInIdentity(server, user) {
			out[name] = Scope{Level: "user", Reason: "same name and sign-in identity as your user-level server"}
			continue
		}
		out[name] = Scope{Level: "workspace", Reason: mcpIdentityDifference(server, user)}
	}
	return out
}

func mcpObject(value any) (map[string]any, bool) {
	plain := jsonx.Plain(value)
	object, ok := plain.(map[string]any)
	return object, ok && object != nil
}

func sameMCPSignInIdentity(a, b map[string]any) bool {
	remoteA, remoteAOK := a["url"].(string)
	remoteB, remoteBOK := b["url"].(string)
	if remoteAOK || remoteBOK {
		return remoteAOK && remoteBOK && remoteA == remoteB &&
			mcpStringDefault(a, "transport", "http") == mcpStringDefault(b, "transport", "http") &&
			reflect.DeepEqual(mcpMapDefault(a, "headers"), mcpMapDefault(b, "headers")) &&
			mcpOAuthClientID(a) == mcpOAuthClientID(b)
	}
	return reflect.DeepEqual(a["command"], b["command"]) && reflect.DeepEqual(a["args"], b["args"])
}

func mcpIdentityDifference(a, b map[string]any) string {
	if _, ar := a["url"]; ar {
		if _, br := b["url"]; !br {
			return "its transport differs from your user-level server"
		}
		if a["url"] != b["url"] {
			return "its url differs from your user-level server"
		}
		if mcpStringDefault(a, "transport", "http") != mcpStringDefault(b, "transport", "http") {
			return "its transport differs from your user-level server"
		}
		if !reflect.DeepEqual(mcpMapDefault(a, "headers"), mcpMapDefault(b, "headers")) {
			return "its headers differ from your user-level server"
		}
		if mcpOAuthClientID(a) != mcpOAuthClientID(b) {
			return "its OAuth client id differs from your user-level server"
		}
	} else if _, br := b["url"]; br {
		return "its transport differs from your user-level server"
	} else {
		if !reflect.DeepEqual(a["command"], b["command"]) {
			return "its command differs from your user-level server"
		}
		if !reflect.DeepEqual(a["args"], b["args"]) {
			return "its arguments differ from your user-level server"
		}
	}
	return "its sign-in identity differs from your user-level server"
}

func mcpStringDefault(server map[string]any, key, fallback string) string {
	if value, ok := server[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func mcpMapDefault(server map[string]any, key string) map[string]any {
	if value, ok := server[key].(map[string]any); ok && value != nil {
		return value
	}
	return map[string]any{}
}

func mcpOAuthClientID(server map[string]any) string {
	oauth, _ := server["oauth"].(map[string]any)
	if oauth == nil {
		return ""
	}
	id, _ := oauth["client_id"].(string)
	return id
}
