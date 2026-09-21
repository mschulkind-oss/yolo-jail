// basehomedisclosure_test.go pins the check-side half of the base-home disclosure.
//
// The call site here CANNOT be reached behaviourally: baseOptions sets
// SkipEnsureStorage: true and no test in this package sets it false, so the whole
// ensure-storage block — the v2 layout migration and now the base-home walk — is never
// executed by the suite. That is deliberate (a golden run must not touch the developer's
// real home), which leaves an AST pin as the only witness that the call exists at all,
// the shape sectionwiring_test.go already uses for the same reason.
package check

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckCallsTheBaseHomeDisclosure pins the call site.
//
// ⚠ IF THIS FAILS BECAUSE THE CALL MOVED, MOVE THIS PIN WITH IT — do not delete it.
// Deleting o.noteLegacyBaseHome() from Check stops the disclosure on the one host command
// a user runs when they suspect something is wrong with their storage, and the symptom is
// indistinguishable from a clean base home.
func TestCheckCallsTheBaseHomeDisclosure(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "check.go", nil, 0)
	if err != nil {
		t.Fatalf("parse check.go: %v", err)
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Check" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteLegacyBaseHome" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("Check no longer calls o.noteLegacyBaseHome(). The design (§5.7) names BOTH host " +
			"call sites — run.go and check.go — and this one is the detection-only site by " +
			"construction, since check's Options has no stdin seam to prompt on.")
	}
}

// TestCheckBaseHomeDisclosureWritesToStderr drives the method itself. Stderr is a
// separate stream from the report on purpose: a disclosure is not part of an artifact a
// caller may reshape with --format json.
func TestCheckBaseHomeDisclosureWritesToStderr(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".local", "share", "yolo-jail", "home",
		".claude", "projects", "-home-someone-code-thing")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "t.jsonl"), []byte(strings.Repeat("x", 2048)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out, errBuf bytes.Buffer
	o := baseOptions(t, &out)
	o.Stderr = &errBuf
	// baseOptions stubs Getenv to "" for everything, so inJail() is false — the host arm.
	o.noteLegacyBaseHome()

	if !strings.Contains(errBuf.String(), ".claude") {
		t.Fatalf("stderr = %q, want the base-home summary", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("the report stream received %q; a disclosure is not part of the report", out.String())
	}
	if strings.Contains(errBuf.String(), "-home-someone-code-thing") {
		t.Errorf("stderr = %q leaks a workspace name", errBuf.String())
	}
}
