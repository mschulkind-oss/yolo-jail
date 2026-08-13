package svcendpoint

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This file closes gaps found by mutation-testing the transport against PR #32's
// suite as the acceptance bar. Every property here was CLAIMED in a comment and
// asserted nowhere: the existing tests passed with the behaviour inverted.

// TestOversizedFrameAllocatesNothingBeforeTheCap makes "the cap is checked BEFORE the
// buffer is allocated" a MEASURED fact.
//
// TestOversizedTokenFrameRejected states that property and cannot see it: moving
// `got := make([]byte, n)` above the bounds check leaves every assertion in that test
// passing (the drop is still prompt, the log line is unchanged, the handler is still
// never reached) while each unauthenticated connection allocates n bytes. Measured
// under that mutation: 1024.0 MiB per connection, from a peer that has presented no
// credential — a one-packet remote memory-exhaustion primitive, which is exactly what
// the cap exists to prevent and is one of the properties carried over from #32.
//
// So the assertion has to be on ALLOCATION, not on the reject. The margin is ~64x, so
// the threshold is not sensitive to unrelated allocation by idle listeners from
// earlier tests in this package.
func TestOversizedFrameAllocatesNothingBeforeTheCap(t *testing.T) {
	const frameLen = 1 << 30     // 1 GiB claimed by a 4-byte length prefix
	const allocBudget = 16 << 20 // 64x headroom over the ~15 KiB a clean run uses

	s := startServer(t)
	conn := dialPinnedRaw(t, s.path)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	writeRawTokenFrame(t, conn, frameLen, nil)
	expectNoAck(t, conn)

	runtime.ReadMemStats(&after)
	if delta := after.TotalAlloc - before.TotalAlloc; delta > allocBudget {
		t.Errorf("an unauthenticated %d-byte length prefix allocated %d bytes (%.1f MiB); "+
			"the cap must be checked BEFORE the buffer is allocated",
			frameLen, delta, float64(delta)/(1<<20))
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times, want 0", n)
	}
}

// TestPublishDoesNotFollowAPlantedSymlink is #32's TestRelayTCPTokenRejectsPlantedFile,
// carried over — it had no counterpart here.
//
// #32 kept its token in a host-side file and tested that a symlink planted at that
// path was neither trusted nor FOLLOWED BY THE WRITE. Under the token-in-the-endpoint-
// file decision the "not trusted" half is gone (the listener mints its token; it never
// reads one), but the write half became MORE important, not less: the endpoint file is
// now the credential, it lives at a fully deterministic path, and its directory's
// parent is a world-writable /tmp.
//
// temp-file-plus-rename gives the property for free — rename REPLACES a symlink rather
// than following it — which is precisely why nothing asserted it. Mutation-verified:
// resolving the path first (one plausible EvalSymlinks call in Publish) writes the live
// 64-hex token into an attacker-chosen file with the whole suite still green.
//
// ensurePrivateDir covers a symlinked DIRECTORY. This is the leaf.
func TestPublishDoesNotFollowAPlantedSymlink(t *testing.T) {
	dir := privateDir(t)
	// The victim lives OUTSIDE the publication directory: the point of planting a
	// symlink is to redirect the write somewhere the publisher never inspects.
	victim := filepath.Join(t.TempDir(), "victim")
	const untouched = "someone else's file\n"
	if err := os.WriteFile(victim, []byte(untouched), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "svc.endpoint")
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}

	ep := sampleEndpoint(t)
	if err := Publish(path, ep); err != nil {
		t.Fatalf("Publish over a planted symlink: %v", err)
	}

	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ep.Token) {
		t.Errorf("THE BEARER TOKEN WAS WRITTEN THROUGH THE PLANTED SYMLINK into %s", victim)
	}
	if string(data) != untouched {
		t.Errorf("the planted symlink's target was modified: %q", string(data))
	}
	// The symlink must have been REPLACED, not written through — otherwise the next
	// republish redirects again and the endpoint the client reads is the attacker's.
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s is still a symlink after Publish", path)
	}
	// And the publication really did land, so the assertions above are not vacuous.
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read after publishing over a planted symlink: %v", err)
	}
	if got.Token != ep.Token {
		t.Errorf("published token = %q, want the one Publish was given", got.Token)
	}
}

