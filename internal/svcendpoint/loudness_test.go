package svcendpoint

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// EVERY TEST IN THIS FILE FAILS IF A PRODUCTION Logger CALL IS DELETED, and that is
// the only reason the file exists. The defect class is a decision this package takes
// — a bind that failed, an accept loop that died, a deadline it could not clear, a
// probe that answered "no" — and reports nowhere.
//
// # Why loudness is the ONLY instrument for half of this package
//
// svcendpoint owns the BIND/ADVERTISE PAIR: the address a listener binds
// (127.0.0.1, always) versus the name it publishes for a jail to dial. A wrong
// advertise value cannot be caught by any test on this machine — podman-in-podman
// forces --net=host, so the two loopbacks are ONE and a mismatch cannot reproduce
// (AGENTS.md, Testing, carve-out 1; docs/reference/loopback-tls-reachability.md,
// "a nested jail is structurally blind to this"). What a test CAN pin is that the
// runtime line states both halves and where the advertised one came from, which is
// what turns "the jail cannot reach this service" from a hypothesis into a reading.
//
// # The sink
//
// The package Logger (listen.go), which is a *log.Logger on stderr with no level and
// no dial — a host daemon's stderr is its log file, and the entrypoint's is boot.log.
// Nothing here is gated, and nothing may become gated: OQ-RO3 (report-tiers.md)
// forbids suppressing a disclosure, and the jail diagnostic TIER is an unbuilt design
// awaiting a ruling (docs/design/diagnostics-past-the-boundary.md).

// waitForLog polls the captured log for want, because most of these lines are
// emitted by the accept loop's goroutine rather than by the call under test.
func waitForLog(t *testing.T, logs *syncBuf, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := logs.String()
		if strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("no logged line contains %q after 3s.\nlog:\n%s", want, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireAllInLog(t *testing.T, what, log string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(log, w) {
			t.Errorf("%s does not mention %q; got:\n%s", what, w, log)
		}
	}
}

// TestBindFailureNamesTheAddressAndTheSyscallError: "address already in use" with no
// address is the bug. The bind is 127.0.0.1:0 and cannot be made to fail for real,
// which is exactly why listenLoopback is indirected — a host with no usable loopback
// (an empty netns, EMFILE, a sandbox policy) gets this line and nothing else.
func TestBindFailureNamesTheAddressAndTheSyscallError(t *testing.T) {
	logs := captureLogger(t)
	syscallErr := errors.New("bind: address already in use")
	prev := listenLoopback
	listenLoopback = func(addr string) (net.Listener, error) {
		return nil, &net.OpError{Op: "listen", Net: "tcp", Err: syscallErr}
	}
	t.Cleanup(func() { listenLoopback = prev })

	path := filepath.Join(privateDir(t), "svc.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen succeeded although the bind failed")
	}
	requireAllInLog(t, "the returned error", err.Error(), bindAddr, "address already in use", path)
	if !errors.Is(err, syscallErr) {
		t.Errorf("the syscall error did not survive the wrap: %v", err)
	}
	requireAllInLog(t, "the bind failure line", logs.String(), bindAddr, "address already in use", path)
}

