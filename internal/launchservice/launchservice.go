// Package launchservice runs a pack service's HOST HALF as a LAUNCH-OWNED SERVICE: a child of the
// one host or macos-user launch whose agent is paired through the service, on a loopback port that
// launch picked, answering only callers that carry that launch's caller token, and gone when the
// agent exits (docs/design/host-notch-services.md; OQ-HS3 ruled per launch, OQ-HS4 decided as
// leaned). "Launch-owned service" is that doc's term (§1.2).
//
// ONE PATH FOR EVERY HOST HALF. Nothing here knows what the wire bridge is: a service is admitted
// by its declaration (packdecl.ServiceHostDaemon) and its pack's origin
// (packload.Pack.MayRunHostHalf: a pack yolo ships or a local one, never a fetched one), its
// addresses are the adaptations it serves (packload.ServiceAdaptations), and it is handed one
// input file whose shape is this package's contract (Input). `yolo host --` and the macos-user
// launch both call it, so the two notches cannot start a service two ways.
//
// NOT THE JAIL'S SUPERVISOR, and on purpose (the doc's HS-D8 records it, as HS-D28 revised it).
// `yolo-jaild supervise` inherits its readiness pipe from the entrypoint and logs under the jail
// home; a launch-owned service owns its readiness pipe itself and must die with its launch even
// when the launch is SIGKILLed. What the two share is the one contract a daemon speaks, the
// readiness line on the descriptor paths.JailDaemonReadyFDEnv names, which the wire bridge writes
// identically under either, and since HS-D28 the restart policy: a service that dies while its
// agent runs is restarted under its declared `restart`, on the supervisor's schedule, and on the
// SAME address, because the launch keeps the socket it reserved for that address for the
// service's whole life (reserve.go), so the agent's fixed base URL still reaches it
// (Running.Supervise).
package launchservice

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

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
// and a stopped service gets this long between SIGTERM and SIGKILL. A supervised service that
// dies is started again after RestartBackoff, the wait doubling after every restart up to
// RestartBackoffMax: the jail supervisor's 1s to 30s (internal/supervisor), so a service is
// restarted on one schedule at every notch (HS-D28).
var (
	ReadyTimeout      = 5 * time.Second
	StopGrace         = 2 * time.Second
	RestartBackoff    = time.Second
	RestartBackoffMax = 30 * time.Second
)

// restartSleep waits d before a restart, or until stop closes, and reports whether the wait ran
// out (false: the service was stopped meanwhile). A var so a test can record the waits and skip
// them.
var restartSleep = func(d time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}

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

// Declared is a service admitted to run its host half: the holding pack, its argv, and its
// restart policy.
type Declared struct {
	Service string
	Pack    string
	Cmd     []string
	// Restart is the declaration's restart policy, one of the supervisor's three ("always",
	// "on-failure", "no"; internal/loopholedecl.ValidRestarts), "" meaning its default,
	// "on-failure": a service's `jail_daemon.restart`, a doorway's loophole `jail_daemon.restart`.
	// Running.Supervise applies it to a service that dies while its agent runs (HS-D28).
	Restart string
	// Local reports that the pack holding the declaration is a local one (packload.Pack.Local),
	// not one yolo ships: its host code is the user's own, which every notch names, argv and all,
	// before it starts (HS-D27).
	Local bool
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
// must be one whose host code a launch runs (packload.Pack.MayRunHostHalf: a pack yolo ships or
// a local one, so a FETCHED pack's host half is refused by name, failing closed until the
// maintainer rules on third-party host code; HS-D27), and its argv must name `yolo`, the only
// binary the host ships. The Declared carries the service's `jail_daemon.restart`, the policy
// its supervision applies to its host half too ("" when it declares no jail daemon).
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
			"a host half", "a fetched pack's host half does not run on your machine "+
				"(docs/design/host-notch-services.md OQ-HS4)"); why != "" {
			return Declared{}, &AdmissionError{Service: service, Pack: h.Pack, Why: why}
		}
		restart := ""
		if h.Service.JailDaemon != nil {
			restart = h.Service.JailDaemon.Restart
		}
		return Declared{Service: service, Pack: h.Pack, Cmd: append([]string(nil), h.Service.HostDaemon.Cmd...),
			Restart: restart, Local: packLocal(packs, h.Pack)}, nil
	}
	return Declared{}, &AdmissionError{Service: service, Why: "no selected pack declares it"}
}

