package run

// macosuserlocalpacks_test.go pins three things the macos-user arm does with host code it runs
// outside the sandbox (docs/design/host-notch-services.md):
//
//   - HS-D27: a LOCAL pack's doorway and service host half run, as a pack yolo ships does, and are
//     named, argv and all, before they start; a FETCHED pack's do not (fetchedPackSource makes
//     one, since a local pack is no longer the refused case);
//   - HS-D28: every service and doorway the arm starts is supervised from its start, so one that
//     dies while the sandboxed command runs is named and comes back on its address;
//   - HS-D29: a pure worker that declares only a host half runs beside the command.
//
// Driven through Run's macos-user arm, MacosUserRun stubbed, so deleting a call from the arm or
// from the helpers it calls fails a test here.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// fetchedPackSource makes a GIT pack (git+file://, so genuinely fetched, with no network) of
// files (pack-relative path → body, written 0755 so a module program is executable), puts it in
// this HOME's pack store as `yolo pack install` would, and returns its source. Call it after the
// HOME is set (packHome). YOLO_PACK_ROOT is cleared, so a jail's staged tree cannot answer for it.
func fetchedPackSource(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "pack"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	src := "git+file://" + repo + "?ref=main"
	syncPackStore(t, src)
	return src
}

// acmeDoorwayManifest is a loophole whose jail daemon is a doorway: it declares the host argv
// that opens it outside a sandbox (`jail_daemon.host_cmd`).
const acmeDoorwayManifest = `{"name": "acme-proxy",
	"description": "acme proxy", "default_enabled": true, "transport": "loopback-tls",
	"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
	"listen": "127.0.0.1:1999", "caller_token": true,
	"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`

// A LOCAL PACK'S DOORWAY OPENS OUTSIDE THE SANDBOX, and its argv is named before it does
// (HS-D27): the conventional local pack is the user's own, so its `host_cmd` runs as a pack yolo
// ships would, at the address the guest's copy would have taken, and the guest is not handed the
// jail daemon too. Before this the launch refused it and ran it in the guest.
func TestMacosUserOpensALocalPacksDoorwayOutsideTheSandbox(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholePack(t, home, "acme-proxy", acmeDoorwayManifest)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	plan, _ := doors.only(t, "acme-proxy")
	if !plan.Local || plan.Pack != "local" || len(plan.Addresses()) != 1 {
		t.Fatalf("the doorway's plan is %+v, want the local pack's, at one address", plan.Declared)
	}
	argv := "yolo internal daemon acme-adapter --listen " + plan.Addresses()[0]
	if strings.Join(plan.Cmd, " ") != argv {
		t.Errorf("the doorway runs %q, want %q", strings.Join(plan.Cmd, " "), argv)
	}
	named := strings.Index(got.out, `This launch runs pack code on your machine, outside the sandbox: the `+
		`"acme-proxy" doorway's host argv from pack "local", a local pack yolo does not ship: `+argv)
	opened := strings.Index(got.out, `Opened the "acme-proxy" doorway (pack "local"`)
	if named < 0 || opened < 0 || named > opened {
		t.Errorf("the local doorway's argv must be named before it opens (named %d, opened %d):\n%s",
			named, opened, got.out)
	}
	for _, s := range payloadOf(t, got.jailDaemons) {
		if s.Name == "acme-proxy" {
			t.Errorf("the guest was handed the doorway it opened outside: %+v", s)
		}
	}
	if strings.Contains(got.out, `Not opened outside the sandbox: the "acme-proxy"`) {
		t.Errorf("a local pack's doorway was refused:\n%s", got.out)
	}
}

// supervisedFake is a started launch-owned service that records how the arm supervises it, and
// reports a death the way launchservice.Running.Supervise does: one line, to the writer the arm
// handed it, naming the command it was given, unless the arm has stopped it.
type supervisedFake struct {
	mu      sync.Mutex
	stopped bool
	agent   string
	prefix  string
	w       io.Writer
}

func (f *supervisedFake) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func (f *supervisedFake) PID() int { return 4343 }

func (f *supervisedFake) Supervise(agent string, w io.Writer, prefix string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agent, f.w, f.prefix = agent, w, prefix
}