// TestTheListenLineStatesTheBindAdvertisePairAndItsProvenance is the item this
// package cannot test any other way. It pins three things per bind: the address that
// was BOUND, the address that was ADVERTISED (same port, different host), and WHICH
// OF THE THREE SOURCES chose the advertised host — because those are three different
// fixes, and a reader debugging an unreachable service cannot otherwise tell whether
// the run pipeline, a human's environment override, or the runtime gateway default
// is the half that is wrong.
func TestTheListenLineStatesTheBindAdvertisePairAndItsProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, advertise, env, wantHost, wantSource string
	}{
		{"caller-supplied wins", "127.0.0.1", "", "127.0.0.1", advertiseFromCaller},
		{"the env override", "", "gateway.example", "gateway.example", advertiseFromEnv},
		{"the runtime default", "", "", DefaultAdvertiseHost, advertiseFromDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AdvertiseHostEnv, tc.env) // "" is "unset" to AdvertiseHost
			logs := captureLogger(t)
			path := filepath.Join(privateDir(t), "svc.endpoint")
			ln, err := Listen(path, tc.advertise)
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}
			t.Cleanup(func() { _ = ln.Close() })

			bound := ln.Addr().String()
			_, port, err := net.SplitHostPort(bound)
			if err != nil {
				t.Fatalf("bound address %q does not split: %v", bound, err)
			}
			line := logs.String()
			requireAllInLog(t, "the listen line", line,
				bound,                               // the BIND half, verbatim
				net.JoinHostPort(tc.wantHost, port), // the ADVERTISE half, same port
				tc.wantSource,                       // and who chose it
				path)                                // and where it was published
			// The published file is a CREDENTIAL; the line that describes it must not
			// quote it. Pinned here because this is the line most likely to be edited.
			ep, err := Read(path)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if strings.Contains(line, ep.Token) {
				t.Error("the listen line printed the bearer token")
			}
			if strings.Contains(line, base64.StdEncoding.EncodeToString(ep.CertDER)) {
				t.Error("the listen line printed the certificate")
			}
		})
	}
}

// TestCredentialMintFailureIsReported: both mints draw on crypto/rand, so a failure
// means a host whose entropy source is broken — and the caller's error is the only
// other trace, which a daemon that logs "could not start" swallows. The line names
// the endpoint that will therefore not exist.
func TestCredentialMintFailureIsReported(t *testing.T) {
	boom := errors.New("crypto/rand: getrandom failed")

	t.Run("the certificate", func(t *testing.T) {
		logs := captureLogger(t)
		prev := mintCertificate
		mintCertificate = func() (tls.Certificate, []byte, error) { return tls.Certificate{}, nil, boom }
		t.Cleanup(func() { mintCertificate = prev })
		path := filepath.Join(privateDir(t), "svc.endpoint")
		if ln, err := Listen(path, "127.0.0.1"); err == nil {
			_ = ln.Close()
			t.Fatal("Listen succeeded with no certificate")
		} else if !strings.Contains(err.Error(), path) {
			t.Errorf("the error does not name the endpoint: %v", err)
		}
		requireAllInLog(t, "the cert-mint line", logs.String(), path, "getrandom failed")
	})

	t.Run("the token", func(t *testing.T) {
		logs := captureLogger(t)
		prev := mintBearerToken
		mintBearerToken = func() (string, error) { return "", boom }
		t.Cleanup(func() { mintBearerToken = prev })
		path := filepath.Join(privateDir(t), "svc.endpoint")
		if ln, err := Listen(path, "127.0.0.1"); err == nil {
			_ = ln.Close()
			t.Fatal("Listen succeeded with no token")
		} else if !strings.Contains(err.Error(), path) {
			t.Errorf("the error does not name the endpoint: %v", err)
		}
		requireAllInLog(t, "the token-mint line", logs.String(), path, "getrandom failed")
	})
}

// TestAFailedPublicationSaysWhichBindItThrowsAway is the fault that is loudest in its
// consequences and quietest in its evidence: the bind SUCCEEDED, so a port was held
// and a credential minted, and then nothing was published — which every consumer
// sees as "the service never came up", identical to a daemon that never ran.
func TestAFailedPublicationSaysWhichBindItThrowsAway(t *testing.T) {
	logs := captureLogger(t)
	path := filepath.Join(privateDir(t), "svc.endpoint")
	// The DIRECTORY is fine (0700, ours), so the pre-bind check passes and the
	// failure lands on the rename — which is the interesting ordering.
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatalf("planting the obstruction: %v", err)
	}
	ln, err := Listen(path, "127.0.0.1")
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen succeeded although nothing could be published")
	}
	requireAllInLog(t, "the publication failure line", logs.String(),
		path, "127.0.0.1:", "retired unused")
}

