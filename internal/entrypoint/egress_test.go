package entrypoint

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeEgress is a dialer that never touches the network: each address answers with the
// behaviour named for it, and every address dialled is recorded.
type fakeEgress struct {
	mu     sync.Mutex
	dialed []string
	// answer returns the conn or error for one address; nil means "reachable".
	answer func(ctx context.Context, addr string) (net.Conn, error)
}

func (f *fakeEgress) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	f.mu.Lock()
	f.dialed = append(f.dialed, addr)
	f.mu.Unlock()
	if f.answer != nil {
		return f.answer(ctx, addr)
	}
	return reachableConn(), nil
}

// reachableConn is one end of a pipe, the other end discarded: a connection that opened.
func reachableConn() net.Conn {
	c, s := net.Pipe()
	go func() { _ = s.Close() }()
	return c
}

func dnsFailure(host string) error {
	return &net.OpError{Op: "dial", Net: "tcp",
		Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
}

// blackhole never answers: it returns only when the probe's context ends, as a dropped SYN does.
func blackhole(ctx context.Context, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: ctx.Err()}
}

// runEgress runs the probe over targets with dial and returns the terminal and boot-log text.
func runEgress(t *testing.T, vars map[string]string, native bool, targets []string, dial egressDialer) (term, log string) {
	t.Helper()
	return runEgressWithin(t, 200*time.Millisecond, vars, native, targets, dial)
}

// proxyExchangeGuard is the probe budget for a test whose fake answers on its own, so the budget
// is only a failure guard that turns a hang into a red test. A test whose fake never answers
// (blackhole) needs the short budget to expire, and uses runEgress.
const proxyExchangeGuard = 60 * time.Second

func runEgressWithin(t *testing.T, budget time.Duration, vars map[string]string, native bool, targets []string, dial egressDialer) (term, log string) {
	t.Helper()
	var termBuf, logBuf strings.Builder
	e := NewEnv(vars)
	e.GNUStat = !native
	e.Stderr, e.LogOnly = &termBuf, &logBuf
	reportEgress(e, startEgressProbe(e, targets, dial, budget))
	if err := genFailuresError(e); err != nil {
		t.Errorf("the egress check must never refuse a boot, got %v", err)
	}
	return termBuf.String(), logBuf.String()
}

var oneTarget = []string{"github.com:443"}

func TestEgressProbeIsSilentWhenReachable(t *testing.T) {
	f := &fakeEgress{}
	term, log := runEgress(t, nil, false, oneTarget, f.dial)
	if term != "" {
		t.Errorf("a reachable jail must print nothing, got:\n%s", term)
	}
	if !strings.Contains(log, "egress: reached github.com:443") {
		t.Errorf("a healthy probe must leave a positive record in the boot log, got:\n%s", log)
	}
}

func TestEgressProbeNamesADNSFailure(t *testing.T) {
	f := &fakeEgress{answer: func(context.Context, string) (net.Conn, error) { return nil, dnsFailure("github.com") }}
	term, _ := runEgress(t, nil, false, oneTarget, f.dial)
	for _, want := range []string{"no outbound network access", "github.com:443: the name did not resolve",
		"no such host", "Next step:"} {
		if !strings.Contains(term, want) {
			t.Errorf("warning lacks %q:\n%s", want, term)
		}
	}
	if !strings.HasPrefix(term, "warning: ") {
		t.Errorf("it is a warning, never an error:\n%s", term)
	}
}

func TestEgressProbeNamesATimeoutWithinItsBudget(t *testing.T) {
	f := &fakeEgress{answer: blackhole}
	start := time.Now()
	term, _ := runEgress(t, nil, false, oneTarget, f.dial)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("a blackholed network must cost no more than the budget, took %s", took)
	}
	if !strings.Contains(term, "github.com:443: the connection timed out") {
		t.Errorf("a timeout must be named as one, distinct from DNS:\n%s", term)
	}
	if strings.Contains(term, "did not resolve") {
		t.Errorf("a timeout must not read as a DNS failure:\n%s", term)
	}
}

func TestEgressProbeNamesARefusedConnection(t *testing.T) {
	f := &fakeEgress{answer: func(context.Context, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}}
	term, _ := runEgress(t, nil, false, oneTarget, f.dial)
	if !strings.Contains(term, "the connection failed (connection refused)") {
		t.Errorf("got:\n%s", term)
	}
}

