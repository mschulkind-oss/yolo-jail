package run

// macosuserjaildaemon_test.go pins the macos-user arm's half of the jail-daemon lifecycle since
// OQ-DP8 and OQ-DP9 (docs/design/declaration-parity.md, ruled 2026-09-28; described in the
// jail-daemon section of docs/reference/macos-user-nix-and-features.md): the daemons the Seatbelt guest RUNS are
// handed to its supervisor (MacosUserRun's JailDaemons), with the declared argv verbatim, their
// caller tokens and endpoints; and the ones it does not run are declined BY NAME, with a reason.
//
// THE TESTS DRIVE Run(), never the printer or the composer, for the reason
// macosuserloopholes_test.go states at length: AGENTS.md has this repo shipping "a test that
// pins the CALLEE while the CALL SITE is unpinned" five times. Delete the guestJailDaemons
// argument, the JailDaemonsRunIn split, or the noteMacosUserJailDaemonDeclines call from the
// macos-user arm, and a test here fails.
//
// ⚠ NONE OF THIS HAS EXECUTED ON HARDWARE. What a Mac still has to settle is that the
// supervisor these tests hand over really starts under sandbox-exec and its daemons bind.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// payloadOf is the supervisor's parse of the YOLO_JAIL_DAEMONS the arm handed the guest.
func payloadOf(t *testing.T, jd macosuser.JailDaemons) []supervisor.Spec {
	t.Helper()
	if jd.Env == nil {
		return nil
	}
	v, _ := jd.Env.Get("YOLO_JAIL_DAEMONS")
	s, _ := v.(string)
	return supervisor.ParseEnv(s)
}

// A LOOPHOLE'S jail_daemon RUNS IN THE GUEST, AS DECLARED. The payload the supervisor reads
// carries the manifest's argv word for word, with only {listen} resolved — to a port this launch
// picked, because the sandbox shares the Mac's loopback — and no decline line names it.
func TestMacosUserRunsALoopholeJailDaemonInTheGuestAsDeclared(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1460", "caller_token": true}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || specs[0].Name != "acme-proxy" {
		t.Fatalf("the guest was handed %+v, want the acme-proxy jail daemon\n%s", specs, got.out)
	}
	cmd := specs[0].Cmd
	if len(cmd) != 4 || cmd[0] != "yolo-jaild" || cmd[1] != "acme-adapter" || cmd[2] != "--listen" ||
		!strings.HasPrefix(cmd[3], "127.0.0.1:") || cmd[3] == "127.0.0.1:1460" {
		t.Errorf("the declared argv did not reach the supervisor verbatim (only {listen} "+
			"resolved, to a picked port): %v", cmd)
	}
	if strings.Contains(got.out, "acme-proxy: yolo-jaild") {
		t.Errorf("a daemon the guest runs was declined:\n%s", got.out)
	}
	// Its caller token reaches the supervisor, and — unscoped — the agent too, as a
	// container's shared channel exports it; never on an argv.
	tokVar := "YOLO_SERVICE_ACME_PROXY_TOKEN"
	dv, _ := got.jailDaemons.Env.Get(tokVar)
	av, _ := got.env.Get(tokVar)
	if tok, _ := dv.(string); !svcendpoint.IsToken(tok) || av != dv {
		t.Errorf("the caller token is not handed to both the supervisor and the agent: daemon %v, agent %v", dv, av)
	}
}

