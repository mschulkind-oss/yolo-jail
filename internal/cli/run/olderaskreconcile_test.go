package run

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func olderAskFrozen(t *testing.T, user, workspace string) (map[string]config.CallBrokerCellPolicy, []loopholedecl.CallBrokerCell, json.RawMessage) {
	t.Helper()
	cells := []loopholedecl.CallBrokerCell{{Name: "update", Tables: []string{"working_set", "all"}}}
	u, err := jsonx.Decode([]byte(user))
	if err != nil {
		t.Fatal(err)
	}
	w, err := jsonx.Decode([]byte(workspace))
	if err != nil {
		t.Fatal(err)
	}
	um, wm := u.(*jsonx.OrderedMap), w.(*jsonx.OrderedMap)
	frozen, err := config.EffectiveCallBrokerCells("tasks", &loopholedecl.CallBroker{Source: "tasks", Cells: cells}, um, wm)
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if requests, ok := wm.Get("requests"); ok {
		if source, ok := requests.(*jsonx.OrderedMap).Get("tasks"); ok {
			text, err := jsonx.DumpsCompact(source)
			if err != nil {
				t.Fatal(err)
			}
			raw = json.RawMessage(text)
		}
	}
	return frozen, cells, raw
}

// A cell the config forced to refuse (a `never`, or a narrower false) reconciles against the
// rebuilt workspace and ceiling, so `yolo config apply` on a running pre-snapshot jail is not
// blocked by a config that merely contains `never`. Deleting the never reconstruction in
// reconcileFrozenCells makes the first two cases fail; the last case proves a real difference
// still fails closed.
func TestFrozenRefusedCellsReconcile(t *testing.T) {
	const always = `"always":["working_set.update","all.update"]`
	for name, tc := range map[string]struct{ user, workspace string }{
		"never over always":     {`{"allow":{"tasks":{` + always + `,"never":["all.update"]}}}`, `{"requests":{"tasks":{"working_set":{"update":true},"all":{"update":true}}}}`},
		"never with no request": {`{"allow":{"tasks":{"never":["working_set.update"]}}}`, `{}`},
		"no never":              {`{"allow":{"tasks":{` + always + `}}}`, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			frozen, cells, raw := olderAskFrozen(t, tc.user, tc.workspace)
			if err := reconcileFrozenCells("tasks", cells, frozen, raw); err != nil {
				t.Fatalf("equivalent policy did not reconcile: %v", err)
			}
		})
	}
	t.Run("a real difference fails closed", func(t *testing.T) {
		frozen, cells, raw := olderAskFrozen(t, `{"allow":{"tasks":{"never":["all.update"]}}}`, `{}`)
		table := frozen["update"].Tables["all"]
		table.Decision = "run"
		frozen["update"].Tables["all"] = table
		if err := reconcileFrozenCells("tasks", cells, frozen, raw); err == nil {
			t.Fatal("a frozen run beside a refused rebuild reconciled")
		}
	})
}

// Call-site pin: the older-asks check reconciles through reconcileFrozenCells. It has no
// process-level fixture short of a delivered launch, so this names the call in the function body.
func TestCheckOlderAsksLockedReconcilesThroughTheFrozenCellRebuild(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "olderaskapply.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "checkOlderAsksLocked" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "reconcileFrozenCells" {
					called = true
				}
			}
			return true
		})
	}
	if !called {
		t.Fatal("checkOlderAsksLocked does not call reconcileFrozenCells")
	}
}
