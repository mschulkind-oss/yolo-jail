package integration

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// These run under -short (no container). They cover the pure halves of the #10 probe in
// applecontainerparity_test.go (TestAppleContainerExplicitHostModeKeepsPublishedPorts): how a
// dial is classified and recorded, how the jail's phase is read, how the Mac's route is parsed,
// and when the evidence names macOS Local Network privacy. A mistake there is then caught by
// `just check-ci`, not by the next run on the one Mac that can conduct the experiment. The
// Mac-only call sites are pinned by reading the source (TestACPortProbeCallSites), the technique
// TestRequireAppleContainerInstallsTheLateSkipGuard uses. None is named TestAppleContainer…, so
// the Mac job's `-list '^TestAppleContainer'` selection does not pick them up.

// acServe runs a loopback TCP server that hands each accepted connection to handle.
//
// It does not number the connections: anything on the machine can connect to a loopback port,
// so a count of accepted connections is not a count of this test's dials. A handler that must
// tell the test's own dials apart asks acConnectGate.admit.
func acServe(t *testing.T, handle func(c *net.TCPConn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c.(*net.TCPConn))
		}
	}()
	return ln.Addr().String()
}

func acReply(line string) func(*net.TCPConn) {
	return func(c *net.TCPConn) {
		_, _ = c.Write([]byte(line))
		c.Close()
	}
}

func acResetConn(c *net.TCPConn) {
	_ = c.SetLinger(0) // close with RST, not FIN
	c.Close()
}

// acConnectGate tells an accept-then-reset fixture when, and whether, to reset: after the
// dialer's connect has returned, and only for a connection this test dialed. So the fixture
// produces one outcome, "accepted, then reset", every time.
//
// WHEN: ungated, the fixture was a scheduling race on a real socket. When the server's reset beat
// the dialing goroutine to the connect's result, the dial read as acKindConnReset (acDialHook says
// why). That failed the short suite in CI (run 36255255312), and fails most runs under CPU load.
// WHETHER: anything on the machine can connect to the listener. A test binary running beside this
// one (go test runs packages in parallel) that dials a port it released reaches this listener if
// the listener took that port. Measured with two of these binaries at once: the listener counted
// the other process's dial as one of the poll's, and the token came a dial early.
//
// A dial is registered BEFORE its connect: the gate's dialer binds the socket's local address
// itself (net.Dialer's Control, which runs before the connect) and records it, and an accepted
// connection is matched by its RemoteAddr, which is that address. So no wait here is on a clock.
// A connection whose address was never registered is a stranger's and is closed at once; one that
// was is held only until its dial returns, connected or failed, which acConnectTimeout bounds.
// The bind is exclusive (no SO_REUSEADDR), so no other socket on the machine can hold that local
// address while the dial is registered.
type acConnectGate struct {
	mu        sync.Mutex
	dials     map[string]*acGatedDial // by the dialer's local address, from bind until close
	strangers atomic.Int32            // connections admit found no registered dial for
}

// acGatedDial is one registered dial. ok is written before done closes and read only after.
type acGatedDial struct {
	done chan struct{} // closed when the dial returns
	ok   bool          // the connect succeeded
}

// acGateDials routes acDialLine's connects through a new gate until t ends.
func acGateDials(t *testing.T) *acConnectGate {
	t.Helper()
	g := &acConnectGate{dials: map[string]*acGatedDial{}}
	dial := g.dial
	prev := acDialHook.Swap(&dial)
	t.Cleanup(func() { acDialHook.Store(prev) })
	return g
}

// dial is acDialLine's connect through the gate. It differs from the Mac run's only in binding
// the local address before the connect, which is how the gate learns it in time.
func (g *acConnectGate) dial(addr string) (net.Conn, error) {
	var local string
	d := net.Dialer{Timeout: acConnectTimeout, Control: func(network, _ string, rc syscall.RawConn) error {
		var err error
		if cerr := rc.Control(func(fd uintptr) { local, err = acBindLoopback(network, int(fd)) }); cerr != nil {
			return cerr
		}
		if err != nil {
			return err
		}
		g.register(local)
		return nil
	}}
	c, err := d.Dial("tcp", addr)
	if local != "" {
		g.finish(local, err == nil)
	}
	if err != nil {
		return nil, err
	}
	return acGatedConn{c, func() { g.forget(local) }}, nil
}

func (g *acConnectGate) register(local string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dials[local] = &acGatedDial{done: make(chan struct{})}
}

