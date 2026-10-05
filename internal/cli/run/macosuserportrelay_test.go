package run

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// macosuserportrelay_test.go pins the port remaps macos-user delivers (macosuserportrelay.go):
// the relay itself, the grammar and the plan, and — through Run() — that the macos-user arm
// opens the planned relays for the command's lifetime and a --dry-run opens none.

// relayIOTimeout bounds every read and dial in these tests, so a relay that drops a half-close
// or never stops fails the test rather than hanging it.
const relayIOTimeout = 5 * time.Second

// freeLoopbackPort is a TCP port on 127.0.0.1 nothing listens on at the moment of the call.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// loopback is 127.0.0.1:port.
func loopback(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// serveOnce accepts connections on ln and hands each to handle on its own goroutine, until ln
// closes.
func serveOnce(ln net.Listener, handle func(net.Conn)) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
}

// echoUpper reads its peer to EOF, then answers what it read, upper-cased, and closes. It answers
// only after the client's half-close arrives, so a relay that does not carry the half-close never
// gets an answer back.
func echoUpper(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(relayIOTimeout))
	b, err := io.ReadAll(c)
	if err != nil {
		return
	}
	_, _ = c.Write([]byte(strings.ToUpper(string(b))))
}

// speaksFirst writes line to its peer and closes, the shape of a service that speaks first.
func speaksFirst(line string) func(net.Conn) {
	return func(c net.Conn) {
		defer c.Close()
		_, _ = c.Write([]byte(line + "\n"))
	}
}

// readBanner dials addr and returns the first line it is sent.
func readBanner(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, relayIOTimeout)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(relayIOTimeout))
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

