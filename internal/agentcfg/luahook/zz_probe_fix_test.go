package luahook

import (
	"sort"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// goToLuaSorted is the 3-line candidate fix: iterate map keys in sorted order.
func goToLuaSorted(L *lua.LState, v any) (lua.LValue, error) {
	switch val := v.(type) {
	case map[string]any:
		t := L.NewTable()
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ev, err := goToLuaSorted(L, val[k])
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, ev)
		}
		return t, nil
	default:
		return goToLua(L, v)
	}
}

func TestProbeSortedFixDeterministic(t *testing.T) {
	m := map[string]any{"a": 1.0, "b": 2.0, "c": 3.0, "d": 4.0, "e": 5.0, "f": 6.0, "g": 7.0, "h": 8.0}
	seen := map[string]int{}
	for run := 0; run < 50; run++ {
		L := lua.NewState()
		lv, err := goToLuaSorted(L, m)
		if err != nil {
			t.Fatal(err)
		}
		L.SetGlobal("t", lv)
		if err := L.DoString("o = \"\"\nfor k, v in pairs(t) do o = o .. k end\n"); err != nil {
			t.Fatal(err)
		}
		seen[L.GetGlobal("o").String()]++
		L.Close()
	}
	t.Logf("with sorted goToLua, distinct orders over 50 runs: %d -> %v", len(seen), seen)
	if len(seen) != 1 {
		t.Fatalf("still nondeterministic")
	}
}
