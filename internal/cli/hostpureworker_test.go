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
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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
	return runWorkerLaunchAs(t, dir, service, "tool", body, signals, args...)
}

// runWorkerLaunchAs is runWorkerLaunch with the command named agent, for a launch whose selection
// is that agent's: `yolo host <flags> -- <agent>`.
func runWorkerLaunchAs(t *testing.T, dir, service, agent, body string, signals chan os.Signal,
	args ...string) workerLaunch {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nkill -0 \"$(cat '" + dir + "/pid." + service + "' 2>/dev/null)\" 2>/dev/null && " +
		"echo alive > '" + dir + "/agent'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, agent), []byte(script), 0o755); err != nil {
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
	got.rc = hostMain(append(append([]string{}, args...), "--", agent), &out, &errw, false, nil)
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
// one with the launch that does (OQ-HS3), as `yolo host env` does. The config declares
// `host_management: "own"`: under the unset key (`none` since OQ-CO14) the apply refuses first.
func TestHostApplyNamesAPureWorkerItDoesNotStart(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), workerManifest("acme-worker"))
	hostComputedHome(t, `{"packs": ["claude", {"source": "file://`+pack+`", "name": "acme"}], "host_management": "own"}`)
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

// pointedWorkerPack is a local pack whose host-only worker acme-worker two pack env variables point
// at: ACME_WORKER_URL, ungated, and ACME_WORKER_TOKEN, the worker's caller token, gated on the
// pack's own profile `p`, since a `{caller_token}` pointer must be gated (OQ-CN7). claude is the
// pack's program, so `-p p -- claude` is a selection the launch resolves.
func pointedWorkerPack(t *testing.T) string {
	t.Helper()
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name": "acme", "contributes": [
		{"kind": "program", "bin": "claude", "via": "npm", "package": "@acme/claude"},
		{"kind": "provider", "name": "p"},
		{"kind": "profile", "name": "p", "provider": "p"},
		{"kind": "service", "name": "acme-worker", "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-worker"]}},
		{"kind": "env", "served_by": "acme-worker", "vars": {"ACME_WORKER_URL": "http://127.0.0.1:1/x"}},
		{"kind": "env", "profile": "p", "served_by": "acme-worker", "vars": {"ACME_WORKER_TOKEN": "{caller_token}"}}]}`)
	return pack
}

// A POINTER AT A WORKER THIS LAUNCH STARTS REACHES THE COMMAND (HS-D29), as a jail composes one at
// the worker's jail daemon: the worker is served at this notch, so ACME_WORKER_URL is set as the
// pack declares it and ACME_WORKER_TOKEN is the caller token the worker was handed, no line names
// either as withheld, and the start line names the pointers rather than calling the worker one no
// agent is pointed at. Planning the workers after the credential gate, or leaving them out of its
// served set or its caller tokens, fails this. TestHostApplyCountsAPointerAtAWorkerAsDeliveredAtLaunch
// is the apply's half: it reports the same pointer as delivered at launch.
func TestHostPointsTheCommandAtAWorkerItStarts(t *testing.T) {
	pack := pointedWorkerPack(t)
	hostGateHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "acme"}]}`, nil)
	dir := workerStandIn(t)
	l := runWorkerLaunchAs(t, dir, "acme-worker", "claude",
		`printf '%s\n%s\n' "$ACME_WORKER_URL" "$ACME_WORKER_TOKEN" > '`+dir+`/pointers'`, nil, "-p", "p")
	if l.rc != 0 || len(l.started) != 1 || l.started[0].Plan.Service != "acme-worker" {
		t.Fatalf("rc = %d, started %d: want the worker started\n%s", l.rc, len(l.started), l.errs)
	}
	var in launchservice.Input
	data, _ := os.ReadFile(filepath.Join(dir, "input.acme-worker"))
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatalf("the worker's input %q: %v", data, err)
	}
	token := in.Env[paths.ServiceCallerTokenEnv("acme-worker")]
	got, _ := os.ReadFile(filepath.Join(dir, "pointers"))
	if want := "http://127.0.0.1:1/x\n" + token + "\n"; token == "" || string(got) != want {
		t.Errorf("the command was handed %q, want the pointer and the worker's caller token %q\n%s", got, want, l.errs)
	}
	for _, withheld := range []string{"ACME_WORKER_URL —", "ACME_WORKER_TOKEN —"} {
		if strings.Contains(l.errs, withheld) {
			t.Errorf("the launch named a pointer at the worker it starts as withheld (%q):\n%s", withheld, l.errs)
		}
	}
	if want := `started the "acme-worker" service (pack "acme", pid ` + strconv.Itoa(l.started[0].PID()) +
		`) for claude, a worker claude is pointed at through ACME_WORKER_TOKEN, ACME_WORKER_URL;`; !strings.Contains(l.errs, want) {
		t.Errorf("the start line must say %q:\n%s", want, l.errs)
	}
	if strings.Contains(l.errs, "a worker no agent is pointed at") {
		t.Errorf("the start line calls a worker claude is pointed at one no agent is pointed at:\n%s", l.errs)
	}
}

