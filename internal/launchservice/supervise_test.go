package launchservice

// supervise_test.go pins a launch-owned service's SUPERVISION (Running.Supervise;
// docs/design/host-notch-services.md HS-D28): a service that dies while its agent runs is named
// in one line and restarted under its declared policy, on the same reserved socket, so the
// agent's fixed address still reaches it and a request made while it is down waits instead of
// failing. The services are this test binary's fakeDoorway, run under Start exactly as a launch
// runs a doorway, on a port reserved as a launch reserves it (doorwayPlan).

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// sleeps replaces the wait before a restart for the test: every wait is recorded, and gate, when
// non-nil, is waited on (or the service's stop) instead of the duration.
type sleeps struct {
	mu    sync.Mutex
	waits []time.Duration
	gate  chan struct{}
	// asleep receives once per wait, as it starts.
	asleep chan struct{}
}

func stubRestartSleep(t *testing.T, gate chan struct{}) *sleeps {
	t.Helper()
	s := &sleeps{gate: gate, asleep: make(chan struct{}, 64)}
	orig := restartSleep
	restartSleep = func(d time.Duration, stop <-chan struct{}) bool {
		s.mu.Lock()
		s.waits = append(s.waits, d)
		s.mu.Unlock()
		s.asleep <- struct{}{}
		if s.gate == nil {
			return true
		}
		select {
		case <-s.gate:
			return true
		case <-stop:
			return false
		}
	}
	t.Cleanup(func() { restartSleep = orig })
	return s
}

