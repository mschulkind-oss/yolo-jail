package svcendpoint

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

// Dial connects to the listener published at endpointPath.
//
// THE ENDPOINT FILE IS RE-READ FRESH ON EVERY DIAL. Nothing here is cached — not
// the address, not the cert, not the token. Three consequences, all deliberate:
//
//   - a listener that restarted on a new port with a new cert is picked up
//     transparently, with no jail relaunch;
//   - token ROTATION is free: the listener rewrites the file, the next dial uses
//     the new value, nothing restarts;
//   - it is the ONLY channel that can update an already-running container, whose
//     environment is frozen at `podman run` time while the host side may re-ensure
//     its daemons on a later attach.
//
// dialTimeout bounds the TCP+TLS dial. NO DEADLINE IS SET ON THE RETURNED CONN: a
// whole-session deadline would pre-empt a daemon's own per-request timeout and
// destroy the canonical timeout-exit path its clients depend on. A caller that
// wants a session deadline sets its own.
func Dial(endpointPath string, dialTimeout time.Duration) (net.Conn, error) {
	ep, err := Read(endpointPath)
	if err != nil {
		return nil, err
	}
	return dial(ep, ep.HostPort, endpointPath, dialTimeout, time.Time{})
}

// DialUntil connects and authenticates before an absolute deadline. Unlike Dial's
// dialTimeout, deadline also bounds the token acknowledgement handshake.
func DialUntil(endpointPath string, deadline time.Time) (net.Conn, error) {
	ep, err := Read(endpointPath)
	if err != nil {
		return nil, err
	}
	return dial(ep, ep.HostPort, endpointPath, 0, deadline)
}

// DialLocal is Dial for a client on the SAME MACHINE as the listener.
//
// The published address names the ADVERTISED host — the container runtime's gateway
// name, which a jail resolves and the host itself generally does not. A host-side
// caller (`yolo check`'s prober) therefore keeps the published PORT and substitutes
// 127.0.0.1, where the listener is actually bound.
//
// Nothing is weakened: the same pinned certificate, the same ServerName
// verification, the same token frame. Loopback is strictly NARROWER than the
// advertised path, not wider.
//
// It is deliberately a separate function rather than a fallback inside Dial. A
// fallback would let an in-jail client that cannot resolve the gateway quietly
// "succeed" against its own loopback — which is the misconfiguration the advertised
// host exists to make loud.
//
// This is what makes a host-side health probe able to AUTHENTICATE at all: it reads
// the same 0600 file as the same uid that published it, a property that exists only
// because the token lives in that file rather than in the jail's environment.
func DialLocal(endpointPath string, dialTimeout time.Duration) (net.Conn, error) {
	ep, err := Read(endpointPath)
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(ep.HostPort)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: address %q does not split", ErrEndpointMalformed, endpointPath, ep.HostPort)
	}
	return dial(ep, net.JoinHostPort("127.0.0.1", port), endpointPath, dialTimeout, time.Time{})
}

func handshakeDeadline(deadline time.Time) time.Time {
	local := time.Now().Add(handshakeTimeout)
	if !deadline.IsZero() && deadline.Before(local) {
		return deadline
	}
	return local
}

// dial is the shared body: pin, TLS-dial addr, present the token, read the ack.
func dial(ep Endpoint, addr, endpointPath string, dialTimeout time.Duration, deadline time.Time) (net.Conn, error) {
	cert, err := x509.ParseCertificate(ep.CertDER)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: cert does not parse", ErrEndpointMalformed, endpointPath)
	}
	// A DEDICATED root pool holding exactly this cert. No system roots, and never
	// the broker CA — issue #33 is why: its private key was readable inside every
	// jail, and pinning must not depend on a CA even now that it no longer crosses.
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	// The dial target is an address while the cert names ServerName, so ServerName
	// overrides the name to verify — full chain AND name verification still run.
	// There is no InsecureSkipVerify here, ever.
	tlsConfig := &tls.Config{
		RootCAs:    pool,
		ServerName: ServerName,
		MinVersion: tls.VersionTLS12,
	}
	var conn net.Conn
	if deadline.IsZero() {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", addr, tlsConfig)
	} else {
		var raw net.Conn
		raw, err = (&net.Dialer{Deadline: deadline}).Dial("tcp", addr)
		if err == nil {
			tlsConn := tls.Client(raw, tlsConfig)
			if err = raw.SetDeadline(deadline); err == nil {
				err = tlsConn.Handshake()
			}
			if err != nil {
				_ = raw.Close()
			} else {
				_ = raw.SetDeadline(time.Time{})
				conn = tlsConn
			}
		}
	}
	if err != nil {
		// THE ADDRESS AND THE FILE THAT NAMED IT. net.OpError carries the address
		// already; what it cannot carry is WHICH ENDPOINT FILE advertised it, and
		// that is the whole question when a dial fails — "no such host
		// host.containers.internal" means the advertised half is wrong for this
		// runtime, and the reader needs the file to know which listener chose it.
		// %w keeps errors.Is/As working for every errno gate downstream.
		return nil, fmt.Errorf("svcendpoint: dial %s (advertised by %s): %w", addr, endpointPath, err)
	}
	// Discarded: an unsettable write deadline makes the write below fail, which is
	// returned. The three Close errors on the failure paths are discarded for one
	// reason — the error being returned IS the diagnosis, and a failed close on a
	// connection the caller will never see adds nothing it can act on.
	_ = conn.SetWriteDeadline(handshakeDeadline(deadline))
	if err := writeTokenFrame(conn, ep.Token); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("svcendpoint: presenting the token to %s (from %s): %w", addr, endpointPath, err)
	}
	if err := readAckUntil(conn, deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w (dialed %s, from %s)", err, addr, endpointPath)
	}
	return conn, nil
}