// finish releases a registered dial's connection to the server side. A failed dial is also
// forgotten: its connection, if the server has one, gets no reset from this test.
func (g *acConnectGate) finish(local string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.dials[local]
	if d == nil {
		return
	}
	d.ok = ok
	close(d.done)
	if !ok {
		delete(g.dials, local)
	}
}

func (g *acConnectGate) forget(local string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.dials, local)
}

// admit reports whether this test dialed c and that dial's connect has returned connected,
// waiting for the connect when it has not returned yet. False for a stranger's connection, at
// once, and for one of this test's whose connect failed.
func (g *acConnectGate) admit(c *net.TCPConn) bool {
	g.mu.Lock()
	d := g.dials[c.RemoteAddr().String()]
	g.mu.Unlock()
	if d == nil {
		g.strangers.Add(1)
		return false
	}
	<-d.done
	return d.ok
}

// resetOurs resets c once its dialer's connect has returned, and closes a stranger's connection.
func (g *acConnectGate) resetOurs(c *net.TCPConn) {
	if !g.admit(c) {
		c.Close()
		return
	}
	acResetConn(c)
}

// note is the gate's part of a failure message: how many connections it turned away.
func (g *acConnectGate) note() string {
	n := g.strangers.Load()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" [%d connection(s) matched no dial this test had open and were closed "+
		"uncounted: another process's, or acDialLine no longer connects through acDialHook]", n)
}

// acGatedConn drops its gate entry when acDialLine closes it, before the local port is released.
type acGatedConn struct {
	net.Conn
	forget func()
}

func (c acGatedConn) Close() error {
	c.forget()
	return c.Conn.Close()
}

// acBindLoopback binds the TCP socket fd to an ephemeral port on 127.0.0.1 and returns the
// address it got. network is the socket's, as net.Dialer's Control names it.
func acBindLoopback(network string, fd int) (string, error) {
	if network != "tcp4" {
		return "", fmt.Errorf("binding a %s socket: only IPv4 loopback is handled", network)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		return "", os.NewSyscallError("bind", err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		return "", os.NewSyscallError("getsockname", err)
	}
	in4, ok := sa.(*syscall.SockaddrInet4)
	if !ok {
		return "", fmt.Errorf("getsockname on an AF_INET socket returned %T", sa)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(in4.Port)), nil
}

// acClosedPort returns a loopback address nothing listens on: port 1 (tcpmux), which no test
// host serves and no test here binds. A connect there is refused on Linux and on darwin alike.
//
// Two earlier spellings each failed one platform. Binding a listener and closing it released the
// port before the dial, and a test binary running beside this one could bind it in between
// (measured with two binaries at once: "nothing listening" was answered with the other process's
// token). Holding the port with a bound socket that never listens fixed that on Linux, which
// refuses such a connect, but darwin's TCP stack silently drops a SYN aimed at a bound,
// non-listening socket, so the dial timed out there (check-macos, run 36271509679). A port
// nothing binds avoids both. If something does answer on it, the test cannot judge "nothing
// listening", so it says so rather than misreading the answer.
func acClosedPort(t *testing.T) string {
	t.Helper()
	const addr = "127.0.0.1:1"
	if c, err := net.DialTimeout("tcp", addr, acConnectTimeout); err == nil {
		_ = c.Close()
		t.Skipf("something listens on %s on this host, so it cannot stand in for a port nothing listens on", addr)
	}
	return addr
}

// acPollBounded runs acPollListener with a stop it closes first when closedStop is set, and with
// a bound of its own, so a poll that stops honoring maxDials fails the test instead of dialing
// until go test's timeout. A poll that honors it returns within maxDials dials of
// acConnectTimeout plus acReadTimeout each, plus its waits. Past that, with margin, stop is
// closed, and the poll is judged by what it did rather than by the clock: more than maxDials
// dials fails. A poll that honors neither is left dialing in a failed test.
func acPollBounded(t *testing.T, closedStop bool, addr, token string, clock acProbeClock, every time.Duration, maxDials int) acListenerResult {
	t.Helper()
	const margin = 5 * time.Second
	stop := make(chan struct{})
	var once sync.Once
	halt := func() { once.Do(func() { close(stop) }) }
	if closedStop {
		halt()
	}
	done := make(chan acListenerResult, 1)
	go func() { done <- acPollListener(stop, addr, token, clock, every, maxDials) }()
	bound := time.Duration(maxDials)*(acConnectTimeout+acReadTimeout+every) + margin
	select {
	case r := <-done:
		return r
	case <-time.After(bound):
	}
	halt()
	var r acListenerResult
	select {
	case r = <-done:
	case <-time.After(acConnectTimeout + acReadTimeout + margin):
		select {
		case r = <-done: // it returned while this process was not running
		default:
			t.Fatalf("the poll of %s honors neither maxDials (%d) nor stop: still dialing after %s "+
				"and a closed stop; its goroutine is left running", addr, maxDials, bound)
		}
	}
	if len(r.dials) > maxDials {
		t.Fatalf("the poll of %s, capped at %d dial(s), made %d before stop closed %s after it "+
			"started: it no longer honors maxDials", addr, maxDials, len(r.dials), bound)
	}
	t.Logf("the poll of %s outlasted its %s bound and was stopped after %d dial(s), within its "+
		"cap: this process was starved, and a short result below is that", addr, bound, len(r.dials))
	return r
}

// TestACDialKindsAgainstLocalListeners pins acDialLine and acDialKind together against real
// sockets. The split that matters is the connect/read one: "accepted, then reset" (the third
// Mac run's gateway answer, a forwarder that cannot reach its container) must never read as a
// refusal, and the kind is only right if acDialLine marks a post-accept failure. The reset waits
// for the dialer's connect (acConnectGate), or the case is a race between the two kinds.
func TestACDialKindsAgainstLocalListeners(t *testing.T) {
	const token = "YOLO-AC-PORT-TEST"
	gate := acGateDials(t)
	for _, tc := range []struct {
		name string
		addr string
		want string
	}{
		{"token", acServe(t, acReply(token+"\n")), acKindReached},
		{"wrong reply", acServe(t, acReply("nope\n")), `accepted, read "nope" instead of the token`},
		{"accept then close", acServe(t, func(c *net.TCPConn) { c.Close() }), acKindEOF},
		{"accept then reset", acServe(t, gate.resetOurs), acKindReset},
		{"nothing listening", acClosedPort(t), acKindRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, err := acDialLine(tc.addr)
			if got := acDialKind(line, err, token); got != tc.want {
				t.Errorf("dialing %s: kind %q, want %q (line %q, err %v)%s", tc.addr, got, tc.want, line, err, gate.note())
			}
		})
	}
}

