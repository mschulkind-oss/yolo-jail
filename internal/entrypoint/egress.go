package entrypoint

// egress.go is the boot's OUTBOUND-NETWORK check: does this jail reach the internet at all?
//
// # Why it exists
//
// A jail with no route out does not look like one. Every client fails on its own terms, later,
// and names itself rather than the network: an agent's login refresh, an npm install, a model
// call. Measured 2026-10 on Apple Container 1.5.0, where the container VMs carried no traffic out
// while the Mac reached everything (a plain `container run curlimages/curl https://github.com/`
// timed out): the only symptom anyone saw was a Claude authentication failure, and it took four
// rounds of support to find the network under it. One line at launch says it directly.
//
// # A WARNING, never a refusal
//
// Offline work is legitimate — a plane, an air-gapped machine, a jail used only to build what is
// already cached — so nothing here touches the boot's refusal gate, and there is no opt-out: a
// line that only informs has nothing to escape (the escape-hatch stance in AGENTS.md: a hatch is
// for broken user config, and this check refuses nothing). It names the next step per backend,
// which is the happy-path principle (docs/reference/happy-path-principle.md).
//
// # Cheap, bounded, and off the boot's critical path
//
// startEgressProbe runs near the TOP of the boot table and only spawns goroutines; reportEgress
// runs just above the service witness at the bottom and collects them. Every dial shares one
// context of egressBudget, so a healthy jail's answer (one TCP handshake, tens of milliseconds)
// is in long before the boot needs it, and a blackholed one costs at most egressBudget of
// wall-clock that the boot spent doing its own work anyway.
//
// # What it dials, and why not TLS
//
// A TCP connection to port 443 (or the declared URL's port), directly or through the proxy the
// jail's environment names. That is the whole question — "does traffic leave this jail" — and it
// is the fault the motivating case had. A TLS handshake would add a second, different question
// (does this jail TRUST what answers), which depends on the CA bundle the boot only assembles
// later (generate_ca_bundle), and asking it first would also freeze Go's process-wide root pool
// before that bundle exists. A TLS-inspecting corporate proxy is not "no network" and must not
// read as one.
//
// The targets are the HOSTS THE SELECTED PACKS DECLARE — every program installer URL and every
// provider endpoint (egressTargets) — because those are the hosts this jail's agents will
// actually need, and reading them from the packs keeps core free of any vendor's name. They are
// joined by one neutral host, github.com, which every jail reaches for whatever its packs say
// (mise's tool downloads are GitHub releases, and so are most installers' payloads), and which is
// the probe on a jail with no packs. The verdict is "reachable" when ANY target answers, so one
// vendor's outage or one blocked domain never reads as a jail with no network.
//
// # A DNS lookup that times out is slow DNS, not "no network"
//
// A lookup still pending when the budget runs out proves nothing about the network: measured
// 2026-10-24 on a Mac behind the AWS VPN client, every launch's first lookup through the jail's
// resolver took about 4 s while curl reached github.com fine, and the old wording called that
// jail offline. So a lookup timeout is its own fault (egressSlowDNS), and when any target ended
// on one the warning says DNS was slow and outbound access was NOT CONFIRMED, never that there is
// no network. A definitive answer (no such host, connection refused, no route, a connection that
// resolved and then timed out) keeps the "no outbound network access" warning.

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// egressBudget bounds the whole probe: every target's dial, a proxy's CONNECT exchange
// included, shares one context of this length. A few seconds is long enough for a slow but
// working network's first handshake, including a DNS lookup on a cold cache, and short enough
// that a blackholed jail's warning arrives while the boot is still busy with its own steps.
var egressBudget = 3 * time.Second

// egressNeutralHost is the one target core names itself — see the file comment for why it is
// this host and not a vendor's.
const egressNeutralHost = "github.com"

// egressMaxPackTargets caps how many pack-declared hosts are dialled, so a jail that selects
// many provider packs still opens a handful of connections at boot, not one per vendor.
const egressMaxPackTargets = 3

// egressDialer is the probe's one seam onto the network: tests hand it a fake, production the
// zero net.Dialer. A DNS failure arrives from it as a *net.DNSError inside the dial error, which
// is how classifyEgressDial tells name resolution from a connection that went nowhere — and the
// dialer's own Happy Eyeballs means an IPv6 address a jail cannot route is not mistaken for the
// IPv4 one it can.
type egressDialer func(ctx context.Context, network, addr string) (net.Conn, error)

