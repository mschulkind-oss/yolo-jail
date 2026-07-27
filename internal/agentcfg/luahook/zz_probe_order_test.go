package luahook

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestProbePairsFollowsInsertion(t *testing.T) {
	for run := 0; run < 5; run++ {
		L := lua.NewState()
		tbl := L.NewTable()
		for _, k := range []string{"h", "g", "f", "e", "d", "c", "b", "a"} {
			tbl.RawSetString(k, lua.LString(k))
		}
		L.SetGlobal("t", tbl)
		if err := L.DoString("o = \"\"\nfor k, v in pairs(t) do o = o .. k end\n"); err != nil {
			t.Fatal(err)
		}
		t.Logf("inserted h..a -> pairs order: %s", L.GetGlobal("o").String())
		L.Close()
	}
}

func TestProbeGoToLuaOrderRandom(t *testing.T) {
	m := map[string]any{"a": 1.0, "b": 2.0, "c": 3.0, "d": 4.0, "e": 5.0, "f": 6.0, "g": 7.0, "h": 8.0}
	seen := map[string]int{}
	for run := 0; run < 20; run++ {
		L := lua.NewState()
		lv, err := goToLua(L, m)
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
	t.Logf("distinct pairs orders after goToLua over 20 runs: %d -> %v", len(seen), seen)
}