// TestAnUnreachableUpstreamIsReported pins a line that already existed, because the
// front publishes BEFORE its upstream is known to be there (deliberately) — so a
// daemon that never bound its Unix socket yields a healthy-looking endpoint whose
// every connection dies, and this is the only place that says why.
func TestAnUnreachableUpstreamIsReported(t *testing.T) {
	logs := captureLogger(t)
	upstream := filepath.Join(privateSocketDir(t), "absent.sock")
	assertSockPathFits(t, upstream)
	endpoint := filepath.Join(privateDir(t), "front.endpoint")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	for !Probe(endpoint) {
		time.Sleep(5 * time.Millisecond)
	}
	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err) // the front is up; only the upstream is missing
	}
	defer func() { _ = conn.Close() }()
	requireAllInLog(t, "the upstream dial line", waitForLog(t, logs, "dial upstream"), upstream)
}

// deadAcceptListener is a bound listener whose Accept fails the way a host out of
// file descriptors does. It embeds a REAL listener so Addr() is a *net.TCPAddr,
// which listenWith asserts on.
type deadAcceptListener struct {
	net.Listener
	err error
}

func (d *deadAcceptListener) Accept() (net.Conn, error) { return nil, d.err }

// TestAnAcceptLoopThatDiesIsReported: the end of the accept loop is the end of the
// service, and the published endpoint file survives it — so the jail's next dial
// fails at connect and `yolo check` calls the front dead, with nothing anywhere
// saying when or why it stopped. ServeFrontWithOptions cannot be the place that
// reports it: it returns nil on any accept failure and every caller in the tree
// discards that return.
func TestAnAcceptLoopThatDiesIsReported(t *testing.T) {
	logs := captureLogger(t)
	bound, err := net.Listen("tcp", bindAddr)
	if err != nil {
		t.Fatalf("test listener: %v", err)
	}
	sentinel := errors.New("accept: too many open files")
	prev := listenLoopback
	listenLoopback = func(addr string) (net.Listener, error) {
		return &deadAcceptListener{Listener: bound, err: sentinel}, nil
	}
	t.Cleanup(func() { listenLoopback = prev; _ = bound.Close() })

	path := filepath.Join(privateDir(t), "svc.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	if _, err := ln.Accept(); !errors.Is(err, sentinel) {
		t.Errorf("Accept error = %v, want the accept failure", err)
	}
	line := waitForLog(t, logs, "accept loop")
	requireAllInLog(t, "the dead-accept-loop line", line,
		path, "too many open files", bound.Addr().String())
}

// TestAnOrdinaryCloseIsNotReportedAsADeadAcceptLoop is the control for the test
// above: Close is the caller's own act and makes Accept return net.ErrClosed, so
// reporting it would put a scary line in every clean shutdown — which is how an
// always-on line earns its deletion.
func TestAnOrdinaryCloseIsNotReportedAsADeadAcceptLoop(t *testing.T) {
	logs := captureLogger(t)
	s := startServer(t)
	if err := s.ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // long enough for the accept loop to exit
	if got := logs.String(); strings.Contains(got, "accept loop") {
		t.Errorf("a clean Close was reported as a dead accept loop:\n%s", got)
	}
}

// TestFailingToRetireTheEndpointFileIsReported: Close UNLINKS the published file, so
// a failure there leaves a live-looking credential naming a dead port. Both front
// paths discard Close's error, which is why the report lives in Close itself.
//
// The unlink is made to fail in a way that works for ANY uid, root included: a
// non-empty directory where the file was.
func TestFailingToRetireTheEndpointFileIsReported(t *testing.T) {
	logs := captureLogger(t)
	path := filepath.Join(privateDir(t), "svc.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("clearing the published file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatalf("planting an unremovable path: %v", err)
	}

	err = ln.Close()
	if err == nil {
		t.Error("Close reported success although the endpoint file could not be retired")
	}
	requireAllInLog(t, "the retirement line", logs.String(), "retiring", path)
}

