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
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// configapproval_test.go covers the LAUNCHER half of docs/reference/config-safety.md's
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
		IsTTYStderr: func() bool { return false },
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

func approveWorkspaceConfigThroughGate(t *testing.T, o *Options, ws, body string, output *bytes.Buffer) {
	t.Helper()
	writeWorkspaceConfig(t, ws, body)
	previousAccept := o.AcceptConfigChanges
	previousStdinTTY, previousStderrTTY := o.IsTTYStdin, o.IsTTYStderr
	o.AcceptConfigChanges = true
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStderr = func() bool { return false }
	approved := o.checkConfigChanges(nil, "podman")
	o.AcceptConfigChanges = previousAccept
	o.IsTTYStdin, o.IsTTYStderr = previousStdinTTY, previousStderrTTY
	if !approved {
		t.Fatalf("seeding the workspace approval through the public gate failed: %s", output.String())
	}
	output.Reset()
}

// A REFUSED LAUNCH MUST SAY HOW TO PROCEED. The reader of this message is, by
// construction, someone who cannot be prompted — so the launcher has to print the
// flag, the files the merged config came from, and the change itself. Printing a
// bare "config changed" and exiting would leave a CI operator with no move.
func TestNonInteractiveConfigChangeRefusalIsActionable(t *testing.T) {
	ws := t.TempDir()
	o, buf := approvalOptions(t, ws, false /*non-tty*/)

	approveWorkspaceConfigThroughGate(t, o, ws, `{"packages": ["strace"]}`, buf)
	writeWorkspaceConfig(t, ws, `{"packages": ["strace", "htop"]}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("a changed config with no terminal to approve it on must refuse the launch")
	}

	out := buf.String()
	for _, want := range []string{
		config.AcceptConfigChangesFlag,
		filepath.Join(ws, config.WorkspaceConfigName),
		config.ApprovalSnapshotPath(ws),
		"Interpreted",
		"Exact",
		"install.nix",
		"htop",
		"yolo config approval-diff",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal output does not mention %q:\n%s", want, out)
		}
	}
}

func TestConfigChangePromptRequiresTerminalStdinAndStderr(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		ttyIn, ttyOut, ttyErr bool
		wantPrompt            bool
	}{
		{"stderr redirected", true, true, false, false},
		{"stdout redirected", true, false, true, true},
		{"stdin redirected", false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			o, buf := approvalOptions(t, ws, tc.ttyIn)
			o.IsTTYStdout = func() bool { return tc.ttyOut }
			o.IsTTYStderr = func() bool { return tc.ttyErr }
			stdin := strings.NewReader("y\n")
			o.Stdin = stdin
			approveWorkspaceConfigThroughGate(t, o, ws, `{"packages": ["strace"]}`, buf)
			writeWorkspaceConfig(t, ws, `{"packages": ["strace", "htop"]}`)

			got := o.checkConfigChanges(nil, "podman")
			if got != tc.wantPrompt {
				t.Fatalf("config-change approval = %v, want %v; output:\n%s", got, tc.wantPrompt, buf)
			}
			if tc.wantPrompt {
				if !strings.Contains(buf.String(), "Accept these workspace config changes?") || stdin.Len() != 0 {
					t.Errorf("interactive prompt did not display on stderr and read stdin: output=%q remaining bytes=%d",
						buf.String(), stdin.Len())
				}
			} else {
				if stdin.Len() != len("y\n") {
					t.Errorf("noninteractive refusal consumed scripted stdin: remaining bytes=%d", stdin.Len())
				}
				if !strings.Contains(buf.String(), config.AcceptConfigChangesFlag) {
					t.Errorf("noninteractive refusal lacks explicit next step: %s", buf.String())
				}
			}
		})
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
	// The jail reads its own setup's records (SUP-D37): the one this launch wrote under.
	t.Setenv("YOLO_SETUP", o.setupName())
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
	o, buf := approvalOptions(t, ws, false /*non-tty*/)
	o.AcceptConfigChanges = true

	approveWorkspaceConfigThroughGate(t, o, ws, `{"packages": ["strace"]}`, buf)

	writeWorkspaceConfig(t, ws, `{"packages": ["strace", "htop"]}`)
	if !o.checkConfigChanges(nil, "podman") {
		t.Fatal("--accept-config-changes must let a non-interactive launch proceed")
	}
	// Recorded: the same config now passes with the flag off.
	o.AcceptConfigChanges = false
	if !o.checkConfigChanges(nil, "podman") {
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

// A change to the machine-wide User config must not trigger approval prompts or refusals
// on an existing workspace whose own config is unchanged (OQ-S1).
func TestUserConfigChangeDoesNotTriggerWorkspacePrompt(t *testing.T) {
	ws := t.TempDir()
	o, output := approvalOptions(t, ws, false /*non-tty*/)
	t.Setenv(config.UserLayerEnv, "")

	approveWorkspaceConfigThroughGate(t, o, ws, `{"packages": ["strace"]}`, output)
	workspacePath := filepath.Join(ws, config.WorkspaceConfigName)
	workspaceBefore, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	workspaceInfo, err := os.Stat(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	baselineRead, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	baselineJSON, err := config.SnapshotJSON(baselineRead.Config)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := config.ConfigPartChanged(ws, baselineRead.Config); err != nil || changed {
		t.Fatalf("public approval baseline does not match the typed workspace read: changed=%v err=%v", changed, err)
	}
	userBefore, err := config.LoadReadableUserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	userBeforeJSON, err := config.SnapshotJSON(userBefore)
	if err != nil {
		t.Fatal(err)
	}
	userPath := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte(`{"packs": ["copilot"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	userAfter, err := config.LoadReadableUserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	userAfterJSON, err := config.SnapshotJSON(userAfter)
	if err != nil {
		t.Fatal(err)
	}
	if userAfterJSON == userBeforeJSON {
		t.Fatal("typed User-scope read did not observe the changed user config")
	}
	packsValue, present := userAfter.Get("packs")
	packs, ok := packsValue.([]any)
	if !present || !ok || len(packs) != 1 || packs[0] != "copilot" {
		t.Fatal("typed User-scope read did not contain the changed packs value")
	}
	gateRead, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := gateRead.Config.Get("packs"); present {
		t.Fatal("workspace approval read acquired a User-scope key")
	}
	currentJSON, err := config.SnapshotJSON(gateRead.Config)
	if err != nil {
		t.Fatal(err)
	}
	if currentJSON != baselineJSON {
		t.Fatal("User-scope change altered the typed workspace approval input")
	}
	if changed, err := config.ConfigPartChanged(ws, gateRead.Config); err != nil || changed {
		t.Fatalf("User-scope change made the public workspace approval baseline pending: changed=%v err=%v", changed, err)
	}
	workspaceAfter, err := os.ReadFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	workspaceInfoAfter, err := os.Stat(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(workspaceBefore, workspaceAfter) || workspaceInfo.Mode().Perm() != workspaceInfoAfter.Mode().Perm() {
		t.Fatal("User-scope change modified the workspace config bytes or mode")
	}

	// CheckConfigChanges must pass with zero prompts and zero refusals.
	if !o.checkConfigChanges(nil, "podman") {
		t.Fatal("user-level config change must not refuse or prompt on an unchanged workspace")
	}
	if output.Len() != 0 {
		t.Fatalf("unchanged workspace config produced approval output: %q", output.String())
	}
}

func TestConfigApprovalRefusalRendersBothViewsAndRecordsNothing(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"packages":["jq"]}`, out)
	before, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
	if err != nil {
		t.Fatal(err)
	}

	writeWorkspaceConfig(t, ws, `{"packages":["jq","ripgrep"]}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("changed config without a terminal must refuse")
	}
	for _, want := range []string{"Interpreted", "Exact", "[1]", "install.nix", "ripgrep",
		"yolo config approval-diff", config.AcceptConfigChangesFlag} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	after, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refusal rewrote the approval identity")
	}
}

func TestConfigApprovalInvalidPreviousIdentityFallsBackToRawDiff(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"agents_md_extra":"old"}`, out)
	path := config.ApprovalSnapshotPath(ws)
	if err := os.WriteFile(path, []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceConfig(t, ws, `{"agents_md_extra":"new"}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("changed config without a terminal must refuse")
	}
	output := out.String()
	if !strings.Contains(output, "The change list could not account for every difference; showing the raw comparison instead.") ||
		!strings.Contains(output, "--- previous workspace config") || !strings.Contains(output, "+++ current workspace config") {
		t.Fatalf("invalid previous identity did not use the complete raw fallback:\n%s", output)
	}
	if strings.Contains(output, "Interpreted") || strings.Contains(output, "\nExact\n") {
		t.Fatalf("fallback printed a partial structured view:\n%s", output)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refusal repaired or rewrote the malformed approval record")
	}
}

