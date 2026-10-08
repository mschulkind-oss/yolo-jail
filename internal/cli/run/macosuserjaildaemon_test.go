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
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
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

// THE GUEST'S DAEMON FINDS ITS PICKED PORT FREE WHEN THE SANDBOX STARTS (NC-D69). The launch
// holds each port it picked from the pick on (servedaddresses.go), and the guest's supervisor,
// which starts with the sandbox, binds the ones its daemons were composed with: so the arm must
// let them go just before MacosUserRun, not at Run's deferred release, which runs only once the
// sandbox has exited. Inside the stub handler, where the supervisor would be binding, the port the
// daemon's argv names must be bindable. Deleting the release above MacosUserRun fails this.
func TestTheMacosUserGuestDaemonsPickedPortIsFreeWhenTheSandboxStarts(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1460", "caller_token": true}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	var listen string
	var bindErr error
	got := macosUserLaunchDuring(t, ws, func(jd macosuser.JailDaemons) {
		specs := payloadOf(t, jd)
		if len(specs) != 1 || len(specs[0].Cmd) != 4 {
			return
		}
		listen = specs[0].Cmd[3]
		l, err := net.Listen("tcp", listen)
		if err != nil {
			bindErr = err
			return
		}
		_ = l.Close()
	})
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if !strings.HasPrefix(listen, "127.0.0.1:") || listen == "127.0.0.1:1460" {
		t.Fatalf("fixture: the guest's daemon was not handed a picked port (%q)\n%s", listen, got.out)
	}
	if bindErr != nil {
		t.Errorf("the guest's daemon could not bind %s, the port it was composed with, as the "+
			"sandbox started: the launch still held it: %v", listen, bindErr)
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

// A pack SERVICE's jail_daemon that publishes an endpoint file is declined by name, with the
// reason: the file's path is a container one the sandbox has no counterpart of
// (docs/design/jail-daemon-on-macos-user-plan.md JD-9).
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
	if !strings.Contains(got.out, "acme-bridge: yolo-jaild acme-bridge — it publishes its endpoint file at /run/yolo-services/acme-bridge.endpoint") {
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
		{"kind": "service", "name": "acme-bridge", "endpoint": "acme-bridge.endpoint",
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

// A PACK SERVICE WITH NO HOST HALF RUNS IN THE GUEST, as a container runs it (OQ-DP8; JD-9). It
// used to be declined as "a pack service runs its host half on this backend instead" although it
// has none, so it ran nowhere. Restoring a bare Service decline in the guest split fails this.
func TestMacosUserRunsAJailDaemonOnlyServiceInTheGuest(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalPackJSON(t, home, `{"contributes": [{"kind": "service", "name": "acme-svc",
		"jail_daemon": {"cmd": ["acme-svc", "--serve"]}}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || specs[0].Name != "acme-svc" || strings.Join(specs[0].Cmd, " ") != "acme-svc --serve" {
		t.Fatalf("the guest was handed %+v, want the acme-svc jail daemon as declared\n%s", specs, got.out)
	}
	if strings.Contains(got.out, "Declined: these jail daemons") {
		t.Errorf("a service the guest runs was declined:\n%s", got.out)
	}
	dv, _ := got.jailDaemons.Env.Get("YOLO_SERVICE_ACME_SVC_TOKEN")
	if tok, _ := dv.(string); !svcendpoint.IsToken(tok) {
		t.Errorf("the service's caller token did not reach the supervisor: %v", dv)
	}
}

// A HOST HALF FROM A FETCHED PACK NEVER RUNS (OQ-HS4; a local pack's does since HS-D27), so the
// service's jail daemon runs in the guest instead, confined, and the launch says so. The service
// serves an adaptation, the shape whose admitted host half the guest defers to: deleting
// AdmitServiceHosts from jailDaemonsFor fails this, because the guest then declines the daemon
// for a host half that never starts.
func TestMacosUserRunsAServiceInTheGuestWhenItsHostHalfIsNotAdmitted(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	src := fetchedPackSource(t, map[string]string{"pack.json": `{"name": "acme", "contributes": [
		{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8299"},
		{"kind": "service", "name": "acme-svc", "jail_daemon": {"cmd": ["acme-svc"]},
		 "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-svc"]}}]}`})
	writeUserConfigJSON(t, home, `{"packs": [{"name": "acme", "source": "`+src+`"}]}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if specs := payloadOf(t, got.jailDaemons); len(specs) != 1 || specs[0].Name != "acme-svc" {
		t.Fatalf("the guest was handed %+v, want the acme-svc jail daemon\n%s", specs, got.out)
	}
	if !strings.Contains(got.out, `Not started outside the sandbox: the "acme-svc" service's host half (pack "acme")`) ||
		!strings.Contains(got.out, "not one yolo ships") ||
		!strings.Contains(got.out, "Its jail daemon runs in the sandbox instead.") {
		t.Errorf("the refused host half is not disclosed with why and where its daemon runs:\n%s", got.out)
	}
	if strings.Contains(got.out, "Declined: these jail daemons") {
		t.Errorf("the guest declined a daemon it runs:\n%s", got.out)
	}
}

// A REFUSED HOST HALF WHOSE JAIL DAEMON THE GUEST DECLINES TOO IS SAID TO RUN NOWHERE, with the
// next step: this service publishes an endpoint file, a container path the sandbox has no
// counterpart of (JD-9's rule (b)), so its refused host half leaves it unserved this launch, and
// a container backend runs it. Deleting the decline lookup from noteRefusedServiceHosts fails
// this, as does a line naming no next step (docs/reference/happy-path-principle.md).
func TestMacosUserSaysARefusedServiceHostHalfTheGuestDeclinesRunsNowhere(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	src := fetchedPackSource(t, map[string]string{"pack.json": `{"name": "acme", "contributes": [
		{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8299"},
		{"kind": "service", "name": "acme-svc", "endpoint": "acme-svc.endpoint",
		 "jail_daemon": {"cmd": ["acme-svc"]},
		 "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-svc"]}}]}`})
	writeUserConfigJSON(t, home, `{"packs": [{"name": "acme", "source": "`+src+`"}]}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if specs := payloadOf(t, got.jailDaemons); len(specs) != 0 {
		t.Fatalf("the guest was handed %+v, want nothing: the service publishes a container endpoint", specs)
	}
	const dial = "a container runtime runs it (`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container` for one launch)"
	var refusal, decline string
	for _, line := range strings.Split(got.out, "\n") {
		switch {
		case strings.Contains(line, `Not started outside the sandbox: the "acme-svc" service's host half (pack "acme")`):
			refusal = line
		case strings.Contains(line, "acme-svc: acme-svc — "):
			decline = line
		}
	}
	if refusal == "" || !strings.Contains(refusal, "declined in the sandbox too") ||
		strings.Contains(refusal, "runs in the sandbox instead") {
		t.Errorf("the refused host half is not said to leave its daemon running nowhere:\n%s", got.out)
	}
	if !strings.Contains(refusal, "nothing serves it this launch; "+dial) {
		t.Errorf("the refusal line names no next step:\n%s", refusal)
	}
	if !strings.Contains(decline, "/run/yolo-services/acme-svc.endpoint") || !strings.Contains(decline, dial) {
		t.Errorf("the Declined: line does not name the endpoint and the next step:\n%s", got.out)
	}
}

// A PURE WORKER RUNS IN THE GUEST: a held service that publishes no endpoint (packdecl's word for
// one) and serves no adaptation, so its host half has no pairing to serve on this backend and is
// not started, and its jail half runs confined, as a container runs it (JD-9: confinement
// preferred). Declared with both halves, as a worker may be.
func TestMacosUserRunsAPureWorkerInTheGuest(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalPackJSON(t, home, `{"contributes": [{"kind": "service", "name": "acme-worker",
		"jail_daemon": {"cmd": ["acme-worker", "--queue", "jobs"]},
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-worker"]}}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	starts := 0
	prev := startMacosUserService
	startMacosUserService = func(*launchservice.Plan, map[string]string) (launchedService, string, error) {
		starts++
		return nil, "", errors.New("a pure worker's host half was started")
	}
	t.Cleanup(func() { startMacosUserService = prev })

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || strings.Join(specs[0].Cmd, " ") != "acme-worker --queue jobs" {
		t.Fatalf("the guest was handed %+v, want the worker's jail half as declared\n%s", specs, got.out)
	}
	if starts != 0 {
		t.Errorf("the worker's host half was started %d times outside the sandbox", starts)
	}
	if strings.Contains(got.out, "Declined: these jail daemons") {
		t.Errorf("the guest declined the worker:\n%s", got.out)
	}
}

// A LOOPHOLE DAEMON NAMING ITS MODULE DIRECTORY RUNS FROM THE SANDBOX'S COPY OF THE STAGED PACKS
// (JD-10): `{jail_loophole_dir}` resolves to the module directory's place under
// macosuser.StagedPackRoot, which the orchestrator copies the launch's pack tree to. This is the
// hello-daemon shape. Deleting placeModuleDirsInGuest from jailDaemonsFor fails this: the guest is
// handed the container's mount point, which it does not have, and declines it.
func TestMacosUserRunsAModuleDirJailDaemonFromTheStagedCopy(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "hello", jailOnlyLoophole("hello", `["{jail_loophole_dir}/bin/hello", "--conf", "{jail_loophole_dir}/hello.conf"]`))
	writeLocalModuleFile(t, home, "hello", "bin/hello", []byte("#!/bin/sh\necho \"hello from $0\"\n"))
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	dir := filepath.Join(macosuser.StagedPackTreeRoot(runtime.FromWorkspace(ws), got.hostPackRoot, ""),
		"local", "loopholes", "hello")
	specs := payloadOf(t, got.jailDaemons)
	if len(specs) != 1 || len(specs[0].Cmd) != 3 || specs[0].Cmd[0] != dir+"/bin/hello" || specs[0].Cmd[2] != dir+"/hello.conf" {
		t.Fatalf("the guest was handed %+v, want the program at %s/bin/hello\n%s", specs, dir, got.out)
	}
	if strings.Contains(got.out, "Declined: these jail daemons") {
		t.Errorf("a module-dir daemon the guest runs was declined:\n%s", got.out)
	}
}

func TestMacosUserModuleDirPayloadUsesEachLaunchPackTree(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeUserConfigJSON(t, home, `{"packs": []}`)

	type launch struct {
		hostRoot  string
		plan      macosuser.RunPlan
		specs     []supervisor.Spec
		module    []byte
		moduleErr error
	}
	var launches []launch
	run := func(body []byte, pack bool) {
		t.Helper()
		if pack {
			writeLocalLoopholePack(t, home, "hello", jailOnlyLoophole("hello", ` ["{jail_loophole_dir}/bin/hello", "--conf", "{jail_loophole_dir}/hello.conf"]`))
			writeLocalModuleFile(t, home, "hello", "bin/hello", body)
		} else {
			writeLocalPackJSON(t, home, `{"contributes": []}`)
			if err := os.RemoveAll(filepath.Join(home, ".config", "yolo-jail", "local", "loopholes", "hello")); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.MacosUserRun = func(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string,
			_, hostRoot string, overlay macosuser.HomeOverlay, hostCtx macosuser.HostContext, _ bool,
			packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool, jd macosuser.JailDaemons) int {
			plan := macosuser.BuildRunPlan(workspace, cfg, agents, agentArgv, "/usr/local/bin/yolo",
				hostRoot, overlay, hostCtx, packEnv, nil, blocked)
			hostModule, moduleErr := os.ReadFile(filepath.Join(hostRoot, "local", "loopholes", "hello", "bin", "hello"))
			launches = append(launches, launch{hostRoot: hostRoot, plan: plan, specs: payloadOf(t, jd),
				module: hostModule, moduleErr: moduleErr})
			return 0
		}
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
		}
	}

	run([]byte("#!/bin/sh\necho A\n"), true)
	run([]byte("#!/bin/sh\necho B\n"), true)
	run(nil, false)
	if len(launches) != 3 {
		t.Fatalf("observed %d macos-user launches, want A/B/C", len(launches))
	}
	for i, launch := range launches {
		if i > 0 && launch.plan.PackRoot == launches[i-1].plan.PackRoot {
			t.Fatalf("launch %d reuses guest pack root %s", i+1, launch.plan.PackRoot)
		}
		if launch.plan.PackRoot == "" || filepath.Base(launch.plan.PackRoot) == runtime.FromWorkspace(ws) {
			t.Errorf("launch %d did not derive its root from its host tree %s: %s", i+1,
				launch.hostRoot, launch.plan.PackRoot)
		}
	}
	for i, body := range [][]byte{[]byte("#!/bin/sh\necho A\n"), []byte("#!/bin/sh\necho B\n")} {
		launch := launches[i]
		if len(launch.specs) != 1 || len(launch.specs[0].Cmd) != 3 {
			t.Fatalf("launch %d payload = %+v, want the module-dir daemon", i+1, launch.specs)
		}
		want := filepath.Join(launch.plan.PackRoot, "local", "loopholes", "hello", "bin", "hello")
		if launch.specs[0].Cmd[0] != want || launch.specs[0].Cmd[2] != filepath.Join(launch.plan.PackRoot,
			"local", "loopholes", "hello", "hello.conf") {
			t.Errorf("launch %d payload = %v, plan root %s", i+1, launch.specs[0].Cmd, launch.plan.PackRoot)
		}
		if launch.moduleErr != nil || !bytes.Equal(launch.module, body) {
			t.Errorf("launch %d host tree has module bytes %q, want %q (err %v)", i+1,
				launch.module, body, launch.moduleErr)
		}
	}
	if len(launches[2].specs) != 0 {
		t.Errorf("launch C kept a dropped module daemon in its payload: %+v", launches[2].specs)
	}
	if !os.IsNotExist(launches[2].moduleErr) {
		t.Errorf("launch C host tree retained the dropped module (err %v)", launches[2].moduleErr)
	}
	firstTarget := launches[0].specs[0].Cmd[0]
	if firstTarget != filepath.Join(launches[0].plan.PackRoot, "local", "loopholes", "hello", "bin", "hello") {
		t.Errorf("launch A's stored restart target changed after B/C: %s", firstTarget)
	}
}

// A MODULE DIRECTORY OUTSIDE THE LAUNCH'S STAGED TREE has no copy in the sandbox, so its daemon
// keeps the container path and loses its ModuleDir, and the guest declines it by name rather than
// running a path nothing copied. Inside it, through a symlinked spelling of the root included (a
// darwin temp dir is reached through /var, a link to /private/var), it is placed.
func TestAModuleDirOutsideTheStagedPacksIsDeclinedNotPlaced(t *testing.T) {
	base := floortest.ResolvedTemp(t)
	tree := filepath.Join(base, "tree")
	inside := filepath.Join(tree, "local", "loopholes", "hello")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(tree, link); err != nil {
		t.Fatal(err)
	}
	spec := func(name, dir string) loopholes.JailDaemonSpec {
		return loopholes.JailDaemonSpec{Name: name, Cmd: []string{loopholedecl.JailLoopholeDir(name) + "/bin/hello"},
			ModuleDir: dir, ModuleCmd: []string{loopholedecl.TokenJailLoopholeDir + "/bin/hello"}}
	}
	o := &Options{Workspace: base, packTree: link}
	out := o.placeModuleDirsInGuest("macos-user", []loopholes.JailDaemonSpec{
		spec("hello", inside), spec("stray", filepath.Join(base, "elsewhere", "stray"))})
	want := filepath.Join(macosuser.StagedPackTreeRoot(runtime.FromWorkspace(base), link, ""),
		"local", "loopholes", "hello", "bin", "hello")
	if out[0].Cmd[0] != want {
		t.Errorf("the staged module dir is placed at %q, want %q", out[0].Cmd[0], want)
	}
	if out[1].ModuleDir != "" || out[1].Cmd[0] != loopholedecl.JailLoopholeDir("stray")+"/bin/hello" {
		t.Errorf("a module dir outside the staged tree was placed: %+v", out[1])
	}
	runs, declined := loopholes.JailDaemonsRunIn("macos-user", out)
	if len(runs) != 1 || runs[0].Name != "hello" || len(declined) != 1 || declined[0].Spec.Name != "stray" {
		t.Errorf("the guest runs %+v and declines %+v, want hello run and stray declined", runs, declined)
	}
	if same := o.placeModuleDirsInGuest("podman", out[:1]); same[0].Cmd[0] != out[0].Cmd[0] {
		t.Errorf("a container launch's specs were placed again: %v", same[0].Cmd)
	}
	if kept := o.placeModuleDirsInGuest("podman", []loopholes.JailDaemonSpec{spec("hello", inside)}); kept[0].Cmd[0] != loopholedecl.JailLoopholeDir("hello")+"/bin/hello" {
		t.Errorf("a container launch's module dir was moved off the mount point: %v", kept[0].Cmd)
	}
}

// linuxProgram is the start of an ELF image, which the macos-user guest declines to run.
var linuxProgram = []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}

// writeLocalModuleFile writes rel, mode 0755, into the local pack's loophole module directory.
func writeLocalModuleFile(t *testing.T, home, loophole, rel string, body []byte) {
	t.Helper()
	path := filepath.Join(home, ".config", "yolo-jail", "local", "loopholes", loophole, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
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