// TestAnAuthenticatedConnectionDroppedAtShutdownIsReported: the client got its ack
// and then the connection died, which from its side is a successful handshake
// followed by nothing. The tier-1 record calls that an accepted crossing with zero
// bytes — indistinguishable from an idle client — so the drop says so itself.
func TestAnAuthenticatedConnectionDroppedAtShutdownIsReported(t *testing.T) {
	logs := captureLogger(t)
	path := filepath.Join(privateDir(t), "svc.endpoint")
	// NOTHING CALLS Accept, so an authenticated connection has nowhere to go.
	ln, err := Listen(path, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	conn, err := Dial(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	_ = ln.Close()
	requireAllInLog(t, "the dropped-connection line",
		waitForLog(t, logs, "dropping an authenticated connection"), path)
}

// deadlineFussyConn sets deadlines but cannot CLEAR them — the one failure that is
// invisible by construction, because the connection then works normally until every
// read on it dies at handshakeTimeout, mid-stream, with no error text.
type deadlineFussyConn struct {
	net.Conn
	clearErr error
}

func (d *deadlineFussyConn) SetReadDeadline(when time.Time) error {
	if when.IsZero() {
		return d.clearErr
	}
	return d.Conn.SetReadDeadline(when)
}

func (d *deadlineFussyConn) SetDeadline(when time.Time) error {
	if when.IsZero() {
		return d.clearErr
	}
	return d.Conn.SetDeadline(when)
}

// TestAFailedDeadlineClearIsReported covers all three clears — the server's after
// authentication, the client's after the ack, and the preamble reader's deferred one.
// Each must still SUCCEED (refusing an authenticated connection over a deadline would
// turn a five-second stream into no stream at all) and must say what it did.
func TestAFailedDeadlineClearIsReported(t *testing.T) {
	clearErr := errors.New("setsockopt: invalid argument")

	t.Run("the server's, after authentication", func(t *testing.T) {
		logs := captureLogger(t)
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		token, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_ = writeTokenFrame(client, token)
			var ack [1]byte
			_, _ = io.ReadFull(client, ack[:])
		}()
		if err := verifyTokenFrame(&deadlineFussyConn{Conn: server, clearErr: clearErr}, token); err != nil {
			t.Fatalf("a good token was refused because the deadline clear failed: %v", err)
		}
		requireAllInLog(t, "the server's deadline line", waitForLog(t, logs, "clearing"),
			"invalid argument", handshakeTimeout.String())
	})

	t.Run("the client's, after the ack", func(t *testing.T) {
		logs := captureLogger(t)
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		go func() { _, _ = server.Write([]byte{authAck}) }()
		if err := readAck(&deadlineFussyConn{Conn: client, clearErr: clearErr}); err != nil {
			t.Fatalf("a good ack was rejected because the deadline clear failed: %v", err)
		}
		requireAllInLog(t, "the client's deadline line", waitForLog(t, logs, "clearing"),
			"invalid argument", handshakeTimeout.String())
	})

	t.Run("the preamble reader's", func(t *testing.T) {
		logs := captureLogger(t)
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		go func() {
			_, _ = client.Write(encodePreamble(Preamble{JailID: "j", Service: "s", V: PreambleVersion}))
		}()
		p, err := ReadPreamble(&deadlineFussyConn{Conn: server, clearErr: clearErr})
		if err != nil {
			t.Fatalf("a good preamble was rejected because the deadline clear failed: %v", err)
		}
		if p.JailID != "j" {
			t.Errorf("preamble = %+v, want the one that was written", p)
		}
		requireAllInLog(t, "the preamble deadline line", waitForLog(t, logs, "clearing"),
			"invalid argument", handshakeTimeout.String())
	})
}

// TestAFailedUpstreamHalfCloseIsReported is the silent-hang shape itself: the
// front's CloseWrite is what tells a daemon its request has ended, so a failure
// there means no reply, which means the response copy never returns, which means
// this connection's goroutines and fds live until the process exits.
//
// Reproduced without a fake, from splice's own measured ordering. The upstream
// READS the connection preamble (so the request-direction copy's one write
// succeeds) and only then hangs up, which makes the response direction return
// while the request direction is still blocked reading from a client that stays
// SILENT AND OPEN. splice's deferred closes then run — up first, client second —
// and only the second unblocks the request copy, which reaches CloseWrite on a
// socket yolo itself has already closed. Every step is ordered by a real wakeup,
// so there is no sleep and no race: reverse any of the three and CloseWrite
// returns nil.
func TestAFailedUpstreamHalfCloseIsReported(t *testing.T) {
	logs := captureLogger(t)
	dir := privateSocketDir(t)
	upstream := filepath.Join(dir, "up.sock")
	assertSockPathFits(t, upstream)
	uln, err := net.Listen("unix", upstream)
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	t.Cleanup(func() { _ = uln.Close() })
	go func() {
		for {
			c, err := uln.Accept()
			if err != nil {
				return
			}
			// Read the preamble's length prefix, THEN hang up with no reply. The read
			// is what makes the request direction's write succeed; the close is what
			// ends the response direction.
			var lenPrefix [4]byte
			_, _ = io.ReadFull(c, lenPrefix[:])
			_ = c.Close()
		}
	}()

	endpoint := filepath.Join(privateDir(t), "front.endpoint")
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = ServeFront(endpoint, "127.0.0.1", upstream, stop) }()
	for !Probe(endpoint) {
		time.Sleep(5 * time.Millisecond)
	}

	conn, err := Dial(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	requireAllInLog(t, "the half-close line", waitForLog(t, logs, "half-closing upstream"), upstream)
}

// TestPublishFailureNamesTheTargetPath: os.File's own errors carry the TEMP name — a
// random ".endpoint-1873492" that no longer exists by the time anyone reads the
// message — so without the target nobody can tell which service failed to publish.
func TestPublishFailureNamesTheTargetPath(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "svc.endpoint")
	// A non-empty directory at the target: rename onto it fails for any uid.
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatalf("planting the obstruction: %v", err)
	}

	err := Publish(path, sampleEndpoint(t))
	if err == nil {
		t.Fatal("Publish succeeded onto a directory")
	}
	requireAllInLog(t, "the publish error", err.Error(), path, "rename")

	// And the partial credential is gone: the temp lives in the same 0700 directory.
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".endpoint-") {
			t.Errorf("a partial publication was left behind: %s", e.Name())
		}
	}
}

