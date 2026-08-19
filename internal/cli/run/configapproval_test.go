package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// configapproval_test.go covers the LAUNCHER half of docs/design/config-safety.md's
// two rulings: what a refused non-interactive launch actually prints (OQ-D2), and
// that the launcher never reaches into the workspace for the approval record
// (OQ-D1). The decision logic itself lives in internal/config and is tested there;
// what is only observable here is the rendering and the wiring of the flag.

// approvalOptions builds the minimal Options the config gate needs: an isolated
// $HOME (so the host-side approval record lands in a temp state dir), a captured
// stdout, and both tty seams pinned so the test never consults the real terminal.
func approvalOptions(t *testing.T, ws string, isTTY bool) (*Options, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	return &Options{
		Workspace:   ws,
		Stdout:      &buf,
		Stderr:      &buf,
		IsTTYStdin:  func() bool { return isTTY },
		IsTTYStdout: func() bool { return false },
	}, &buf
}

func approvalConfig(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("not an object: %T", v)
	}
	return m
}

// A REFUSED LAUNCH MUST SAY HOW TO PROCEED. The reader of this message is, by
// construction, someone who cannot be prompted — so the launcher has to print the
// flag, the files the merged config came from, and the change itself. Printing a
// bare "config changed" and exiting would leave a CI operator with no move.
func TestNonInteractiveConfigChangeRefusalIsActionable(t *testing.T) {
	ws := t.TempDir()
	o, buf := approvalOptions(t, ws, false /*non-tty*/)

	// Establish an approved baseline the same way an earlier launch would have.
	if ok, err := config.CheckConfigChanges(ws, approvalConfig(t, `{"packages": ["strace"]}`),
		false, true, nil); err != nil || !ok {
		t.Fatalf("seeding the baseline: ok=%v err=%v", ok, err)
	}

	if o.checkConfigChanges(approvalConfig(t, `{"packages": ["strace", "htop"]}`)) {
		t.Fatal("a changed config with no terminal to approve it on must refuse the launch")
	}

	out := buf.String()
	for _, want := range []string{
		config.AcceptConfigChangesFlag,
		filepath.Join(ws, config.WorkspaceConfigName),
		config.ApprovalSnapshotPath(ws),
		`+    "htop"`, // the diff, not just the fact of one
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal output does not mention %q:\n%s", want, out)
		}
	}
}

// THE SPLIT FILE MUST ACTUALLY BE DELIVERED. Moving the approval record host-side
// (OQ-D1) left the in-jail LoadConfig short-circuit without a source, so the launch
// now writes the merged config to <workspace>/.yolo/config-assembled.json for it.
// The writer lives here and the reader lives in internal/config, which is exactly
// the shape that rots quietly: drop this call and every unit test still passes
// while every jail silently falls back to a REDUCED re-assemble that has lost the
// host-only include_if_found overrides. So the round trip is pinned end to end.
func TestFreshLaunchDeliversTheAssembledConfigTheJailReadsBack(t *testing.T) {
	ws := t.TempDir()
	o, _ := approvalOptions(t, ws, false)

	// A merged config carrying a key that exists ONLY in the merge — the shape of
	// a host-side include_if_found override, which is the whole reason the jail
	// reads a delivered copy instead of re-assembling.
	merged := approvalConfig(t, `{"packages": ["ripgrep"], "mcp_servers": {"tavily": {"command": "npx"}}}`)
	o.writeLaunchConfigArtifacts(merged)

	// Read it back the way an in-jail LoadConfig does, for the jail's OWN workspace.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	t.Setenv("YOLO_WORKSPACE", ws)
	got, err := config.LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Get("mcp_servers"); !ok {
		t.Errorf("the in-jail read did not get the delivered merge (keys %v) — a jail would "+
			"silently run on a reduced config", got.Keys())
	}
	// And it is NOT written to the retired approval location.
	if _, err := os.Stat(config.LegacyWorkspaceSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Errorf("a launch must not write the old workspace-side snapshot (err=%v)", err)
	}
}