// listenLoopback listens on 127.0.0.1:port (0 for any) and closes the listener at cleanup.
func listenLoopback(t *testing.T, port int) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", loopback(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// THE SPLICE, both directions and the half-close between them: the client's bytes reach the far
// side, the client half-closes, and the far side's answer — sent only once that EOF arrived —
// comes back whole, followed by the relay's own EOF.
func TestPortRelayCarriesBothDirectionsAndTheHalfClose(t *testing.T) {
	far := listenLoopback(t, 0)
	serveOnce(far, echoUpper)
	r, err := startPortRelay("127.0.0.1:0", far.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()

	c, err := net.DialTimeout("tcp", r.addr(), relayIOTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(relayIOTimeout))
	if _, err := c.Write([]byte("ping through the relay")); err != nil {
		t.Fatal(err)
	}
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("reading the answer back through the relay: %v (got %q) — a relay that does not "+
			"half-close the far side leaves it waiting for an EOF that never comes", err, got)
	}
	if string(got) != "PING THROUGH THE RELAY" {
		t.Errorf("the relayed answer is %q, want %q", got, "PING THROUGH THE RELAY")
	}

	// And the other direction alone: a far side that speaks first and closes.
	speaker := listenLoopback(t, 0)
	serveOnce(speaker, speaksFirst("hello from the far side"))
	r2, err := startPortRelay("127.0.0.1:0", speaker.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer r2.stop()
	if line, err := readBanner(r2.addr()); err != nil || line != "hello from the far side" {
		t.Errorf("a far side that speaks first: got %q, %v", line, err)
	}
}

// A connection to a far side nothing listens on is closed at once rather than held open, so the
// client sees the failure as it would dialing the port itself.
func TestPortRelayClosesAConnectionItCannotCarry(t *testing.T) {
	r, err := startPortRelay("127.0.0.1:0", loopback(freeLoopbackPort(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	c, err := net.DialTimeout("tcp", r.addr(), relayIOTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(relayIOTimeout))
	if n, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Errorf("read %d bytes, err %v; want the relay to close a connection whose far side "+
			"refused it", n, err)
	}
}

// STOP ends everything it started: the listener goes, and a connection spliced through it is
// closed on both sides rather than left to the far side's lifetime.
func TestPortRelayStopClosesTheListenerAndLiveConnections(t *testing.T) {
	far := listenLoopback(t, 0)
	farConn := make(chan net.Conn, 1)
	serveOnce(far, func(c net.Conn) { farConn <- c })
	r, err := startPortRelay("127.0.0.1:0", far.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	addr := r.addr()

	c, err := net.DialTimeout("tcp", addr, relayIOTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var held net.Conn
	select {
	case held = <-farConn:
		defer held.Close()
	case <-time.After(relayIOTimeout):
		t.Fatal("the relay never dialed the far side")
	}

	stopped := make(chan struct{})
	go func() { r.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(relayIOTimeout):
		t.Fatal("stop did not return while a spliced connection was open")
	}

	_ = c.SetDeadline(time.Now().Add(relayIOTimeout))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Errorf("the client's connection is still open after stop (err %v)", err)
	}
	_ = held.SetDeadline(time.Now().Add(relayIOTimeout))
	if _, err := held.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Errorf("the far side's connection is still open after stop (err %v)", err)
	}
	if c2, err := net.DialTimeout("tcp", addr, relayIOTimeout); err == nil {
		c2.Close()
		t.Errorf("%s still accepts connections after stop", addr)
	}
}

// isTimeout reports whether err is a deadline expiring, which here means the peer stayed open.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// THE GRAMMAR: config.validatePublishPort's, which podman's -p takes. ParsePortForwards splits at
// the first colon and cannot read these forms, which is why the key has a reader of its own.
func TestParsePublishPortsReadsThePublishGrammar(t *testing.T) {
	got := parsePublishPorts([]any{
		"8000:3000",
		"127.0.0.1:8001:3001",
		"8002:3002/tcp",
		"8003:3003/udp",
		"0.0.0.0:8004:3004/tcp",
		"3005:3005",
		8006,             // not a string: validation refuses it
		"8007",           // one field: validation refuses it
		"1:2:3:4",        // four fields
		"8008:3008/sctp", // not tcp or udp
		"x:3009",
		"70000:3010",
	})
	want := []publishPort{
		{entry: "8000:3000", host: 8000, jail: 3000, proto: "tcp"},
		{entry: "127.0.0.1:8001:3001", ip: "127.0.0.1", host: 8001, jail: 3001, proto: "tcp"},
		{entry: "8002:3002/tcp", host: 8002, jail: 3002, proto: "tcp"},
		{entry: "8003:3003/udp", host: 8003, jail: 3003, proto: "udp"},
		{entry: "0.0.0.0:8004:3004/tcp", ip: "0.0.0.0", host: 8004, jail: 3004, proto: "tcp"},
		{entry: "3005:3005", host: 3005, jail: 3005, proto: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePublishPorts:\n got %+v\nwant %+v", got, want)
	}
}

// relayNetConfig is a config whose network section is the given JSON object.
func relayNetConfig(t *testing.T, network string) *jsonx.OrderedMap {
	t.Helper()
	cfg, err := jsonx.Decode([]byte(`{"network": ` + network + `}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := cfg.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("config is %T", cfg)
	}
	return m
}

// THE PLAN: one relay per TCP remap, in each key's own direction, and nothing for an entry that is
// already satisfied. A remap it does not relay is named with why.
func TestPlanMacosUserPortRelaysByEntryForm(t *testing.T) {
	cfg := relayNetConfig(t, `{
	  "ports": ["8000:3000", "127.0.0.1:8001:3001", "8002:3002/tcp", "8003:3003/udp", "3005:3005",
	            "127.0.0.1:3006:3006"],
	  "forward_host_ports": [5432, "6379:6379", "8080:9090"]
	}`)
	plan := planMacosUserPortRelays(cfg, "")
	want := []macosUserPortRelay{
		{key: keyNetworkPorts, entry: "8000:3000", listen: "0.0.0.0:8000", dial: "127.0.0.1:3000"},
		{key: keyNetworkPorts, entry: "127.0.0.1:8001:3001", listen: "127.0.0.1:8001", dial: "127.0.0.1:3001"},
		{key: keyNetworkPorts, entry: "8002:3002/tcp", listen: "0.0.0.0:8002", dial: "127.0.0.1:3002"},
		{key: keyForwardHostPorts, entry: "8080:9090", listen: "127.0.0.1:8080", dial: "127.0.0.1:9090"},
	}
	if !reflect.DeepEqual(plan.relays, want) {
		t.Errorf("relays:\n got %+v\nwant %+v", plan.relays, want)
	}
	if len(plan.unrelayed) != 1 || plan.unrelayed[0].entry != "8003:3003/udp" ||
		!strings.Contains(plan.unrelayed[0].why, "TCP only") {
		t.Errorf("the UDP remap is not the one unrelayed entry, with its reason: %+v", plan.unrelayed)
	}

	// Withheld: no relay at all, and every remap — the UDP one included — carries the reason.
	plan = planMacosUserPortRelays(cfg, "because")
	if len(plan.relays) != 0 {
		t.Errorf("a withheld plan still relays %+v", plan.relays)
	}
	var named []string
	for _, u := range plan.unrelayed {
		if u.why != "because" {
			t.Errorf("%s carries %q, want the withholding reason", u.entry, u.why)
		}
		named = append(named, u.entry)
	}
	if want := []string{"8000:3000", "127.0.0.1:8001:3001", "8002:3002/tcp", "8003:3003/udp", "8080:9090"}; !reflect.DeepEqual(named, want) {
		t.Errorf("withheld remaps named %v, want %v", named, want)
	}

	if plan := planMacosUserPortRelays(jsonx.NewOrderedMap(), ""); len(plan.relays)+len(plan.unrelayed) != 0 {
		t.Errorf("a config with no network section planned %+v", plan)
	}
}

// THE GATE is the mode the launch RESOLVES: an explicit `network.mode: "host"` and a typed
// `--network host` both drop the two keys, as they do on podman, each naming itself as the thing to
// change; the seal relays nothing; and no backend but macos-user plans a relay at all.
func TestMacosUserPortPlanReadsTheResolvedMode(t *testing.T) {
	remap := `"forward_host_ports": ["8080:9090"]`
	for _, tc := range []struct {
		name, network, flag string
		sealed              bool
		rt                  string
		wantRelays          int
		wantWhy             string
	}{
		{name: "default bridge", network: `{` + remap + `}`, rt: "macos-user", wantRelays: 1},
		{name: "explicit bridge", network: `{"mode": "bridge", ` + remap + `}`, rt: "macos-user", wantRelays: 1},
		{name: "config host", network: `{"mode": "host", ` + remap + `}`, rt: "macos-user",
			wantWhy: "`network.mode: \"host\"` drops both port keys"},
		{name: "flag host", network: `{` + remap + `}`, flag: "host", rt: "macos-user",
			wantWhy: "`--network host` drops both port keys"},
		{name: "flag bridge over config host", network: `{"mode": "host", ` + remap + `}`, flag: "bridge",
			rt: "macos-user", wantRelays: 1},
		{name: "sealed", network: `{` + remap + `}`, sealed: true, rt: "macos-user", wantWhy: relayWhySealed},
		{name: "podman", network: `{` + remap + `}`, rt: "podman"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{Network: tc.flag, Sealed: tc.sealed}
			plan := o.macosUserPortPlan(tc.rt, relayNetConfig(t, tc.network))
			if len(plan.relays) != tc.wantRelays {
				t.Errorf("relays = %+v, want %d", plan.relays, tc.wantRelays)
			}
			if tc.wantWhy == "" {
				if len(plan.unrelayed) != 0 {
					t.Errorf("unrelayed = %+v, want none", plan.unrelayed)
				}
				return
			}
			if len(plan.unrelayed) != 1 || !strings.Contains(plan.unrelayed[0].why, tc.wantWhy) {
				t.Errorf("unrelayed = %+v, want one naming %q", plan.unrelayed, tc.wantWhy)
			}
		})
	}
}

// relayRun drives Run() to the macos-user arm with `network` as the user config's network
// section, and inSandbox standing in for the sandboxed command: it runs where MacosUserRun would
// start the sandbox, while the launch's own listeners are open. The network section is in the
// USER config so a live launch has no workspace change to approve.
func relayRun(t *testing.T, network string, tweak func(*Options), inSandbox func()) (int, string) {
	t.Helper()
	home := packHome(t)
	writeUserConfig(t, home, `{"network": `+network+`}`)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	if tweak != nil {
		tweak(o)
	}
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string,
		macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool,
		macosuser.JailDaemons) int {
		inSandbox()
		return 0
	}
	rc := Run(*o)
	return rc, stderr.String()
}

// THE CALL SITE, forward direction: a host service on 127.0.0.1:H answers at the sandbox's
// 127.0.0.1:J for a "J:H" entry while the command runs, and nothing listens at J once Run returns.
// Fails if the macos-user arm stops starting the relays, or stops closing them.
func TestMacosUserLaunchRelaysAForwardHostPortRemap(t *testing.T) {
	host := listenLoopback(t, 0)
	serveOnce(host, speaksFirst("the host's service"))
	h := host.Addr().(*net.TCPAddr).Port
	j := freeLoopbackPort(t)
	entry := fmt.Sprintf("%d:%d", j, h)

	var line string
	var dialErr error
	rc, stderr := relayRun(t, `{"forward_host_ports": ["`+entry+`"]}`, nil, func() {
		line, dialErr = readBanner(loopback(j))
	})
	if rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr)
	}
	if dialErr != nil || line != "the host's service" {
		t.Errorf("the sandbox's 127.0.0.1:%d did not reach the host's %d: %q, %v\nstderr:\n%s",
			j, h, line, dialErr, stderr)
	}
	want := fmt.Sprintf("Relaying 127.0.0.1:%d -> 127.0.0.1:%d for `network.forward_host_ports` entry %s, "+
		"outside the sandbox, until the command exits.", j, h, entry)
	if !strings.Contains(stderr, want) {
		t.Errorf("the launch did not disclose the relay (want %q):\n%s", want, stderr)
	}
	if c, err := net.DialTimeout("tcp", loopback(j), relayIOTimeout); err == nil {
		c.Close()
		t.Errorf("127.0.0.1:%d still listens after the command exited", j)
	}
}

