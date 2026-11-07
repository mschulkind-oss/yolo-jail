package callbroker

import (
	"encoding/base64"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// sealFixtureFront seals h's prepared generation the way the front does at startup and returns
// a sender of ordinary frames that name it.
func sealFixtureFront(t *testing.T, h *HTTPHandler, generation string) func(op string, call HTTPCall) (int, string) {
	t.Helper()
	cert := frontFixtureCertificate(t, "127.0.0.1", true)
	if _, err := h.seal(h.input.FrontJailID, generation, httpFrontSealRequest{Address: "127.0.0.1:41000",
		CertificateBase64: base64.StdEncoding.EncodeToString(cert)}); err != nil {
		t.Fatalf("fixture seal: %v", err)
	}
	frame := selectedHTTPRawFrames(t, h.Handle)
	return func(op string, call HTTPCall) (int, string) {
		t.Helper()
		return frame(map[string]any{"op": op, "http": call, "front_generation": generation})
	}
}

// heldHandshake gates every pinned TLS handshake once armed: the first one held signals entered
// and waits for release.
type heldHandshake struct {
	armed     atomic.Bool
	entered   chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func newHeldHandshake() *heldHandshake {
	return &heldHandshake{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *heldHandshake) gate() {
	if !g.armed.Load() {
		return
	}
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
}

func (g *heldHandshake) open() { g.closeOnce.Do(func() { close(g.release) }) }

func (g *heldHandshake) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(time.Minute):
		t.Fatal("the call never reached its pinned TLS handshake")
	}
}

type frameResult struct {
	rc      int
	message string
}

func waitFrame(t *testing.T, finished <-chan frameResult) frameResult {
	t.Helper()
	select {
	case r := <-finished:
		return r
	case <-time.After(time.Minute):
		t.Fatal("the call did not return after the handshake was released")
		return frameResult{}
	}
}

func TestSealedFrontWithdrawalBeforeSendHasNoEffects(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	for _, write := range []bool{false, true} {
		name := "read"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			held := newHeldHandshake()
			defer held.open()
			_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() == "/items/a1" {
					_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
					return
				}
				requests.Add(1)
			}), held.gate)
			var base *HTTPHandler
			var f *onceFixture
			call := HTTPCall{Method: "GET", Path: "/project"}
			if write {
				base, f = selectedHTTPWriteFixture(t, origin, executor, []byte(original))
				call = HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
			} else {
				base, f = selectedHTTPReadFixture(t, origin, executor)
			}
			h, generation := preparedHTTPFixture(t, base, f)
			forward := sealFixtureFront(t, h, generation)
			uses := -1
			if write {
				if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
					t.Fatalf("ask: rc=%d message=%q", rc, message)
				}
				pending, _, err := f.store.List()
				if err != nil || len(pending) != 1 {
					t.Fatalf("pending: %+v err=%v", pending, err)
				}
				if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
					t.Fatal(err)
				}
				_, grants, err := f.store.List()
				if err != nil || len(grants) != 1 {
					t.Fatalf("grants: %+v err=%v", grants, err)
				}
				uses = grants[0].Uses
				// Hold only the final send's handshake, after the native lookups.
				h.beforeHTTPCommit = func() { held.armed.Store(true) }
			} else {
				held.armed.Store(true)
			}
			finished := make(chan frameResult, 1)
			go func() {
				rc, message := forward("act", call)
				finished <- frameResult{rc, message}
			}()
			held.waitEntered(t)
			h.withdrawFront()
			held.open()
			got := waitFrame(t, finished)
			if got.rc == 0 || !strings.Contains(got.message, "withdrawn before sending") || requests.Load() != 0 {
				t.Fatalf("withdrawn front sent: rc=%d message=%q requests=%d", got.rc, got.message, requests.Load())
			}
			if write {
				_, grants, err := f.store.List()
				if err != nil || len(grants) != 1 || grants[0].Uses != uses {
					t.Fatalf("withdrawal before send spent the Allow once: grants=%+v err=%v", grants, err)
				}
			}
		})
	}
}

func TestSealedFrontWithdrawalAfterPossibleSendDoesNotRefundOrRetry(t *testing.T) {
	const original = `{"operationName":"Rename","title":"ready"}`
	var writes atomic.Int32
	var front atomic.Pointer[HTTPHandler]
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		writes.Add(1)
		// The signal lands while the origin holds the only request: it was sent.
		if h := front.Load(); h != nil {
			h.withdrawFront()
		}
	}))
	base, f := selectedHTTPWriteFixture(t, origin, executor, []byte(original))
	h, generation := preparedHTTPFixture(t, base, f)
	forward := sealFixtureFront(t, h, generation)
	call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
	if rc, message := forward("ask", call); rc != 0 || !strings.Contains(message, `"verdict":"asks"`) {
		t.Fatalf("ask: rc=%d message=%q", rc, message)
	}
	pending, _, err := f.store.List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v err=%v", pending, err)
	}
	if _, err := f.store.Decide(pending[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	front.Store(h)
	forward("act", call)
	if writes.Load() != 1 {
		t.Fatalf("origin saw %d writes, want exactly 1", writes.Load())
	}
	_, grants, err := f.store.List()
	if err != nil || len(grants) != 1 || grants[0].Uses != 0 || grants[0].EndedBy() != brokerstore.EndUsed {
		t.Fatalf("a possibly sent write was refunded: grants=%+v err=%v", grants, err)
	}
	if rc, message := forward("act", call); rc == 0 || !strings.Contains(message, "withdrawn before sending") || writes.Load() != 1 {
		t.Fatalf("a withdrawn front retried the write: rc=%d message=%q writes=%d", rc, message, writes.Load())
	}
}

// TestDaemonSignalWithdrawsBeforeCancel pins Main's signal goroutine: it withdraws the selected
// front before it cancels the broker's context, so no call begins a send after the signal.
func TestDaemonSignalWithdrawsBeforeCancel(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "daemon.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var main *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "Main" {
			main = fd
		}
	}
	if main == nil {
		t.Fatal("daemon.go has no Main")
	}
	found := false
	ast.Inspect(main.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CommClause)
		if !ok {
			return true
		}
		recv, ok := clause.Comm.(*ast.ExprStmt)
		if !ok {
			return true
		}
		unary, ok := recv.X.(*ast.UnaryExpr)
		if !ok || unary.Op != token.ARROW {
			return true
		}
		if ident, ok := unary.X.(*ast.Ident); !ok || ident.Name != "signals" {
			return true
		}
		withdraw, cancel := -1, -1
		for i, stmt := range clause.Body {
			ast.Inspect(stmt, func(m ast.Node) bool {
				callExpr, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := callExpr.Fun.(type) {
				case *ast.SelectorExpr:
					if fn.Sel.Name == "withdrawFront" && withdraw < 0 {
						withdraw = i
					}
				case *ast.Ident:
					if fn.Name == "cancel" && cancel < 0 {
						cancel = i
					}
				}
				return true
			})
		}
		if withdraw < 0 || cancel < 0 || withdraw >= cancel {
			t.Fatalf("the signal case must call withdrawFront before cancel: withdraw at %d, cancel at %d", withdraw, cancel)
		}
		found = true
		return false
	})
	if !found {
		t.Fatal("Main has no case receiving from signals")
	}
}