// TestListenNamesTheFileItWillNotPublish: the directory check's errors name the
// DIRECTORY, which several services share a parent of; the file is what identifies
// the service that will not come up.
func TestListenNamesTheFileItWillNotPublish(t *testing.T) {
	dir := privateDir(t)
	if err := os.Chmod(dir, 0o755); err != nil { // group/world-accessible: refused
		t.Fatal(err)
	}
	path := filepath.Join(dir, "svc.endpoint")
	ln, err := Listen(path, "127.0.0.1")
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen published a credential into a world-readable directory")
	}
	requireAllInLog(t, "the refusal", err.Error(), path, dir)
}

// TestProbeSaysWhyTheFileIsUnusable. Probe is the readiness predicate for every
// service in the launch path and its answer is a bool, so the reason it computed had
// nowhere to go: a caller polls until a deadline and reports a timeout, while the
// file that was there the whole time and unusable goes undescribed.
func TestProbeSaysWhyTheFileIsUnusable(t *testing.T) {
	good := sampleEndpoint(t)
	goodCert := base64.StdEncoding.EncodeToString(good.CertDER)

	for _, tc := range []struct {
		name, line string
		wantClass  string
		wantSaid   []string
	}{
		{
			name:      "a truncated publication",
			line:      good.HostPort + " " + goodCert + "\n",
			wantClass: probeFaultUnreadable,
			wantSaid:  []string{"3 whitespace-separated fields"},
		},
		{
			name:      "an address that does not split",
			line:      "host.containers.internal " + goodCert + " " + good.Token + "\n",
			wantClass: probeFaultAddress,
			wantSaid:  []string{`"host.containers.internal"`},
		},
		{
			name:      "a certificate that does not parse",
			line:      good.HostPort + " " + base64.StdEncoding.EncodeToString([]byte("not a cert")) + " " + good.Token + "\n",
			wantClass: probeFaultCert,
			wantSaid:  []string{"x509"},
		},
		{
			name:      "a token that is not a token",
			line:      good.HostPort + " " + goodCert + " nothex\n",
			wantClass: probeFaultToken,
			wantSaid:  []string{"6 characters"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogger(t)
			path := filepath.Join(privateDir(t), "svc.endpoint")
			if err := os.WriteFile(path, []byte(tc.line), 0o600); err != nil {
				t.Fatal(err)
			}
			if Probe(path) {
				t.Fatal("Probe called an unusable file healthy")
			}
			requireAllInLog(t, "the probe fault line", logs.String(),
				append([]string{path, tc.wantClass}, tc.wantSaid...)...)
			if strings.Contains(logs.String(), good.Token) {
				t.Error("a probe fault line printed the bearer token")
			}

			// ONCE PER EPISODE, not once per poll: waitForEndpoint calls Probe ~20
			// times a second, and a line per iteration is how an always-on line gets
			// deleted. This is dedup, not gating — the first one is unconditional.
			before := strings.Count(logs.String(), path)
			for i := 0; i < 20; i++ {
				_ = Probe(path)
			}
			if after := strings.Count(logs.String(), path); after != before {
				t.Errorf("20 further polls added %d lines, want 0", after-before)
			}

			// And the report RE-ARMS once the file becomes healthy, or a fault that
			// recurs is silent for the life of the process.
			if err := Publish(path, good); err != nil {
				t.Fatal(err)
			}
			if !Probe(path) {
				t.Fatal("Probe rejected a freshly published endpoint")
			}
			if err := os.WriteFile(path, []byte(tc.line), 0o600); err != nil {
				t.Fatal(err)
			}
			if Probe(path) {
				t.Fatal("Probe called an unusable file healthy")
			}
			if again := strings.Count(logs.String(), path); again <= before {
				t.Error("the fault recurred after a healthy probe and was not reported again")
			}
		})
	}
}

