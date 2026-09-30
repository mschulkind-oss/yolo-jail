package launchservice

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// helperEnv makes this test binary act as a host half (or an agent) when it is the child: TestMain
// reads it before any test runs. The parent sets it with t.Setenv, which the child inherits.
const helperEnv = "LAUNCHSERVICE_TEST_HELPER"

func TestMain(m *testing.M) {
	switch mode := os.Getenv(helperEnv); {
	case mode == "":
		os.Exit(m.Run())
	case strings.HasPrefix(mode, "agent:"):
		os.Exit(fakeAgent(strings.TrimPrefix(mode, "agent:")))
	case strings.HasPrefix(mode, "doorway:"):
		os.Exit(fakeDoorway(strings.TrimPrefix(mode, "doorway:")))
	default:
		os.Exit(fakeHostHalf(mode))
	}
}

// fakeHostHalf is a host half in miniature: it reads its input, binds the address the input
// names, answers the readiness pipe, and serves until SIGTERM or its launch's lifeline ends.
func fakeHostHalf(mode string) int {
	in, err := ReadInput(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ready := os.NewFile(3, "ready")
	if mode == "fail" {
		fmt.Fprintf(ready, "failed %s the upstream key is missing\n", in.Service)
		return 1
	}
	if mode == "silent-exit" {
		return 1
	}
	ln, err := net.Listen("tcp", in.Env["ADDR"])
	if err != nil {
		fmt.Fprintf(ready, "failed %s %v\n", in.Service, err)
		return 1
	}
	defer ln.Close()
	if mode == "ignore-term" {
		signalIgnore()
	}
	fmt.Fprintf(ready, "ready %s\n", in.Service)
	_ = ready.Close()
	ctx, cancel := Lifeline(context.Background(), os.Getenv)
	defer cancel()
	term := make(chan os.Signal, 1)
	notifyTerm(term)
	select {
	case <-ctx.Done():
	case <-term:
	}
	return 0
}

func fakeAgent(mode string) int {
	switch mode {
	case "exit3":
		return 3
	case "kill-self":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(time.Second)
		return 0
	default: // "sleep": until a signal kills it
		time.Sleep(30 * time.Second)
		return 0
	}
}

// selfAsHostHalf makes SelfExec run this test binary.
func selfAsHostHalf(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := SelfExec
	SelfExec = func(argv []string) []string { return append([]string{exe}, argv[1:]...) }
	t.Cleanup(func() { SelfExec = old })
	t.Setenv("HOME", t.TempDir())
}

func testPlan(t *testing.T) (*Plan, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return &Plan{Declared: Declared{Service: "svc", Pack: "p", Cmd: []string{"yolo", "internal", "daemon", "svc"}},
		TokenEnv: paths.ServiceCallerTokenEnv("svc"), Token: strings.Repeat("ab", 32),
		Moved: map[string]string{"127.0.0.1:1": addr}}, addr
}

func dials(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		_ = c.Close()
	}
	return err == nil
}

func waitGone(t *testing.T, r *Running, within time.Duration) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(within):
		t.Fatalf("the service (pid %d) was still running %s later", r.PID(), within)
	}
}

// Start returns once the service reports it is listening, hands it its input in a 0600 file (the
// caller token among it, never on the argv), and Stop ends it.
func TestStartWaitsForReadinessAndStopEndsTheService(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	if !dials(addr) {
		t.Fatal("Start returned before the service was listening")
	}
	for _, a := range r.Argv {
		if strings.Contains(a, plan.Token) {
			t.Errorf("the caller token is on the service's argv: %q", r.Argv)
		}
	}
	r.Stop()
	waitGone(t, r, time.Second)
	if dials(addr) {
		t.Error("the service still listens after Stop")
	}
	if _, err := os.Stat(r.dir); !os.IsNotExist(err) {
		t.Errorf("the input directory %s outlived the service", r.dir)
	}
}

// A launch that dies without cleanup takes its service with it: the lifeline's EOF alone ends it,
// with no signal sent (§4.4 item 4's bound).
func TestAServiceEndsWhenItsLaunchIsGone(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	_ = r.lifeline.Close() // what the kernel does when the launch process dies
	waitGone(t, r, 5*time.Second)
}

// A service that fails, or exits before it is listening, refuses the launch, naming the service,
// its argv and its log.
func TestAServiceThatDoesNotStartIsNamed(t *testing.T) {
	selfAsHostHalf(t)
	for _, mode := range []string{"fail", "silent-exit"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(helperEnv, mode)
			plan, addr := testPlan(t)
			_, err := Start(plan, map[string]string{"ADDR": addr})
			if err == nil {
				t.Fatal("Start succeeded for a service that never listened")
			}
			for _, want := range []string{`"svc"`, "internal daemon svc", LogPath("svc")} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name %q: %v", want, err)
				}
			}
			if mode == "fail" && !strings.Contains(err.Error(), "the upstream key is missing") {
				t.Errorf("the service's own reason is lost: %v", err)
			}
		})
	}
}

