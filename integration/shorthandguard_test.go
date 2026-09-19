package integration

// shorthandguard_test.go tests the fixture guard, because nothing else in this package can.
//
// Every test here is `requireJail`-gated and skips under `-short`, which is the only mode a
// contributor's `just test-fast` and the pre-commit hook run. So a guard added to the
// harness would itself be unexercised by the gate that is supposed to catch things — the
// same blind spot that let two fixtures carry a removed config key into CI. These run
// unconditionally: they touch no jail.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestProviderShorthandGuard(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want bool
	}{
		{"the removed shorthand", `{"providers": {"llamacpp": {"base_url": "http://x/v1"}}}`, true},
		{"the live per-protocol spelling", `{"providers": {"llamacpp": {"endpoints": {"openai": {"base_url": "http://x/v1"}}}}}`, false},
		{"no providers at all", `{"packs": ["claude"]}`, false},
		{"an entry with no address", `{"providers": {"llamacpp": {"models": {"default": "llama"}}}}`, false},
		// A null entry DELETES the provider, so it declares no address and is not a defect.
		{"a deleted provider", `{"providers": {"llamacpp": null}}`, false},
		// Unparseable is the launch's error to report, with its own message — not this
		// guard's to pre-empt with a different one.
		{"not valid JSON", `{"providers": {`, false},
		// THE CASE THAT KILLED THE FIRST IMPLEMENTATION. A correct entry, then a bad one.
		// The original string heuristic found only the FIRST `base_url`, saw it nested
		// under endpoints, and called the whole config clean — never looking at the second
		// entry. That is why this predicate parses instead of scanning.
		{"good entry then a bad one", `{"providers": {"a": {"endpoints": {"openai": {"base_url": "http://a/v1"}}}, "b": {"base_url": "http://b/v1"}}}`, true},
		// And its mirror: a bad one first must not be excused by a later good one.
		{"bad entry then a good one", `{"providers": {"a": {"base_url": "http://a/v1"}, "b": {"endpoints": {"openai": {"base_url": "http://b/v1"}}}}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasProviderShorthand(tc.cfg); got != tc.want {
				t.Errorf("hasProviderShorthand() = %v, want %v\n%s", got, tc.want, tc.cfg)
			}
		})
	}
}

// THE CALL-SITE PIN. The predicate being right is worth nothing if nothing calls it, and
// `isolateHome` is jail-gated so no runnable test reaches it. An AST check is what is left
// — the same shape internal/cli/run uses for the call sites inside runContainer.
func TestIsolateHomeCallsTheShorthandGuard(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "packs_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "isolateHome" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && strings.Contains(id.Name, "ProviderShorthand") {
				found = true
			}
			return true
		})
		return false
	})
	if !found {
		t.Error("isolateHome does not call refuseProviderShorthand. Every user-config " +
			"fixture in this package goes through it, and without the call a fixture " +
			"carrying the removed `base_url` shorthand fails as an opaque `rc 1` minutes " +
			"into CI's container suite — invisible to `just test-fast`, which only " +
			"compiles this package under -short.")
	}
}
