package cli

// macosusergrantswiring_test.go pins the production wiring of the grant skip
// (docs/design/jail-lifetime-last-session-wins.md §9.9.5, JL-D88): the macos-user front door hands its
// backend the arm's SkipGrant and Staged, which carry Run's macosUserGrants, so a joining session's
// stage skips the keeper's endpoint files another session granted and records what its own stage
// granted. Run's half is driven in internal/cli/run (TestAJoiningMacosUserSessionSkipsTheGrantsTheFirstMade),
// whose stub calls the arm directly; this is the call site that test cannot see.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Deleting `SkipGrant: arm.SkipGrant` or `OnStaged: arm.Staged` from macosUserRun's
// macosuser.Options fails this.
func TestMacosUserRunHandsTheBackendTheKeepersGrantAnswers(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "macosUserRun" {
			run = fd
		}
	}
	if run == nil {
		t.Fatal("commands.go has no macosUserRun")
	}
	isSelector := func(e ast.Expr, x, sel string) bool {
		s, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := s.X.(*ast.Ident)
		return ok && id.Name == x && s.Sel.Name == sel
	}
	want := map[string]string{"SkipGrant": "SkipGrant", "OnStaged": "Staged"}
	found := map[string]bool{}
	literals := 0
	ast.Inspect(run, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSelector(lit.Type, "macosuser", "Options") {
			return true
		}
		literals++
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			if method, wanted := want[key.Name]; wanted && isSelector(kv.Value, "arm", method) {
				found[key.Name] = true
			}
		}
		return true
	})
	if literals == 0 {
		t.Fatal("macosUserRun builds no macosuser.Options")
	}
	for field, method := range want {
		if !found[field] {
			t.Errorf("macosUserRun's macosuser.Options has no %s: arm.%s, so the backend's stage never "+
				"hears which keeper endpoint files a session granted", field, method)
		}
	}
}
