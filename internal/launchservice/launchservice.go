// Package launchservice runs a pack service's HOST HALF as a LAUNCH-OWNED SERVICE: a child of the
// one host or macos-user launch whose agent is paired through the service, on a loopback port that
// launch picked, answering only callers that carry that launch's caller token, and gone when the
// agent exits (docs/design/host-notch-services.md; OQ-HS3 ruled per launch, OQ-HS4 decided as
// leaned). "Launch-owned service" is that doc's term (§1.2).
//
// ONE PATH FOR EVERY HOST HALF. Nothing here knows what the wire bridge is: a service is admitted
// by its declaration (packdecl.ServiceHostDaemon) and its pack's origin (packload.Pack.Official),
// its addresses are the adaptations it serves (packload.ServiceAdaptations), and it is handed one
// input file whose shape is this package's contract (Input). `yolo host --` and the macos-user
// launch both call it, so the two notches cannot start a service two ways.
//
// NOT THE JAIL'S SUPERVISOR, and on purpose (the doc's HS-D8 records it). `yolo-jaild supervise`
// restarts a daemon under a policy, inherits its readiness pipe from the entrypoint, and logs
// under the jail home. A launch-owned service is never restarted (a restart could not reach an
// agent whose base URL was fixed at its start, §4.4), owns its readiness pipe itself, and must
// die with its launch even when the launch is SIGKILLed. What the two share is the one contract a
// daemon speaks: the readiness line on the descriptor paths.JailDaemonReadyFDEnv names, which the
// wire bridge writes identically under either.
package launchservice

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The host half's side of the contract: where its input file is, and the descriptor whose EOF
// means its launch is gone. The readiness descriptor is the jail's own (paths.JailDaemonReadyFDEnv).
const (
	InputEnv      = "YOLO_HOST_SERVICE_INPUT"
	LifelineFDEnv = "YOLO_HOST_SERVICE_LIFELINE_FD"
	// HalfEnv is set, in the environment a host half builds from its input, to the service's
	// name: how a daemon that also runs in a jail knows it is the host half.
	HalfEnv = "YOLO_HOST_SERVICE_HALF"
)

// The bounds §4.4 of the design states: the launch waits this long for the service to listen,
// and a stopped service gets this long between SIGTERM and SIGKILL.
var (
	ReadyTimeout = 5 * time.Second
	StopGrace    = 2 * time.Second
)

// Input is the host half's whole input, written by the launch into a 0600 file in a 0700
// directory of its own and read once by the service, which removes it. Env holds the variables
// the service reads as if they were its environment: the wire tables, its caller token, the
// host credential socket, and the credential values the credential gate delivers to the agent
// the service is started for. Nothing in it is on an argv or in a log.
type Input struct {
	Service string            `json:"service"`
	Env     map[string]string `json:"env"`
}

// ReadInput reads the input file named by InputEnv and removes it.
func ReadInput(getenv func(string) string) (Input, error) {
	path := getenv(InputEnv)
	if path == "" {
		return Input{}, fmt.Errorf("%s is unset: a host half is started by a host or macos-user "+
			"launch, which hands it its input file", InputEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Input{}, fmt.Errorf("read the launch's input file: %w", err)
	}
	_ = os.Remove(path)
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return Input{}, fmt.Errorf("decode the launch's input file: %w", err)
	}
	return in, nil
}

// Lifeline returns a context cancelled when the launch that started this process is gone: the
// write end of the pipe LifelineFDEnv names is held only by that launch, so its death, however
// it dies, is an EOF here. That is the design's 5-second bound for a launch killed without
// cleanup (§4.4 item 4), met at once and the same way on Linux and macOS. With no descriptor it
// returns parent unchanged.
func Lifeline(parent context.Context, getenv func(string) string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	fd, err := strconv.Atoi(getenv(LifelineFDEnv))
	if err != nil || fd < 3 {
		return ctx, cancel
	}
	f := os.NewFile(uintptr(fd), "host-service-lifeline")
	go func() {
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		cancel()
	}()
	return ctx, cancel
}

// Declared is a service admitted to run its host half: the holding pack, and its argv.
type Declared struct {
	Service string
	Pack    string
	Cmd     []string
}

// AdmissionError is a service whose host half this launch will not run, and why, in words a
// refusal quotes (HS-D5's first two cases).
type AdmissionError struct {
	Service, Pack, Why string
}

