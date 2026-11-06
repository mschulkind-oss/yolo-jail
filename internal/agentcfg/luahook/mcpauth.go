package luahook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// MCPServerRef is the server identity supplied to a pack's mcp_auth key function,
// or returned by its match function.
type MCPServerRef struct {
	Name      string
	URL       string
	Transport string
	Headers   map[string]string
	ClientID  string
	// Issuer is the authorization server a match function says the entry was issued by, or
	// "" when it names none. Only a match result carries it.
	Issuer string
}

type mcpAuthFunctions struct {
	key   *lua.LFunction
	match *lua.LFunction
}

func installMCPAuth(L *lua.LState, yolo *lua.LTable, session *deriveSession) {
	L.SetField(yolo, "sha256", L.NewFunction(func(L *lua.LState) int {
		sum := sha256.Sum256([]byte(L.CheckString(1)))
		L.Push(lua.LString(hex.EncodeToString(sum[:])))
		return 1
	}))
	L.SetField(yolo, "canonical_json", L.NewFunction(func(L *lua.LState) int {
		value, err := luaJSONValue(L.Get(1), map[*lua.LTable]bool{})
		if err != nil {
			L.RaiseError("canonical_json: %v", err)
			return 0
		}
		encoded, err := canonicalJSON(value)
		if err != nil {
			L.RaiseError("canonical_json: %v", err)
			return 0
		}
		L.Push(lua.LString(encoded))
		return 1
	}))
	L.SetField(yolo, "mcp_auth", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		declaration := L.CheckTable(2)
		key, ok := L.GetField(declaration, "key").(*lua.LFunction)
		if !ok {
			L.ArgError(2, "mcp_auth.key must be a function")
		}
		match, ok := L.GetField(declaration, "match").(*lua.LFunction)
		if !ok {
			L.ArgError(2, "mcp_auth.match must be a function")
		}
		session.mcpAuth[agent] = mcpAuthFunctions{key: key, match: match}
		return 0
	}))
}

// MCPAuthKeys runs a pack's mcp_auth key function once per composed server.
func MCPAuthKeys(script, agent string, servers []MCPServerRef) (map[string]string, error) {
	s, functions, err := mcpAuthSession(script, agent)
	if err != nil {
		return nil, err
	}
	defer s.close()

	keys := make(map[string]string, len(servers))
	for _, server := range servers {
		if _, exists := keys[server.Name]; exists {
			return nil, fmt.Errorf("luahook: duplicate MCP server name %q", server.Name)
		}
		arg, err := goToLua(s.L, mcpServerMap(server))
		if err != nil {
			return nil, err
		}
		if err := s.L.CallByParam(lua.P{Fn: functions.key, NRet: 1, Protect: true}, arg); err != nil {
			return nil, wrapLuaErr(err)
		}
		result := s.L.Get(-1)
		s.L.Pop(1)
		key, ok := result.(lua.LString)
		if !ok {
			return nil, fmt.Errorf("luahook: mcp_auth key for %s returned %s, want string", agent, result.Type())
		}
		keys[server.Name] = string(key)
	}
	return keys, nil
}

// MCPAuthMatch asks a pack whether an existing store entry belongs to the server
// identified by key. A nil Lua result means that the entry is not recognized.
func MCPAuthMatch(script, agent, key string, entry map[string]any) (*MCPServerRef, error) {
	s, functions, err := mcpAuthSession(script, agent)
	if err != nil {
		return nil, err
	}
	defer s.close()

	arg, err := goToLua(s.L, entry)
	if err != nil {
		return nil, err
	}
	if err := s.L.CallByParam(lua.P{Fn: functions.match, NRet: 1, Protect: true}, lua.LString(key), arg); err != nil {
		return nil, wrapLuaErr(err)
	}
	result := s.L.Get(-1)
	s.L.Pop(1)
	if result == lua.LNil {
		return nil, nil
	}
	matched, ok := result.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("luahook: mcp_auth match for %s returned %s, want table or nil", agent, result.Type())
	}
	name, nameOK := luaStringField(matched, "name")
	url, urlOK := luaStringField(matched, "url")
	if !nameOK || !urlOK {
		return nil, fmt.Errorf("luahook: mcp_auth match for %s must return string name and url fields", agent)
	}
	ref := &MCPServerRef{Name: name, URL: url}
	switch issuer := matched.RawGetString("issuer").(type) {
	case lua.LString:
		ref.Issuer = string(issuer)
	case *lua.LNilType:
	default:
		return nil, fmt.Errorf("luahook: mcp_auth match for %s returned a non-string issuer", agent)
	}
	return ref, nil
}

