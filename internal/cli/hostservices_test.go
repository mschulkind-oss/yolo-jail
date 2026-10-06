package cli

// hostservices_test.go pins the host launch's LAUNCH-OWNED SERVICES
// (docs/design/host-notch-services.md; OQ-NC1 ruled A, OQ-HS3 per launch, OQ-HS4 as leaned):
// `yolo host -p codex -- claude` starts the wire bridge's host half as its child, on a port it
// picked, behind its caller token, points claude at it, and stops it when claude exits, by any
// route. Every cell goes through hostMain. The agent is this test binary in its fake-agent mode
// (a `claude` script on PATH that re-execs it), which reads the environment it was handed and
// talks to the bridge the way claude would; no agent CLI runs and no request leaves loopback: the
// upstream is an httptest server and the OpenAI credential service a fake on the host socket.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// testFakeAgentArg makes a child of this test binary the fake agent (TestMain).
const testFakeAgentArg = "-yolo-cli-test-fake-agent"

// fakeAgentReport is what the fake agent writes to the file YOLO_CLI_TEST_AGENT_DUMP names.
type fakeAgentReport struct {
	Env map[string]string `json:"env"`
	// Path is the PATH the agent was handed, and Copy the YOLO_CLI_TEST_AGENT_COPY a launcher
	// script set on its way to the agent — which copy of it the launch ran.
	Path        string `json:"path"`
	Copy        string `json:"copy"`
	WithToken   int    `json:"with_token"`
	WithoutAuth int    `json:"without_auth"`
	Body        string `json:"body"`
}