// AdmitDoorway is Admit's gate for a loophole's DOORWAY opened outside a sandbox
// (docs/design/host-notch-services.md HS-D15; the loophole's `jail_daemon.host_cmd`): the same
// rule, read for a declaration that is not a service's. pack is the pack that ships the
// loophole ("" when none of packs does), and it must be one yolo ships or a local one; cmd, the
// resolved host argv, must name `yolo`; restart is the loophole's `jail_daemon.restart`, which
// the doorway's supervision applies. The Declared it returns carries the loophole's name as its
// Service, the name the readiness line, the input file and the caller token variable all use.
func AdmitDoorway(packs []*packload.Pack, pack, loophole string, cmd []string, restart string) (Declared, error) {
	if pack == "" {
		return Declared{}, &AdmissionError{Service: loophole,
			Why: "no selected pack ships the loophole, so there is no pack whose origin could admit its host argv"}
	}
	if len(cmd) == 0 {
		return Declared{}, &AdmissionError{Service: loophole, Pack: pack,
			Why: "it declares no host argv (`jail_daemon.host_cmd`)"}
	}
	if why := admitHostArgv(packs, pack, cmd, "its jail_daemon.host_cmd", "a doorway outside the sandbox",
		"a fetched pack's doorway does not run on your machine outside the sandbox "+
			"(docs/design/host-notch-services.md OQ-HS4, HS-D15)"); why != "" {
		return Declared{}, &AdmissionError{Service: loophole, Pack: pack, Why: why}
	}
	return Declared{Service: loophole, Pack: pack, Cmd: append([]string(nil), cmd...), Restart: restart,
		Local: packLocal(packs, pack)}, nil
}

