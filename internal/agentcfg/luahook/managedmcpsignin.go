package luahook

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

func installMCPSignIn(L *lua.LState, yolo *lua.LTable, session *deriveSession) {
	L.SetField(yolo, "mcp_sign_in", L.NewFunction(func(L *lua.LState) int {
		agent := L.CheckString(1)
		declaration := L.CheckTable(2)
		native, ok := L.GetField(declaration, "native_name").(*lua.LFunction)
		if !ok {
			L.ArgError(2, "mcp_sign_in.native_name must be a function")
			return 0
		}
		if _, exists := session.mcpNativeNames[agent]; exists {
			L.ArgError(1, "duplicate mcp_sign_in registration")
			return 0
		}
		session.mcpNativeNames[agent] = native
		return 0
	}))
}

// MCPNativeName runs the agent pack's pure delivered-name/server-key function.
// Core does not infer names from directories or know any agent's naming syntax.
func MCPNativeName(script, agent, deliveredName, serverKey string) (string, error) {
	s, err := newDeriveSession(GopherLuaVM{}, script, &DeriveCtx{})
	if err != nil {
		return "", err
	}
	defer s.close()
	fn, exists := s.mcpNativeNames[agent]
	if !exists {
		return "", fmt.Errorf("luahook: no mcp_sign_in registration for %q", agent)
	}
	if err := s.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, lua.LString(deliveredName), lua.LString(serverKey)); err != nil {
		return "", wrapLuaErr(err)
	}
	value := s.L.Get(-1)
	s.L.Pop(1)
	name, ok := value.(lua.LString)
	if !ok || name == "" {
		return "", fmt.Errorf("luahook: mcp_sign_in native_name must return a nonempty string")
	}
	return string(name), nil
}