var defaultEgressDialer egressDialer = (&net.Dialer{}).DialContext

// egressFault is WHY a target was not reached, because each sends the reader somewhere else.
type egressFault int

const (
	egressReached egressFault = iota
	// egressDNS: the name did not resolve — the resolver answered that it does not exist, or
	// failed outright.
	egressDNS
	// egressSlowDNS: the lookup (of the target, or of the proxy when proxy is set) was still
	// pending when the budget ran out. Not evidence of a missing network; see the file comment.
	egressSlowDNS
	// egressTimeout: the name resolved, and the connection never completed within the budget —
	// the signature of a network that drops traffic, the motivating case's.
	egressTimeout
	// egressConnect: the connection failed outright (refused, network unreachable, no route).
	egressConnect
	// egressProxyDNS / egressProxyConnect: the proxy the environment names could not be
	// resolved / connected to — the jail never reached its way out.
	egressProxyDNS
	egressProxyConnect
	// egressProxyRefused: the proxy answered, and its CONNECT answer was not a success — the
	// proxy itself could not reach the target, or will not.
	egressProxyRefused
)

// egressResult is one target's verdict.
type egressResult struct {
	target string // host:port
	fault  egressFault
	detail string // the short error, or the proxy's status line
	proxy  string // the proxy's host:port when one was used, never its credentials
	took   time.Duration
}

// egressProxy is the proxy one target is dialled through, resolved from the environment once,
// synchronously, before any goroutine runs (startEgressProbe says why).
type egressProxy struct {
	u      *url.URL
	source string // the variable that named it: HTTPS_PROXY or https_proxy
}

// egressProbe is a probe in flight: started near the top of the boot, collected near the bottom.
type egressProbe struct {
	done    chan struct{}
	results []egressResult

	// The facts the report needs, read from the Env when the probe started — the probe's
	// goroutines never touch the Env, whose Vars map later boot steps write.
	backend     string
	budget      time.Duration
	disposition loopbackDisposition
	native      bool
	proxySource string
}

// startEgressProbe reads every input the probe needs from e NOW — targets, proxy variables,
// backend — and dials in the background. The synchronous read is load-bearing rather than
// tidy: e.Vars is a plain map that later boot steps write (generate_ca_bundle's setEnvBoth), so
// a goroutine reading it would race them.
func startEgressProbe(e *Env, targets []string, dial egressDialer, budget time.Duration) *egressProbe {
	p := &egressProbe{
		done:        make(chan struct{}),
		results:     make([]egressResult, len(targets)),
		backend:     e.Getenv(paths.JailBackendEnv),
		budget:      budget,
		disposition: launcherLoopbackDisposition(e),
		native:      nativeSandbox(e),
	}
	proxies := make([]*egressProxy, len(targets))
	for i, t := range targets {
		host, _, _ := net.SplitHostPort(t)
		proxies[i] = egressProxyFor(e, host)
		if proxies[i] != nil {
			p.proxySource = proxies[i].source
		}
	}
	go func() {
		defer close(p.done)
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		finished := make(chan struct{}, len(targets))
		for i, t := range targets {
			go func(i int, t string) {
				defer func() { finished <- struct{}{} }()
				start := time.Now()
				res := probeEgressTarget(ctx, dial, t, proxies[i])
				res.took = time.Since(start)
				if res.fault == egressTimeout {
					res.detail = "no answer in " + budget.String()
				}
				p.results[i] = res
			}(i, t)
		}
		for range targets {
			<-finished
		}
	}()
	return p
}

// wait returns the results once every target has answered or run out of budget. Every dial is
// bound to the probe's context, so this returns within the budget plus the time a dial takes to
// notice its cancellation.
func (p *egressProbe) wait() []egressResult {
	<-p.done
	return p.results
}