func (s *sleeps) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// waitFor polls out until it contains want.
func waitFor(t *testing.T, out *lockedBuilder, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no line said %q:\n%s", want, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startSupervisedDoorway starts fakeDoorway on a reserved port with restart policy restart, and
// supervises it for "claude".
func startSupervisedDoorway(t *testing.T, mode, restart string) (*Running, *Plan, string, *lockedBuilder) {
	t.Helper()
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:"+mode)
	plan, addr := doorwayPlan(t)
	plan.Restart = restart
	r, err := Start(plan, map[string]string{"UPSTREAM": "from-the-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	out := &lockedBuilder{}
	r.Supervise("claude", out, "yolo host: ")
	return r, plan, addr, out
}

// A DOORWAY KILLED WHILE ITS AGENT RUNS COMES BACK ON THE SAME ADDRESS, BEHIND THE SAME TOKEN, AS A
// NEW PROCESS, and a request made while it was down is answered once it is back rather than
// refused: the launch kept the reserved socket listening for it. One line names the death, the
// status, the address and the log; one more says it is back. Its own Stop is silent.
func TestASupervisedDoorwayKilledMidSessionComesBackOnTheSameAddress(t *testing.T) {
	gate := make(chan struct{})
	sl := stubRestartSleep(t, gate)
	r, plan, addr, out := startSupervisedDoorway(t, "ok", "")
	old := r.PID()
	if err := syscall.Kill(old, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-sl.asleep
	for _, want := range []string{`yolo host: the "door" service (pack "p") exited (killed by SIGKILL) while claude runs`,
		"restarting it in 1s on the same address (" + addr + "), which holds claude's requests until it is back",
		"Its log: " + r.Log} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the death line must say %q:\n%s", want, out.String())
		}
	}
	// A request made while the service is down waits in the reserved socket's queue.
	type reply struct {
		code int
		body string
	}
	during := make(chan reply, 1)
	go func() {
		code, body, err := fetch(addr, "/", plan.Token, 10*time.Second)
		if err != nil {
			body = err.Error()
		}
		during <- reply{code, body}
	}()
	select {
	case got := <-during:
		t.Fatalf("a request while the service was down was answered %d %q before it came back: "+
			"refused, not held", got.code, got.body)
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	if got := <-during; got.code != 200 || got.body != "from-the-launch" {
		t.Errorf("the request made while the service was down got %d %q, want 200 and the input's value",
			got.code, got.body)
	}
	waitFor(t, out, `yolo host: the "door" service is back (pid `)
	now := r.PID()
	if now == old {
		t.Errorf("the service still names pid %d: it was not replaced", old)
	}
	if !strings.Contains(out.String(), "is back (pid "+strconv.Itoa(now)+") on "+addr+" for claude.") {
		t.Errorf("the line saying it is back must name the new pid %d and %s:\n%s", now, addr, out.String())
	}
	if code, body := get(t, addr, plan.Token); code != 200 || body != "from-the-launch" {
		t.Errorf("the restarted service at the same address got %d %q", code, body)
	}
	if code, _ := get(t, addr, ""); code != 401 {
		t.Errorf("the restarted service let a request without the caller token through: %d", code)
	}
	if _, body, _ := fetch(addr, "/pid", plan.Token, 2*time.Second); body != strconv.Itoa(now) {
		t.Errorf("the address is answered by pid %s, want the restarted %d", body, now)
	}
	before := out.String()
	r.Stop()
	waitGone(t, r, 3*time.Second)
	if out.String() != before {
		t.Errorf("the launch's own Stop was reported:\n%s", strings.TrimPrefix(out.String(), before))
	}
	if dials(addr) {
		t.Error("the address still accepts connections after the service stopped")
	}
}

// A RESTART POLICY OF "no" LEAVES THE SERVICE DOWN AND FREES ITS PORT: the death is named with the
// policy and the next step, and the port then refuses a connection rather than queueing it for a
// process that is never coming.
func TestARestartPolicyOfNoLeavesTheServiceDownAndFreesItsPort(t *testing.T) {
	sl := stubRestartSleep(t, nil)
	r, _, addr, out := startSupervisedDoorway(t, "ok", "no")
	_ = syscall.Kill(r.PID(), syscall.SIGKILL)
	waitGone(t, r, 3*time.Second)
	for _, want := range []string{`the "door" service (pack "p") exited (killed by SIGKILL) while claude runs`,
		`its restart policy is "no", so it is not restarted`, "until claude is launched again", r.Log} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the line must say %q:\n%s", want, out.String())
		}
	}
	if dials(addr) {
		t.Error("the port still accepts connections once the service is given up on")
	}
	if got := sl.recorded(); len(got) != 0 {
		t.Errorf("a service under \"no\" waited to restart: %v", got)
	}
}

// A CLEAN EXIT IS NOT RESTARTED UNDER "on-failure", the default: the doorway exits 0 on SIGTERM
// from outside its launch, and the line says why it stays down.
func TestACleanExitIsNotRestartedUnderOnFailure(t *testing.T) {
	stubRestartSleep(t, nil)
	r, _, addr, out := startSupervisedDoorway(t, "ok", "")
	_ = syscall.Kill(r.PID(), syscall.SIGTERM)
	waitGone(t, r, 3*time.Second)
	if want := `the "door" service (pack "p") exited cleanly while claude runs, and its restart ` +
		`policy ("on-failure") restarts it only after a failure`; !strings.Contains(out.String(), want) {
		t.Errorf("the line must say %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "service is back") || dials(addr) {
		t.Errorf("a clean exit was restarted:\n%s", out.String())
	}
}

// A STOP DURING THE WAIT BEFORE A RESTART IS SILENT AND CUTS THE WAIT SHORT: the launch's own
// teardown is no death, so nothing more is said, and nothing is started.
func TestAStopDuringTheRestartWaitIsSilent(t *testing.T) {
	sl := stubRestartSleep(t, make(chan struct{})) // never released: only the Stop ends the wait
	r, _, addr, out := startSupervisedDoorway(t, "ok", "always")
	_ = syscall.Kill(r.PID(), syscall.SIGKILL)
	<-sl.asleep
	before := out.String()
	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not cut the wait before the restart short")
	}
	if out.String() != before {
		t.Errorf("a Stop during the wait was reported:\n%s", strings.TrimPrefix(out.String(), before))
	}
	if dials(addr) {
		t.Error("the address accepts connections after a Stop during the wait")
	}
}

// EVERY START WRITES THE INPUT AGAIN, 0600, AND THE SERVICE REMOVES IT: a restarted service reads
// the same input its first process did, from a file only this user can read, and none is left.
func TestARestartWritesTheInputAgainPrivately(t *testing.T) {
	modes := filepath.Join(t.TempDir(), "modes")
	t.Setenv(doorwayInputModesEnv, modes)
	stubRestartSleep(t, nil)
	r, plan, addr, out := startSupervisedDoorway(t, "ok", "")
	_ = syscall.Kill(r.PID(), syscall.SIGKILL)
	waitFor(t, out, "service is back (pid ")
	data, _ := os.ReadFile(modes)
	if got := strings.Fields(string(data)); len(got) != 2 || got[0] != "600" || got[1] != "600" {
		t.Errorf("the input files' modes were %q, want two starts, each 600", data)
	}
	if code, body := get(t, addr, plan.Token); code != 200 || body != "from-the-launch" {
		t.Errorf("the restarted service did not read the launch's input: %d %q", code, body)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "input.json")); !os.IsNotExist(err) {
		t.Errorf("the restart's input file was left behind: %v", err)
	}
}

