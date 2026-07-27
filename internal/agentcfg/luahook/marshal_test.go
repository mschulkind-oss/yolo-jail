package luahook

import "testing"

// F3: a hook that iterates a table with pairs() must see a DETERMINISTIC order.
//
// goToLua built the Lua table by ranging a Go map, and Go randomizes map iteration —
// so a hook that reordered or rebuilt a table produced different output run to run.
// Probed before the fix: 8 distinct renders over 30 runs of the same input.
//
// Fixed in goToLua (sorted keys) rather than by adding a sorted_pairs() API, because
// this fixes every hook including already-written ones, instead of adding a rule an
// author has to know.
func TestGoToLuaIterationOrderIsDeterministic(t *testing.T) {
	script := `
yolo.transform("pi", function(ctx)
  local order = {}
  for k, _ in pairs(ctx.config.tbl) do order[#order + 1] = k end
  ctx.config.seen = table.concat(order, ",")
end)`
	in := map[string]any{"tbl": map[string]any{
		"delta": 1, "alpha": 2, "charlie": 3, "bravo": 4, "echo": 5,
		"foxtrot": 6, "golf": 7, "hotel": 8,
	}}

	first := ""
	for i := 0; i < 30; i++ {
		ctx := NewCtx("pi", "settings", in, nil)
		got, err := Apply(Transform{VM: &GopherLuaVM{}, Script: script}, ctx)
		if err != nil {
			t.Fatal(err)
		}
		seen, _ := got.(map[string]any)["seen"].(string)
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