// THE BARE DEFAULT, `"packs": ["claude"]`, END TO END, since HS-D15 (the doorway rule): the
// OpenAI refresh doorway opens OUTSIDE the guest as this launch's own listener, so the guest's
// supervisor is handed nothing at all; the Claude OAuth terminator is declined (it needs a
// container's --add-host and :443); the wire bridge is declined as a jail daemon (a pack service
// runs its host half here); and the refresh adapter's jail daemon is declined too, the line
// saying its doorway runs for this launch. The doorway is handed its token and the host
// service's endpoint and private socket.
func TestMacosUserBareClaudeOpensTheOpenAIDoorwayOutsideAndDeclinesTheRestByName(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t, `{"packs": ["claude"]}`, shellWith(nil))
	o.ProfileName = ""
	doors := observeDoorways(t)
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if specs := payloadOf(t, seen.jailDaemons); len(specs) != 0 || seen.jailDaemons.Env != nil {
		t.Errorf("the guest was handed a supervisor for %+v; every shipped doorway opens outside it", specs)
	}
	plan, in := doors.only(t, "openai-auth-broker")
	if strings.Join(plan.Cmd[:3], " ") != "yolo internal daemon" || plan.Cmd[3] != "openai-auth-adapter" {
		t.Errorf("the doorway's argv is not the manifest's host_cmd: %v", plan.Cmd)
	}
	// AT A PICKED PORT, the one in its argv: the doorway binds the Mac's loopback, where a second
	// launch would find the declared 1460 held. Deleting the doorways from the served set the
	// launch settles ports for (loopholes.ServedJailDaemons) leaves it at 1460 and fails this.
	if addr := plan.Addresses(); len(addr) != 1 || addr[0] == "127.0.0.1:1460" ||
		!strings.HasPrefix(addr[0], "127.0.0.1:") || plan.Cmd[len(plan.Cmd)-1] != addr[0] {
		t.Errorf("the doorway is not at a port picked for this launch: argv %v, addresses %v", plan.Cmd, addr)
	}
	out := stderr.String()
	for _, want := range []string{
		"Declined: these jail daemons do not run in the macos-user sandbox",
		"claude-oauth-broker: yolo-jaild oauth-terminator — it terminates TLS for an intercepted hostname",
		"wire-bridge: yolo-jaild wire-bridge — a pack service runs its host half on this backend",
		"openai-auth-broker: yolo-jaild openai-auth-adapter --listen " + plan.Addresses()[0] +
			" — its doorway opens outside the sandbox on this backend instead",
		"(its doorway runs for this launch, outside the sandbox)",
		`Opened the "openai-auth-broker" doorway (pack "openai-auth", pid 4242) on ` + plan.Addresses()[0],
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch does not say %q:\n%s", want, out)
		}
	}
	if tok, _ := seen.env.Get("YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN"); tok != plan.Token || !svcendpoint.IsToken(plan.Token) {
		t.Errorf("the agent's token for the Codex launcher's marker (%v) is not the doorway's (%q)", tok, plan.Token)
	}
	for _, k := range []string{"YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", "YOLO_OPENAI_AUTH_HOST_SOCKET"} {
		if in[k] == "" {
			t.Errorf("the doorway's input lacks %s, a route to its host service: %v", k, in)
		}
	}
}