// fakeAgentMain is the fake agent: it records the ANTHROPIC_*, COPILOT_* and AWS_* environment it
// was handed, sends the bridge one canned request with the token claude (or copilot, on its
// provider base URL and key) would send and one with none, writes the report, and then exits or,
// in mode "sleep", waits to be killed.
func fakeAgentMain() int {
	rep := fakeAgentReport{Env: map[string]string{}, Path: os.Getenv("PATH"),
		Copy: os.Getenv("YOLO_CLI_TEST_AGENT_COPY")}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && (strings.HasPrefix(k, "ANTHROPIC_") ||
			strings.HasPrefix(k, "COPILOT_") || strings.HasPrefix(k, "AWS_")) {
			rep.Env[k] = v
		}
	}
	base, key := rep.Env["ANTHROPIC_BASE_URL"], rep.Env["ANTHROPIC_AUTH_TOKEN"]
	if base == "" && rep.Env["COPILOT_PROVIDER_TYPE"] == "anthropic" {
		base, key = rep.Env["COPILOT_PROVIDER_BASE_URL"], rep.Env["COPILOT_PROVIDER_API_KEY"]
	}
	if base != "" {
		post := func(auth string) (int, string) {
			req, _ := http.NewRequest(http.MethodPost, base+"/v1/messages",
				strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			if auth != "" {
				req.Header.Set("Authorization", "Bearer "+auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return -1, err.Error()
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		rep.WithToken, rep.Body = post(key)
		rep.WithoutAuth, _ = post("")
	}
	data, _ := json.Marshal(rep)
	_ = os.WriteFile(os.Getenv("YOLO_CLI_TEST_AGENT_DUMP"), data, 0o600)
	if os.Getenv("YOLO_CLI_TEST_AGENT_MODE") == "sleep" {
		time.Sleep(30 * time.Second)
	}
	return 0
}

// fakeUpstream is the provider: it answers the Responses and chat-completions paths with a
// canned completion, and records the Authorization each request carried.
func fakeUpstream(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			fmt.Fprint(w, `{"id":"r","model":"gpt","status":"completed","output":[{"type":"message",`+
				`"role":"assistant","content":[{"type":"output_text","text":"ok"}]}],`+
				`"usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		fmt.Fprint(w, `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant",`+
			`"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), auths...)
	}
}

// fakeHostBroker serves the OpenAI credential service's access-token view on the host socket a
// host half dials (openaiauthhost.HostSocketPath, under the package's private singleton dir).
func fakeHostBroker(t *testing.T) {
	t.Helper()
	socket := openaiauthhost.HostSocketPath()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop); _ = os.Remove(socket) })
	go func() {
		_ = hostservice.ServeUnix(func(s *hostservice.Session) {
			_ = s.JSON(map[string]any{"access_token": "chatgpt-access", "account_id": "acct",
				"expires_at": time.Now().Add(time.Hour).UnixMilli(), "generation": 1})
			s.Exit(0)
		}, socket, stop)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake host broker never listened")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// serviceLaunch is one `yolo host` run whose agent is the fake agent.
type serviceLaunch struct {
	rc      int
	errs    string
	report  fakeAgentReport
	started []*launchservice.Running
	// listening is, for each started service, every address its plan moved that accepted a
	// connection once the service said it was ready: what it opened, as against what it planned.
	listening [][]string
	// inputs is, index for index with started, the input each started service was handed.
	inputs []map[string]string
	execed bool
}

// runServiceLaunch runs `yolo host <flags> -- claude` over cfg, the fake agent in mode, and
// returns what happened. signals, when non-nil, is the launch's signal channel.
func runServiceLaunch(t *testing.T, cfg string, flags []string, mode string, signals chan os.Signal) serviceLaunch {
	t.Helper()
	return runServiceLaunchWith(t, cfg, flags, mode, signals, nil)
}

// runServiceLaunchWith is runServiceLaunch with a setup step, run once the test HOME exists and
// before the launch, handed the fake agent's exec line (fakeAgentExec) for a second copy of it.
func runServiceLaunchWith(t *testing.T, cfg string, flags []string, mode string, signals chan os.Signal,
	setup func(agentExec string)) serviceLaunch {
	t.Helper()
	return runServiceLaunchAs(t, cfg, flags, "claude", mode, signals, setup)
}

// runServiceLaunchAs is runServiceLaunchWith launching agent, a script of that name on PATH that
// runs the fake agent.
func runServiceLaunchAs(t *testing.T, cfg string, flags []string, agent, mode string, signals chan os.Signal,
	setup func(agentExec string)) serviceLaunch {
	t.Helper()
	hostGateHome(t, cfg, wcShell(nil))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentExec := "exec '" + exe + "' " + testFakeAgentArg + " \"$@\"\n"
	bin := t.TempDir()
	script := "#!/bin/sh\n" + agentExec
	if err := os.WriteFile(filepath.Join(bin, agent), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(agentExec)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dump := filepath.Join(t.TempDir(), "agent.json")
	t.Setenv("YOLO_CLI_TEST_AGENT_DUMP", dump)
	t.Setenv("YOLO_CLI_TEST_AGENT_MODE", mode)

	var got serviceLaunch
	origStart := startLaunchService
	startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
		r, err := origStart(p, env)
		if r != nil {
			got.started = append(got.started, r)
			got.inputs = append(got.inputs, env)
			var open []string
			for _, a := range p.Addresses() {
				if c, derr := net.DialTimeout("tcp", a, 200*time.Millisecond); derr == nil {
					_ = c.Close()
					open = append(open, a)
				}
			}
			got.listening = append(got.listening, open)
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
	got.rc = hostMain(append(append([]string{}, flags...), "--", agent), &out, &errw, false, nil)
	got.errs = errw.String()
	if data, err := os.ReadFile(dump); err == nil {
		_ = json.Unmarshal(data, &got.report)
	}
	return got
}

// assertServiceGone checks that every started service had exited when the launch returned, and
// that nothing listens where the agent was pointed any more.
func assertServiceGone(t *testing.T, l serviceLaunch) {
	t.Helper()
	for _, r := range l.started {
		select {
		case <-r.Done():
		default:
			t.Errorf("the %q service (pid %d) outlived its launch", r.Plan.Service, r.PID())
		}
	}
	if u, err := url.Parse(l.report.Env["ANTHROPIC_BASE_URL"]); err == nil && u.Host != "" {
		if c, err := net.DialTimeout("tcp", u.Host, 200*time.Millisecond); err == nil {
			_ = c.Close()
			t.Errorf("something still listens at %s after the launch", u.Host)
		}
	}
}

// codexConfig is claude alone, with the subscription's Responses endpoint moved to the fake
// upstream (a user-scope provider address, which is legal there and only there).
func codexConfig(upstream string) string {
	return `{"packs": ["claude"], "providers": {"openai-codex": {"endpoints": {"openai-responses": ` +
		`{"base_url": "` + upstream + `/codex"}}}}}`
}

// THE HEADLINE: `yolo host -p codex -- claude` on a bare `"packs": ["claude"]` runs claude on the
// ChatGPT subscription through a bridge this launch started: claude is pointed at a loopback port
// the launch picked (never the manifest's 8215), carries the caller token as its credential rather
// than its login, is served with it and refused without it, the bridge forwards with the view the
// host broker issued, and the bridge is gone once claude exits.
func TestHostCodexClaudeRunsThroughALaunchOwnedBridge(t *testing.T) {
	upstream, auths := fakeUpstream(t)
	fakeHostBroker(t)
	l := runServiceLaunch(t, codexConfig(upstream.URL), []string{"-p", "codex"}, "", nil)
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	base := l.report.Env["ANTHROPIC_BASE_URL"]
	u, err := url.Parse(base)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Port() == "8215" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want a loopback port this launch picked\n%s", base, l.errs)
	}
	token := l.report.Env["ANTHROPIC_AUTH_TOKEN"]
	if len(token) != 64 || token == "local" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the launch's caller token", token)
	}
	if l.report.WithToken != http.StatusOK {
		t.Errorf("claude's request through the bridge got %d: %s", l.report.WithToken, l.report.Body)
	}
	if l.report.WithoutAuth != http.StatusUnauthorized {
		t.Errorf("a request without the caller token got %d, want 401", l.report.WithoutAuth)
	}
	if got := auths(); len(got) != 1 || got[0] != "/codex/responses Bearer chatgpt-access" {
		t.Errorf("upstream saw %q, want one Responses request with the host broker's view", got)
	}
	if len(l.started) != 1 || l.started[0].Plan.Service != "wire-bridge" || l.execed {
		t.Errorf("started %d services, exec'd %v; want the wire bridge as a child and no exec", len(l.started), l.execed)
	}
	for _, a := range l.started[0].Argv {
		if strings.Contains(a, token) {
			t.Errorf("the caller token is on the service's argv: %q", l.started[0].Argv)
		}
	}
	for _, want := range []string{`started the "wire-bridge" service (pack "wire-bridge"`, "stops when claude exits",
		"for claude on " + u.Host + ";"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must disclose the service it runs (%q):\n%s", want, l.errs)
		}
	}
	// THE SERVICE OPENS, AND THE LAUNCH NAMES, ONLY THE ROUTE CLAUDE WAS POINTED AT (HS-D24). The
	// plan moves every address the bridge's adaptations declare (HS-D9), and the composed provider
	// table claude now carries (FT-D2) names every one of them, packs/wire-bridge's Bedrock
	// adapter's included, which it composes onto the bedrock provider for a via profile claude is
	// not on (WG-I39). Neither the bridge's listeners nor the disclosure may follow the table.
	if unused := l.started[0].Plan.Moved["127.0.0.1:8214"]; unused == "" || strings.Contains(l.errs, unused) {
		t.Errorf("the disclosure names %q, a route claude was not pointed at:\n%s", unused, l.errs)
	}
	if len(l.listening) != 1 || len(l.listening[0]) != 1 || l.listening[0][0] != u.Host {
		t.Errorf("the bridge listened at %v, want only %s, the route claude was pointed at", l.listening, u.Host)
	}
	// THE HAND-OVER LINE on the launch-owned-services path: what starts and where it came from,
	// after the service lines — the last thing yolo says before the agent's own startup.
	started := strings.Index(l.errs, `started the "wire-bridge" service`)
	starting := strings.Index(l.errs, "yolo host: starting claude (from your PATH, ")
	if starting < 0 || starting < started {
		t.Errorf("the launch must say what it starts, after the service lines:\n%s", l.errs)
	}
	assertServiceGone(t, l)
}

// askForPlannedPortsFirst has another listener ask for every address a launch-owned service was
// planned on, just before the real start, and returns the addresses it was given. The pick used to
// let the port go at once, so whatever bound next could be handed it, and the service's own bind
// then failed with "address already in use" (docs/plans/test-suite-speed.md); asking for the exact
// port makes that race deterministic.
func askForPlannedPortsFirst(t *testing.T) *[]string {
	t.Helper()
	var taken []string
	inner := startLaunchService
	startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
		for _, addr := range p.Addresses() {
			if other, err := net.Listen("tcp", addr); err == nil {
				t.Cleanup(func() { _ = other.Close() })
				taken = append(taken, addr)
			}
		}
		return inner(p, env)
	}
	t.Cleanup(func() { startLaunchService = inner })
	return &taken
}

// THE PORTS A `yolo host` LAUNCH PICKED FOR ITS BRIDGE ARE STILL THE BRIDGE'S WHEN ANOTHER LISTENER
// ASKS FOR THEM FIRST: launchservice.NewPlan's pick, the real bridge host half, and claude served
// through it at the address it was pointed at.
func TestHostBridgesPickedPortsAreStillItsOwnWhenAnotherListenerAsksFirst(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	var taken *[]string
	l := runServiceLaunchWith(t, codexConfig(upstream.URL), []string{"-p", "codex"}, "", nil,
		func(string) { taken = askForPlannedPortsFirst(t) })
	if len(*taken) > 0 {
		t.Errorf("another listener bound %v, ports this launch picked for its bridge, before the "+
			"bridge started: the pick let them go", *taken)
	}
	if l.rc != 0 {
		t.Fatalf("rc = %d: the bridge lost the port claude was pointed at\n%s", l.rc, l.errs)
	}
	if l.report.WithToken != http.StatusOK {
		t.Errorf("claude's request through the bridge got %d: %s", l.report.WithToken, l.report.Body)
	}
	assertServiceGone(t, l)
}

// THE FLOOR ON THE LAUNCH-OWNED-SERVICES PATH (HP-DIR4, HE-D1): `yolo host -p codex -- claude` is
// the main way claude runs through a service, and it must run the floor's copy with the floor's
// PATH exactly as the exec path does. The floor holds claude from the machine's capture (Linux's
// recipe; the platform is the test's, so this runs on every host), its copy marks itself on the
// way to the fake agent, and the agent reports what it was handed: the floor's copy, the caller's
// PATH first and the floor's bin/ last, and the hand-over line naming the floor's copy — after
// the service lines. Point the services path at the PATH copy, or hand it launch.environ(), and
// this fails.
func TestHostServicesLaunchRunsTheFloorsCopyWithTheFloorsPath(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	orig := newHostFloor
	t.Cleanup(func() { newHostFloor = orig })
	l := runServiceLaunchWith(t, codexConfig(upstream.URL), []string{"-p", "codex"}, "", nil, func(agentExec string) {
		newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
			f := productionHostFloor(out, progs)
			f.GOOS = "linux"
			f.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
			f.Capture = func(bin string) error { return fmt.Errorf("test guard: no `yolo capture %s`", bin) }
			return f
		}
		admitRelocatableCapture(t, "claude", "#!/bin/sh\nexport YOLO_CLI_TEST_AGENT_COPY=floor\n"+agentExec)
	})
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	if len(l.started) != 1 || l.execed {
		t.Fatalf("started %d services, exec'd %v; want the bridge and no exec\n%s", len(l.started), l.execed, l.errs)
	}
	if l.report.Copy != "floor" {
		t.Errorf("the agent that ran is not the floor's copy (copy mark %q)\n%s", l.report.Copy, l.errs)
	}
	floorBin := filepath.Join(paths.HostFloorDir(), "bin")
	sep := string(os.PathListSeparator)
	if want := hostChildPath(hostLaunchPath(), floorBin); l.report.Path != want ||
		!strings.HasSuffix(l.report.Path, sep+floorBin) {
		t.Errorf("the agent's PATH = %q, want the caller's then the floor's bin/: %q", l.report.Path, want)
	}
	started := strings.Index(l.errs, `started the "wire-bridge" service`)
	starting := strings.Index(l.errs, "yolo host: starting claude (yolo's floor copy, ")
	if started < 0 || starting < started {
		t.Errorf("the hand-over line must name the floor's copy, after the service lines:\n%s", l.errs)
	}
	assertServiceGone(t, l)
}

// The same through profile, which is how a host wrapper (`exec yolo host -- claude`) reaches
// it, and through the bridge's other route: claude on cerebras, whose key reaches the service from
// the launch's composition and nowhere else.
func TestHostWrapperSpellingAndTheChatRouteRunThroughTheBridge(t *testing.T) {
	upstream, auths := fakeUpstream(t)
	fakeHostBroker(t)
	l := runServiceLaunch(t, `{"packs": ["claude"], "profile": {"claude": "codex"}, "providers": `+
		`{"openai-codex": {"endpoints": {"openai-responses": {"base_url": "`+upstream.URL+`/codex"}}}}}`,
		nil, "", nil)
	if l.rc != 0 || l.report.WithToken != http.StatusOK {
		t.Fatalf("profile claude=codex: rc = %d, request %d\n%s", l.rc, l.report.WithToken, l.errs)
	}
	assertServiceGone(t, l)

	cerebras := `{"packs": ["claude", "cerebras"], "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}], ` +
		`"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "` + upstream.URL + `/v1"}}}}}`
	l = runServiceLaunch(t, cerebras, []string{"-p", "cerebras"}, "", nil)
	if l.rc != 0 || l.report.WithToken != http.StatusOK {
		t.Fatalf("-p cerebras: rc = %d, request %d: %s\n%s", l.rc, l.report.WithToken, l.report.Body, l.errs)
	}
	if got := auths(); got[len(got)-1] != "/v1/chat/completions Bearer tok-c" {
		t.Errorf("the chat route's upstream saw %q, want cerebras's key from the launch", got)
	}
	assertServiceGone(t, l)
}

// A signal the launch receives reaches the agent, and the agent's death by it ends the service:
// SIGTERM to `yolo host` is forwarded, claude dies of it, the launch exits 128+15, and the bridge
// is gone. An agent that exits on its own is the headline's case.
func TestHostServiceStopsWhenTheAgentDiesOfASignal(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	signals := make(chan os.Signal, 1)
	dump := make(chan struct{})
	go func() {
		// Wait for the fake agent to have written its report, i.e. to be running, then signal.
		for i := 0; i < 1000; i++ {
			if p := os.Getenv("YOLO_CLI_TEST_AGENT_DUMP"); p != "" {
				if _, err := os.Stat(p); err == nil {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		signals <- syscall.SIGTERM
		close(dump)
	}()
	l := runServiceLaunch(t, codexConfig(upstream.URL), []string{"-p", "codex"}, "sleep", signals)
	<-dump
	if l.rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want %d (the agent died of the forwarded SIGTERM)\n%s", l.rc, 128+int(syscall.SIGTERM), l.errs)
	}
	if l.report.WithToken != http.StatusOK {
		t.Errorf("the bridge did not serve the agent before the signal: %d", l.report.WithToken)
	}
	assertServiceGone(t, l)
}

// NOTHING STARTS WHEN NO SELECTED PROFILE NEEDS A SERVICE: an unprofiled launch, a profile the
// agent's provider serves directly (claude on bedrock), and one whose agent speaks the provider's
// own wire (copilot on cerebras, which a jail would bridge and the host sends direct, §4.2) all
// exec as before and start no child.
func TestHostStartsNoServiceWhenNoProfileNeedsOne(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		flags     []string
	}{
		{"no profile", `{"packs": ["claude"]}`, nil},
		{"bedrock", `{"packs": ["claude"]}`, []string{"-p", "bedrock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := runServiceLaunch(t, tc.cfg, tc.flags, "", nil)
			if l.rc != 0 || !l.execed || len(l.started) != 0 {
				t.Errorf("rc = %d, exec'd %v, started %d services; want an exec and no service\n%s",
					l.rc, l.execed, len(l.started), l.errs)
			}
			if strings.Contains(l.errs, "started the") {
				t.Errorf("a launch that needs no service said it started one:\n%s", l.errs)
			}
		})
	}
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("copilot on cerebras started a service")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	if _, errs := hostGateLaunchWith(t, bridgeConfig, wcShell(nil), []string{"-p", "cerebras"}, "copilot"); strings.Contains(errs, "started the") {
		t.Errorf("copilot on cerebras started a service:\n%s", errs)
	}
}

// A host half that cannot start refuses the launch before the agent runs, naming the service, its
// argv and its log (HS-D5's third case).
func TestHostRefusesWhenTheServiceCannotStart(t *testing.T) {
	origStart := startLaunchService
	startLaunchService = func(p *launchservice.Plan, _ map[string]string) (*launchservice.Running, error) {
		return nil, fmt.Errorf("the %q service (pack %q) did not start: it exited before it reported "+
			"listening. Its argv: yolo internal daemon wire-bridge. Its log: /x.log", p.Service, p.Pack)
	}
	t.Cleanup(func() { startLaunchService = origStart })
	rc, env, errs := hostGateRun(t, claudeAlone, nil, []string{"-p", "codex"}, "claude")
	if rc != 1 || env != nil {
		t.Fatalf("rc = %d, env = %v: a service that cannot start must refuse before the agent\n%s", rc, env, errs)
	}
	for _, want := range []string{"refusing to launch", `the "wire-bridge" service`, "Its log: /x.log"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// `yolo host env` refuses a bridged profile: an environment script owns no process, so it cannot
// own a service's lifetime (OQ-HS3). It names the launch that works, with nothing on stdout.
func TestHostEnvRefusesABridgedProfile(t *testing.T) {
	for _, tc := range []struct {
		cfg   string
		args  []string
		spell string
	}{
		{claudeAlone, []string{"env", "--agent", "claude", "-p", "codex"}, "`yolo host -p codex -- claude`"},
		{`{"packs": ["claude"], "profile": {"claude": "codex"}}`, []string{"env", "--agent", "claude"},
			"`yolo host -- claude`"},
	} {
		hostGateHome(t, tc.cfg, nil)
		origStart := startLaunchService
		startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			t.Fatal("yolo host env started a service")
			return nil, nil
		}
		var out, errw bytes.Buffer
		rc := hostMain(tc.args, &out, &errw, false, nil)
		startLaunchService = origStart
		if rc == 0 || out.Len() != 0 {
			t.Fatalf("%v: rc = %d, stdout = %q", tc.args, rc, out.String())
		}
		for _, want := range []string{`profile "codex"`, `pack "wire-bridge"'s "wire-bridge" service`,
			"a host service lives only for the command that starts it", tc.spell} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("%v: the refusal must say %q:\n%s", tc.args, want, errw.String())
			}
		}
	}
}

// `yolo host apply` writes no bridged address and says where a bridged profile selection
// takes effect (OQ-HS3). Every apply test here declares `host_management: "own"`: the unset key
// is `none` since OQ-CO14, under which the apply refuses before it renders or says anything.
func TestHostApplyWritesNoBridgedAddressAndSaysWhy(t *testing.T) {
	home := hostComputedHome(t, `{"packs": ["claude"], "host_management": "own", "profile": {"claude": "codex"}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{"profile claude → codex renders no address here",
		`pack "wire-bridge"'s "wire-bridge" service`, "`yolo host -- claude` or the host wrappers"} {
		if !strings.Contains(report, want) {
			t.Errorf("the apply must say %q:\n%s", want, report)
		}
	}
	for _, rel := range []string{".claude/settings.json", ".claude.json"} {
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "127.0.0.1:82") || strings.Contains(string(data), "ANTHROPIC_BASE_URL") {
			t.Errorf("%s names a bridged address:\n%s", rel, data)
		}
	}
}

// AND A VIA PROFILE (WG-I12 as WG-I46 narrowed it; host-notch-services.md HS-D30): `yolo host apply`
// stays inert for a via, since the address `yolo host --` serves it at is picked per launch and no
// file can name it. pi's config the apply renders on `bedrock-bridge` names no via route, and the
// apply starts no service; pi there keeps its own client, as it does at `yolo host --`, where its
// file-carried via is cleared too (HS-D31).
func TestHostApplyWritesNoViaAddressForAViaProfile(t *testing.T) {
	home := hostComputedHome(t, `{"packs": ["pi", "bedrock", "wire-bridge"], "host_management": "own", `+
		`"profile": {"pi": "bedrock-bridge"}, `+
		`"providers": {"bedrock": {"region": "eu-west-1"}}}`)
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("yolo host apply started a service")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	rendered := false
	for _, rel := range []string{".pi/agent/models.json", ".pi/agent/settings.json"} {
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			continue
		}
		rendered = true
		if strings.Contains(string(data), "/agent/pi") || strings.Contains(string(data), "127.0.0.1:8216") {
			t.Errorf("%s names a via route:\n%s", rel, data)
		}
	}
	if !rendered {
		t.Fatalf("the apply rendered none of pi's config, so this proves nothing:\n%s%s", out.String(), errw.String())
	}
	// And it says why, as it does for a bridged selection: pi's route is file-carried, so only a jail
	// or macos-user launch serves it (HS-D31).
	report := out.String() + errw.String()
	for _, want := range []string{"profile pi → bedrock-bridge renders no address for its via here",
		"pi reads its route from ~/.pi/agent/models.json", "`yolo -p bedrock-bridge -- pi`"} {
		if !strings.Contains(report, want) {
			t.Errorf("the apply must say %q:\n%s", want, report)
		}
	}
}

// `yolo host apply` SAYS WHERE A VIA OR CARRIER TAKES EFFECT (HS-D33, OQ-HS3), as it does for a
// bridged selection: copilot on bedrock-bridge (its via) and on plain bedrock (the carrier) rides
// the bridge's adapter address at a port `yolo host --` picks per launch, so the apply renders no
// address for it, starts nothing, and names `yolo host -- copilot` and the host wrappers, where
// the selection does take effect. Deleting hostinputs' viaSelectionNote call fails this.
func TestHostApplySaysWhereAViaOrCarrierTakesEffect(t *testing.T) {
	for _, tc := range []struct{ profile, route string }{
		{"bedrock-bridge", "its via"},
		{"bedrock", `its carrier "wire-bridge"`},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			hostComputedHome(t, `{"packs": ["copilot", "bedrock", "wire-bridge"], "host_management": "own", `+
				`"profile": {"copilot": "`+tc.profile+`"}, `+
				`"providers": {"bedrock": {"region": "eu-west-1"}}}`)
			origStart := startLaunchService
			startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
				t.Fatal("yolo host apply started a service")
				return nil, nil
			}
			t.Cleanup(func() { startLaunchService = origStart })
			var out, errw bytes.Buffer
			if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
				t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
			}
			report := out.String() + errw.String()
			for _, want := range []string{"profile copilot → " + tc.profile + " renders no address for " + tc.route + " here",
				`pack "wire-bridge"'s "wire-bridge" service`, "`yolo host -- copilot` or the host wrappers"} {
				if !strings.Contains(report, want) {
					t.Errorf("the apply must say %q:\n%s", want, report)
				}
			}
		})
	}
}

