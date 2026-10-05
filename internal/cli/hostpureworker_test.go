package cli

// hostpureworker_test.go pins the host notch's PURE WORKERS (hostPureWorkers;
// docs/design/host-notch-services.md §1.2 and HS-D29): a held pack service no adaptation names,
// so no agent's pairing starts it. `yolo host -- <command>` starts each one that declares a host
// half, admission admits and the selection's gate asks for, as a launch-owned child beside the
// command, and stops it when the command exits; every worker it does not start is named.
//
// Every cell runs `yolo host` through hostMain. The worker's host half is a shell stand-in
// (workerStandIn) in place of `yolo`, doing what a host half does at its start: it reads its
// input file, answers `ready <service>` on the readiness descriptor, and lives until its launch's
// lifeline ends or it is signalled. The command is a script on PATH that looks for the worker.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workerManifest is a pack whose one contribution is a host-only worker named service.
func workerManifest(service string) string {
	return `{"contributes": [{"kind": "service", "name": "` + service + `",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "` + service + `"]}}]}`
}

// workerStandIn stands a shell in for every host half this test starts (launchservice.SelfExec):
// it records its pid, its input and the listen descriptors it was handed under dir, removes the
// input as a host half does, answers `ready <service>` (the argv's last word), and reads its
// lifeline until the launch closes it.
func workerStandIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `svc="$1"; out="$2"; echo $$ > "$out/pid.$svc"; cat "$YOLO_HOST_SERVICE_INPUT" > "$out/input.$svc"; ` +
		`rm -f "$YOLO_HOST_SERVICE_INPUT"; printf '%s' "$YOLO_HOST_SERVICE_LISTEN_FDS" > "$out/fds.$svc"; ` +
		`printf 'ready %s\n' "$svc" >&3; exec 3>&-; exec cat <&4 >/dev/null`
	orig := launchservice.SelfExec
	launchservice.SelfExec = func(argv []string) []string {
		return []string{"/bin/sh", "-c", script, "sh", argv[len(argv)-1], dir}
	}
	t.Cleanup(func() { launchservice.SelfExec = orig })
	return dir
}

// workerLaunch is one `yolo host <flags> -- tool` over the HOME hostGateHome made, tool being a
// script that records whether the worker named service was alive when it ran, then runs body.
type workerLaunch struct {
	rc      int
	errs    string
	started []*launchservice.Running
	execed  bool
	alive   bool
}

func runWorkerLaunch(t *testing.T, dir, service, body string, signals chan os.Signal, args ...string) workerLaunch {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nkill -0 \"$(cat '" + dir + "/pid." + service + "' 2>/dev/null)\" 2>/dev/null && " +
		"echo alive > '" + dir + "/agent'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "tool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var got workerLaunch
	origStart := startLaunchService
	startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
		r, err := origStart(p, env)
		if r != nil {
			got.started = append(got.started, r)
		}
		return r, err
	}
	t.Cleanup(func() { startLaunchService = origStart })
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { got.execed = true; return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })
	origSignals := hostServiceSignals
	hostServiceSignals = signals
	t.Cleanup(func() { hostServiceSignals = origSignals })
	var out, errw bytes.Buffer
	got.rc = hostMain(append(append([]string{}, args...), "--", "tool"), &out, &errw, false, nil)
	got.errs = errw.String()
	data, _ := os.ReadFile(filepath.Join(dir, "agent"))
	got.alive = strings.TrimSpace(string(data)) == "alive"
	return got
}

// officialWorkerHome is a host HOME selecting `worker`, a pack yolo "ships": the embedded set is
// swapped for one holding it alone, after hostGateHome has read the shipped providers it blanks.
func officialWorkerHome(t *testing.T) {
	t.Helper()
	hostGateHome(t, `{"packs": ["worker"]}`, nil)
	withEmbeddedFS(t, fstest.MapFS{"worker/pack.json": {Data: []byte(workerManifest("worker"))}})
}