// A pack SERVICE's jail_daemon is declined by name, with the reason: on this backend a pack
// service runs its host half (OQ-NC1 A), never its jail daemon.
func TestMacosUserDeclinesAServiceJailDaemonByName(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalPackJSON(t, home, `{"contributes": [{"kind": "service", "name": "acme-bridge",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-bridge"]},
		"endpoint": "acme-bridge.endpoint"}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if !strings.Contains(got.out, "acme-bridge: yolo-jaild acme-bridge — a pack service runs its host half") {
		t.Errorf("a pack SERVICE's jail daemon is not declined by name with its reason:\n%s", got.out)
	}
	if len(payloadOf(t, got.jailDaemons)) != 0 {
		t.Errorf("the guest was handed a pack service's jail daemon: %+v", payloadOf(t, got.jailDaemons))
	}
}

// A launch whose selected packs declare NO jail daemon says nothing and hands the guest nothing:
// a notice on every native launch is the one OQ-BP-3 says people learn to skip.
func TestMacosUserWithNoJailDaemonDeclinesAndRunsNothing(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "cmd": `+testHostDaemonCmdJSON("acme-no-jd")+`}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if strings.Contains(got.out, "Declined: these jail daemons") {
		t.Errorf("a launch with no declared jail daemon printed a decline:\n%s", got.out)
	}
	if got.jailDaemons.Env != nil {
		t.Errorf("a launch with no jail daemon handed the guest a supervisor env")
	}
}

// A --dry-run hands the plan the same guest daemons and states the same declines: a plan must
// describe the launch a user would really get.
func TestMacosUserDryRunDescribesTheGuestDaemonsToo(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter"]}}`)
	writeLocalPackJSON(t, home, `{"contributes": [
		{"kind": "loophole", "from": "loopholes/acme-proxy"},
		{"kind": "service", "name": "acme-bridge",
		 "jail_daemon": {"cmd": ["yolo-jaild", "acme-bridge"]}}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	var handed macosuser.JailDaemons
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, jd macosuser.JailDaemons) int {
		handed = jd
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d, want 0\n%s%s", rc, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	if !strings.Contains(out, "acme-bridge: yolo-jaild acme-bridge — ") {
		t.Errorf("the plan render does not state the decline:\n%s", out)
	}
	if specs := payloadOf(t, handed); len(specs) != 1 || specs[0].Name != "acme-proxy" {
		t.Errorf("the plan render was handed %+v, want the acme-proxy guest daemon", specs)
	}
}

// writeLocalPackJSON writes the CONVENTIONAL local pack's manifest (~/.config/yolo-jail/local),
// which config.LoadPacks appends with no `packs` entry naming it — the cheapest way for a
// launch-level test to own a pack. writeLocalLoopholePack (macosuserloopholes_test.go) is the
// loophole-shaped version of this; a SERVICE contribution needs the manifest written whole.
func writeLocalPackJSON(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ONE PAYLOAD PER LAUNCH, composed above the backend dispatch — the structural half, which the
// runtime tests above cannot see.
//
// Every test in this file would still pass if the composition moved back inside container-argv
// assembly and the native arm grew a second call of its own: both arms would print and emit
// the right thing, from two compositions that agree only because two call sites pass the same
// arguments. That is the shape the hoist exists to remove (the same argument Run's own comment
// makes for the pack trees, the launch flags and the channel), so it is asserted where it
// lives: `jailDaemonsFor` is called ONCE, as a statement of Run's own body, BEFORE the
// macos-user branch — and the container arm reads that value rather than composing one.
func TestTheJailDaemonPayloadIsComposedAboveTheBackendDispatch(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "run.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the file under test: %v", err)
	}

	var run, container *ast.FuncDecl
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "Run":
			run = fn
		case "runContainer":
			container = fn
		}
	}
	if run == nil || container == nil {
		t.Fatal("Run or runContainer is gone — this guard has lost its subject; repoint it at " +
			"whatever composes the launch's jail-daemon payload")
	}

	composedAt, dispatchAt := -1, -1
	for i, stmt := range run.Body.List {
		if assign, ok := stmt.(*ast.AssignStmt); ok {
			for _, rhs := range assign.Rhs {
				if callsMethod(rhs, "jailDaemonsFor") {
					composedAt = i
				}
			}
		}
		// THE DISPATCH, identified by what its body DOES rather than by its condition:
		// `rt != "macos-user"` guards the --dry-run refusal a hundred lines earlier, and
		// keying on the string alone measured that one instead (it found this on the first
		// run). The arm that matters is the one that hands the launch to the backend.
		if ifs, ok := stmt.(*ast.IfStmt); ok && dispatchAt < 0 {
			ast.Inspect(ifs, func(n ast.Node) bool {
				if callsMethod(n, "MacosUserRun") {
					dispatchAt = i
				}
				return true
			})
		}
	}
	if composedAt < 0 {
		t.Fatal("Run does not compose the jail-daemon payload as a statement of its own body. " +
			"A composition nested in one arm of the dispatch is invisible to the other, which " +
			"is how the native backend came to start neither of the two daemons a bare " +
			"`\"packs\": [\"claude\"]` selects")
	}
	if dispatchAt < 0 {
		t.Fatal("no statement of Run's body dispatches to MacosUserRun — the backend dispatch " +
			"moved and this guard can no longer see whether the payload precedes it")
	}
	if composedAt > dispatchAt {
		t.Errorf("the jail-daemon payload is composed at statement %d and the macos-user "+
			"dispatch is at %d: the native arm returns before the composition, so it has "+
			"nothing to decline", composedAt, dispatchAt)
	}

	// And the container arm CONSUMES that value. Without this the hoist could stay and
	// assembly could quietly compose a second payload, which is the state this replaced.
	threaded := false
	ast.Inspect(container, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "assembleInput" {
			return true
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "jailDaemons" {
				threaded = true
			}
		}
		return true
	})
	if !threaded {
		t.Error("runContainer's assembleInput does not carry `jailDaemons`, so the container " +
			"argv is being built from something other than the launch's one composed payload")
	}
}