func TestConfigApprovalInteractivePromptShowsViewsAndKeepsRecordOnNo(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, true)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"agents_md_extra":"old"}`, out)
	before, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceConfig(t, ws, `{"agents_md_extra":"new"}`)
	o.IsTTYStderr = func() bool { return true }
	o.IsTTYStdin = func() bool { return true }
	o.Stdin = strings.NewReader("n\n")
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("n must refuse a changed config")
	}
	output := out.String()
	footer := strings.Index(output, approvalDiffFooter)
	question := strings.Index(output, "Accept these workspace config changes? [y/N]")
	if !strings.Contains(output, "Interpreted") || !strings.Contains(output, "Exact") || !strings.Contains(output, "agents_md_extra") {
		t.Fatalf("interactive gate omitted the structured views:\n%s", output)
	}
	if footer < 0 || question < 0 || footer > question || strings.TrimSpace(output[footer+len(approvalDiffFooter):question]) != "" {
		t.Fatalf("raw-comparison footer was not immediately above the question:\n%s", output)
	}
	after, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("answering n rewrote the approval identity")
	}
}

func TestAcceptConfigChangesReportsConfigOnlyBeforeWritingRecord(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"agents_md_extra":"old"}`, out)
	writeWorkspaceConfig(t, ws, `{"agents_md_extra":"approved"}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(nil, "podman") {
		t.Fatalf("--accept-config-changes refused:\n%s", out.String())
	}
	output := out.String()
	for _, want := range []string{"Workspace config changed since last run", "Interpreted", "was approved", "Exact", "approved"} {
		if !strings.Contains(output, want) {
			t.Errorf("accepted report missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, approvalDiffFooter) || strings.Contains(output, "[y/N]") {
		t.Fatalf("accepted report included a prompt footer or question:\n%s", output)
	}
	record, exists, err := config.ReadApprovalRecord(ws)
	if err != nil || !exists {
		t.Fatalf("read accepted record: exists=%v err=%v", exists, err)
	}
	current, err := config.ApprovalRecordPart(ws)
	if err != nil {
		t.Fatal(err)
	}
	if record != current {
		t.Fatalf("accepted identity bytes differ from current gate identity:\nrecord=%s\ncurrent=%s", record, current)
	}
}

func TestConfigApprovalDisplayEscapesUnicodeWithoutChangingStoredIdentity(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"agents_md_extra":"before"}`, out)
	writeWorkspaceConfig(t, ws, `{"agents_md_extra":"emoji 😀 control \u001b bidi \u202e"}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("changed config without a terminal must refuse")
	}
	output := out.String()
	for _, want := range []string{`\U0001F600`, `\u001B`, `\u202E`} {
		if !strings.Contains(output, want) {
			t.Errorf("display missing TOML escape %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"😀", "\u202e", "\x1b", `\uD83D\uDE00`} {
		if strings.Contains(output, forbidden) {
			t.Errorf("display contains raw/unordered Unicode %q:\n%s", forbidden, output)
		}
	}
}

func TestConfigApprovalDisplayTreatsMarkupAsLiteralValue(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"agents_md_extra":"old"}`, out)
	writeWorkspaceConfig(t, ws, `{"agents_md_extra":"literal [bold] and [red] text"}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("changed config without a terminal must refuse")
	}
	if !strings.Contains(out.String(), `"literal \u005Bbold\u005D and \u005Bred\u005D text"`) {
		t.Fatalf("rich-text markup in a value was not rendered as an ASCII-safe literal:\n%s", out.String())
	}
	if strings.ContainsRune(out.String(), '\u2060') {
		t.Fatal("rich-text escaping added non-ASCII to the approval presentation")
	}
}

func TestConfigApprovalRemovalShowsPreviousValuesThroughGate(t *testing.T) {
	for _, tc := range []struct {
		name, previous, want string
	}{
		{"scalar", `{"agents_md_extra":"old"}`, `was  "old"`},
		{"long scalar", `{"agents_md_extra":"` + strings.Repeat("long", 30) + `"}`, strings.Repeat("long", 30)},
		{"table", `{"example":{"gone":{"a":1,"b":"old"}}}`, `example.gone.b = "old"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			o, out := approvalOptions(t, ws, false)
			approveWorkspaceConfigThroughGate(t, o, ws, tc.previous, out)
			writeWorkspaceConfig(t, ws, `{}`)
			if o.checkConfigChanges(nil, "podman") {
				t.Fatal("removed config without a terminal must refuse")
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("removed previous value %q missing from gate display:\n%s", tc.want, out.String())
			}
		})
	}
}