// THE CALL SITE, publish direction: the sandbox's service on 127.0.0.1:J answers at the host's
// 127.0.0.1:H for a "127.0.0.1:H:J" entry while the command runs, and H is closed once Run returns.
// A loopback listen address, so the test publishes nothing beyond this machine.
func TestMacosUserLaunchRelaysAPublishedPortRemap(t *testing.T) {
	sandboxSvc := listenLoopback(t, 0)
	serveOnce(sandboxSvc, speaksFirst("the sandbox's service"))
	j := sandboxSvc.Addr().(*net.TCPAddr).Port
	h := freeLoopbackPort(t)
	entry := fmt.Sprintf("127.0.0.1:%d:%d", h, j)

	var line string
	var dialErr error
	rc, stderr := relayRun(t, `{"ports": ["`+entry+`"]}`, nil, func() {
		line, dialErr = readBanner(loopback(h))
	})
	if rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr)
	}
	if dialErr != nil || line != "the sandbox's service" {
		t.Errorf("the host's 127.0.0.1:%d did not reach the sandbox's %d: %q, %v\nstderr:\n%s",
			h, j, line, dialErr, stderr)
	}
	if !strings.Contains(stderr, fmt.Sprintf("Relaying 127.0.0.1:%d -> 127.0.0.1:%d for `network.ports` entry %s", h, j, entry)) {
		t.Errorf("the launch did not disclose the relay:\n%s", stderr)
	}
	if strings.Contains(stderr, "Unlike podman's -p") {
		t.Errorf("a loopback-only relay was disclosed as publishing more broadly than podman:\n%s", stderr)
	}
	if c, err := net.DialTimeout("tcp", loopback(h), relayIOTimeout); err == nil {
		c.Close()
		t.Errorf("127.0.0.1:%d still listens after the command exited", h)
	}
}

