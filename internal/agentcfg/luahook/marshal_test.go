package luahook

import "testing"

// F3: a script that iterates a table with pairs() must see a DETERMINISTIC order.
//
// goToLua built the Lua table by ranging a Go map, and Go randomizes map iteration —
// so a script that reordered or rebuilt a table produced different output run to run.
// Probed before the fix: 8 distinct renders over 30 runs of the same input.
//
// Fixed in goToLua (sorted keys) rather than by adding a sorted_pairs() API, because
// this fixes every script including already-written ones, instead of adding a rule an
// author has to know.
//
// RE-HOMED onto Derive (docs/design/lua-transform-removal.md §5.5): this file asserted
// the property through Apply — the config transform, which that design removes — and so
// would not have COMPILED after the cut, despite the design's test table listing it
// untouched. The property under test is goToLua's, which stays; only the caller moved.
// The derive path is the one that still marshals a live Go map in (buildDeriveCtxTable
// hands ctx.mcp_servers through goToLua), and the six shipped packs' derive.lua all
// iterate those tables with pairs(), so this is now where the proof belongs.
func TestGoToLuaIterationOrderIsDeterministic(t *testing.T) {
	script := `
yolo.derive("pi", "settings", function(ctx)
  local order = {}
  for k, _ in pairs(ctx.mcp_servers) do order[#order + 1] = k end
  return { seen = table.concat(order, ",") }
end)`
	tables := map[string]map[string]any{"mcp_servers": {
		"delta": 1, "alpha": 2, "charlie": 3, "bravo": 4, "echo": 5,
		"foxtrot": 6, "golf": 7, "hotel": 8,
	}}

	first := ""
	for i := 0; i < 30; i++ {
		got, err := runDerive(t, "pi", "settings", script, tables)
		if err != nil {
			t.Fatal(err)
		}
		seen, _ := got["seen"].(string)
		if i == 0 {
			first = seen
			continue
		}
		if seen != first {
			t.Fatalf("pairs() order is nondeterministic across runs:\n run 1: %s\n run %d: %s",
				first, i+1, seen)
		}
	}
	// And it is SORTED, which is the only order that is both stable and predictable.
	if first != "alpha,bravo,charlie,delta,echo,foxtrot,golf,hotel" {
		t.Errorf("iteration order = %q, want sorted", first)
	}
}