// A LOOKUP THAT TIMES OUT IS SLOW DNS, NOT "NO NETWORK"; a definitive failure keeps the
// no-network fault. The classification table the warning is chosen from.
func TestClassifyEgressDialTellsSlowDNSFromADefinitiveFailure(t *testing.T) {
	op := func(err error) error { return &net.OpError{Op: "dial", Net: "tcp", Err: err} }
	cases := []struct {
		name     string
		err      error
		viaProxy bool
		want     egressFault
	}{
		{"lookup timed out", op(&net.DNSError{Err: "i/o timeout", Name: "github.com", IsTimeout: true}), false, egressSlowDNS},
		{"lookup outlived the context", op(&net.DNSError{Err: context.DeadlineExceeded.Error(), Name: "github.com",
			UnwrapErr: context.DeadlineExceeded}), false, egressSlowDNS},
		{"proxy lookup timed out", op(&net.DNSError{Err: "i/o timeout", Name: "proxy", IsTimeout: true}), true, egressSlowDNS},
		{"no such host", dnsFailure("github.com"), false, egressDNS},
		{"proxy: no such host", dnsFailure("proxy"), true, egressProxyDNS},
		{"server failure", op(&net.DNSError{Err: "server misbehaving", Name: "github.com"}), false, egressDNS},
		{"connection refused", op(syscall.ECONNREFUSED), false, egressConnect},
		{"no route", op(syscall.EHOSTUNREACH), false, egressConnect},
		{"network unreachable", op(syscall.ENETUNREACH), false, egressConnect},
		{"resolved, then the connection timed out", op(context.DeadlineExceeded), false, egressTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, detail := classifyEgressDial(c.err, c.viaProxy); got != c.want {
				t.Errorf("classified as %d (%s), want %d", got, detail, c.want)
			}
		})
	}
}

// What Go's own resolver returns when the probe's budget ends mid-lookup must land on the
// slow-DNS fault: a resolver that receives the query and never answers, as a VPN's slow one does
// for longer than the budget. No packet leaves the machine.
func TestARealResolverTimeoutIsSlowDNS(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	defer silent.Close()
	d := &net.Dialer{Resolver: &net.Resolver{PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", silent.LocalAddr().String())
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = d.DialContext(ctx, "tcp", "slow-dns.example:443")
	if err == nil {
		t.Fatal("a resolver that never answers resolved the name")
	}
	if got, detail := classifyEgressDial(err, false); got != egressSlowDNS {
		t.Errorf("%v classified as %d (%s), want slow DNS", err, got, detail)
	}
}

// The terminal text for slow DNS says so, never "no outbound network", and names a next step.
// One target's definitive failure beside another's slow lookup still does not prove the jail
// offline, so it gets the slow-DNS text too.
func TestEgressProbeCallsADNSTimeoutSlowNotOffline(t *testing.T) {
	slow := func(context.Context, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: "i/o timeout", Name: "github.com", IsTimeout: true}}
	}
	podman := []string{"warning: DNS lookups in this jail took longer than 200ms",
		"outbound access was not confirmed", "github.com:443: looking up the name outlasted",
		"Next step:", "time getent hosts github.com", "podman machine stop"}
	for _, c := range []struct {
		name    string
		native  bool
		targets []string
		answer  func(context.Context, string) (net.Conn, error)
		want    []string
		notWant string
	}{
		{"one target", false, oneTarget, slow, podman, "dscacheutil"},
		{"beside a refusal", false, []string{"api.vendor.example:443", "github.com:443"},
			func(ctx context.Context, addr string) (net.Conn, error) {
				if strings.HasPrefix(addr, "api.vendor.example") {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				return slow(ctx, addr)
			}, podman, "dscacheutil"},
		// macOS has no getent, so the macos-user sandbox names dscacheutil.
		{"macos-user", true, oneTarget, slow, []string{
			"warning: DNS lookups in this sandbox took longer than 200ms",
			"outbound access was not confirmed", "Next step:",
			"time dscacheutil -q host -a name github.com", "this sandbox uses the Mac's own network"}, "getent"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeEgress{answer: c.answer}
			vars := map[string]string{paths.JailBackendEnv: "podman"}
			if c.native {
				vars = nil
			}
			term, log := runEgress(t, vars, c.native, c.targets, f.dial)
			for _, want := range c.want {
				if !strings.Contains(term, want) {
					t.Errorf("warning lacks %q:\n%s", want, term)
				}
			}
			if strings.Contains(term, c.notWant) {
				t.Errorf("warning names %q, the other backend's lookup:\n%s", c.notWant, term)
			}
			if strings.Contains(term, "no outbound network") {
				t.Errorf("a DNS timeout must not read as no network:\n%s", term)
			}
			if !strings.Contains(log, "a DNS lookup outlasted") {
				t.Errorf("the boot log must record the slow-DNS verdict:\n%s", log)
			}
		})
	}
}