func (f *supervisedFake) die(service string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || f.w == nil {
		return
	}
	fmt.Fprintf(f.w, "%sthe %q service died while %s runs\n", f.prefix, service, f.agent)
}

// THE ARM SUPERVISES EVERY SERVICE AND DOORWAY IT STARTS, from its start, on its own stderr, naming
// the command: claude on cerebras runs through the bridge's host half, and codex's refresh pointer
// opens the OpenAI doorway, and a death of either while the command runs is reported where the
// launch's lines go. Deleting the Supervise call from startMacosUserServices or from
// startMacosUserDoorways fails this.
func TestTheMacosUserArmSupervisesEachServiceAndDoorwayItStarts(t *testing.T) {
	o, stderr, _ := overrideNativeLaunch(t, `{"packs": ["claude", "cerebras", "codex"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "csk-test"}]}`, shellWith(nil))
	o.ProfileName = "cerebras"
	fakes := map[string]*supervisedFake{}
	record := func(p *launchservice.Plan) (launchedService, string, error) {
		f := &supervisedFake{}
		fakes[p.Service] = f
		return f, "/log/launch-service-" + p.Service + ".log", nil
	}
	origService, origDoorway := startMacosUserService, startMacosUserDoorway
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		return record(p)
	}
	startMacosUserDoorway = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		return record(p)
	}
	t.Cleanup(func() { startMacosUserService, startMacosUserDoorway = origService, origDoorway })
	run := o.MacosUserRun
	o.MacosUserRun = func(cfg *jsonx.OrderedMap, ws string, a, b []string, c, d string, h macosuser.HomeOverlay,
		ctx macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
		for name, f := range fakes {
			f.die(name)
		}
		return run(cfg, ws, a, b, c, d, h, ctx, dry, env, bt, jd)
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	for _, name := range []string{"wire-bridge", "openai-auth-broker"} {
		if fakes[name] == nil {
			t.Fatalf("the arm did not start %q: the premise is gone\n%s", name, stderr.String())
		}
		if want := `yolo: the "` + name + `" service died while claude runs`; !strings.Contains(stderr.String(), want) {
			t.Errorf("a death of %q while the command ran was not reported as %q:\n%s", name, want, stderr.String())
		}
	}
}

// THE REAL DOORWAY COMES BACK ON ITS ADDRESS WHEN IT IS KILLED WHILE THE COMMAND RUNS (HS-D28), on
// this machine's loopback: the start is not stubbed, the doorway's process is SIGKILLed from inside
// the stubbed sandbox, and a refresh codex makes after that, at the address it was pointed at, is
// answered by the restarted doorway (its own 401 for a marker without the caller token) rather
// than refused. The launch says it died and that it is back.
func TestTheRealCodexDoorwayComesBackOnItsAddressWhenKilled(t *testing.T) {
	origBackoff := launchservice.RestartBackoff
	launchservice.RestartBackoff = 50 * time.Millisecond
	t.Cleanup(func() { launchservice.RestartBackoff = origBackoff })
	o, _, _ := codexNativeLaunch(t)
	stderr := &lockedBuffer{}
	o.Stderr = stderr
	var pid int
	orig := startMacosUserDoorway
	startMacosUserDoorway = func(p *launchservice.Plan, env map[string]string) (launchedService, string, error) {
		r, log, err := orig(p, env)
		if r != nil && p.Service == "openai-auth-broker" {
			pid = r.PID()
		}
		return r, log, err
	}
	t.Cleanup(func() { startMacosUserDoorway = orig })
	var before, after int
	var url string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, env *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		u, _ := env.Get("CODEX_REFRESH_TOKEN_URL_OVERRIDE")
		url, _ = u.(string)
		before = postRefresh(t, url, "yolo-broker:1")
		if pid == 0 {
			return 0
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		// Gone and reaped, so the next request is the restarted process's to answer.
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		after = postRefresh(t, url, "yolo-broker:1")
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, stderr.String())
	}
	if pid == 0 || before != 401 {
		t.Fatalf("the doorway did not serve before it was killed (pid %d, got %d)\n%s", pid, before, stderr.String())
	}
	if after != 401 {
		t.Errorf("a refresh at %s after the doorway was killed got %d, want the restarted doorway's 401", url, after)
	}
	for _, want := range []string{`yolo: the "openai-auth-broker" service (pack "openai-auth") exited ` +
		`(killed by SIGKILL) while codex runs; restarting it in 50ms on the same address (`,
		`yolo: the "openai-auth-broker" service is back (pid `} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch must say %q:\n%s", want, stderr.String())
		}
	}
}