// A --dry-run describes the relays and opens none: the plan names each with "Would relay", and its
// port is closed while the stand-in for the sandbox runs.
func TestMacosUserDryRunNamesTheRelaysAndOpensNone(t *testing.T) {
	j, h := freeLoopbackPort(t), freeLoopbackPort(t)
	entry := fmt.Sprintf("%d:%d", j, h)
	var listening bool
	rc, stderr := relayRun(t, `{"forward_host_ports": ["`+entry+`"], "ports": ["8000:3000"]}`,
		func(o *Options) { o.DryRun = true }, func() {
			if c, err := net.DialTimeout("tcp", loopback(j), relayIOTimeout); err == nil {
				c.Close()
				listening = true
			}
		})
	if rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr)
	}
	if listening {
		t.Errorf("a --dry-run opened a relay on 127.0.0.1:%d", j)
	}
	for _, want := range []string{
		fmt.Sprintf("Would relay 127.0.0.1:%d -> 127.0.0.1:%d for `network.forward_host_ports` entry %s", j, h, entry),
		"Would relay 0.0.0.0:8000 -> 127.0.0.1:3000 for `network.ports` entry 8000:3000",
		"Unlike podman's -p, it also publishes a service the sandbox bound to 127.0.0.1 alone.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the plan lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "Relaying ") {
		t.Errorf("a --dry-run said it is relaying:\n%s", stderr)
	}
}