// TestProbeIsSilentWhileNothingIsPublishedYet is the control: ErrEndpointMissing is
// the EXPECTED state of a healthy launch — every wait loop starts there — so
// reporting it would make every launch look broken.
func TestProbeIsSilentWhileNothingIsPublishedYet(t *testing.T) {
	logs := captureLogger(t)
	path := filepath.Join(privateDir(t), "not-yet.endpoint")
	for i := 0; i < 5; i++ {
		if Probe(path) {
			t.Fatal("Probe found an endpoint that was never published")
		}
	}
	if got := logs.String(); got != "" {
		t.Errorf("polling an unpublished endpoint logged:\n%s", got)
	}
}

// TestDialFailureNamesTheEndpointFileThatAdvertisedTheAddress: net.OpError carries
// the address, and the address is the half a reader already suspects. What it cannot
// carry is WHICH published file chose it — the question when several services are
// unreachable and only one is misadvertised.
func TestDialFailureNamesTheEndpointFileThatAdvertisedTheAddress(t *testing.T) {
	dir := privateDir(t)
	live := filepath.Join(dir, "live.endpoint")
	ln, err := Listen(live, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ep, err := Read(live)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// A second, STALE publication of the same listener, kept after it is retired.
	stale := filepath.Join(dir, "stale.endpoint")
	if err := Publish(stale, ep); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	conn, err := Dial(stale, 2*time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial reached a listener that was closed")
	}
	requireAllInLog(t, "the dial error", err.Error(), stale, ep.HostPort)
}