// admitHostArgv is the one admission rule both gates apply to host code a launch would run as
// its child (HS-D12, as HS-D27 widened it): the pack must be one whose host code a launch runs
// (packAdmitted), and the argv must name `yolo`, which the launch resolves to its own binary. It
// returns why not, "" when admitted; field and what name the declaration in the refusal, and
// fetched is its sentence for a fetched pack.
//
// THE FETCHED REFUSAL NAMES THE NEXT STEP (docs/reference/happy-path-principle.md): a local
// checkout of the same pack, selected by its file:// path, is the user's own word for the code,
// which is what admits it.
func admitHostArgv(packs []*packload.Pack, pack string, cmd []string, field, what, fetched string) string {
	if !packAdmitted(packs, pack) {
		return "its pack was fetched: it is not one yolo ships, nor one your config selects by " +
			"path, and " + fetched + "; to run it, select a local checkout of the pack by its " +
			"file:// path"
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

// packAdmitted reports whether the pack named name, the LAST pack of the name (the one the
// later-wins rule reads its declaration from), is one whose host code a launch runs
// (packload.Pack.MayRunHostHalf). So a fetched pack that takes an official pack's name later in
// the order is refused, and a local one is admitted.
func packAdmitted(packs []*packload.Pack, name string) bool {
	return holder(packs, name).MayRunHostHalf()
}

// packLocal reports whether the pack named name, read as packAdmitted reads it, is a local one
// and not one yolo ships (Declared.Local).
func packLocal(packs []*packload.Pack, name string) bool {
	p := holder(packs, name)
	return p != nil && p.Local && !p.Official
}

// holder is the LAST pack of packs named name, nil when none is.
func holder(packs []*packload.Pack, name string) *packload.Pack {
	var out *packload.Pack
	for _, p := range packs {
		if p != nil && p.Name == name {
			out = p
		}
	}
	return out
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
	// the pick until Start hands it to the Running, which keeps it for the service's life
	// (reserve.go). An address with none, in a plan built by hand, is one the service binds
	// itself.
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

// Running is one started launch-owned service: a process, and, once its launch supervises it
// (Supervise), every process that replaces it after a death, on the same addresses.
type Running struct {
	Plan *Plan
	Argv []string
	Log  string

	// restart is the declared policy Supervise applies ("" is on-failure).
	restart string
	// input is the marshaled Input, kept so a restart writes the same file again: the service
	// removes the one it reads.
	input []byte
	// dir is the 0700 directory each start writes input.json into, removed with the service.
	dir string
	// reserved is the plan's reservations, taken over at Start and held for the service's whole
	// life (reserve.go): every process of the service is handed the same sockets, so a restarted
	// one listens where its clients were composed, and a request made while it is down waits in
	// the socket's queue instead of being refused. Released when the service is gone for good.
	reserved map[string]*Reserved

	mu sync.Mutex
	// cur is the process serving now, or the last one when the service has ended.
	cur *serviceProcess
	// sup is the supervision Supervise set up, nil until then.
	sup *supervision
	// ended is set, under mu, when the service ended before Supervise was called.
	ended bool
	// backoff is the wait before the next restart (RestartBackoff, doubling).
	backoff time.Duration

	done     chan struct{}
	stopping chan struct{}
	stopOnce sync.Once
}

// serviceProcess is one process of a Running service.
type serviceProcess struct {
	cmd *exec.Cmd
	// lifeline is the write end of the pipe whose read end is the process's descriptor 4: its
	// EOF tells the process its launch is gone.
	lifeline *os.File
	// exited is closed once the process has been reaped; err is cmd.Wait's error, set before.
	exited chan struct{}
	err    error
}

// supervision is where Supervise reports what happens to a service while its agent runs.
type supervision struct {
	agent  string
	w      io.Writer
	prefix string
}

// PID is the process id of the service's current process. Safe from any goroutine.
func (r *Running) PID() int {
	return r.current().cmd.Process.Pid
}

func (r *Running) current() *serviceProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur
}

// Done is closed once the service has stopped for good and been reaped: stopped by Stop, given up
// on under its restart policy, or, never supervised, exited.
func (r *Running) Done() <-chan struct{} { return r.done }

// Start runs plan's host half with env as its input, and returns once it reports it is listening.
// A service that does not within ReadyTimeout, reports failure, or exits first is stopped, and
// the error names it, its argv and its log: the launch then refuses before its agent starts.
//
// THE PLAN'S RESERVED PORTS ARE THE SERVICE'S (reserve.go): each is handed over as a descriptor
// from fd 5, named in ListenFDsEnv, for the service to listen on (Listen), so it serves the port
// its clients were composed with and no other listener could be given it in between. The Running
// takes the reservations over from the plan and keeps its own copies open for the service's life,
// so a process that replaces a dead one is handed the same sockets (HS-D28); they close when the
// service is gone for good, and at once when it does not start.
func Start(plan *Plan, env map[string]string) (*Running, error) {
	argv := SelfExec(plan.Cmd)
	logPath := LogPath(plan.Service)
	reserved := plan.takeReserved()
	fail := func(why string) error {
		ReleaseAll(reserved)
		return fmt.Errorf("the %q service (pack %q) did not start: %s. Its argv: %s. Its log: %s",
			plan.Service, plan.Pack, why, strings.Join(argv, " "), logPath)
	}
	in := Input{Service: plan.Service, Env: map[string]string{}}
	for k, v := range env {
		in.Env[k] = v
	}
	in.Env[plan.TokenEnv] = plan.Token
	in.Env[HalfEnv] = plan.Service
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, fail(err.Error())
	}
	dir, err := os.MkdirTemp("", "yolo-launch-service-")
	if err != nil {
		return nil, fail(err.Error())
	}
	r := &Running{Plan: plan, Argv: argv, Log: logPath, restart: plan.Restart, input: data, dir: dir,
		reserved: reserved, backoff: RestartBackoff, done: make(chan struct{}), stopping: make(chan struct{})}
	p, why := r.spawn("starting")
	if p == nil {
		_ = os.RemoveAll(dir)
		return nil, fail(why)
	}
	r.cur = p
	go r.run()
	return r, nil
}

// spawn starts one process of the service and waits for its readiness line: the input file
// written again (0600, in the Running's 0700 directory), a readiness pipe and a lifeline of its
// own, the Running's reserved sockets from fd 5, and output appended to the service's log under a
// line naming the launch and verb. It returns the process once it is listening, or nil and why
// not, the process stopped: it did not answer within ReadyTimeout, it answered `failed`, it
// exited first, or the service was stopped meanwhile.
func (r *Running) spawn(verb string) (*serviceProcess, string) {
	inputPath := filepath.Join(r.dir, "input.json")
	if err := os.WriteFile(inputPath, r.input, 0o600); err != nil {
		return nil, err.Error()
	}
	// WriteFile keeps an existing file's mode, so a restart's input is made 0600 explicitly.
	if err := os.Chmod(inputPath, 0o600); err != nil {
		return nil, err.Error()
	}
	// Bounded at open (internal/logcap), as the host-service and socat logs are: past the cap its
	// newest lines move to the one archived generation beside it, rather than every launch that
	// starts this service appending to it forever.
	logFile, err := logcap.Open(r.Log, 0o600)
	if err != nil {
		return nil, err.Error()
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "[yolo launch %d %s] %s %s\n", os.Getpid(),
		time.Now().Format(time.RFC3339), verb, strings.Join(r.Argv, " "))
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, err.Error()
	}
	lifeR, lifeW, err := os.Pipe()
	if err != nil {
		_ = readyR.Close()
		_ = readyW.Close()
		return nil, err.Error()
	}
	cmd := exec.Command(r.Argv[0], r.Argv[1:]...)
	reservedFiles, listenFDs := handOver(r.reserved)
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
		return nil, err.Error()
	}
	p := &serviceProcess{cmd: cmd, lifeline: lifeW, exited: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.exited)
	}()

	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(readyR).ReadString('\n')
		_ = readyR.Close()
		answer <- strings.TrimSpace(line)
	}()
	failed := func(why string) (*serviceProcess, string) {
		terminate(p)
		return nil, why
	}
	select {
	case line := <-answer:
		switch {
		case line == "ready "+r.Plan.Service:
			return p, ""
		case strings.HasPrefix(line, "failed "+r.Plan.Service):
			return failed(strings.TrimSpace(strings.TrimPrefix(line, "failed "+r.Plan.Service)))
		case line == "":
			return failed("it exited before it reported listening")
		default:
			return failed(fmt.Sprintf("it answered %q, which is not a readiness line", line))
		}
	case <-time.After(ReadyTimeout):
		return failed(fmt.Sprintf("it was not listening within %s", ReadyTimeout))
	case <-r.stopping:
		return failed("it was stopped before it reported listening")
	}
}