// probeEgressTarget dials one host:port, through proxy when it is non-nil.
func probeEgressTarget(ctx context.Context, dial egressDialer, target string, proxy *egressProxy) egressResult {
	res := egressResult{target: target}
	if proxy == nil {
		conn, err := dial(ctx, "tcp", target)
		if err != nil {
			res.fault, res.detail = classifyEgressDial(err, false)
			return res
		}
		_ = conn.Close()
		return res
	}

	res.proxy = proxy.u.Host
	conn, err := dial(ctx, "tcp", proxy.u.Host)
	if err != nil {
		res.fault, res.detail = classifyEgressDial(err, true)
		return res
	}
	defer conn.Close()
	// Only an http:// proxy speaks a CONNECT this probe can send in the clear. Any other scheme
	// (https://, socks5://) is checked as far as a TCP connection to it, which is still the
	// question the motivating case failed: whether traffic leaves the jail at all.
	if proxy.u.Scheme != "http" {
		return res
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if user := proxy.u.User; user != nil {
		pass, _ := user.Password()
		req += "Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+pass)) + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		res.fault, res.detail = egressProxyConnect, shortNetErr(err)
		return res
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		res.fault, res.detail = egressProxyConnect, "no answer to CONNECT: "+shortNetErr(err)
		return res
	}
	_ = resp.Body.Close()
	// 407 is the proxy demanding credentials: traffic reached the proxy, and whether this jail
	// holds the right ones is a configuration question for the client that uses it, not a jail
	// with no network.
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusProxyAuthRequired {
		return res
	}
	res.fault, res.detail = egressProxyRefused, resp.Status
	return res
}

// classifyEgressDial maps a dial error onto the fault the reader acts on.
func classifyEgressDial(err error, viaProxy bool) (egressFault, string) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout || isEgressTimeout(err) {
			return egressSlowDNS, "DNS: lookup timed out"
		}
		if viaProxy {
			return egressProxyDNS, "DNS: " + dnsErrSummary(dnsErr)
		}
		return egressDNS, "DNS: " + dnsErrSummary(dnsErr)
	}
	if viaProxy {
		return egressProxyConnect, shortNetErr(err)
	}
	if isEgressTimeout(err) {
		return egressTimeout, "no answer within the probe's budget"
	}
	return egressConnect, shortNetErr(err)
}

func dnsErrSummary(d *net.DNSError) string {
	switch {
	case d.IsTimeout:
		return "lookup timed out"
	case d.IsNotFound:
		return "no such host"
	}
	return d.Err
}

func isEgressTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// shortNetErr is the end of a dial error's chain — "connection refused", "network is
// unreachable" — rather than the whole "dial tcp 1.2.3.4:443: connect: …" sentence.
func shortNetErr(err error) string {
	if isEgressTimeout(err) {
		return "timed out"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// egressProxyFor resolves the proxy for host the way Go's own net/http (golang.org/x/net's
// httpproxy) does: HTTPS_PROXY before https_proxy, and NO_PROXY before no_proxy, a NO_PROXY
// match dialling direct. The targets are all https endpoints, so HTTP_PROXY does not apply.
// A value with no scheme is an http:// proxy, as both Go and curl read it. nil is "dial direct".
func egressProxyFor(e *Env, host string) *egressProxy {
	raw, source := "", ""
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		if v := strings.TrimSpace(e.Getenv(k)); v != "" {
			raw, source = v, k
			break
		}
	}
	if raw == "" {
		return nil
	}
	noProxy := e.Getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = e.Getenv("no_proxy")
	}
	if noProxyMatches(noProxy, host) {
		return nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	if u.Port() == "" {
		port := "80"
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	return &egressProxy{u: u, source: source}
}

// noProxyMatches is NO_PROXY's domain rule as Go and curl share it: `*` matches everything, and
// an entry matches the host itself and every subdomain, with or without a leading dot. A port on
// an entry is ignored (every target here is on the same scheme's port anyway).
func noProxyMatches(noProxy, host string) bool {
	host = strings.ToLower(host)
	for _, entry := range strings.Split(noProxy, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if h, _, err := net.SplitHostPort(entry); err == nil {
			entry = h
		}
		entry = strings.TrimPrefix(entry, ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// egressTargets is the host:port list the probe dials: the selected packs' declared https/http
// hosts (program installer URLs, provider endpoints), sorted and capped, then the neutral host.
// A host that cannot be on the far side of the internet — localhost, a private or loopback
// literal, a `.internal`/`.local` name — is skipped: a provider pointed at a proxy on the user's
// own machine says nothing about whether this jail reaches the internet.
func egressTargets(packs []*packload.Pack) []string {
	seen := map[string]bool{}
	var declared []string
	add := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return
		}
		host := strings.ToLower(u.Hostname())
		if !publicEgressHost(host) {
			return
		}
		port := u.Port()
		if port == "" {
			port = "443"
			if u.Scheme == "http" {
				port = "80"
			}
		}
		t := net.JoinHostPort(host, port)
		if !seen[t] {
			seen[t] = true
			declared = append(declared, t)
		}
	}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			switch c.Kind {
			case packdecl.KindProgram:
				if c.URL != "" {
					add(c.URL)
				}
			case packdecl.KindProvider:
				for _, ep := range c.Endpoints {
					add(ep.BaseURL)
				}
			}
		}
	}
	sort.Strings(declared)
	neutral := net.JoinHostPort(egressNeutralHost, "443")
	var out []string
	for _, t := range declared {
		if t == neutral {
			continue
		}
		if len(out) == egressMaxPackTargets {
			break
		}
		out = append(out, t)
	}
	return append(out, neutral)
}

func publicEgressHost(host string) bool {
	if host == "" || host == "localhost" || (!strings.Contains(host, ".") && net.ParseIP(host) == nil) {
		return false
	}
	if strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	}
	return true
}