// The flag is the whole of Design Goal 5 after the ruling: non-interactive use
// still works, through an explicit approval rather than an implicit yes. Wiring it
// on the Options must both let the launch through AND record the approval, or the
// next scripted run refuses over the same unchanged change.
func TestAcceptConfigChangesFlagLetsANonInteractiveLaunchThrough(t *testing.T) {
	ws := t.TempDir()
	o, _ := approvalOptions(t, ws, false /*non-tty*/)
	o.AcceptConfigChanges = true

	if ok, err := config.CheckConfigChanges(ws, approvalConfig(t, `{"packages": ["strace"]}`),
		false, true, nil); err != nil || !ok {
		t.Fatalf("seeding the baseline: ok=%v err=%v", ok, err)
	}

	changed := approvalConfig(t, `{"packages": ["strace", "htop"]}`)
	if !o.checkConfigChanges(changed) {
		t.Fatal("--accept-config-changes must let a non-interactive launch proceed")
	}
	// Recorded: the same config now passes with the flag off.
	o.AcceptConfigChanges = false
	if !o.checkConfigChanges(changed) {
		t.Error("the flag must record the approval, or the next launch refuses again")
	}
}

// TestFreshLaunchCallsTheConfigArtifactWriter pins the half of the delivery the
// test above cannot reach, and the half its own comment promises.
//
// TestFreshLaunchDeliversTheAssembledConfigTheJailReadsBack calls
// writeLaunchConfigArtifacts DIRECTLY, so it pins the helper's body and nothing
// about who invokes it. Deleting `o.writeLaunchConfigArtifacts(cfg)` from
// runContainer therefore leaves the whole unit gate green while every jail
// silently falls back to the reduced in-jail re-assemble — the failure that
// comment names as the reason the round trip is pinned at all. Verified: the
// deletion is invisible to `go test -short ./...`.
//
// The call site is not reachable any other way from a unit test: runContainer
// starts a real container, so integration/ is the only runtime witness and it does
// not run on this gate. Reading the SOURCE is the repo's existing answer to that
// shape — internal/cli's TestRunUsageListsEveryRunFlag walks parseRunArgs's AST for
// the same reason — and an AST walk rather than a substring search is what keeps
// the match off a mention in a comment or a test.
//
// ORDER is asserted as well as presence. The write must follow the approval gate:
// a launch that checkConfigChanges REFUSED has, by construction, a config no human
// approved, and delivering that config into the workspace on the way out would
// leave the jail a copy of exactly the thing the refusal exists to withhold.
func TestFreshLaunchCallsTheConfigArtifactWriter(t *testing.T) {
	const (
		approvalGate = "checkConfigChanges"
		writer       = "writeLaunchConfigArtifacts"
	)
	fn := methodDecl(t, "run.go", "runContainer")

	// Collect both call sites by source position. Positions come from one file
	// parsed in one FileSet, so comparing them is comparing statement order.
	pos := map[string]token.Pos{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// FIRST occurrence wins: a later, conditional re-call must not be what
		// satisfies the ordering assertion below.
		if _, seen := pos[sel.Sel.Name]; !seen {
			pos[sel.Sel.Name] = call.Pos()
		}
		return true
	})

	if _, ok := pos[writer]; !ok {
		t.Fatalf("runContainer no longer calls %s. The merged config is then never "+
			"delivered to <workspace>/.yolo/config-assembled.json, and every in-jail "+
			"LoadConfig for the jail's own workspace falls back to a REDUCED re-assemble "+
			"that has lost the host-only include_if_found overrides — silently, on every "+
			"launch. If the call moved to another function on the fresh-launch path, move "+
			"this check with it rather than deleting it.", writer)
	}
	if _, ok := pos[approvalGate]; !ok {
		t.Fatalf("runContainer no longer calls %s — the fresh-launch approval gate is "+
			"gone, which is a larger regression than the one this test was written for",
			approvalGate)
	}
	if pos[writer] < pos[approvalGate] {
		t.Errorf("runContainer calls %s BEFORE %s: a launch the approval gate refuses "+
			"would still deliver the unapproved merged config into the workspace the jail "+
			"reads it from", writer, approvalGate)
	}
}

