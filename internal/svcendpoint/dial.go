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
	cert, err := x509.ParseCertificate(ep.CertDER)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: cert does not parse", ErrEndpointMalformed, endpointPath)
	}
	// A DEDICATED root pool holding exactly this cert. No system roots, and never
	// the broker CA — issue #33 is why: its private key was readable inside every
	// jail, and pinning must not depend on a CA even now that it no longer crosses.
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	// The dial target is the advertised gateway name while the cert names
	// ServerName, so ServerName overrides the name to verify — full chain AND name
	// verification still run. There is no InsecureSkipVerify here, ever.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", ep.HostPort, &tls.Config{
		RootCAs:    pool,
		ServerName: ServerName,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	if err := writeTokenFrame(conn, ep.Token); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := readAck(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