func (e *AdmissionError) Error() string {
	return fmt.Sprintf("service %q of pack %q: %s", e.Service, e.Pack, e.Why)
}

// Admit is the gate every launch asks before it runs a service's host half (OQ-HS4): the service
// held under the one later-wins rule (packload.HeldServices) must declare a host half, its pack
// must be one yolo ships (packload.Pack.Official, so a fetched or local pack's host half is
// refused by name, failing closed until a trust ruling exists for third-party host code), and
// its argv must name `yolo`, the only binary the host ships.
func Admit(packs []*packload.Pack, service string) (Declared, error) {
	held, _ := packload.HeldServices(packs)
	for _, h := range held {
		if h.Service.Name != service {
			continue
		}
		if h.Service.HostDaemon == nil || len(h.Service.HostDaemon.Cmd) == 0 {
			return Declared{}, &AdmissionError{Service: service, Pack: h.Pack,
				Why: "it declares no host half (`host_daemon`), so nothing runs it outside a container jail"}
		}
		if why := admitHostArgv(packs, h.Pack, h.Service.HostDaemon.Cmd, "its host_daemon argv",
			"a host half", "only an official pack's host half runs on your machine "+
				"(docs/design/host-notch-services.md OQ-HS4)"); why != "" {
			return Declared{}, &AdmissionError{Service: service, Pack: h.Pack, Why: why}
		}
		return Declared{Service: service, Pack: h.Pack,
			Cmd: append([]string(nil), h.Service.HostDaemon.Cmd...)}, nil
	}
	return Declared{}, &AdmissionError{Service: service, Why: "no selected pack declares it"}
}

// AdmitDoorway is Admit's gate for a loophole's DOORWAY opened outside a sandbox
// (docs/design/host-notch-services.md HS-D15; the loophole's `jail_daemon.host_cmd`): the same
// rule, read for a declaration that is not a service's. pack is the pack that ships the
// loophole ("" when none of packs does), and it must be one yolo ships; cmd, the resolved host
// argv, must name `yolo`. The Declared it returns carries the loophole's name as its Service,
// the name the readiness line, the input file and the caller token variable all use.
func AdmitDoorway(packs []*packload.Pack, pack, loophole string, cmd []string) (Declared, error) {
	if pack == "" {
		return Declared{}, &AdmissionError{Service: loophole,
			Why: "no selected pack ships the loophole, so there is no pack whose origin could admit its host argv"}
	}
	if len(cmd) == 0 {
		return Declared{}, &AdmissionError{Service: loophole, Pack: pack,
			Why: "it declares no host argv (`jail_daemon.host_cmd`)"}
	}
	if why := admitHostArgv(packs, pack, cmd, "its jail_daemon.host_cmd", "a doorway outside the sandbox",
		"only an official pack's doorway runs on your machine outside the sandbox "+
			"(docs/design/host-notch-services.md OQ-HS4, HS-D15)"); why != "" {
		return Declared{}, &AdmissionError{Service: loophole, Pack: pack, Why: why}
	}
	return Declared{Service: loophole, Pack: pack, Cmd: append([]string(nil), cmd...)}, nil
}

// admitHostArgv is the one admission rule both gates apply to host code a launch would run as
// its child (HS-D12): the pack must be official (packOfficial), and the argv must name `yolo`,
// which the launch resolves to its own binary. It returns why not, "" when admitted; field and
// what name the declaration in the refusal, and official is its sentence for a pack yolo does
// not ship.
func admitHostArgv(packs []*packload.Pack, pack string, cmd []string, field, what, official string) string {
	if !packOfficial(packs, pack) {
		return "its pack is not one yolo ships (a fetched or local pack), and " + official
	}
	if len(cmd) == 0 || cmd[0] != "yolo" {
		first := ""
		if len(cmd) > 0 {
			first = cmd[0]
		}
		return fmt.Sprintf("%s starts %q, and %s must name `yolo`, the one binary the host ships",
			field, first, what)
	}
	return ""
}

func packOfficial(packs []*packload.Pack, name string) bool {
	// The LAST pack of the name, the one the later-wins rule reads its declaration from.
	official := false
	for _, p := range packs {
		if p != nil && p.Name == name {
			official = p.Official
		}
	}
	return official
}

