package integration

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// Inspect the actual callers, not the fixture helper's advertised platform support.
func TestOpencodeActiveSetKeepsItsOriginalNonfixtureAssertions(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "opencode_active_set_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestOpencodeRunsOnEveryProviderOfItsSet", "TestOpencodeRunsOnABedrockEntryAfterItsFirst"} {
		t.Run(name, func(t *testing.T) {
			var fn *ast.FuncDecl
			for _, decl := range file.Decls {
				if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name.Name == name {
					fn = candidate
				}
			}
			if fn == nil {
				t.Fatal("original cross-platform caller deleted")
			}
			var text bytes.Buffer
			if err := printer.Fprint(&text, set, fn.Body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text.String(), `privateProducer := runtime.GOOS == "linux" && privateSuite.armed`) {
				t.Fatal("physical producer must be an explicitly armed Linux-only addition")
			}
			var walk func(ast.Node, bool)
			unconditional := map[string]int{}
			walk = func(node ast.Node, guarded bool) {
				if node == nil {
					return
				}
				ast.Inspect(node, func(current ast.Node) bool {
					if branch, ok := current.(*ast.IfStmt); ok {
						var condition bytes.Buffer
						if err := printer.Fprint(&condition, set, branch.Cond); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(condition.String(), "privateProducer") || strings.Contains(condition.String(), "runtime.GOOS") {
							walk(branch.Body, true)
							walk(branch.Else, guarded)
							return false
						}
					}
					if call, ok := current.(*ast.CallExpr); ok {
						callee := callName(call.Fun)
						if (callee == "privateOfficialMachineHome" || callee == "withPrivateOfficialProducer") && !guarded {
							t.Errorf("%s can skip the original non-Linux caller", callee)
						}
						if !guarded {
							unconditional[callee]++
						}
					}
					return true
				})
			}
			walk(fn.Body, false)
			for _, callee := range []string{"runCommand", "readPioencodeSurface", "requireNotCataloged", "t.Errorf"} {
				if unconditional[callee] == 0 {
					t.Errorf("original provider/render assertion edge %s deleted or hidden behind fixture mode", callee)
				}
			}
			if name == "TestOpencodeRunsOnABedrockEntryAfterItsFirst" {
				if unconditional["requireCataloged"] == 0 || !strings.Contains(text.String(), `opts["region"] != region`) || !strings.Contains(text.String(), `models[opus] == nil`) {
					t.Error("original Bedrock native row/region/model assertions deleted")
				}
			} else if !strings.Contains(text.String(), `cfg.slashJoin != "zai-coding-plan/glm-5.3"`) || !strings.Contains(text.String(), `[]any{"zai-coding-plan", "openrouter"}`) {
				t.Error("original model/provider-order assertions deleted")
			}
		})
	}
}