// reportEgress collects the probe and says what it found: a boot-log line always, and on the
// terminal only when NO target answered. Silence is the healthy output, as it is for the service
// witness (reachability.go).
func reportEgress(e *Env, p *egressProbe) {
	if p == nil {
		return
	}
	results := p.wait()
	for _, r := range results {
		if r.fault == egressReached {
			via := ""
			if r.proxy != "" {
				via = " via proxy " + r.proxy
			}
			e.note(fmt.Sprintf("egress: reached %s%s in %s", r.target, via, r.took.Round(time.Millisecond)))
			return
		}
	}
	if len(results) == 0 {
		return
	}
	if egressAnySlowDNS(results) {
		e.warn(egressSlowDNSWarning(p, results))
		e.note(fmt.Sprintf("egress: none of %d target(s) confirmed, a DNS lookup outlasted %s, backend=%q disposition=%s",
			len(results), p.budget, p.backend, p.disposition))
		return
	}
	e.warn(egressWarning(p, results))
	e.note(fmt.Sprintf("egress: none of %d target(s) reachable, backend=%q disposition=%s",
		len(results), p.backend, p.disposition))
}

func egressAnySlowDNS(results []egressResult) bool {
	for _, r := range results {
		if r.fault == egressSlowDNS {
			return true
		}
	}
	return false
}

// egressSlowDNSWarning is the terminal text when no target answered and at least one lookup was
// still pending at the end of the budget: DNS was slow, and whether traffic leaves the jail is
// unknown. Its next step is to time a lookup, which tells slow DNS from none.
func egressSlowDNSWarning(p *egressProbe, results []egressResult) string {
	var b strings.Builder
	// macOS has no getent; dscacheutil asks the same resolver the sandbox's programs use.
	where, lookup := "this jail", "time getent hosts "+egressNeutralHost
	if p.native {
		where, lookup = "this sandbox", "time dscacheutil -q host -a name "+egressNeutralHost
	}
	b.WriteString("warning: DNS lookups in " + where + " took longer than " + p.budget.String() +
		"; outbound access was not confirmed:\n")
	for _, r := range results {
		b.WriteString("  - " + egressResultLine(r, p.proxySource) + "\n")
	}
	b.WriteString("  A resolver this slow can make clients time out, each naming itself rather than DNS;\n" +
		"  a VPN's resolver is a common cause.\n" +
		"  Next step: time a lookup inside " + where + " with `" + lookup + "`.\n" +
		"  If it answers, the network works and DNS is slow: check the VPN or the resolver the host\n" +
		"  hands out. If it never answers, treat this as no network:\n")
	b.WriteString(egressNextStep(p, false))
	return strings.TrimRight(b.String(), "\n")
}

// egressWarning is the terminal text: what was found, one line per target, what it will look
// like later, and the next step for this backend.
func egressWarning(p *egressProbe, results []egressResult) string {
	var b strings.Builder
	where := "this jail"
	if p.native {
		where = "this sandbox"
	}
	b.WriteString("warning: " + where + " has no outbound network access — nothing it tried answered:\n")
	proxyFault := false
	for _, r := range results {
		b.WriteString("  - " + egressResultLine(r, p.proxySource) + "\n")
		switch r.fault {
		case egressProxyDNS, egressProxyConnect, egressProxyRefused:
			proxyFault = true
		}
	}
	b.WriteString("  Anything that needs the internet will fail, and each client will name itself rather\n" +
		"  than the network: an agent's login or token refresh, a package install, a model call.\n" +
		"  If " + where + " is meant to be offline, ignore this warning.\n")
	b.WriteString(egressNextStep(p, proxyFault))
	return strings.TrimRight(b.String(), "\n")
}