// terminate ends one process of the service: its lifeline closed, then SIGTERM, then SIGKILL
// after StopGrace, and returns once it has been reaped.
func terminate(p *serviceProcess) {
	_ = p.lifeline.Close()
	select {
	case <-p.exited:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(StopGrace):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}

// run owns the service from its start until it is gone for good: it waits on the current
// process, ends it when the service is stopped, and applies the restart policy to a death while
// the service is supervised. Once it returns, the reservations are released, so the port refuses
// connections rather than queueing them, and the input directory is removed.
func (r *Running) run() {
	defer func() {
		ReleaseAll(r.reserved)
		_ = os.RemoveAll(r.dir)
		close(r.done)
	}()
	for {
		p := r.current()
		select {
		case <-p.exited:
		case <-r.stopping:
			terminate(p)
			return
		}
		_ = p.lifeline.Close()
		r.mu.Lock()
		sup := r.sup
		if sup == nil {
			r.ended = true
		}
		r.mu.Unlock()
		if r.stoppingNow() || sup == nil {
			// Not supervised (yet): the service is gone, and Supervise names it when it is
			// called.
			return
		}
		if !r.restartAfter(p, sup) {
			return
		}
	}
}

// Stop ends the service: SIGTERM, then SIGKILL after StopGrace, a backoff before a restart cut
// short, and its input directory and reservations released. Silent: a service its own launch
// stops is no death to report. Safe to call more than once and from any goroutine.
func (r *Running) Stop() {
	r.stopOnce.Do(func() { close(r.stopping) })
	<-r.done
}

// stoppingNow reports whether Stop has been called.
func (r *Running) stoppingNow() bool {
	select {
	case <-r.stopping:
		return true
	default:
		return false
	}
}

// Supervise watches the service for the rest of agent's run, writing to w, each line led by
// prefix: a service that dies is named, with its exit status and its log, in one line, and then
// restarted under its declared restart policy (Declared.Restart; HS-D28). "no" leaves it down, and
// so does "on-failure", the default, after a clean exit; otherwise it is started again after the
// supervisor's backoff (RestartBackoff, doubling to RestartBackoffMax), on the same sockets, so
// the agent's requests to it wait rather than fail, and one more line says it is back. A restart
// that does not come back is named and tried again. A service that died before Supervise was
// called is named, and not restarted. A Stop, the launch's own teardown, is never reported, and
// cuts a backoff short. Call it once, after the agent starts; a second call does nothing.
func (r *Running) Supervise(agent string, w io.Writer, prefix string) {
	r.mu.Lock()
	if r.sup != nil {
		r.mu.Unlock()
		return
	}
	r.sup = &supervision{agent: agent, w: w, prefix: prefix}
	ended, p := r.ended, r.cur
	r.mu.Unlock()
	if !ended {
		return
	}
	<-r.done
	if r.stoppingNow() {
		return
	}
	status, _ := exitStatusOf(p.err)
	fmt.Fprintf(w, "%sthe %q service (pack %q) exited (%s) before %s started, so it is not "+
		"restarted, and %s's requests to it fail until %s is launched again. Its log: %s\n",
		prefix, r.Plan.Service, r.Plan.Pack, status, agent, agent, agent, r.Log)
}

// restartAfter applies the restart policy to p's death, reporting through sup: false when the
// service stays down (the policy, or a Stop), true once a new process is serving.
func (r *Running) restartAfter(p *serviceProcess, sup *supervision) bool {
	say := func(format string, args ...any) {
		fmt.Fprintf(sup.w, "%s%s\n", sup.prefix, fmt.Sprintf(format, args...))
	}
	svc, pack, agent := r.Plan.Service, r.Plan.Pack, sup.agent
	status, failed := exitStatusOf(p.err)
	switch {
	case r.restart == "no":
		say("the %q service (pack %q) exited (%s) while %s runs, and its restart policy is \"no\", "+
			"so it is not restarted: %s's requests to it fail until %s is launched again. Its log: %s",
			svc, pack, status, agent, agent, agent, r.Log)
		return false
	case !failed && r.restart != "always":
		say("the %q service (pack %q) exited cleanly while %s runs, and its restart policy "+
			"(\"on-failure\") restarts it only after a failure, so %s's requests to it fail until %s "+
			"is launched again. Its log: %s", svc, pack, agent, agent, agent, r.Log)
		return false
	}
	where := r.restartWhere(agent)
	say("the %q service (pack %q) exited (%s) while %s runs; restarting it in %s%s. Its log: %s",
		svc, pack, status, agent, r.backoff, where, r.Log)
	for {
		if !restartSleep(r.backoff, r.stopping) {
			return false
		}
		r.backoff *= 2
		if r.backoff > RestartBackoffMax {
			r.backoff = RestartBackoffMax
		}
		next, why := r.spawn("restarting")
		if next != nil {
			r.mu.Lock()
			r.cur = next
			r.mu.Unlock()
			on := ""
			if addrs := r.Plan.Addresses(); len(addrs) > 0 {
				on = " on " + strings.Join(addrs, ", ")
			}
			say("the %q service is back (pid %d)%s for %s.", svc, next.cmd.Process.Pid, on, agent)
			return true
		}
		if r.stoppingNow() {
			return false
		}
		say("the %q service (pack %q) did not come back: %s; trying again in %s. Its log: %s",
			svc, pack, why, r.backoff, r.Log)
	}
}

// restartWhere is the clause a restart line adds about the service's addresses: the same ones,
// held for it meanwhile when the launch reserved them.
func (r *Running) restartWhere(agent string) string {
	addrs := r.Plan.Addresses()
	if len(addrs) == 0 {
		return ""
	}
	if len(r.reserved) > 0 {
		return fmt.Sprintf(" on the same address (%s), which holds %s's requests until it is back",
			strings.Join(addrs, ", "), agent)
	}
	return " on " + strings.Join(addrs, ", ")
}

// exitStatusOf words a process's exit for a line, and reports whether it failed: anything but a
// clean exit with status 0, a signal death included.
func exitStatusOf(err error) (string, bool) {
	if err == nil {
		return "status 0", false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "killed by " + unix.SignalName(ws.Signal()), true
		}
		return fmt.Sprintf("status %d", exitErr.ExitCode()), exitErr.ExitCode() != 0
	}
	return err.Error(), true
}