// A PURE WORKER WITH ONLY A HOST HALF RUNS BESIDE THE COMMAND (HS-D29): nothing else would run it
// on this backend, so the arm starts its host half outside the sandbox as a launch-owned service
// with no address, names it (a local pack's argv first), and stops it after the command; a dry run
// says it would. Deleting the planMacosUserWorkers call fails this.
func TestMacosUserStartsAHostOnlyWorkerOutsideTheSandbox(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalPackJSON(t, home, `{"contributes": [{"kind": "service", "name": "acme-worker",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-worker"]}}]}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	var started []*launchservice.Plan
	stopped := 0
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		started = append(started, p)
		return fakeLaunched{&stopped}, "/log/launch-service-" + p.Service + ".log", nil
	}
	t.Cleanup(func() { startMacosUserService = orig })

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if len(started) != 1 || started[0].Service != "acme-worker" || len(started[0].Moved) != 0 ||
		strings.Join(started[0].Cmd, " ") != "yolo internal daemon acme-worker" {
		t.Fatalf("started %+v, want the acme-worker's host half with no address\n%s", started, got.out)
	}
	if stopped != 1 {
		t.Errorf("the worker was stopped %d times, want once, after the command", stopped)
	}
	named := strings.Index(got.out, `This launch runs pack code on your machine, outside the sandbox: the `+
		`"acme-worker" service's host half from pack "local", a local pack yolo does not ship: yolo internal daemon acme-worker`)
	line := strings.Index(got.out, `Started the "acme-worker" service (pack "local", pid 4242) for this launch, `+
		`outside the sandbox: a worker no agent is pointed at`)
	if named < 0 || line < 0 || named > line {
		t.Errorf("the worker must be named before it starts and its start said (named %d, started %d):\n%s",
			named, line, got.out)
	}
	if specs := payloadOf(t, got.jailDaemons); len(specs) != 0 {
		t.Errorf("the guest was handed %+v for a worker with no jail daemon", specs)
	}

	var dryOut, dryErr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &dryOut, &dryErr, nil)
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		return 0
	}
	o.DryRun = true
	started = nil
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d\n%s", rc, dryErr.String())
	}
	if len(started) != 0 {
		t.Errorf("a dry run started %d workers", len(started))
	}
	if want := `Would start the "acme-worker" service (pack "local") on [no address: a worker no agent is ` +
		`pointed at] for this launch`; !strings.Contains(dryErr.String(), want) {
		t.Errorf("the dry run must say %q:\n%s", want, dryErr.String())
	}
}

// A FETCHED PACK'S HOST-ONLY WORKER DOES NOT RUN (OQ-HS4), and since it declares no jail daemon
// nothing runs it this launch, which the line says with the refusal's next step.
func TestMacosUserNamesAFetchedHostOnlyWorkerItDoesNotStart(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	src := fetchedPackSource(t, map[string]string{"pack.json": `{"name": "acme", "contributes": [
		{"kind": "service", "name": "acme-worker", "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-worker"]}}]}`})
	writeUserConfigJSON(t, home, `{"packs": [{"name": "acme", "source": "`+src+`"}]}`)
	orig := startMacosUserService
	startMacosUserService = func(p *launchservice.Plan, _ map[string]string) (launchedService, string, error) {
		t.Errorf("a fetched pack's worker was started: %v", p.Cmd)
		return nil, "", errors.New("no")
	}
	t.Cleanup(func() { startMacosUserService = orig })

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	for _, want := range []string{`Not started outside the sandbox: the "acme-worker" service's host half ` +
		`(pack "acme"): its pack was fetched`, "select a local checkout of the pack by its file:// path",
		"It declares no jail daemon, so nothing runs it this launch."} {
		if !strings.Contains(got.out, want) {
			t.Errorf("the launch must say %q:\n%s", want, got.out)
		}
	}
}
