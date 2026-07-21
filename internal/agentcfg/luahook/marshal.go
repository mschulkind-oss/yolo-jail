package luahook

import (
	"fmt"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// Go <-> Lua marshalling for the config value model
// (docs/plans/agent-settings-composition.md §3.2 "plain Lua over a plain
// table"). The engine's decoded-config value model is exactly:
//
//	map[string]any  ·  []any  ·  string  ·  float64  ·  bool  ·  nil
//
// which is what encoding/json decodes into, and what the TOML codec is
// normalized to. These functions round-trip that model through gopher-lua's
// value types (LTable / LString / LNumber / LBool / LNil).
//
// # Round-trip fidelity notes (the Lua impedance mismatches, tested)
//
//   - **Numbers collapse to float64.** Lua 5.1 has a single number type
//     (LNumber == float64); there is no distinct integer. So a Go int / int64 /
//     float64 all marshal to the same LNumber, and everything numeric marshals
//     BACK as float64. This is lossless for the JSON value model (JSON numbers
//     are float64 already) but a Go int64 does NOT survive a round trip as int64
//     — it returns as float64. Callers whose model is int64 (some managed-layer
//     fixtures) must account for this; the config surfaces this engine drives
//     decode to float64, so they are unaffected.
//   - **Arrays are 1-indexed tables.** A Go []any becomes a Lua table with keys
//     1..n; on the way back a table whose keys are exactly the contiguous
//     integers 1..n (and nothing else) decodes to []any, any other table
//     decodes to map[string]any (integer keys stringified). An empty table is
//     ambiguous (Lua cannot distinguish {} the array from {} the object) and
//     decodes to an empty map[string]any — the object shape, since every config
//     surface here is object-rooted.
//   - **nil cannot live inside an array.** Lua tables cannot hold nil as a
//     value (assigning nil deletes the key), so a []any containing a nil element
//     cannot be represented as a dense Lua array and will not round-trip
//     faithfully. The config value model does not put nulls inside arrays (a
//     null deletes a *key*, §4), so this is a documented non-case rather than a
//     bug.

// goToLua converts a Go value from the config value model into a Lua value in
// L. Unsupported Go types are a loud error (fail-closed, §3.4) rather than a
// silent tostring.
func goToLua(L *lua.LState, v any) (lua.LValue, error) {
	switch val := v.(type) {
	case nil:
		return lua.LNil, nil
	case bool:
		return lua.LBool(val), nil
	case string:
		return lua.LString(val), nil
	case float64:
		return lua.LNumber(val), nil
	case float32:
		return lua.LNumber(val), nil
	case int:
		return lua.LNumber(float64(val)), nil
	case int32:
		return lua.LNumber(float64(val)), nil
	case int64:
		return lua.LNumber(float64(val)), nil
	case []any:
		t := L.NewTable()
		for i, e := range val {
			ev, err := goToLua(L, e)
			if err != nil {
				return nil, err
			}
			// 1-indexed, matching Lua array convention.
			t.RawSetInt(i+1, ev)
		}
		return t, nil
	case map[string]any:
		t := L.NewTable()
		for k, e := range val {
			ev, err := goToLua(L, e)
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, ev)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("luahook: cannot marshal Go value of type %T into Lua", v)
	}
}

// luaToGo converts a Lua value back into the Go config value model. Functions,
// userdata, threads, and other non-config values are a loud error.
func luaToGo(lv lua.LValue) (any, error) {
	switch v := lv.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(v), nil
	case lua.LNumber:
		return float64(v), nil
	case lua.LString:
		return string(v), nil
	case *lua.LTable:
		return luaTableToGo(v)
	default:
		return nil, fmt.Errorf("luahook: cannot marshal Lua value of type %s back to Go", lv.Type())
	}
}

// luaTableToGo decodes a Lua table into either a []any (pure 1..n array) or a
// map[string]any (everything else), per the fidelity rules above.
func luaTableToGo(tbl *lua.LTable) (any, error) {
	strKeys := map[string]lua.LValue{}
	intKeys := map[int]lua.LValue{}
	otherKey := false
	var iterErr error
	tbl.ForEach(func(k, val lua.LValue) {
		switch key := k.(type) {
		case lua.LString:
			strKeys[string(key)] = val
		case lua.LNumber:
			f := float64(key)
			if i := int(f); float64(i) == f {
				intKeys[i] = val
			} else {
				otherKey = true
			}
		default:
			otherKey = true
		}
	})
	if iterErr != nil {
		return nil, iterErr
	}

	// Pure array: only contiguous positive integer keys 1..n, no string/other
	// keys. Anything else is treated as a map (§ fidelity note).
	if len(strKeys) == 0 && !otherKey && len(intKeys) > 0 && contiguousFrom1(intKeys) {
		out := make([]any, len(intKeys))
		for i := 1; i <= len(intKeys); i++ {
			gv, err := luaToGo(intKeys[i])
			if err != nil {
				return nil, err
			}
			out[i-1] = gv
		}
		return out, nil
	}

	out := make(map[string]any, len(strKeys)+len(intKeys))
	for k, v := range strKeys {
		gv, err := luaToGo(v)
		if err != nil {
			return nil, err
		}
		out[k] = gv
	}
	for k, v := range intKeys {
		gv, err := luaToGo(v)
		if err != nil {
			return nil, err
		}
		out[strconv.Itoa(k)] = gv
	}
	return out, nil
}

// contiguousFrom1 reports whether the integer keys are exactly 1..len(keys).
func contiguousFrom1(intKeys map[int]lua.LValue) bool {
	for i := 1; i <= len(intKeys); i++ {
		if _, ok := intKeys[i]; !ok {
			return false
		}
	}
	return true
}