// ANY target answering is a jail with a network: one vendor's outage or one blocked domain is not
// what this check reports.
func TestEgressProbeIsSilentWhenAnyTargetAnswers(t *testing.T) {
	f := &fakeEgress{answer: func(ctx context.Context, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "api.vendor.example") {
			return blackhole(ctx, addr)
		}
		return reachableConn(), nil
	}}
	term, _ := runEgress(t, nil, false, []string{"api.vendor.example:443", "github.com:443"}, f.dial)
	if term != "" {
		t.Errorf("one reachable target means the jail has a network, got:\n%s", term)
	}
}

// proxyServer answers one CONNECT on a pipe with status, recording the request it read.
func proxyServer(t *testing.T, status string, got *http.Request, mu *sync.Mutex) net.Conn {
	t.Helper()
	c, s := net.Pipe()
	go func() {
		defer s.Close()
		req, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		mu.Lock()
		*got = *req
		mu.Unlock()
		_, _ = s.Write([]byte("HTTP/1.1 " + status + "\r\n\r\n"))
	}()
	return c
}

func TestEgressProbeGoesThroughTheProxyTheEnvironmentNames(t *testing.T) {
	var mu sync.Mutex
	var req http.Request
	f := &fakeEgress{answer: func(_ context.Context, addr string) (net.Conn, error) {
		if addr != "proxy.corp.example:3128" {
			return nil, errors.New("dialled " + addr + " directly with a proxy set")
		}
		return proxyServer(t, "200 Connection established", &req, &mu), nil
	}}
	vars := map[string]string{"HTTPS_PROXY": "http://alice:s3cret@proxy.corp.example:3128"}
	term, log := runEgressWithin(t, proxyExchangeGuard, vars, false, oneTarget, f.dial)
	if term != "" {
		t.Errorf("a proxy that connects is a working network, got:\n%s", term)
	}
	if !strings.Contains(log, "via proxy proxy.corp.example:3128") {
		t.Errorf("the record must say the proxy carried it, got:\n%s", log)
	}
	mu.Lock()
	defer mu.Unlock()
	if req.Method != http.MethodConnect || req.Host != "github.com:443" {
		t.Errorf("the proxy must be asked to CONNECT the target, got %s %s", req.Method, req.Host)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret")); req.Header.Get("Proxy-Authorization") != want {
		t.Errorf("the proxy URL's credentials must be offered, got %q", req.Header.Get("Proxy-Authorization"))
	}
}

func TestEgressProbeNamesAProxyThatCannotReachOut(t *testing.T) {
	var mu sync.Mutex
	var req http.Request
	f := &fakeEgress{answer: func(context.Context, string) (net.Conn, error) {
		return proxyServer(t, "502 Bad Gateway", &req, &mu), nil
	}}
	vars := map[string]string{"https_proxy": "http://alice:s3cret@proxy.corp.example:3128"}
	term, _ := runEgressWithin(t, proxyExchangeGuard, vars, false, oneTarget, f.dial)
	for _, want := range []string{"the proxy proxy.corp.example:3128 named by https_proxy",
		"answered CONNECT with 502 Bad Gateway"} {
		if !strings.Contains(term, want) {
			t.Errorf("warning lacks %q:\n%s", want, term)
		}
	}
	if strings.Contains(term, "s3cret") {
		t.Errorf("the warning must never print the proxy's credentials:\n%s", term)
	}
}

func TestEgressProbeNamesAnUnreachableProxy(t *testing.T) {
	f := &fakeEgress{answer: blackhole}
	vars := map[string]string{"HTTPS_PROXY": "127.0.0.1:3128"}
	term, _ := runEgress(t, vars, false, oneTarget, f.dial)
	for _, want := range []string{"the proxy 127.0.0.1:3128 named by HTTPS_PROXY could not be reached",
		"not reachable at that address from inside a container"} {
		if !strings.Contains(term, want) {
			t.Errorf("warning lacks %q:\n%s", want, term)
		}
	}
}

func TestEgressProbeHonorsNoProxy(t *testing.T) {
	f := &fakeEgress{}
	vars := map[string]string{"HTTPS_PROXY": "http://proxy.corp.example:3128", "NO_PROXY": "localhost,.github.com"}
	runEgress(t, vars, false, oneTarget, f.dial)
	if len(f.dialed) != 1 || f.dialed[0] != "github.com:443" {
		t.Errorf("a NO_PROXY host must be dialled directly, dialled %v", f.dialed)
	}
}

func TestNoProxyMatching(t *testing.T) {
	cases := []struct {
		noProxy, host string
		want          bool
	}{
		{"*", "github.com", true},
		{"github.com", "github.com", true},
		{"github.com", "api.github.com", true},
		{".github.com", "github.com", true},
		{"github.com:443", "github.com", true},
		{"hub.com", "github.com", false},
		{"", "github.com", false},
	}
	for _, c := range cases {
		if got := noProxyMatches(c.noProxy, c.host); got != c.want {
			t.Errorf("noProxyMatches(%q, %q) = %v, want %v", c.noProxy, c.host, got, c.want)
		}
	}
}