// `yolo host env` STARTS NO WORKER, SO IT SETS NO POINTER AT ONE, and the line naming the pointers
// says which launch starts the worker they point at (OQ-HS3), as a doorway's pointer is named, never
// with the doorway clause that no selection opens it.
func TestHostEnvSaysWhichLaunchStartsTheWorkerAPointerNames(t *testing.T) {
	pack := pointedWorkerPack(t)
	hostGateHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "acme"}]}`, nil)
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("yolo host env started a worker")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "p"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env rc = %d\n%s", rc, errw.String())
	}
	if strings.Contains(out.String(), "ACME_WORKER") {
		t.Errorf("yolo host env exported a pointer at a worker it does not start:\n%s", out.String())
	}
	if want := `ACME_WORKER_TOKEN, ACME_WORKER_URL — points at the "acme-worker" jail daemon, which only a ` +
		"launch that runs a command starts, beside that command, and this command runs none: " +
		"`yolo host -- <command>` starts it"; !strings.Contains(errw.String(), want) {
		t.Errorf("yolo host env must say %q:\n%s", want, errw.String())
	}
	if strings.Contains(errw.String(), "opens for no selection") {
		t.Errorf("a pointer at a worker was given the doorway clause:\n%s", errw.String())
	}
}

// THE APPLY'S HALF OF TestHostPointsTheCommandAtAWorkerItStarts: `yolo host apply` reports the
// worker and the pointer at it as delivered at launch (`yolo host --`) and as nothing that does not
// apply at the host, which is what that launch now does with them.
func TestHostApplyCountsAPointerAtAWorkerAsDeliveredAtLaunch(t *testing.T) {
	pack := pointedWorkerPack(t)
	hostComputedHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "acme"}]}`)
	defaultReport(t)
	_, report := surveyApply(t)
	var line string
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, atLaunchClause) {
			line = l
		}
	}
	clauses := notchClauses(line)
	for _, k := range []packdecl.Kind{packdecl.KindService, packdecl.KindEnv} {
		if countWord(clauses[atLaunchClause], string(k)) != 1 || countWord(clauses[doesNotApplyClause], string(k)) != 0 {
			t.Errorf("%s must be named once, delivered at launch, and not as not applying: %q\n%s", k, line, report)
		}
	}
}

// A WORKER IS HANDED NO PROVIDER CREDENTIAL, WHILE THE BRIDGE BESIDE IT IS HANDED ITS PAIRING'S
// (HS-D29): claude on cerebras pairs through the wire bridge, whose host half is given the
// CEREBRAS_API_KEY the gate delivers to claude for that provider (serviceInput), and the worker
// started beside it gets the wire tables and its caller token and no key (workerInput), since no
// pairing names a credential it serves. Handing the worker the bridge's input fails this.
func TestHostHandsAWorkerNoProviderCredentialTheBridgeGets(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), workerManifest("acme-worker"))
	hostGateHome(t, `{"packs": ["claude", "cerebras", {"source": "file://`+pack+`", "name": "acme"}], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`, wcShell(nil))
	dir := workerStandIn(t)
	l := runWorkerLaunchAs(t, dir, "acme-worker", "claude", "exit 0", nil, "-p", "cerebras")
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	var names []string
	for _, r := range l.started {
		names = append(names, r.Plan.Service)
	}
	if strings.Join(names, ",") != "wire-bridge,acme-worker" {
		t.Fatalf("started %v, want the bridge and then the worker\n%s", names, l.errs)
	}
	input := func(service string) launchservice.Input {
		var in launchservice.Input
		data, _ := os.ReadFile(filepath.Join(dir, "input."+service))
		if err := json.Unmarshal(data, &in); err != nil {
			t.Fatalf("the %s input %q: %v", service, data, err)
		}
		return in
	}
	if got := input("wire-bridge").Env["CEREBRAS_API_KEY"]; got != "tok-c" {
		t.Errorf("the bridge was handed CEREBRAS_API_KEY = %q, want its pairing's credential: the fixture "+
			"must hand the bridge a key for the worker's absence of one to mean anything", got)
	}
	worker := input("acme-worker")
	if v, ok := worker.Env["CEREBRAS_API_KEY"]; ok {
		t.Errorf("the worker was handed CEREBRAS_API_KEY = %q, a provider credential no pairing names for it", v)
	}
	if worker.Env[paths.ServiceCallerTokenEnv("acme-worker")] == "" || worker.Env["YOLO_PROVIDERS"] == "" {
		t.Errorf("the worker's input lacks its caller token or the wire tables: %v", worker.Env)
	}
}