// `--network host` drops both keys at launch as it does in the plan: nothing listens at J during
// the command, and the notice says why with the step that has it relayed.
func TestMacosUserLaunchRelaysNothingUnderHostNetworking(t *testing.T) {
	j, h := freeLoopbackPort(t), freeLoopbackPort(t)
	entry := fmt.Sprintf("%d:%d", j, h)
	var listening bool
	rc, stderr := relayRun(t, `{"forward_host_ports": ["`+entry+`"]}`,
		func(o *Options) { o.Network = "host" }, func() {
			if c, err := net.DialTimeout("tcp", loopback(j), relayIOTimeout); err == nil {
				c.Close()
				listening = true
			}
		})
	if rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr)
	}
	if listening || strings.Contains(stderr, "Relaying ") {
		t.Errorf("a `--network host` launch relayed %s:\n%s", entry, stderr)
	}
	if !strings.Contains(stderr, entry+" is a port REMAP this launch does not relay: `--network host` "+
		"drops both port keys, as it does on podman; launch without it to have the remap relayed.") {
		t.Errorf("the notice does not say why the remap is not relayed:\n%s", stderr)
	}
}

// A relay whose port is taken is skipped with a warning naming the entry and the command that finds
// the holder, and the launch goes on: the command still runs.
func TestMacosUserLaunchWarnsAndContinuesWhenARelayPortIsTaken(t *testing.T) {
	taken := listenLoopback(t, 0)
	j := taken.Addr().(*net.TCPAddr).Port
	entry := fmt.Sprintf("%d:%d", j, freeLoopbackPort(t))
	ran := false
	rc, stderr := relayRun(t, `{"forward_host_ports": ["`+entry+`"]}`, nil, func() { ran = true })
	if rc != 0 || !ran {
		t.Fatalf("Run() = %d (command ran: %v), want the launch to go on\nstderr:\n%s", rc, ran, stderr)
	}
	for _, want := range []string{
		"Warning: not relaying `network.forward_host_ports` entry " + entry,
		fmt.Sprintf("`lsof -iTCP:%d -sTCP:LISTEN` names it", j),
		"The launch goes on without this remap.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the bind failure lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "Relaying ") {
		t.Errorf("a relay that could not listen was disclosed as running:\n%s", stderr)
	}
}