func egressResultLine(r egressResult, proxySource string) string {
	switch r.fault {
	case egressDNS:
		return r.target + ": the name did not resolve (" + r.detail + ")"
	case egressSlowDNS:
		if r.proxy != "" {
			return r.target + ": looking up the proxy " + r.proxy + " named by " + proxySource +
				" outlasted the probe's budget (" + r.detail + ")"
		}
		return r.target + ": looking up the name outlasted the probe's budget (" + r.detail + ")"
	case egressTimeout:
		return r.target + ": the connection timed out (" + r.detail + ")"
	case egressConnect:
		return r.target + ": the connection failed (" + r.detail + ")"
	case egressProxyDNS:
		return r.target + ": the proxy " + r.proxy + " named by " + proxySource +
			" did not resolve (" + r.detail + ")"
	case egressProxyConnect:
		return r.target + ": the proxy " + r.proxy + " named by " + proxySource +
			" could not be reached (" + r.detail + ")"
	case egressProxyRefused:
		return r.target + ": the proxy " + r.proxy + " named by " + proxySource +
			" answered CONNECT with " + r.detail
	}
	return r.target
}

// egressNextStep is the per-backend next step. The backend is the launcher's word for it
// (paths.JailBackendEnv); the jail cannot tell Apple Container from podman by itself, and
// YOLO_RUNTIME says `podman` on both (it is the runtime a NESTED launch uses).
func egressNextStep(p *egressProbe, proxyFault bool) string {
	var s string
	switch {
	case p.native:
		s = "  Next step: this sandbox uses the Mac's own network, so check that the Mac is online,\n" +
			"  then any VPN or network filter (a firewall or endpoint-security product) that applies\n" +
			"  to the sandbox account, and relaunch.\n"
	case p.backend == "container":
		s = "  Next step: if the Mac itself is online, Apple Container's network is not carrying\n" +
			"  traffic out. Check for a VPN or an endpoint-security network filter that blocks the\n" +
			"  container network, then restart Apple Container's services and relaunch:\n" +
			"      container system stop && container system start\n"
	case p.disposition == dispositionShared:
		s = "  Next step: this jail shares the network of whatever launched it, so that machine has\n" +
			"  no route out either. Check its connection, then relaunch.\n"
	default:
		s = "  Next step: if the host itself is online, the container network is not carrying\n" +
			"  traffic out. Check for a VPN or firewall rule that drops traffic from podman's\n" +
			"  network; on a podman machine (macOS), restart it with\n" +
			"      podman machine stop && podman machine start\n" +
			"  then relaunch.\n"
	}
	switch {
	case proxyFault && !p.native:
		s += "  The proxy is the first thing to check: a proxy listening on the host's 127.0.0.1 is\n" +
			"  not reachable at that address from inside a container.\n"
	case p.proxySource == "":
		s += "  If this network only lets traffic out through a proxy, give the jail HTTPS_PROXY with\n" +
			"  `yolo config set env.set.HTTPS_PROXY <url> --user`.\n"
	}
	return s
}

// egressDialOverride replaces the real dialer for the boot step; only tests set it.
var egressDialOverride egressDialer

// bootEgressDialer is the dialer the boot step uses, and false when it must not dial at all.
//
// A UNIT TEST NEVER DIALS THE INTERNET. Many tests run the whole boot table, in this package and
// beside it (macos-user's and the run pipeline's RunDarwinBootstrap fixtures), and none of them
// is about the network: a real dial would make them slower offline and print a warning into
// output they compare. So a test binary dials only through egressDialOverride, which the tests
// of this file set. The test binary is recognised the way packload's testBinaryCacheDir
// recognises it — package testing registers `test.v` before any test runs — and not by
// testing.Testing(), which would link package testing into the shipped entrypoint.
func bootEgressDialer() (egressDialer, bool) {
	if egressDialOverride != nil {
		return egressDialOverride, true
	}
	if flag.Lookup("test.v") != nil {
		return nil, false
	}
	return defaultEgressDialer, true
}

// startEgressProbeStep is the boot step's body: the packs' targets, the real dialer, the budget.
func startEgressProbeStep(b *bootRun) {
	dial, ok := bootEgressDialer()
	if !ok {
		return
	}
	packs, _ := b.jailPacks() // a load failure is reported where it always was, at configure_pack_surfaces
	b.egress = startEgressProbe(b.e, egressTargets(packs), dial, egressBudget)
}

// reportEgressStep collects what startEgressProbeStep started.
func reportEgressStep(b *bootRun) {
	reportEgress(b.e, b.egress)
}