func TestConfigApprovalValueToTableReplacementShowsPreviousLiteralThroughGate(t *testing.T) {
	for _, tc := range []struct {
		name, old, want string
	}{
		{"scalar", `"old"`, `was  "old"`},
		{"list", `["old", "value"]`, `was  ["old", "value"]`},
		{"null", `null`, `was  (null)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			o, out := approvalOptions(t, ws, false)
			approveWorkspaceConfigThroughGate(t, o, ws, `{"example":`+tc.old+`}`, out)
			writeWorkspaceConfig(t, ws, `{"example":{"new":"value"}}`)
			if o.checkConfigChanges(nil, "podman") {
				t.Fatal("replacement without a terminal must refuse")
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("previous %s literal missing from gate display:\n%s", tc.name, out.String())
			}
		})
	}
}

func TestConfigApprovalMixedArrayScalarOccurrencesUseTheirOwnShapeThroughGate(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{"host_files":["a",{"path":"b"}]}`, out)
	writeWorkspaceConfig(t, ws, `{"host_files":["c",{"path":"b"}]}`)
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("changed config without a terminal must refuse")
	}
	output := out.String()
	if strings.Count(output, "table;") > 1 {
		t.Errorf("scalar occurrence was described as a table from its list sibling:\n%s", output)
	}
	for _, want := range []string{`"a"`, `"c"`, "removed", "added"} {
		if !strings.Contains(output, want) {
			t.Errorf("mixed-array gate display missing %q:\n%s", want, output)
		}
	}
}