// TestDialDoesNotFallBackToLoopback pins the decision stated in DialLocal's doc
// comment — "deliberately a separate function rather than a fallback inside Dial. A
// fallback would let an in-jail client that cannot resolve the gateway quietly
// 'succeed' against its own loopback — which is the misconfiguration the advertised
// host exists to make loud."
//
// Nothing asserted it. Mutation-verified: adding exactly that fallback (retry the
// published PORT on 127.0.0.1 when the advertised host fails) leaves the whole suite
// green — in svcendpoint AND in oauthterminator. The inverse direction is covered
// (dropping DialLocal's substitution is killed by two `yolo check` tests), so this
// was the unguarded half of a two-function contract.
//
// The failure is silent and platform-shaped: on macOS + podman the advertised host
// is host.containers.internal, and a jail that resolves it to the podman-machine VM
// instead of the host would, with a fallback, connect to its OWN loopback and report
// success — the exact class of confusion this transport exists to end.
//
// The advertised host here is ::1 with the RIGHT port: a literal (no DNS in a unit
// test) that the IPv4-bound listener cannot possibly answer on, so the dial fails
// for the one reason under test. DialLocal on the SAME file is the anti-vacuity
// control: it substitutes 127.0.0.1, keeps the port, and must complete a full
// authenticated round trip — proving the address, certificate and token were all
// usable and only the advertised HOST was unreachable.
func TestDialDoesNotFallBackToLoopback(t *testing.T) {
	s := startServer(t)
	ep, err := Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(ep.HostPort)
	if err != nil {
		t.Fatal(err)
	}
	unreachable := filepath.Join(privateDir(t), "unreachable-host.endpoint")
	if err := Publish(unreachable, Endpoint{
		HostPort: net.JoinHostPort("::1", port), CertDER: ep.CertDER, Token: ep.Token,
	}); err != nil {
		t.Fatal(err)
	}

	if conn, err := Dial(unreachable, 2*time.Second); err == nil {
		_ = conn.Close()
		t.Error("Dial SUCCEEDED against an endpoint whose advertised host has no listener — " +
			"it fell back to loopback, which hides the misconfiguration the advertised " +
			"host exists to make loud")
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times through the unreachable advertised host, want 0", n)
	}

	// Control: the same file, dialled the way a host-side prober dials it.
	conn, err := DialLocal(unreachable, 5*time.Second)
	if err != nil {
		t.Fatalf("control case failed — DialLocal must complete on this file, else the "+
			"rejection above proves nothing about the HOST: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if got := roundTrip(t, conn, "hi"); got != "echo:hi" {
		t.Errorf("round trip = %q, want %q", got, "echo:hi")
	}
}

// TestUnexpectedAckByteRejected pins the VALUE of the accept ack, not just its
// presence.
//
// Mutation-verified: making readAck accept any first byte leaves the whole suite
// green. The adjacent property IS covered — losing the ErrAuthRejected sentinel is
// killed by four tests across three packages — so the layer ATTRIBUTION was pinned
// while the wire value it is read from was not.
//
// It matters because the ack is new wire (docs/design/loophole-transport.md §8.3)
// and loophole-protocol.md invites third-party daemons: one writing the wrong first
// byte would otherwise be silently accepted, and the client would then read that
// byte as the head of the first response frame — a desync reported as whatever the
// daemon behind the transport looked like at the time.
//
// 0x01 is the anti-vacuity control on the same fixture: identical cert, identical
// pin, identical token, and it MUST authenticate.
func TestUnexpectedAckByteRejected(t *testing.T) {
	caCert, caKey := mintTestCA(t)

	for _, tc := range []struct {
		name string
		ack  byte
		want bool // want the dial to succeed
	}{
		{"zero byte", 0x00, false},
		{"the next plausible value", 0x02, false},
		{"all bits", 0xff, false},
		{"the real ack", authAck, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Pin the ISSUER and serve a leaf it signed: the handshake succeeds, so the
			// dial reaches readAck and nothing upstream of it can explain a failure.
			ln := serveAckByte(t, mintLeafSignedBy(t, caCert, caKey), tc.ack)
			tok, err := NewToken()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(privateDir(t), "ack.endpoint")
			if err := Publish(path, Endpoint{
				HostPort: ln.Addr().String(), CertDER: caCert.Raw, Token: tok,
			}); err != nil {
				t.Fatal(err)
			}

			conn, err := Dial(path, 5*time.Second)
			if tc.want {
				if err != nil {
					t.Fatalf("control case failed — ack %#02x must authenticate, else the "+
						"rejections in this table prove nothing: %v", tc.ack, err)
				}
				_ = conn.Close()
				return
			}
			if err == nil {
				_ = conn.Close()
				t.Fatalf("Dial ACCEPTED a listener whose accept byte was %#02x, not %#02x",
					tc.ack, authAck)
			}
			if !errors.Is(err, ErrAuthRejected) {
				t.Errorf("err = %v, want the ErrAuthRejected sentinel so the caller can "+
					"attribute the layer", err)
			}
			if !strings.Contains(err.Error(), "accept byte") {
				t.Errorf("err = %v, want it to name the unexpected accept byte", err)
			}
		})
	}
}

// serveAckByte is serveAckAnyToken with the ack byte as a parameter: it reads and
// DISCARDS the token frame, then writes ack. Kept separate rather than folded into
// serveAckAnyToken so the pinning tests that depend on that helper acking correctly
// cannot be perturbed by a table entry here.
func serveAckByte(t *testing.T, cert tls.Certificate, ack byte) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				hdr := make([]byte, 4)
				if _, err := io.ReadFull(conn, hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				if n == 0 || n > tokenFrameMax {
					return
				}
				if _, err := io.ReadFull(conn, make([]byte, n)); err != nil {
					return
				}
				_, _ = conn.Write([]byte{ack})
			}()
		}
	}()
	return ln
}