// TestFreshLaunchENFORCESTheApprovalGate is the assertion the presence check above
// cannot make, and the gap was measured rather than imagined: replacing
//
//	if !o.checkConfigChanges(cfg) { return 1 }
//
// with a bare `_ = o.checkConfigChanges(cfg)` left the ENTIRE unit gate green
// (2026-08-18). The call is still there, so the AST presence check above is satisfied,
// the ordering check is satisfied, and every container launch proceeds on a config no
// human approved — which is the whole of OQ-D2, switched off by deleting two characters.
//
// A CALL IS NOT A GATE. `TestNonInteractiveConfigChangeRefusalIsActionable` and
// `TestAcceptConfigChangesFlagLetsANonInteractiveLaunchThrough` drive
// o.checkConfigChanges directly and pin what it DECIDES; `TestMacosUserLaunchGatesOnConfigApproval`
// pins that the macos-user arm obeys the decision. Nothing pinned that the CONTAINER
// arm — the primary path, and the only one integration/ ever exercises — obeys it, and
// integration/ cannot stand in either: the harness passes --accept-config-changes on
// every launch by construction, so a deleted gate looks identical to an honoured one.
//
// So this asserts the SHAPE: the call is the condition of an `if` whose body leaves
// runContainer. That is a structural claim rather than a behavioural one, for the reason
// the test above already states — runContainer starts a real container, so reading the
// source is the only witness a unit test has. It is deliberately loose about the
// spelling (any `if` whose condition mentions the call, any body that returns), so a
// refactor that keeps the gate keeps the test.
func TestFreshLaunchENFORCESTheApprovalGate(t *testing.T) {
	const approvalGate = "checkConfigChanges"
	fn := methodDecl(t, "run.go", "runContainer")

	enforced := false
	ast.Inspect(fn, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok || ifStmt.Cond == nil || ifStmt.Body == nil {
			return true
		}
		if !subtreeCalls(ifStmt.Cond, approvalGate) || !bodyReturns(ifStmt.Body) {
			return true
		}
		enforced = true
		return false
	})
	if !enforced {
		t.Errorf("runContainer calls %s but nothing branches on the answer: the gate has to "+
			"be the condition of an `if` that RETURNS, or a refused approval is computed, "+
			"printed and then ignored while the container starts anyway. This is the exact "+
			"mutation the presence check in the test above walks straight through "+
			"(`_ = o.%s(cfg)`). If the enforcement moved — to an early-return helper, or up "+
			"into Run — move this check with it rather than deleting it.",
			approvalGate, approvalGate)
	}
}

// subtreeCalls reports whether any call to a method named `name` appears anywhere in
// the expression — so `!o.f(x)`, `a && o.f(x)` and a bare `o.f(x)` all count. The
// receiver is deliberately not matched: runContainer has one receiver, and pinning its
// spelling would fail a rename that changed nothing.
func subtreeCalls(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// bodyReturns reports whether a block's own statements include a return — not a nested
// one inside a closure, which would leave runContainer running.
func bodyReturns(b *ast.BlockStmt) bool {
	for _, stmt := range b.List {
		if _, ok := stmt.(*ast.ReturnStmt); ok {
			return true
		}
	}
	return false
}

// methodDecl parses one file of THIS package and returns the method with the
// given name. Test files are never parsed, so a helper of the same name in a
// _test.go file cannot stand in for the production one the assertion is about.
func methodDecl(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok && fd.Recv != nil && fd.Name.Name == name {
			return fd
		}
	}
	t.Fatalf("%s has no method %s", file, name)
	return nil
}
