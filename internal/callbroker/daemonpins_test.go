package callbroker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// mainCalls returns the selector names Main calls on any receiver.
func mainCalls(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "daemon.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" || fn.Recv != nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					calls[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	if len(calls) == 0 {
		t.Fatal("Main not found in daemon.go")
	}
	return calls
}

// Main has no process-level test: a launch/3 input cannot pass its host-scope check until a
// producer exists (review minor 10). Until then the daemon's two snapshot-launch steps are
// pinned here at the call site: deleting either call from Main fails this test.
func TestMainCallsTheLifetimeStartAndConsumerProofSteps(t *testing.T) {
	calls := mainCalls(t)
	for _, name := range []string{"lifetimeStart", "publishSnapshotConsumer"} {
		if !calls[name] {
			t.Fatalf("Main no longer calls %s", name)
		}
	}
}

func TestLifetimeStartIsTheDeliveredBaselineStart(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	p := prepareLegacySnapshotReaderInput(t, f)
	sf, err := brokerscope.ReadFile(f.scopePath)
	if err != nil {
		t.Fatal(err)
	}
	sf.BrokerStart = ""
	want := strings.Repeat("c", 32)
	err = brokerlock.WithTransaction(f.store.Dir, func() error {
		_, err := brokerscope.PublishDeliveredLocked(sf, brokerscope.DeliveredBaseline{Binding: p.Binding, Start: want,
			Asks: brokerscope.DeliveredAsks{Requests: []byte(`{}`), Scopes: []byte(`{}`), PackRead: []byte(`null`), Model: "scoped/1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := newCLIHandler(f.inputPath, f.h.workspace, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.lifetimeStart(sf)
	if err != nil || got != want {
		t.Fatalf("start=%q err=%v, want the delivered baseline start %q", got, err, want)
	}
}

func TestPublishSnapshotConsumerWritesTheDaemonProof(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	p := prepareLegacySnapshotReaderInput(t, f)
	sf, err := brokerscope.ReadFile(f.scopePath)
	if err != nil {
		t.Fatal(err)
	}
	sf.BrokerStart, sf.BrokerPID = strings.Repeat("d", 32), os.Getpid()
	if _, err := brokerscope.Write(sf); err != nil {
		t.Fatal(err)
	}
	h, err := newCLIHandler(f.inputPath, f.h.workspace, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.publishSnapshotConsumer(sf, sf.BrokerStart); err != nil {
		t.Fatal(err)
	}
	err = brokerlock.WithTransaction(f.store.Dir, func() error {
		_, err := brokerscope.ReadSnapshotConsumerLocked(sf, p.Binding.DeclarationHash, p.Binding.InputDigest)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