func mcpAuthSession(script, agent string) (*deriveSession, mcpAuthFunctions, error) {
	s, err := newDeriveSession(GopherLuaVM{}, script, &DeriveCtx{})
	if err != nil {
		return nil, mcpAuthFunctions{}, err
	}
	functions, ok := s.mcpAuth[agent]
	if !ok {
		s.close()
		return nil, mcpAuthFunctions{}, fmt.Errorf("luahook: derive has no mcp_auth registration for %q", agent)
	}
	return s, functions, nil
}

func mcpServerMap(server MCPServerRef) map[string]any {
	headers := make(map[string]any, len(server.Headers))
	for key, value := range server.Headers {
		headers[key] = value
	}
	return map[string]any{
		"name":      server.Name,
		"url":       server.URL,
		"transport": server.Transport,
		"headers":   headers,
		"client_id": server.ClientID,
	}
}

func luaStringField(table *lua.LTable, key string) (string, bool) {
	value, ok := table.RawGetString(key).(lua.LString)
	return string(value), ok
}

func luaJSONValue(value lua.LValue, visiting map[*lua.LTable]bool) (any, error) {
	switch value := value.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(value), nil
	case lua.LString:
		return string(value), nil
	case lua.LNumber:
		return float64(value), nil
	case *lua.LTable:
		if visiting[value] {
			return nil, fmt.Errorf("cyclic table")
		}
		visiting[value] = true
		defer delete(visiting, value)
		stringKeys := make(map[string]lua.LValue)
		integerKeys := make(map[int]lua.LValue)
		var invalidKey bool
		value.ForEach(func(key, item lua.LValue) {
			switch key := key.(type) {
			case lua.LString:
				stringKeys[string(key)] = item
			case lua.LNumber:
				n := float64(key)
				i := int(n)
				if float64(i) != n {
					invalidKey = true
					return
				}
				integerKeys[i] = item
			default:
				invalidKey = true
			}
		})
		if invalidKey {
			return nil, fmt.Errorf("table has a non-string, non-integer key")
		}
		if len(stringKeys) == 0 && len(integerKeys) > 0 && contiguousFrom1(integerKeys) {
			array := make([]any, len(integerKeys))
			for i := 1; i <= len(integerKeys); i++ {
				item, err := luaJSONValue(integerKeys[i], visiting)
				if err != nil {
					return nil, err
				}
				array[i-1] = item
			}
			return array, nil
		}
		object := make(map[string]any, len(stringKeys)+len(integerKeys))
		for key, item := range stringKeys {
			converted, err := luaJSONValue(item, visiting)
			if err != nil {
				return nil, err
			}
			object[key] = converted
		}
		for key, item := range integerKeys {
			name := strconv.Itoa(key)
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("table has colliding object key %q", name)
			}
			converted, err := luaJSONValue(item, visiting)
			if err != nil {
				return nil, err
			}
			object[name] = converted
		}
		return object, nil
	default:
		return nil, fmt.Errorf("cannot encode Lua %s as JSON", value.Type())
	}
}

func canonicalJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	encoded := strings.TrimSuffix(buffer.String(), "\n")
	// Go escapes these two valid JSON characters even when HTML escaping is disabled;
	// serde_json writes them as UTF-8. Replace only actual JSON escape tokens: a literal
	// backslash followed by "u2028" or "u2029" is data and must remain escaped.
	return unescapeJSONSeparators(encoded), nil
}

func unescapeJSONSeparators(encoded string) string {
	var output strings.Builder
	output.Grow(len(encoded))
	inString := false
	escaped := false
	for i := 0; i < len(encoded); {
		if inString && !escaped && encoded[i] == '\\' && i+6 <= len(encoded) {
			token := encoded[i : i+6]
			if token == `\u2028` {
				output.WriteRune('\u2028')
				i += len(token)
				continue
			}
			if token == `\u2029` {
				output.WriteRune('\u2029')
				i += len(token)
				continue
			}
		}
		c := encoded[i]
		output.WriteByte(c)
		i++
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		} else if c == '"' {
			inString = true
		}
	}
	return output.String()
}