// A service that ignores SIGTERM is killed after the grace.
func TestStopKillsAServiceThatIgnoresTerm(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "ignore-term")
	old := StopGrace
	StopGrace = 200 * time.Millisecond
	t.Cleanup(func() { StopGrace = old })
	plan, addr := testPlan(t)
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the lifeline open, so only the signals can end it.
	lifeline := r.lifeline
	r.lifeline = os.NewFile(^uintptr(0), "none")
	r.Stop()
	_ = lifeline.Close()
	waitGone(t, r, time.Second)
}

// runAgent runs this test binary as the agent in mode, with one live service, and reports the
// status and whether the service was gone when RunAgent returned.
func runAgent(t *testing.T, mode string, signals chan os.Signal) (int, *Running) {
	t.Helper()
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	exe, _ := os.Executable()
	environ := append(os.Environ(), helperEnv+"=agent:"+mode)
	var stderr strings.Builder
	rc := RunAgent(exe, []string{"claude"}, environ, nil, nil, &stderr, []*Running{r}, signals, "yolo host: ")
	select {
	case <-r.Done():
	default:
		t.Errorf("RunAgent returned with the service (pid %d) still running", r.PID())
	}
	if strings.Contains(stderr.String(), "exited while") {
		t.Errorf("a service stopped by its launch was reported as dying mid-session: %s", stderr.String())
	}
	return rc, r
}

// The agent's status is the launch's, and the service is stopped once the agent exits.
func TestRunAgentStopsTheServiceWhenTheAgentExits(t *testing.T) {
	if rc, _ := runAgent(t, "exit3", nil); rc != 3 {
		t.Errorf("rc = %d, want the agent's 3", rc)
	}
}

// An agent killed by a signal is 128 plus the signal, and the service still stops.
func TestRunAgentStopsTheServiceWhenTheAgentDiesOfASignal(t *testing.T) {
	if rc, _ := runAgent(t, "kill-self", nil); rc != 128+int(syscall.SIGKILL) {
		t.Errorf("rc = %d, want %d", rc, 128+int(syscall.SIGKILL))
	}
}

// A SIGTERM to the launch is forwarded to the agent, whose death ends the service; a SIGINT is
// absorbed, since a terminal already delivered it to the agent's process group.
func TestRunAgentForwardsTermToTheAgent(t *testing.T) {
	signals := make(chan os.Signal, 2)
	signals <- syscall.SIGINT
	go func() { time.Sleep(300 * time.Millisecond); signals <- syscall.SIGTERM }()
	if rc, _ := runAgent(t, "sleep", signals); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want %d: the forwarded SIGTERM ended the agent", rc, 128+int(syscall.SIGTERM))
	}
}

// A service that dies while the agent runs is named once, and not restarted.
func TestAServiceDyingMidSessionIsNamed(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuilder
	WatchDeath([]*Running{r}, "claude", &stderr, "yolo host: ")
	_ = r.cmd.Process.Kill()
	waitGone(t, r, time.Second)
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(stderr.String(), `the "svc" service exited while claude runs`) {
		if time.Now().After(deadline) {
			t.Fatalf("no line named the dead service: %q", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stderr.String(), r.Log) {
		t.Errorf("the line must name the log: %q", stderr.String())
	}
	r.Stop()
}

// packFrom loads a pack from a manifest written to a temp dir.
func packFrom(t *testing.T, name, manifest string, official bool) *packload.Pack {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(dir, name)
	if p == nil || len(probs) > 0 {
		t.Fatalf("load %s: %v", name, probs)
	}
	p.Official = official
	return p
}

const bridgeManifest = `{"name": "wire-bridge", "contributes": [
  {"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8214"},
  {"kind": "adapter", "adapts": {"from": "openai-responses", "to": "anthropic"}, "address": "http://127.0.0.1:8215"},
  {"kind": "service", "name": "wire-bridge", "endpoint": "wire-bridge.endpoint",
   "jail_daemon": {"cmd": ["yolo-jaild", "wire-bridge"]},
   "host_daemon": {"cmd": %s}}]}`

// Only an official pack's host half runs: a fetched or local pack of the same name, declaring the
// same service, is refused BY NAME (OQ-HS4).
func TestAdmitRefusesAFetchedPacksHostHalfByName(t *testing.T) {
	fetched := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), false)
	_, err := Admit([]*packload.Pack{fetched}, "wire-bridge")
	if err == nil {
		t.Fatal("a fetched pack's host half was admitted")
	}
	for _, want := range []string{`service "wire-bridge"`, `pack "wire-bridge"`, "not one yolo ships"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
	official := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), true)
	d, err := Admit([]*packload.Pack{official}, "wire-bridge")
	if err != nil || strings.Join(d.Cmd, " ") != "yolo internal daemon wire-bridge" {
		t.Fatalf("the official pack's host half: %+v, %v", d, err)
	}
	// The later pack holds the service, and its origin decides.
	if _, err := Admit([]*packload.Pack{official, fetched}, "wire-bridge"); err == nil {
		t.Error("a fetched pack holding the service by the later-wins rule was admitted")
	}
}

