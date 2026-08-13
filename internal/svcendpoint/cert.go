package svcendpoint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// ServerName is the certificate's CommonName and sole SAN, and the ServerName a
// client verifies against. It is a fixed label rather than a hostname because the
// dial target is the ADVERTISED gateway name (host.containers.internal:<port>)
// while the cert is pinned by identity, not by address — so the client overrides
// ServerName and full chain + name verification still runs.
const ServerName = "yolo-host-service"

// certLifetime is deliberately long. The cert is ephemeral in the only sense that
// matters (a fresh one per listener process, never persisted), so expiry buys
// nothing and clock skew across a podman-machine VM costs real debugging time.
const certLifetime = 10 * 365 * 24 * time.Hour

// certSkewSlack backdates NotBefore. Skew between the host and a podman-machine
// VM is real, and a not-yet-valid cert fails the handshake with an error that
// looks nothing like a clock problem.
const certSkewSlack = time.Hour

// mintCert generates an ephemeral P-256 self-signed certificate and returns it
// alongside its DER bytes (what the client pins).
//
// THE PRIVATE KEY NEVER LEAVES THIS PROCESS'S MEMORY. It is not marshaled, not
// PEM-encoded, not written to a file, and not mounted into a jail — which is the
// whole reason a malicious jail cannot impersonate the listener. A fresh cert per
// process is CORRECT, not a compromise: the client re-reads the published cert on
// every dial, so a restarted listener is picked up transparently.
//
// Do NOT reuse internal/oauthbroker/cert.go here. It shells out to openssl and
// writes ca.key/server.key to disk, which is structurally incompatible with the
// above — and the broker CA must not be the trust anchor in any case (issue #33:
// its private key rode a whole-directory mount into every jail).
//
// IsCA: true on a self-signed leaf looks wrong and is kept deliberately, as an
// INTEROP guard rather than a Go requirement.
//
// MEASURED on go1.26.5, contradicting the claim this was carried over with: a
// self-signed leaf that is ITSELF the pinned root verifies fine with IsCA=false,
// BasicConstraintsValid=false and no KeyUsageCertSign — the chain is one
// certificate long, so there is no signing step for Go to authorize. So dropping
// IsCA would NOT break the Go client, and TestMintedCertIsItsOwnTrustAnchor is
// what keeps it from being dropped anyway: docs/design/loophole-protocol.md invites
// third-party clients, and OpenSSL-family verifiers (and older Go) do require a
// trust anchor to carry CA:TRUE unless partial-chain verification is enabled. A
// certificate that only works with Go's verifier would be a silent interop trap.
func mintCert() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: ServerName},
		DNSNames:              []string{ServerName},
		NotBefore:             now.Add(-certSkewSlack),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // self-signed leaf doubles as its own trust anchor
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der, nil
}