// Plan is one launch-owned service this launch will start: what runs, the served addresses its
// adaptations move to, and its caller token.
type Plan struct {
	Declared
	// TokenEnv carries Token (paths.ServiceCallerTokenEnv): the variable the composed endpoint
	// names as its credential, so the agent's derive reads Token for it.
	TokenEnv, Token string
	// Moved maps each declared loopback `host:port` of the service's adaptations to the served
	// address this launch picked (packload.ServedDaemons.WithRebind's input).
	Moved map[string]string
	// reserved is the reservation of each served address's port, keyed by that address, held from
	// the pick until Start hands it to the service (reserve.go). An address with none, in a plan
	// built by hand, is one the service binds itself.
	reserved map[string]*Reserved
}

// NewPlan picks a served address for every adaptation d's service serves, at its DECLARED address
// (a user's `adapters` override does not apply to it here: WithoutOverrides), and mints its caller
// token. Each port is a RESERVED PORT (reserve.go): held by the plan from the pick until Start
// hands it to the service, so no other listener, this launch's own host-service fronts included,
// can be given it in between. A plan the launch never starts is Released.
func NewPlan(packs []*packload.Pack, d Declared) (*Plan, error) {
	var declared []string
	seen := map[string]bool{}
	for _, a := range packload.ServiceAdaptations(packs, nil) {
		if a.Service != d.Service {
			continue
		}
		hp := loopbackHostPort(a.Address)
		if hp == "" || seen[hp] {
			continue
		}
		seen[hp] = true
		declared = append(declared, hp)
	}
	sort.Strings(declared)
	picked, err := ReservePorts(declared)
	if err != nil {
		return nil, fmt.Errorf("pick a loopback port for service %q: %w", d.Service, err)
	}
	token, err := svcendpoint.NewToken()
	if err != nil {
		ReleaseAll(picked)
		return nil, fmt.Errorf("mint service %q's caller token: %w", d.Service, err)
	}
	moved := make(map[string]string, len(picked))
	reserved := make(map[string]*Reserved, len(picked))
	for hp, r := range picked {
		moved[hp] = r.Addr()
		reserved[r.Addr()] = r
	}
	return &Plan{Declared: d, TokenEnv: paths.ServiceCallerTokenEnv(d.Service), Token: token,
		Moved: moved, reserved: reserved}, nil
}

// PlanAt is the plan for an admitted host argv whose one address and caller token the launch
// already settled, a DOORWAY's (HS-D15): its served listen address and the token the launch
// minted for the loophole (internal/cli/run's served addresses and caller tokens), so the
// doorway answers exactly where and to whom its clients were composed. d.Cmd must already carry
// the address. Moved maps the address to itself: nothing a plan made here serves was declared
// somewhere else first. held is the launch's reservation of the address's port, which the plan
// takes over and Start hands to the doorway (reserve.go); nil when the launch reserved none.
func PlanAt(d Declared, address, tokenEnv, token string, held *Reserved) *Plan {
	p := &Plan{Declared: d, TokenEnv: tokenEnv, Token: token, Moved: map[string]string{address: address}}
	if held != nil {
		p.reserved = map[string]*Reserved{address: held}
	}
	return p
}

// WithoutOverrides is addresses without the user's `adapters` override of any conversion one of
// services serves: at a notch that runs the service as a launch-owned child, the launch-chosen
// address overrides both the manifest's and the user's (OQ-HS4, WB-D13 at the host).
func WithoutOverrides(addresses map[string]string, packs []*packload.Pack, services []string) map[string]string {
	if len(addresses) == 0 || len(services) == 0 {
		return addresses
	}
	drop := map[string]bool{}
	for _, a := range packload.ServiceAdaptations(packs, nil) {
		for _, s := range services {
			if a.Service == s {
				drop[packload.AdapterKey(a.From, a.To)] = true
			}
		}
	}
	out := map[string]string{}
	for k, v := range addresses {
		if !drop[k] {
			out[k] = v
		}
	}
	return out
}

// Served is the served set of a launch whose only served daemons are plans' services, at their
// picked addresses.
func Served(plans []*Plan) packload.ServedDaemons {
	var names []string
	moved := map[string]string{}
	for _, p := range plans {
		names = append(names, p.Service)
		for k, v := range p.Moved {
			moved[k] = v
		}
	}
	if len(moved) == 0 {
		moved = nil
	}
	return packload.ServedByLaunch(names).WithRebind(moved)
}

// CallerTokens is the ScopeInput.CallerTokens of plans: each service's token under its variable.
func CallerTokens(plans []*Plan) map[string]string {
	if len(plans) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, p := range plans {
		out[p.TokenEnv] = p.Token
	}
	return out
}

