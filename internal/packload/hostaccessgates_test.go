package packload_test

// hostaccessgates_test.go is a SOURCE-LEVEL assertion, and being source-level is the
// point rather than a shortcut.
//
// What it pins: the two host-access GATES — `internal/cli/pack.go`'s resolveHostApproval
// (which prompts and records the lockfile) and `internal/cli/run/packs.go`'s
// packMayAccessHost (which checks the lockfile at launch) — must build their claim set
// through the ONE merged helper (packload.Pack.HostAccessClaims), never by naming a
// producer directly.
//
// # Why a behavioural test cannot express this
//
// The invariant is not "the two gates agree on this pack". It is "the two gates agree on
// EVERY pack, including the ones a future producer will describe" — and a behavioural test
// can only compare the two on inputs it constructs. The failure mode being prevented is
// exactly a producer nobody thought to construct an input for:
//
//	want := append(p.Decl.HostAccessClaims(), p.PluginHostAccessClaims()...)
//
// was inlined at both sites, and the SECOND producer (plugins) had to be added to both by
// hand. A third — the `loophole` kind, whose claims are host code EXECUTION — makes the
// cost of missing one stop being a config read:
//
//   - added to the PROMPT only → the user approves a claim the launch gate never asks
//     about, so an unapproved crossing is honored;
//   - added to the LAUNCH gate only → the launch demands approval for a claim
//     `pack install` never showed, so the pack is refused with no route to approving it.
//
// A test over the SOURCE catches the omission at the moment it is written, for a producer
// that does not exist yet. The design (loophole-packaging.md §3.3) asks for exactly this
// and says a source-level assertion is acceptable and is the point.
//
// It lives in packload because that is where the helper lives, so the test and the thing
// it protects move together.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostAccessProducers are the methods that each produce PART of a pack's host-access claim
// set. Every one is legitimate INSIDE the merged helper and forbidden at a gate.
var hostAccessProducers = []string{
	"HostAccessClaims",         // pack.json's own contributions (packdecl.Manifest)
	"PluginHostAccessClaims",   // a wrapped agent plugin's code-running components
	"LoopholeHostAccessClaims", // a shipped loophole's daemon, intercepts, binds, devices
}

// The gates, and the function in each that must call the merged helper. Named individually
// rather than scanning every file, because "a gate" is a specific role: these two are the
// ones whose disagreement means an unapproved crossing is honored or an approvable pack is
// unapprovable.
var hostAccessGates = []struct {
	file string // repo-relative
	fn   string // the gate function
}{
	{filepath.Join("internal", "cli", "pack.go"), "resolveHostApproval"},
	{filepath.Join("internal", "cli", "run", "packs.go"), "packMayAccessHost"},
}

// TestHostAccessGatesUseTheMergedHelper fails if either gate calls a claim producer
// directly, or stops calling the merged helper at all.
func TestHostAccessGatesUseTheMergedHelper(t *testing.T) {
	root := repoRootFor(t)
	for _, gate := range hostAccessGates {
		path := filepath.Join(root, gate.file)
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", gate.file, err)
		}
		body := findFuncBody(parsed, gate.fn)
		if body == nil {
			t.Fatalf("%s: function %q not found — it is one of the two host-access gates; if "+
				"it was renamed or moved, update hostAccessGates so the invariant keeps being "+
				"checked (do not just delete the row)", gate.file, gate.fn)
		}

		called := calledSelectors(body)
		if !called["HostAccessClaims"] {
			t.Errorf("%s: %s does not call the merged helper — it must build its claim set "+
				"with p.HostAccessClaims() (packload.Pack), so the install gate and the launch "+
				"gate compare the SAME union. Hand-merging producers is how the two drifted "+
				"before", gate.file, gate.fn)
		}
		// The merged helper is spelled `HostAccessClaims` too (on *Pack, shadowing nothing —
		// packdecl's is on *Manifest), so the forbidden call is the QUALIFIED one: a producer
		// reached through `.Decl`, or either of the two whose names are unambiguous.
		for _, producer := range hostAccessProducers {
			if producer == "HostAccessClaims" {
				continue // the merged helper's own name; the qualified form is checked below
			}
			if called[producer] {
				t.Errorf("%s: %s calls %s() directly. Every producer must be merged by "+
					"packload.Pack.HostAccessClaims and read from there, or this gate and the "+
					"other one can disagree about what a pack asks of the host — which means "+
					"either an unapproved crossing is honored, or an approved pack is refused "+
					"with no way to approve it", gate.file, gate.fn, producer)
			}
		}
		if usesDeclClaims(body) {
			t.Errorf("%s: %s reads p.Decl.HostAccessClaims() — that is pack.json's claims "+
				"ALONE, which silently omits every producer whose declaration lives outside "+
				"pack.json (a wrapped plugin's hooks, a shipped loophole's host daemon). Call "+
				"p.HostAccessClaims() instead", gate.file, gate.fn)
		}
	}
}

// findFuncBody returns the body of the named top-level function, or nil.
func findFuncBody(file *ast.File, name string) *ast.BlockStmt {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn.Body
		}
	}
	return nil
}

// calledSelectors returns the set of method names called as `x.Name(...)` anywhere in body.
func calledSelectors(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			out[sel.Sel.Name] = true
		}
		return true
	})
	return out
}

// usesDeclClaims reports whether body calls `<something>.Decl.HostAccessClaims(...)` — the
// pack.json-only producer, which is the one whose name collides with the merged helper's
// and therefore needs the receiver checked rather than the method name.
func usesDeclClaims(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HostAccessClaims" {
			return true
		}
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Decl" {
			found = true
		}
		return true
	})
	return found
}

// repoRootFor walks up to the dir holding go.mod. (A copy of the package-internal
// findRepoRoot: this file is in packload_test, the external test package.)
func repoRootFor(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// The producer list must not go stale silently: every name in it has to be a real method
// on *Pack or *packdecl.Manifest. A renamed producer would otherwise make the check above
// pass vacuously, which is the exact shape of the bug it exists to catch.
func TestHostAccessProducersExist(t *testing.T) {
	root := repoRootFor(t)
	sources := []string{
		filepath.Join(root, "internal", "packdecl", "contributes.go"),
		filepath.Join(root, "internal", "packload", "plugins.go"),
		filepath.Join(root, "internal", "packload", "loopholesource.go"),
		filepath.Join(root, "internal", "packload", "hostaccess.go"),
	}
	var all strings.Builder
	for _, src := range sources {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(data)
	}
	text := all.String()
	for _, producer := range hostAccessProducers {
		if !strings.Contains(text, ") "+producer+"()") {
			t.Errorf("producer %q is not declared in any of the claim-producing files — it was "+
				"renamed or removed, which makes TestHostAccessGatesUseTheMergedHelper pass "+
				"vacuously for it", producer)
		}
	}
}