// TestACDialKindResetEitherSideOfTheConnect covers the peer the gate keeps out of the cases
// above: one that resets as soon as it accepts, as the third Mac run's gateway did. Nothing waits
// for the dialer here, so whether each reset lands before this process has read its connect's
// result is scheduling, and a dial reads as acKindConnReset or acKindReset (acDialHook says why).
// Which of the two such a peer should read as is not decided here. What both share is: the peer
// accepted, so it never reads as a refusal, an EOF or silence.
func TestACDialKindResetEitherSideOfTheConnect(t *testing.T) {
	const dials = 50
	addr := acServe(t, acResetConn)
	seen := map[string]int{}
	for i := 1; i <= dials; i++ {
		line, err := acDialLine(addr)
		kind := acDialKind(line, err, "TOKEN")
		seen[kind]++
		if kind != acKindReset && kind != acKindConnReset {
			t.Errorf("dial %d of %d to a peer that resets on accept: kind %q, want %q or %q (line %q, err %v)",
				i, dials, kind, acKindReset, acKindConnReset, line, err)
		}
	}
	t.Logf("kinds over %d dials: %v", dials, seen)
}

// TestACDialKindNamesConnectErrors covers the kinds a Linux loopback cannot produce, above all
// EHOSTUNREACH: macOS Local Network privacy's answer, which acMacEvidence.localNetworkDenied
// keys on.
func TestACDialKindNamesConnectErrors(t *testing.T) {
	connect := func(errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
	}
	read := func(localPort int, errno syscall.Errno) error {
		return acReadErr{&net.OpError{Op: "read", Net: "tcp",
			Source: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: localPort},
			Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 43447},
			Err:    os.NewSyscallError("read", errno)}}
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no route to host", connect(syscall.EHOSTUNREACH), acKindNoRoute},
		{"network unreachable", connect(syscall.ENETUNREACH), acKindNetUnreach},
		{"refused", connect(syscall.ECONNREFUSED), acKindRefused},
		{"reset during connect", connect(syscall.ECONNRESET), acKindConnReset},
		{"connect timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, acKindConnTimeout},
		{"reset after accept", acReadErr{os.NewSyscallError("read", syscall.ECONNRESET)}, acKindReset},
		{"silent after accept", acReadErr{os.ErrDeadlineExceeded}, acKindSilent},
		{"unnamed after accept", acReadErr{errors.New("weird")}, "accepted, then weird"},
		// A real read error carries the local ephemeral port, which changes every dial: the kind
		// must be the errno alone, or no two such dials share a kind and timeline merges nothing.
		{"unnamed errno after accept", read(38118, syscall.ENETDOWN), "accepted, then " + syscall.ENETDOWN.Error()},
		{"same errno, the next dial's port", read(38134, syscall.ENETDOWN), "accepted, then " + syscall.ENETDOWN.Error()},
		{"unnamed", errors.New("weird"), "other: weird"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := acDialKind("", tc.err, "TOKEN"); got != tc.want {
				t.Errorf("acDialKind(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestACPollListenerRecordsEveryDial is the fourth Mac run's defect, pinned: a record that
// keeps only the last answer turned "reset while the jail ran" into "refused". Every dial must
// be kept, with the phase it started in.
func TestACPollListenerRecordsEveryDial(t *testing.T) {
	const token = "YOLO-AC-PORT-TEST"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, acPortListenFile), []byte("listening\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clock := acProbeClock{start: time.Now(), dir: dir}

	t.Run("until the token answers", func(t *testing.T) {
		gate := acGateDials(t)
		var ours atomic.Int32 // this test's dials the server has taken, strangers not counted
		addr := acServe(t, func(c *net.TCPConn) {
			switch {
			case !gate.admit(c):
				c.Close()
			case ours.Add(1) <= 3:
				acResetConn(c)
			default:
				acReply(token + "\n")(c)
			}
		})
		// Capped, where the Mac's loopback polls are not. Uncapped, a server that never sends the
		// token (its accept loop ends on any Accept error) left this poll dialing until go test's
		// own timeout; capped, the test fails after two dials past the expected four.
		r := acPollBounded(t, false, addr, token, clock, 5*time.Millisecond, 6)
		if !r.reached || len(r.dials) != 4 {
			t.Fatalf("reached=%v after %d dial(s), want reached after 4: %+v%s", r.reached, len(r.dials), r.dials, gate.note())
		}
		for i, d := range r.dials {
			want := acKindReset
			if i == 3 {
				want = acKindReached
			}
			if d.kind != want || d.phase != acPhaseRunning {
				t.Errorf("dial %d = %q %q, want %q %q%s", i+1, d.phase, d.kind, acPhaseRunning, want, gate.note())
			}
		}
		tl := r.timeline()
		if len(tl) != 2 || !strings.HasPrefix(tl[0], "while the jail ran: 3× "+acKindReset+" (t=") ||
			!strings.HasPrefix(tl[1], "while the jail ran: 1× "+acKindReached+" (t=") {
			t.Errorf("timeline = %q", tl)
		}
	})

	t.Run("bounded", func(t *testing.T) {
		r := acPollBounded(t, false, acClosedPort(t), token, clock, time.Millisecond, 3)
		if r.reached || len(r.dials) != 3 {
			t.Fatalf("reached=%v after %d dial(s), want 3 unanswered dials", r.reached, len(r.dials))
		}
	})

	t.Run("stopped", func(t *testing.T) {
		// Capped at one dial: a poll that dials despite the closed stop still records that dial
		// and fails here, instead of dialing until go test's timeout.
		r := acPollBounded(t, true, acClosedPort(t), token, clock, time.Millisecond, 1)
		if len(r.dials) != 0 || !strings.HasSuffix(r.describe("x"), "never dialed") {
			t.Errorf("a closed stop still dialed: %s", r.describe("x"))
		}
	})
}

func acDials(phase acPhase, kind string, from time.Duration, n int) []acDial {
	out := make([]acDial, n)
	for i := range out {
		out[i] = acDial{at: from + time.Duration(i)*500*time.Millisecond, phase: phase, kind: kind}
	}
	return out
}

// TestACListenerTimelineRunLengthEncodes pins the record's shape: runs in ORDER, split on a
// change of phase OR of kind, never merged into a histogram. A refusal while the jail ran and
// refusals after its script ended share a kind and must stay two runs, or the record hides
// which refusals came after the exit — the fourth Mac run's misreading.
func TestACListenerTimelineRunLengthEncodes(t *testing.T) {
	var dials []acDial
	dials = append(dials, acDials(acPhaseStarting, acKindRefused, 0, 2)...)
	dials = append(dials, acDials(acPhaseRunning, acKindReset, time.Second, 12)...)
	dials = append(dials, acDials(acPhaseRunning, acKindRefused, 7*time.Second, 1)...)
	dials = append(dials, acDials(acPhaseEnded, acKindRefused, 9*time.Second, 3)...)
	r := acListenerResult{addr: "127.0.0.1:5555", dials: dials}
	want := []string{
		"before the jail reported listening: 2× refused (ECONNREFUSED) (t=0.0s–0.5s)",
		"while the jail ran: 12× accepted, then reset (ECONNRESET) (t=1.0s–6.5s)",
		"while the jail ran: 1× refused (ECONNREFUSED) (t=7.0s)",
		"after the jail's script ended: 3× refused (ECONNREFUSED) (t=9.0s–10.0s)",
	}
	if got := r.timeline(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("timeline:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := r.describe("IPv4-only listener"); !strings.HasPrefix(got,
		"IPv4-only listener 127.0.0.1:5555: not reached in 18 dial(s)\n    before the jail reported listening: 2×") {
		t.Errorf("describe:\n%s", got)
	}
	if got := (acListenerResult{}).describe("gateway"); got != "gateway: no address to dial" {
		t.Errorf("describe with no address = %q", got)
	}
}

// TestACJailPhaseReadsTheMarkers pins the phase reading on the two marker files, in the order
// the script writes them. The running phase keys on the listen file, not on the address file the
// jail writes a second or more later, after its in-jail diagnostic.
func TestACJailPhaseReadsTheMarkers(t *testing.T) {
	dir := t.TempDir()
	for _, step := range []struct {
		write string
		want  acPhase
	}{
		{"", acPhaseStarting},
		{acPortListenFile, acPhaseRunning},
		{acPortAddrFile, acPhaseRunning},
		{acPortExitFile, acPhaseEnded},
	} {
		if step.write != "" {
			if err := os.WriteFile(filepath.Join(dir, step.write), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := acJailPhase(dir); got != step.want {
			t.Errorf("after writing %q: phase %q, want %q", step.write, got, step.want)
		}
		if got := (acProbeClock{start: time.Now(), dir: dir}).stamp().phase; got != step.want {
			t.Errorf("after writing %q: stamp phase %q, want %q", step.write, got, step.want)
		}
	}
}

// TestACPublishedPortScriptMarksItsPhases is the jail half of acJailPhase: without the exit
// marker, a dial made while the container is going away reads as one made while it served.
func TestACPublishedPortScriptMarksItsPhases(t *testing.T) {
	s := acPublishedPortScript("V4", "DUAL", ".yolo-it-dialed")
	grace := strings.Index(s, "\nsleep 1\n")
	listen := strings.Index(s, "echo listening > /workspace/"+acPortListenFile)
	listening := strings.Index(s, "\necho LISTENING\n")
	diag := strings.Index(s, `echo "=== DIAG ==="`)
	addr := strings.Index(s, "> /workspace/"+acPortAddrFile)
	endDiag := strings.Index(s, `echo "=== END DIAG ==="`)
	wait := strings.Index(s, "[ -f /workspace/.yolo-it-dialed ] && break")
	ended := strings.Index(s, "echo ended > /workspace/"+acPortExitFile)
	end := strings.Index(s, `echo "=== END ==="`)
	at := []int{grace, listen, listening, diag, addr, endDiag, wait, ended, end}
	names := "grace, listen, LISTENING, diag, addr, end-diag, wait, ended, end"
	for _, i := range at {
		if i < 0 {
			t.Fatalf("a marker is missing (%s = %v):\n%s", names, at, s)
		}
	}
	for k := 1; k < len(at); k++ {
		if at[k-1] >= at[k] {
			t.Errorf("markers out of order (%s = %v): the listen marker must follow the servers' "+
				"one-second grace and precede the in-jail diagnostic, and the exit marker must be "+
				"the script's last write, after it stops waiting for the Mac", names, at)
			break
		}
	}
}

const (
	acRouteDirect = `   route to: 192.168.64.3
destination: 192.168.64.3
  interface: bridge100
      flags: <UP,HOST,DONE,LLINFO,WASCLONED,IFSCOPE,IFREF>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500      1180
`
	acRouteSubnet = `   route to: 192.168.64.3
destination: 192.168.64.0
       mask: 255.255.255.0
  interface: bridge100
      flags: <UP,DONE,CLONING,IFSCOPE,IFREF>
`
	acRouteDefault = `   route to: 192.168.64.3
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,IFSCOPE,GLOBAL>
`
	acRouteDown = `   route to: 192.168.64.3
destination: 192.168.64.3
  interface: bridge100
      flags: <HOST,DONE,LLINFO,WASCLONED,IFSCOPE,IFREF>
`
)

// TestACParseRoute pins what counts as a live route: directly attached and up. A default route
// through a gateway exists for every address on a Mac, so it must not count.
func TestACParseRoute(t *testing.T) {
	for _, tc := range []struct {
		name      string
		out       string
		err       error
		wantLive  bool
		wantIface string
	}{
		{"host route on bridge100", acRouteDirect, nil, true, "bridge100"},
		{"subnet route on bridge100", acRouteSubnet, nil, true, "bridge100"},
		{"default route through a gateway", acRouteDefault, nil, false, "en0"},
		{"route not UP", acRouteDown, nil, false, "bridge100"},
		{"not in table", "route: writing to routing socket: not in table\n", errors.New("exit status 1"), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := acParseRoute("192.168.64.3", tc.out, tc.err)
			if r.live != tc.wantLive || r.iface != tc.wantIface {
				t.Errorf("live=%v iface=%q, want live=%v iface=%q", r.live, r.iface, tc.wantLive, tc.wantIface)
			}
			if !strings.HasPrefix(r.text, "route -n get 192.168.64.3") {
				t.Errorf("text = %q", r.text)
			}
		})
	}
}

// acDeniedEvidence is the Mac-side evidence the fourth Mac run's shape produces: both
// container-address targets EHOSTUNREACH, the gateway accepting then resetting, and a live route
// on bridge100.
func acDeniedEvidence() acMacEvidence {
	return acMacEvidence{
		probes: []acMacProbe{
			{label: "container address, IPv4-only listener", container: true, result: acListenerResult{
				addr: "192.168.64.3:18765", dials: acDials(acPhaseRunning, acKindNoRoute, 2*time.Second, 5)}},
			{label: "container address, dual-stack listener", container: true, result: acListenerResult{
				addr: "192.168.64.3:18766", dials: acDials(acPhaseRunning, acKindNoRoute, 2*time.Second, 5)}},
			{label: "published port on the vmnet gateway, IPv4-only", result: acListenerResult{
				addr: "192.168.64.1:50001", dials: acDials(acPhaseRunning, acKindReset, 2*time.Second, 5)}},
		},
		route:     acParseRoute("192.168.64.3", acRouteDirect, nil),
		inspect:   "container inspect h, published ports and networks: {}",
		listeners: "lsof -nP -sTCP:LISTEN (err=<nil>):",
	}
}

// TestACPortEvidenceNamesLocalNetworkPrivacy pins acPortEvidence, the #10 verdict's evidence:
// it carries every dial's timeline, and it names Local Network privacy, with the fix, exactly
// when the signature is there, and not for any look-alike.
func TestACPortEvidenceNamesLocalNetworkPrivacy(t *testing.T) {
	loopback := acListenerResult{addr: "127.0.0.1:50001",
		dials: append(acDials(acPhaseRunning, acKindReset, time.Second, 12), acDials(acPhaseEnded, acKindRefused, 8*time.Second, 3)...)}
	bridge := acPortResult{mode: "bridge", v4: loopback, dual: loopback, mac: acDeniedEvidence()}
	host := acPortResult{mode: "host", v4: loopback, dual: loopback, mac: acMacEvidence{missing: "the jail exited before writing .yolo-it-addr; no Mac-side evidence"}}

	ev := acPortEvidence(bridge, host)
	if !strings.HasPrefix(ev, "DIAGNOSIS — macOS LOCAL NETWORK PRIVACY") {
		t.Errorf("the evidence does not LEAD with the Local Network privacy diagnosis:\n%s", ev)
	}
	for _, want := range []string{
		"bridge launch: the container at 192.168.64.3, route on bridge100",
		"System Settings → Privacy & Security → Local Network",
		"container-apiserver, container-runtime-linux, container-network-vmnet",
		"Developer-ID-signed `container` package",
		"inferred, not measured",
		// The timelines, through acPortResult.describe and acMacEvidence.String.
		"while the jail ran: 12× accepted, then reset (ECONNRESET)",
		"after the jail's script ended: 3× refused (ECONNREFUSED)",
		"container address, IPv4-only listener 192.168.64.3:18765: not reached in 5 dial(s)",
		"while the jail ran: 5× no route to host (EHOSTUNREACH)",
		"route -n get 192.168.64.3",
		"the jail exited before writing .yolo-it-addr",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("the evidence lacks %q:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "host launch:") {
		t.Errorf("the diagnosis names the host launch, which had no Mac-side evidence:\n%s", ev)
	}

	t.Run("a published port answered", func(t *testing.T) {
		// The run after the helpers are granted and the go test binary is not: the forwarder
		// reaches the container, so the inference about the helpers is refuted, and the
		// diagnosis must not tell the reader this run measured permissions instead of #10.
		b := bridge
		b.v4 = acListenerResult{addr: "127.0.0.1:50001", reached: true,
			dials: acDials(acPhaseRunning, acKindReached, time.Second, 1)}
		ev := acPortEvidence(b, host)
		if !strings.HasPrefix(ev, "DIAGNOSIS — macOS LOCAL NETWORK PRIVACY") {
			t.Errorf("the test process's own denial is no longer diagnosed:\n%s", ev)
		}
		if !strings.Contains(ev, "A published port DID answer") || !strings.Contains(ev, "does not decide #10") {
			t.Errorf("the diagnosis does not say a published port answered:\n%s", ev)
		}
		for _, stale := range []string{"inferred, not measured", "Until then this experiment measures"} {
			if strings.Contains(ev, stale) {
				t.Errorf("the diagnosis still carries %q, which a published port answering refutes:\n%s", stale, ev)
			}
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*acMacEvidence)
	}{
		{"route through a gateway", func(e *acMacEvidence) { e.route = acParseRoute("192.168.64.3", acRouteDefault, nil) }},
		{"no route", func(e *acMacEvidence) {
			e.route = acParseRoute("192.168.64.3", "not in table", errors.New("exit status 1"))
		}},
		{"a container dial reached", func(e *acMacEvidence) {
			e.probes[1].result.reached = true
			e.probes[1].result.dials = append(e.probes[1].result.dials, acDial{phase: acPhaseRunning, kind: acKindReached})
		}},
		{"container dials refused, not unreachable", func(e *acMacEvidence) {
			for i := range e.probes[:2] {
				e.probes[i].result.dials = acDials(acPhaseRunning, acKindRefused, 0, 5)
			}
		}},
		{"EHOSTUNREACH only on a published port", func(e *acMacEvidence) {
			for i := range e.probes[:2] {
				e.probes[i].result.dials = acDials(acPhaseRunning, acKindConnTimeout, 0, 5)
			}
			e.probes[2].result.dials = acDials(acPhaseRunning, acKindNoRoute, 0, 5)
		}},
		{"no container dial made", func(e *acMacEvidence) {
			for i := range e.probes[:2] {
				e.probes[i].result = acListenerResult{}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mac := acDeniedEvidence()
			tc.mutate(&mac)
			b := bridge
			b.mac = mac
			if ev := acPortEvidence(b, host); strings.Contains(ev, "LOCAL NETWORK PRIVACY") {
				t.Errorf("named Local Network privacy without its signature:\n%s", ev)
			}
		})
	}
}

// TestACPortVerdict pins the #10 verdict's four arms, and that the no-port arm defers to the
// Local Network privacy diagnosis when the evidence carries its signature instead of calling
// `-p` itself the finding right above a diagnosis blaming the runner.
func TestACPortVerdict(t *testing.T) {
	reached := acListenerResult{addr: "127.0.0.1:50001", reached: true}
	refused := acListenerResult{addr: "127.0.0.1:50002", dials: acDials(acPhaseRunning, acKindReset, time.Second, 3)}
	up := func(mode string) acPortResult { return acPortResult{mode: mode, v4: reached, dual: refused} }
	down := func(mode string, mac acMacEvidence) acPortResult {
		return acPortResult{mode: mode, v4: refused, dual: refused, mac: mac}
	}
	plain := acMacEvidence{missing: "no Mac-side evidence"}
	for _, tc := range []struct {
		name         string
		bridge, host acPortResult
		holds        bool
		want, reject string
	}{
		{"both modes reach", up("bridge"), up("host"), true, "exactly as under the default", ""},
		{"only the default reaches", up("bridge"), down("host", plain), false, "the defect #10 fixed is back", ""},
		{"only host mode reaches", down("bridge", plain), up("host"), false, "the reverse of the defect", ""},
		{"neither, no signature", down("bridge", plain), down("host", plain), false,
			"the larger finding is `-p` itself", "Local Network"},
		{"neither, with the signature", down("bridge", acDeniedEvidence()), down("host", plain), false,
			"Local Network privacy's signature", "the larger finding is `-p` itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holds, finding := acPortVerdict(tc.bridge, tc.host)
			if holds != tc.holds || !strings.Contains(finding, tc.want) ||
				(tc.reject != "" && strings.Contains(finding, tc.reject)) {
				t.Errorf("acPortVerdict = %v, %q; want %v, containing %q and not %q",
					holds, finding, tc.holds, tc.want, tc.reject)
			}
		})
	}
}

// TestACHaltOnCleanupStopsTheDialersOnGoexit: runCommand fails a launch with t.Fatalf, which is
// runtime.Goexit and skips acPublishedPortProbe's explicit halt. The loopback polls have no dial
// cap, so the cleanup must stop them anyway, and the explicit call plus the cleanup must not close
// stop twice.
func TestACHaltOnCleanupStopsTheDialersOnGoexit(t *testing.T) {
	// One dial in flight when stop closes is bounded by acDialLine's connect and read timeouts;
	// past both, with margin, the poll is not stopping.
	const haltBound = acConnectTimeout + acReadTimeout + 5*time.Second
	addr := acClosedPort(t)
	clock := acProbeClock{start: time.Now(), dir: t.TempDir()}
	start := func(t *testing.T) (chan acListenerResult, func()) {
		stop := make(chan struct{})
		done := make(chan acListenerResult, 1)
		var dialers sync.WaitGroup
		dialers.Add(1)
		go func() {
			defer dialers.Done()
			done <- acPollListener(stop, addr, "TOKEN", clock, 5*time.Millisecond, 0)
		}()
		// The wait is bounded, so a poll that no longer honors stop fails the test instead of
		// holding halt, and the test, until go test's timeout.
		wait := func() {
			waited := make(chan struct{})
			go func() { dialers.Wait(); close(waited) }()
			select {
			case <-waited:
			case <-time.After(haltBound):
				select {
				case <-waited: // the dialers returned while this process was not running
				default:
					t.Errorf("the uncapped poll was still dialing %s after halt closed stop", haltBound)
				}
			}
		}
		return done, acHaltOnCleanup(t, stop, wait)
	}

	var done chan acListenerResult
	t.Run("a launch that dies in t.Fatalf", func(t *testing.T) {
		done, _ = start(t)
		t.Skip("stands in for runCommand's t.Fatalf: both are runtime.Goexit, and a skip does not fail the parent")
	})
	select {
	case <-done:
	default:
		t.Error("the uncapped poll was still dialing after its test ended: nothing closed stop on the Goexit path")
	}

	t.Run("halted, then cleaned up", func(t *testing.T) {
		done, halt := start(t)
		halt() // a second close(stop) in the cleanup would panic
		select {
		case <-done:
		default:
			t.Error("halt returned before the dialers did")
		}
	})
}

// acFuncBody returns the source of func name in src, through its closing brace at column 0.
func acFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	i := strings.Index(src, "\nfunc "+name+"(")
	if i < 0 {
		t.Fatalf("func %s is gone from applecontainerparity_test.go; this test has lost its subject", name)
	}
	j := strings.Index(src[i:], "\n}\n")
	if j < 0 {
		t.Fatalf("could not find the end of func %s", name)
	}
	return src[i : i+j]
}

// TestACPortProbeCallSites is the CALL-SITE half for the parts only the Mac runs. The unit tests
// above pass whether or not the hardware test calls what they pin, the shape AGENTS.md names
// ("does it fail if I delete the call site?"), and the hardware test cannot run here. So this
// reads the source, as TestRequireAppleContainerInstallsTheLateSkipGuard does.
func TestACPortProbeCallSites(t *testing.T) {
	b, err := os.ReadFile("applecontainerparity_test.go")
	if err != nil {
		t.Fatalf("reading the probe's source: %v", err)
	}
	src := string(b)
	for _, tc := range []struct {
		fn    string
		wants []string
		why   string
	}{
		{"TestAppleContainerExplicitHostModeKeepsPublishedPorts", []string{"evidence := acPortEvidence(bridge, host)",
			"holds, finding := acPortVerdict(bridge, host)", "acParityRecord(t, fix, holds, finding, evidence)"},
			"the verdict no longer goes through acPortEvidence and acPortVerdict, so the dial timelines, " +
				"the Local Network privacy diagnosis, or the verdict line that defers to it are lost"},
		{"acPublishedPortProbe", []string{"acProbeClock{start: time.Now(), dir: dir}",
			"r.v4 = acPollListener(", "r.dual = acPollListener(", "r.mac = acPortMacDiag(stop, clock,",
			"halt := acHaltOnCleanup(t, stop, marked.Wait)", "appleContainerEnv())\n\thalt()"},
			"the published-port dialers no longer record every dial with its time and phase, or are " +
				"no longer stopped both after the launch and when a fatal launch skips that"},
		{"acPortMacDiag", []string{"acPollListener(stop, addr, tg.token, clock,", "route:     acPortRoute(jailIP)"},
			"the Mac-side probes no longer record every dial, or no longer carry the parsed route " +
				"localNetworkDenied needs"},
		{"acPortRoute", []string{"return acParseRoute(jailIP, string(out), err)"},
			"the route is no longer parsed, so it can never be live"},
	} {
		body := acFuncBody(t, src, tc.fn)
		for _, want := range tc.wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s no longer contains %q: %s", tc.fn, want, tc.why)
			}
		}
	}
}