// Names is plans' service names, in order.
func Names(plans []*Plan) []string {
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.Service)
	}
	return out
}

// Addresses is every served address the plan's service answers at, sorted.
func (p *Plan) Addresses() []string {
	out := make([]string, 0, len(p.Moved))
	for _, v := range p.Moved {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// PointedAt is the plan's addresses that the provider environment a launch composed for agents
// names: each one's AgentDelivery.Shape, the env derive's output, which is where a derive points
// its agent at a route (claude's ANTHROPIC_BASE_URL). Those are the only routes the service
// opens, since it serves by the same selection the derives composed from, so they are what every
// notch's disclosure names (docs/design/host-notch-services.md HS-D24): `yolo host --`, the
// macos-user start, and that arm's dry run. A nil delivery counts for nothing, and a plan none of
// the agents was pointed into names every address rather than none.
//
// THE SHAPE, NEVER THE AGENT'S WHOLE ENVIRONMENT. That also carries the three wire tables
// (docs/design/agent-footer.md FT-D2), and the composed provider table names every address this
// plan moved, a route no agent was pointed at included: packs/wire-bridge's Bedrock adapter
// composes a `for_via` address onto the bedrock provider whenever that pack joins
// (docs/design/wire-bridge-gateway.md WG-I39), and only a profile routing through the bridge
// uses it. Matched against the whole environment, `yolo host -p codex -- claude` disclosed that
// address as one its bridge opened.
func (p *Plan) PointedAt(deliveries ...*packload.AgentDelivery) []string {
	var out []string
	for _, a := range p.Addresses() {
		if shapesName(deliveries, a) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return p.Addresses()
	}
	return out
}

// shapesName reports whether a value of some delivery's Shape names the address hostPort: an
// occurrence of it that no further digit follows, so 127.0.0.1:4313 is not named by a URL on
// 127.0.0.1:43137.
func shapesName(deliveries []*packload.AgentDelivery, hostPort string) bool {
	for _, d := range deliveries {
		if d == nil {
			continue
		}
		for _, v := range d.Shape {
			for rest := v.Value; ; {
				i := strings.Index(rest, hostPort)
				if i < 0 {
					break
				}
				rest = rest[i+len(hostPort):]
				if rest == "" || rest[0] < '0' || rest[0] > '9' {
					return true
				}
			}
		}
	}
	return false
}

func loopbackHostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if loopholedecl.ListenAddressProblem(u.Host) != "" {
		return ""
	}
	return u.Host
}

// LogPath is where a launch-owned service's output goes: one file per service name, appended to
// by every launch, each start headed by a line naming the launch.
func LogPath(service string) string {
	return filepath.Join(paths.GlobalStorage(), "logs", "launch-service-"+service+".log")
}

// SelfExec resolves a host half's argv (its first word is `yolo`) to this binary. A var so the
// package's tests can stand a test binary in for yolo.
var SelfExec = execx.SelfExecArgv

// Running is one started launch-owned service.
type Running struct {
	Plan     *Plan
	Argv     []string
	Log      string
	cmd      *exec.Cmd
	lifeline *os.File
	dir      string
	done     chan struct{}
	stopping chan struct{}
}

// PID is the service's process id.
func (r *Running) PID() int { return r.cmd.Process.Pid }

// Done is closed once the service has exited and been reaped.
func (r *Running) Done() <-chan struct{} { return r.done }

// Start runs plan's host half with env as its input, and returns once it reports it is listening.
// A service that does not within ReadyTimeout, reports failure, or exits first is stopped, and
// the error names it, its argv and its log: the launch then refuses before its agent starts.
//
// THE PLAN'S RESERVED PORTS ARE THE SERVICE'S (reserve.go): each is handed over as a descriptor
// from fd 5, named in ListenFDsEnv, for the service to listen on (Listen), so it serves the port
// its clients were composed with and no other listener could be given it in between. The launch's
// own copies close once the service holds its own, and close whether or not it started.
func Start(plan *Plan, env map[string]string) (*Running, error) {
	defer plan.Release()
	argv := SelfExec(plan.Cmd)
	logPath := LogPath(plan.Service)
	fail := func(why string) error {
		return fmt.Errorf("the %q service (pack %q) did not start: %s. Its argv: %s. Its log: %s",
			plan.Service, plan.Pack, why, strings.Join(argv, " "), logPath)
	}
	dir, err := os.MkdirTemp("", "yolo-launch-service-")
	if err != nil {
		return nil, fail(err.Error())
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	in := Input{Service: plan.Service, Env: map[string]string{}}
	for k, v := range env {
		in.Env[k] = v
	}
	in.Env[plan.TokenEnv] = plan.Token
	in.Env[HalfEnv] = plan.Service
	data, err := json.Marshal(in)
	if err != nil {
		cleanup()
		return nil, fail(err.Error())
	}
	inputPath := filepath.Join(dir, "input.json")
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		cleanup()
		return nil, fail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		cleanup()
		return nil, fail(err.Error())
	}
	// Bounded at open (internal/logcap), as the host-service and socat logs are: past the cap its
	// newest lines move to the one archived generation beside it, rather than every launch that
	// starts this service appending to it forever.
	logFile, err := logcap.Open(logPath, 0o600)
	if err != nil {
		cleanup()
		return nil, fail(err.Error())
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "[yolo launch %d %s] starting %s\n", os.Getpid(),
		time.Now().Format(time.RFC3339), strings.Join(argv, " "))
	readyR, readyW, err := os.Pipe()
	if err != nil {
		cleanup()
		return nil, fail(err.Error())
	}
	lifeR, lifeW, err := os.Pipe()
	if err != nil {
		_ = readyR.Close()
		_ = readyW.Close()
		cleanup()
		return nil, fail(err.Error())
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	reservedFiles, listenFDs := plan.handOver()
	cmd.Env = append(os.Environ(), InputEnv+"="+inputPath,
		paths.JailDaemonReadyFDEnv+"=3", LifelineFDEnv+"=4", ListenFDsEnv+"="+listenFDs)
	cmd.ExtraFiles = append([]*os.File{readyW, lifeR}, reservedFiles...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// Its own process group, so a terminal's Ctrl-C reaches the agent and not the service the
	// agent is talking to.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	_ = readyW.Close()
	_ = lifeR.Close()
	if err != nil {
		_ = readyR.Close()
		_ = lifeW.Close()
		cleanup()
		return nil, fail(err.Error())
	}
	r := &Running{Plan: plan, Argv: argv, Log: logPath, cmd: cmd, lifeline: lifeW, dir: dir,
		done: make(chan struct{}), stopping: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(r.done) }()

	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(readyR).ReadString('\n')
		_ = readyR.Close()
		answer <- strings.TrimSpace(line)
	}()
	select {
	case line := <-answer:
		switch {
		case line == "ready "+plan.Service:
			return r, nil
		case strings.HasPrefix(line, "failed "+plan.Service):
			r.Stop()
			return nil, fail(strings.TrimSpace(strings.TrimPrefix(line, "failed "+plan.Service)))
		case line == "":
			r.Stop()
			return nil, fail("it exited before it reported listening")
		default:
			r.Stop()
			return nil, fail(fmt.Sprintf("it answered %q, which is not a readiness line", line))
		}
	case <-time.After(ReadyTimeout):
		r.Stop()
		return nil, fail(fmt.Sprintf("it was not listening within %s", ReadyTimeout))
	}
}

// Stop ends the service: SIGTERM, then SIGKILL after StopGrace, and removes its input directory.
// Safe to call more than once.
func (r *Running) Stop() {
	select {
	case <-r.stopping:
		<-r.done
		return
	default:
		close(r.stopping)
	}
	_ = r.lifeline.Close()
	select {
	case <-r.done:
	default:
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-r.done:
		case <-time.After(StopGrace):
			_ = r.cmd.Process.Kill()
			<-r.done
		}
	}
	_ = os.RemoveAll(r.dir)
}

// Stopping reports whether Stop has been called.
func (r *Running) stoppingNow() bool {
	select {
	case <-r.stopping:
		return true
	default:
		return false
	}
}

// WatchDeath prints one line when a service exits while the agent still runs, and never restarts
// it: the agent's base URL was fixed when it started (§4.4 item 5).
func WatchDeath(services []*Running, agent string, stderr io.Writer, prefix string) {
	for _, r := range services {
		go func(r *Running) {
			<-r.done
			if r.stoppingNow() {
				return
			}
			fmt.Fprintf(stderr, "%sthe %q service exited while %s runs; it is not restarted, so "+
				"%s's requests to it now fail. Its log: %s\n", prefix, r.Plan.Service, agent, agent, r.Log)
		}(r)
	}
}