// A FETCHED PACK'S HOST HALF NEVER RUNS (OQ-HS4, HS-D27): a fetched pack that takes the bridge's
// name and declares the same service with a host half is refused by name at the launch, with the
// next step, and nothing starts.
func TestHostRefusesAFetchedPacksHostHalfByName(t *testing.T) {
	src := fetchedBridgeSource(t,
		`{"kind": "adapter", "adapts": {"from": "openai-responses", "to": "anthropic"}, "address": "http://127.0.0.1:8215"}`)
	cfg := `{"packs": ["claude", {"source": "` + src + `", "name": "wire-bridge"}]}`
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("a fetched pack's host half was started")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	rc, env, errs := hostGateRun(t, cfg, nil, []string{"-p", "codex"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("rc = %d: a fetched pack's host half must refuse the launch\n%s", rc, errs)
	}
	for _, want := range []string{`profile "codex"`, `"wire-bridge" service, which this launch cannot start`,
		"its pack was fetched", "select a local checkout of the pack by its file:// path",
		"`yolo -p claude=codex -- claude`, which is a jail launch"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// A LOCAL PACK'S HOST HALF RUNS, AND IS NAMED BEFORE IT DOES (HS-D27): a file:// fork of the bridge
// pack, the user's own copy, holds the bridge's service under the later-wins rule, so `yolo host
// -p codex -- claude` starts ITS host half, and the launch prints that argv as pack code it is
// about to run on the machine before the start line. Through hostMain with the real start, the
// test binary standing in for `yolo`, as the headline runs the shipped bridge.
func TestHostRunsALocalPacksBridgeHostHalfAndNamesItBeforeItStarts(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	manifest, err := fs.ReadFile(packs.FS, "wire-bridge/pack.json")
	if err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(t.TempDir(), "wire-bridge")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fork, "pack.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs": ["claude", {"source": "file://` + fork + `", "name": "wire-bridge"}], ` +
		`"providers": {"openai-codex": {"endpoints": {"openai-responses": {"base_url": "` + upstream.URL + `/codex"}}}}}`
	l := runServiceLaunch(t, cfg, []string{"-p", "codex"}, "", nil)
	if l.rc != 0 {
		t.Fatalf("rc = %d: a local fork's bridge must run\n%s", l.rc, l.errs)
	}
	if len(l.started) != 1 || l.started[0].Plan.Service != "wire-bridge" || !l.started[0].Plan.Local {
		t.Fatalf("started %d services, want the local fork's wire-bridge (Local)\n%s", len(l.started), l.errs)
	}
	if l.report.WithToken != http.StatusOK {
		t.Errorf("the local fork's bridge did not serve claude: %d\n%s", l.report.WithToken, l.errs)
	}
	named := strings.Index(l.errs, `yolo host: this launch runs pack code on your machine: the "wire-bridge" `+
		`service's host half from pack "wire-bridge", a local pack yolo does not ship: yolo internal daemon wire-bridge`)
	started := strings.Index(l.errs, `yolo host: started the "wire-bridge" service`)
	if named < 0 || started < 0 || named > started {
		t.Errorf("the local pack's host argv must be named before its start line (named at %d, started at %d):\n%s",
			named, started, l.errs)
	}
	assertServiceGone(t, l)
}