func TestConfigApprovalNoncanonicalExistingEmptyRecordFallsBackToRawDiff(t *testing.T) {
	ws := t.TempDir()
	o, out := approvalOptions(t, ws, false)
	approveWorkspaceConfigThroughGate(t, o, ws, `{}`, out)
	record := config.ApprovalSnapshotPath(ws)
	if err := os.WriteFile(record, []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o.checkConfigChanges(nil, "podman") {
		t.Fatal("an identity-byte mismatch without a terminal must refuse")
	}
	output := out.String()
	for _, want := range []string{approvalReplayFallback, "previous workspace config", "current workspace config"} {
		if !strings.Contains(output, want) {
			t.Errorf("identity mismatch fallback missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "no config-key paths changed") {
		t.Fatalf("identity-only difference was presented as an empty structural explanation:\n%s", output)
	}
}

func TestConfigApprovalExistingEmptyRecordIsNotMissingBaseline(t *testing.T) {
	t.Run("absent record", func(t *testing.T) {
		ws := t.TempDir()
		o, out := approvalOptions(t, ws, false)
		if !o.checkConfigChanges(nil, "podman") {
			t.Fatalf("empty first-run config should pass silently: %s", out.String())
		}
		if out.Len() != 0 {
			t.Fatalf("empty first-run config asked for approval: %s", out.String())
		}
	})
	t.Run("existing empty record", func(t *testing.T) {
		ws := t.TempDir()
		o, out := approvalOptions(t, ws, false)
		if err := os.MkdirAll(filepath.Dir(config.ApprovalSnapshotPath(ws)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.ApprovalSnapshotPath(ws), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if o.checkConfigChanges(nil, "podman") {
			t.Fatal("an existing empty record is not the missing-record silent baseline")
		}
		if !strings.Contains(out.String(), approvalReplayFallback) || strings.Contains(out.String(), "no config-key paths changed") {
			t.Fatalf("existing empty record was not shown as an unexplained identity difference:\n%s", out.String())
		}
	})
}