// assertWorkerGone checks that every started worker had exited when the launch returned.
func assertWorkerGone(t *testing.T, l workerLaunch, dir string) {
	t.Helper()
	for _, r := range l.started {
		select {
		case <-r.Done():
		default:
			t.Errorf("the %q worker (pid %d) outlived its launch", r.Plan.Service, r.PID())
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pid.worker"))
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && syscall.Kill(pid, 0) == nil {
		t.Errorf("the worker's process %d is still running after the launch", pid)
	}
}

// A PURE WORKER STARTS BEFORE THE COMMAND AND STOPS AFTER IT: `yolo host -- tool` over a pack
// yolo ships that declares one host-only worker stays resident, starts the worker's host half
// first (the command finds it running), reserves it no port (no address of it is composed), hands
// it its caller token and the wire tables, says so, and the worker is gone once the command
// exits. Deleting the hostPureWorkers call from the host composition fails this.
func TestHostStartsAPureWorkerBesideTheCommandAndStopsItAfter(t *testing.T) {
	officialWorkerHome(t)
	dir := workerStandIn(t)
	l := runWorkerLaunch(t, dir, "worker", "exit 0", nil)
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	if len(l.started) != 1 || l.started[0].Plan.Service != "worker" {
		t.Fatalf("started %d services, want the worker\n%s", len(l.started), l.errs)
	}
	if !l.alive {
		t.Errorf("the command did not find the worker running: it must start before the command\n%s", l.errs)
	}
	if l.execed {
		t.Error("a launch with a worker exec'd its command, so nothing was left to stop the worker")
	}
	if p := l.started[0].Plan; len(p.Moved) != 0 {
		t.Errorf("the worker's plan moved %v: a worker is composed into no agent, so it has no address", p.Moved)
	}
	if fds, _ := os.ReadFile(filepath.Join(dir, "fds.worker")); len(fds) != 0 {
		t.Errorf("the worker was handed reserved sockets %q, want none", fds)
	}
	var in launchservice.Input
	data, _ := os.ReadFile(filepath.Join(dir, "input.worker"))
	if err := json.Unmarshal(data, &in); err != nil || in.Service != "worker" ||
		in.Env[paths.ServiceCallerTokenEnv("worker")] == "" || in.Env["YOLO_PROVIDERS"] == "" {
		t.Errorf("the worker's input = %s (%v), want its service, its caller token and the wire tables", data, err)
	}
	if want := `yolo host: started the "worker" service (pack "worker", pid ` +
		strconv.Itoa(l.started[0].PID()) + `) for tool, a worker no agent is pointed at`; !strings.Contains(l.errs, want) {
		t.Errorf("the launch must say %q:\n%s", want, l.errs)
	}
	if strings.Contains(l.errs, "this launch runs pack code on your machine") {
		t.Errorf("a worker of a pack yolo ships was disclosed as a local pack's:\n%s", l.errs)
	}
	assertWorkerGone(t, l, dir)
}

// A COMMAND KILLED BY A SIGNAL STILL TAKES ITS WORKER WITH IT: the SIGTERM the launch receives is
// forwarded to the command, which dies of it, and the worker is stopped.
func TestHostStopsAPureWorkerWhenTheCommandDiesOfASignal(t *testing.T) {
	officialWorkerHome(t)
	dir := workerStandIn(t)
	signals := make(chan os.Signal, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "agent")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		signals <- syscall.SIGTERM
	}()
	l := runWorkerLaunch(t, dir, "worker", "exec sleep 30", signals)
	if l.rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want %d: the command died of the forwarded SIGTERM\n%s", l.rc, 128+int(syscall.SIGTERM), l.errs)
	}
	if !l.alive || len(l.started) != 1 {
		t.Fatalf("the worker was not running beside the command (alive %v, started %d)\n%s", l.alive, len(l.started), l.errs)
	}
	assertWorkerGone(t, l, dir)
}

// `yolo host env` RUNS NO COMMAND, SO IT STARTS NO WORKER, and says which launch does (OQ-HS3).
func TestHostEnvNamesAPureWorkerItDoesNotStart(t *testing.T) {
	officialWorkerHome(t)
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("yolo host env started a worker")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "tool"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env rc = %d\n%s", rc, errw.String())
	}
	if want := `the "worker" service (pack "worker") is a worker no agent's route names: ` +
		"`yolo host -- <command>` starts its host half"; !strings.Contains(errw.String(), want) {
		t.Errorf("yolo host env must say %q:\n%s", want, errw.String())
	}
}