// A host half must name `yolo`, and a service with no host half is named as such.
func TestAdmitRefusesAnArgvThatIsNotYoloAndAMissingHalf(t *testing.T) {
	odd := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["/bin/sh", "-c", "true"]`), true)
	if _, err := Admit([]*packload.Pack{odd}, "wire-bridge"); err == nil || !strings.Contains(err.Error(), "must name `yolo`") {
		t.Errorf("a non-yolo argv: %v", err)
	}
	half := packFrom(t, "b", `{"contributes": [{"kind": "service", "name": "b", "jail_daemon": {"cmd": ["yolo-jaild", "b"]}}]}`, true)
	if _, err := Admit([]*packload.Pack{half}, "b"); err == nil || !strings.Contains(err.Error(), "declares no host half") {
		t.Errorf("a service with no host half: %v", err)
	}
}

// THE DISCLOSURE NAMES THE ROUTES THE AGENTS WERE POINTED AT (HS-D24): PointedAt reads each
// agent's provider environment, the derive's output, and names only the plan's addresses a value
// of it names. The composed provider table names every address the plan moved, so a pointer
// read out of the agent's whole environment (which carries the three wire tables since FT-D2)
// disclosed a route the service never opened. A port that only prefixes another is not named by
// it, a nil delivery counts for nothing, and a plan no agent was pointed into names every
// address rather than none.
func TestPointedAtNamesOnlyTheAddressesTheAgentsShapeNames(t *testing.T) {
	p := &Plan{Moved: map[string]string{"127.0.0.1:8214": "127.0.0.1:4313", "127.0.0.1:8215": "127.0.0.1:38913"}}
	claude := &packload.AgentDelivery{Agent: "claude", Shape: []agentenv.Var{
		{Key: "ANTHROPIC_BASE_URL", Value: "http://127.0.0.1:38913"},
		{Key: "ANTHROPIC_AUTH_TOKEN", Value: "tok"},
	}}
	if got := p.PointedAt(claude, nil); len(got) != 1 || got[0] != "127.0.0.1:38913" {
		t.Errorf("PointedAt(claude) = %v, want the one address claude's ANTHROPIC_BASE_URL names", got)
	}
	prefix := &packload.AgentDelivery{Agent: "copilot", Shape: []agentenv.Var{
		{Key: "COPILOT_PROVIDER_BASE_URL", Value: "http://127.0.0.1:43137"},
	}}
	if got := p.PointedAt(prefix); len(got) != 2 {
		t.Errorf("PointedAt(a port 127.0.0.1:4313 only prefixes) = %v, want every address, as for none named", got)
	}
	both := &packload.AgentDelivery{Agent: "copilot", Shape: []agentenv.Var{
		{Key: "COPILOT_PROVIDER_BASE_URL", Value: "http://127.0.0.1:4313/v1"},
	}}
	if got := p.PointedAt(claude, both); len(got) != 2 || got[0] != "127.0.0.1:38913" || got[1] != "127.0.0.1:4313" {
		t.Errorf("PointedAt(claude, copilot) = %v, want both agents' addresses, sorted", got)
	}
	if got := p.PointedAt(); len(got) != 2 {
		t.Errorf("PointedAt() = %v, want every address when no agent's environment names one", got)
	}
}

// NewPlan moves every address the service's adaptations declare to a port of its own, and the
// served set composes those addresses, so two launches never share a port.
func TestNewPlanMovesEveryDeclaredAddress(t *testing.T) {
	p := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), true)
	d, err := Admit([]*packload.Pack{p}, "wire-bridge")
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewPlan([]*packload.Pack{p}, d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPlan([]*packload.Pack{p}, d)
	for _, hp := range []string{"127.0.0.1:8214", "127.0.0.1:8215"} {
		to := a.Moved[hp]
		if to == "" || to == hp || !strings.HasPrefix(to, "127.0.0.1:") {
			t.Errorf("%s moved to %q", hp, to)
		}
	}
	if a.Token == b.Token || len(a.Token) != 64 {
		t.Errorf("each launch mints its own 256-bit token: %q %q", a.Token, b.Token)
	}
	served := Served([]*Plan{a})
	if !served.Serves("wire-bridge") || served.ServedURL("http://127.0.0.1:8215") != "http://"+a.Moved["127.0.0.1:8215"] {
		t.Errorf("the served set does not compose the picked address")
	}
	if got := WithoutOverrides(map[string]string{"openai-responses->anthropic": "http://127.0.0.1:9999",
		"x->y": "http://h"}, []*packload.Pack{p}, []string{"wire-bridge"}); len(got) != 1 || got["x->y"] == "" {
		t.Errorf("the service's own overrides must be dropped, others kept: %v", got)
	}
}
