package run

// macosuserjaildaemon_test.go pins the macos-user arm's TRUTHFUL DECLINE: every jail daemon
// this launch declared is named, and none is claimed to run
// (docs/design/jail-daemon-on-macos-user-plan.md step 2).
//
// THE TESTS DRIVE Run(), never the printer, for the reason macosuserloopholes_test.go states
// at length: the defect these close survived because the only assertions about this arm called
// the callee directly, and AGENTS.md has this repo shipping "a test that pins the CALLEE while
// the CALL SITE is unpinned" five times. Delete the noteMacosUserJailDaemonDeclines call from
// the macos-user arm, or the `jailDaemons` hoist above the dispatch, and every test here fails.
//
// ⚠ NONE OF THIS HAS EXECUTED ON HARDWARE. What a Mac still has to settle is whether the line
// appears on a real launch (and, later, whether any of these daemons can be made to run at
// all: that is steps 3–4, blocked on two unfiled rulings).

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
)

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

// A LOOPHOLE's jail_daemon is declined BY NAME, with its argv, and the launch still succeeds.
//
// The argv matters as much as the name: the payload is the only place the port a jail-side
// process would have bound is written down (for the shipped openai-auth adapter that is
// 127.0.0.1:1460, the one `packs/codex` already points CODEX_REFRESH_TOKEN_URL_OVERRIDE at),
// so a decline that printed the name alone would not tell a user what is missing.
func TestMacosUserDeclinesALoopholeJailDaemonByName(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "127.0.0.1:1460"]}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0 — a declined jail daemon is not a launch failure\n%s",
			got.rc, got.out)
	}
	if !strings.Contains(got.out, "no jail-side daemon runs on macos-user") {
		t.Errorf("the launch declared a jail daemon on a backend that starts none and said "+
			"nothing. The endpoint is published and ACL-granted with nothing listening, so "+
			"silence here is the B-0 shape: a backend that looked provisioned and configured "+
			"nothing.\n%s", got.out)
	}
	if !strings.Contains(got.out, "acme-proxy: yolo-jaild acme-adapter --listen 127.0.0.1:1460") {
		t.Errorf("the decline does not name the daemon AND its argv, so a user cannot tell "+
			"which declaration is inert or what it would have bound:\n%s", got.out)
	}
}

// A pack SERVICE's jail_daemon is declined too — the half a report over loopholes alone would
// miss, and the half the shipped default actually turns on (`packs/claude` `needs`
// `wire-bridge` unconditionally, and its whole content is one `kind: "service"` daemon).
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
	if !strings.Contains(got.out, "acme-bridge: yolo-jaild acme-bridge") {
		t.Errorf("a pack SERVICE's jail daemon is in the same payload as a loophole's (one env "+
			"contract, one writer) and must be declined in the same breath:\n%s", got.out)
	}
}

// ONE LINE PER DAEMON, not one per launch: two declarations, two named lines.
//
// Asserted because the header alone would satisfy a report that said "some daemons will not
// run" — and the whole complaint against the old silence was that the user could not tell
// WHICH declarations were inert.
func TestMacosUserDeclinesEveryDeclaredJailDaemon(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter"]}}`)
	// The loophole contribution above plus a service one, in the one local pack.
	writeLocalPackJSON(t, home, `{"contributes": [
		{"kind": "loophole", "from": "loopholes/acme-proxy"},
		{"kind": "service", "name": "acme-bridge",
		 "jail_daemon": {"cmd": ["yolo-jaild", "acme-bridge"]}}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	for _, want := range []string{
		"acme-proxy: yolo-jaild acme-adapter",
		"acme-bridge: yolo-jaild acme-bridge",
	} {
		if !strings.Contains(got.out, want) {
			t.Errorf("missing a per-daemon decline line for %q — the report must name each "+
				"declaration, not the count:\n%s", want, got.out)
		}
	}
}

// A launch that declared NO jail daemon says nothing. The silence is the point: a notice that
// printed on every native launch is the one OQ-BP-3 says people learn to skip, and this
// backend's whole selected set can legitimately declare none.
func TestMacosUserWithNoJailDaemonDeclinesNothing(t *testing.T) {
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
	if strings.Contains(got.out, "no jail-side daemon runs on macos-user") {
		t.Errorf("a launch with no declared jail daemon printed the decline anyway:\n%s", got.out)
	}
}

// A --dry-run states the decline too, and that is the opposite of the exec disclosure's rule.
//
// Both rules come from the same principle: say what this invocation will really do. A plan
// render starts nothing, so an exec line would overclaim (TestMacosUserDryRunStartsNoHostServices
// is that half) — while "this daemon is declared and will not run" is exactly as true of the
// plan as of the launch, and a `--dry-run` is the instrument people use to find out.
func TestMacosUserDryRunStillDeclinesJailDaemons(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter"]}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d, want 0\n%s%s", rc, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	if !strings.Contains(out, "acme-proxy: yolo-jaild acme-adapter") {
		t.Errorf("a plan render must describe the launch a user would really get, and this "+
			"declaration is inert in both:\n%s", out)
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