// A LOCAL PACK'S WORKER RUNS, AND ITS ARGV IS NAMED BEFORE IT DOES (HS-D27): the user's own code,
// run on their machine outside every sandbox, is disclosed as such ahead of the start line.
func TestHostStartsALocalPacksWorkerAndNamesItFirst(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), workerManifest("acme-worker"))
	hostGateHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "acme"}]}`, nil)
	dir := workerStandIn(t)
	l := runWorkerLaunch(t, dir, "acme-worker", "exit 0", nil)
	if l.rc != 0 || len(l.started) != 1 || !l.started[0].Plan.Local {
		t.Fatalf("rc = %d, started %d: want the local pack's worker started\n%s", l.rc, len(l.started), l.errs)
	}
	named := strings.Index(l.errs, `yolo host: this launch runs pack code on your machine: the "acme-worker" `+
		`service's host half from pack "acme", a local pack yolo does not ship: yolo internal daemon acme-worker`)
	started := strings.Index(l.errs, `yolo host: started the "acme-worker" service`)
	if named < 0 || started < 0 || named > started {
		t.Errorf("the local worker's argv must be named before its start line (named %d, started %d):\n%s",
			named, started, l.errs)
	}
}

// A FETCHED PACK'S WORKER DOES NOT RUN (OQ-HS4), and the launch names it with the next step, then
// runs the command as it would with no worker at all.
func TestHostNamesAFetchedPacksWorkerItDoesNotStart(t *testing.T) {
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := gitPackRepoWith(t, map[string]string{"pack.json": workerManifest("acme-worker")})
	hostGateHome(t, `{"packs": [{"source": "git+file://`+repo+`?ref=main", "name": "gp"}]}`, nil)
	dir := workerStandIn(t)
	l := runWorkerLaunch(t, dir, "acme-worker", "exit 0", nil)
	if l.rc != 0 || len(l.started) != 0 || !l.execed {
		t.Fatalf("rc = %d, started %d, exec'd %v: want the command exec'd with no worker\n%s",
			l.rc, len(l.started), l.execed, l.errs)
	}
	for _, want := range []string{`yolo host: the "acme-worker" service's host half (pack "gp") is not started: ` +
		"its pack was fetched", "select a local checkout of the pack by its file:// path"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, l.errs)
		}
	}
}

// A WORKER THAT DECLARES ONLY A JAIL DAEMON RUNS ONLY IN A JAIL, and the host says so instead of
// nothing; one whose every pointer is gated on a profile this launch does not select is not
// started, naming the profile that starts it.
func TestHostNamesAJailOnlyWorkerAndAnUngatedOne(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"contributes": [
		{"kind": "service", "name": "acme-jailer", "jail_daemon": {"cmd": ["acme-jailer"]}},
		{"kind": "service", "name": "acme-gated", "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-gated"]}},
		{"kind": "env", "profile": "gatedp", "served_by": "acme-gated", "vars": {"ACME_GATED": "1"}}]}`)
	hostGateHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "acme"}]}`, nil)
	dir := workerStandIn(t)
	l := runWorkerLaunch(t, dir, "acme-gated", "exit 0", nil)
	if l.rc != 0 || len(l.started) != 0 {
		t.Fatalf("rc = %d, started %d: neither worker may start\n%s", l.rc, len(l.started), l.errs)
	}
	for _, want := range []string{
		`yolo host: the "acme-jailer" service (pack "acme") runs only in a jail: it declares no host half`,
		`the "acme-gated" service (pack "acme") is not started: every pointer at it is gated, and its ` +
			`selected profile is not "gatedp"; ` + "`-p gatedp` starts it",
	} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, l.errs)
		}
	}
}

// `yolo host apply` WRITES FILES, WHICH HOLD NO PROCESS, so it starts no worker and names each
// one with the launch that does (OQ-HS3), as `yolo host env` does.
func TestHostApplyNamesAPureWorkerItDoesNotStart(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), workerManifest("acme-worker"))
	hostComputedHome(t, `{"packs": ["claude", {"source": "file://`+pack+`", "name": "acme"}]}`)
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("yolo host apply started a worker")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if want := `the "acme-worker" service (pack "acme") is a worker no agent's route names`; !strings.Contains(out.String()+errw.String(), want) {
		t.Errorf("the apply must say %q:\n%s%s", want, out.String(), errw.String())
	}
}