// THE NEXT STEP IS THE BACKEND'S: Apple Container's restart is wrong advice under podman, and a
// container's advice is wrong on the Mac's own network.
func TestEgressWarningNamesEachBackendsNextStep(t *testing.T) {
	cases := []struct {
		name   string
		vars   map[string]string
		native bool
		want   string
	}{
		{"apple container", map[string]string{paths.JailBackendEnv: "container"}, false,
			"container system stop && container system start"},
		{"podman", map[string]string{paths.JailBackendEnv: "podman"}, false, "podman machine stop && podman machine start"},
		{"older launcher", nil, false, "podman machine stop && podman machine start"},
		{"shared namespace", map[string]string{paths.JailBackendEnv: "podman",
			paths.HostLoopbackEnvVar: paths.HostLoopbackShared}, false, "shares the network of whatever launched it"},
		{"macos-user", nil, true, "this sandbox uses the Mac's own network"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeEgress{answer: blackhole}
			term, _ := runEgress(t, c.vars, c.native, oneTarget, f.dial)
			if !strings.Contains(term, c.want) {
				t.Errorf("warning lacks %q:\n%s", c.want, term)
			}
			if c.want != "container system stop && container system start" && strings.Contains(term, "container system") {
				t.Errorf("only Apple Container gets its restart:\n%s", term)
			}
		})
	}
}

func TestEgressTargetsComeFromThePacksThenTheNeutralHost(t *testing.T) {
	packs := []*packload.Pack{
		{Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, URL: "https://vendor-b.example/install.sh"},
			{Kind: packdecl.KindProvider, Endpoints: map[string]packdecl.ProviderEndpoint{
				"x": {BaseURL: "https://api.vendor-a.example/v1"},
				"y": {BaseURL: "http://localhost:8080/v1"},
				"z": {BaseURL: "https://10.0.0.5/v1"},
			}},
		}}},
		{Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, URL: "https://vendor-b.example/other.sh"},
			{Kind: packdecl.KindProgram, URL: "https://vendor-c.example:8443/i.sh"},
			{Kind: packdecl.KindProgram, URL: "https://vendor-d.example/i.sh"},
			{Kind: packdecl.KindProvider, Endpoints: map[string]packdecl.ProviderEndpoint{
				"x": {BaseURL: "https://host.containers.internal:9/v1"},
			}},
		}}},
		nil,
	}
	got := egressTargets(packs)
	want := []string{"api.vendor-a.example:443", "vendor-b.example:443", "vendor-c.example:8443", "github.com:443"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("egressTargets = %v, want %v (sorted, deduped, capped, local hosts skipped, neutral last)", got, want)
	}
	if got := egressTargets(nil); len(got) != 1 || got[0] != "github.com:443" {
		t.Errorf("a jail with no packs probes the neutral host alone, got %v", got)
	}
}

// THE CALL SITE. The boot table's two egress steps, run as the boot runs them, must start the
// probe and report it; deleting either step from the table, or emptying its body, fails here.
// Both boots run both steps (the order tests in bootsteps_test.go pin where).
func TestTheBootTableRunsTheEgressCheck(t *testing.T) {
	f := &fakeEgress{answer: func(context.Context, string) (net.Conn, error) { return nil, dnsFailure("github.com") }}
	egressDialOverride = f.dial
	t.Cleanup(func() { egressDialOverride = nil })

	for _, target := range []bootTarget{bootContainer, bootDarwin} {
		t.Run(target.String(), func(t *testing.T) {
			var term strings.Builder
			e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
			e.GNUStat = target == bootContainer
			e.Stderr = &term
			b := &bootRun{e: e, target: target}
			runSteps(b, []bootStep{mustBootStep(t, "start_egress_probe"), mustBootStep(t, "report_egress")})
			if !strings.Contains(term.String(), "no outbound network access") {
				t.Errorf("the boot's egress steps did not report a jail with no network:\n%s", term.String())
			}
			if err := genFailuresError(e); err != nil {
				t.Errorf("the egress check must never refuse a boot, got %v", err)
			}
		})
	}
	for _, target := range []bootTarget{bootContainer, bootDarwin} {
		names := strings.Join(bootStepNames(target), " ")
		if !strings.Contains(names, "start_egress_probe") || !strings.Contains(names, "report_egress") {
			t.Errorf("the %s boot must run both egress steps: %s", target, names)
		}
	}
}

// A unit test binary never dials the internet unless a test hands the boot a dialer.
func TestTheBootNeverDialsFromATestBinary(t *testing.T) {
	if _, ok := bootEgressDialer(); ok {
		t.Error("with no override, a test binary's boot must not dial the internet")
	}
}