// A RESTART THAT DOES NOT COME BACK IS NAMED AND TRIED AGAIN, the wait doubling from
// RestartBackoff to RestartBackoffMax, as the jail supervisor's does.
func TestTheRestartWaitDoublesToItsBound(t *testing.T) {
	t.Setenv(doorwayRunsEnv, filepath.Join(t.TempDir(), "runs"))
	orig := RestartBackoffMax
	RestartBackoffMax = 4 * time.Second
	t.Cleanup(func() { RestartBackoffMax = orig })
	sl := stubRestartSleep(t, nil)
	r, _, _, out := startSupervisedDoorway(t, "fail-after-first", "on-failure")
	_ = syscall.Kill(r.PID(), syscall.SIGKILL)
	for i := 0; i < 5; i++ {
		<-sl.asleep
	}
	r.Stop()
	got := sl.recorded()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	if len(got) < len(want) {
		t.Fatalf("waits = %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("wait %d = %s, want %s (all: %v)", i, got[i], w, got)
		}
	}
	if !strings.Contains(out.String(), `the "door" service (pack "p") did not come back: the input lacks `+
		`its upstream second line; trying again in 2s`) {
		t.Errorf("a restart that did not come back was not named with why and when:\n%s", out.String())
	}
}

// RUNAGENT SUPERVISES ITS SERVICES WHILE THE AGENT RUNS: a doorway killed under a running agent
// is restarted, and the agent's exit still stops it. Deleting the Supervise call from RunAgent
// fails this.
func TestRunAgentRestartsAServiceThatDiesWhileTheAgentRuns(t *testing.T) {
	stubRestartSleep(t, nil)
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:ok")
	plan, addr := doorwayPlan(t)
	r, err := Start(plan, map[string]string{"UPSTREAM": "from-the-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	exe, _ := os.Executable()
	environ := append(os.Environ(), helperEnv+"=agent:sleep")
	signals := make(chan os.Signal, 1)
	var stderr lockedBuilder
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(r.PID(), syscall.SIGKILL)
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(stderr.String(), "service is back (pid ") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		signals <- syscall.SIGTERM
	}()
	rc := RunAgent(exe, []string{"claude"}, environ, nil, nil, &stderr, []*Running{r}, signals, "yolo host: ")
	if rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want the agent's death by the forwarded SIGTERM", rc)
	}
	for _, want := range []string{`yolo host: the "door" service (pack "p") exited (killed by SIGKILL) while claude runs`,
		`yolo host: the "door" service is back (pid `} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("RunAgent's stderr must say %q:\n%s", want, stderr.String())
		}
	}
	select {
	case <-r.Done():
	default:
		t.Error("the restarted service outlived the agent")
	}
	if dials(addr) {
		t.Error("the address still accepts connections after the agent exited")
	}
}

// fetch GETs path at addr with auth as its Authorization, for a goroutine (get fails the test,
// which only the test's own goroutine may do).
func fetch(addr, path, auth string, timeout time.Duration) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return 0, "", err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}
