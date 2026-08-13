package svcendpoint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"testing"
	"time"
)

// TestMintedCertIsItsOwnTrustAnchor pins the shape of the minted certificate.
//
// It exists because the Go client CANNOT catch a regression here. MEASURED on
// go1.26.5: a self-signed leaf that is itself the pinned root verifies with
// IsCA=false, BasicConstraintsValid=false and no KeyUsageCertSign — the chain is
// one certificate long, so nothing needs authorizing to sign. Dropping IsCA would
// therefore leave every test in this package green while producing a certificate
// that OpenSSL-family verifiers (and older Go) reject as a trust anchor, because
// they require CA:TRUE unless partial-chain verification is enabled.
// docs/design/loophole-protocol.md invites third-party clients, so that would be a
// silent interop trap. Hence a property assertion rather than a behavioural one.
func TestMintedCertIsItsOwnTrustAnchor(t *testing.T) {
	_, der, err := mintCert()
	if err != nil {
		t.Fatal(err)
	}
	crt, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	if !crt.IsCA {
		t.Error("IsCA = false: a verifier that requires CA:TRUE on a trust anchor would reject this")
	}
	if !crt.BasicConstraintsValid {
		t.Error("BasicConstraintsValid = false: the CA bit is then not asserted at all")
	}
	if crt.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("KeyUsage lacks CertSign, which a CA:TRUE anchor is expected to carry")
	}
	if crt.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("KeyUsage lacks DigitalSignature, needed to serve TLS")
	}

	if crt.Subject.CommonName != ServerName {
		t.Errorf("CommonName = %q, want %q", crt.Subject.CommonName, ServerName)
	}
	if len(crt.DNSNames) != 1 || crt.DNSNames[0] != ServerName {
		t.Errorf("DNSNames = %v, want exactly [%s] — the client verifies this name while "+
			"dialing the advertised gateway address", crt.DNSNames, ServerName)
	}
	var serverAuth bool
	for _, u := range crt.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			serverAuth = true
		}
	}
	if !serverAuth {
		t.Error("ExtKeyUsage lacks ServerAuth")
	}

	// Backdated: skew between the host and a podman-machine VM is real, and a
	// not-yet-valid cert fails the handshake with an error that looks nothing like
	// a clock problem.
	if !crt.NotBefore.Before(time.Now()) {
		t.Errorf("NotBefore = %v is not in the past", crt.NotBefore)
	}
	if crt.NotAfter.Before(time.Now().Add(24 * time.Hour)) {
		t.Errorf("NotAfter = %v expires within a day", crt.NotAfter)
	}

	if _, ok := crt.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Errorf("public key is %T, want *ecdsa.PublicKey", crt.PublicKey)
	} else if pk := crt.PublicKey.(*ecdsa.PublicKey); pk.Curve != elliptic.P256() {
		t.Errorf("curve = %v, want P-256", pk.Curve.Params().Name)
	}

	// Self-signed: issuer == subject, and it verifies against itself.
	if string(crt.RawIssuer) != string(crt.RawSubject) {
		t.Error("certificate is not self-signed")
	}
	if err := crt.CheckSignatureFrom(crt); err != nil {
		t.Errorf("CheckSignatureFrom(self): %v", err)
	}

	// Two calls must not produce the same certificate: the key is per-process and
	// never persisted, so there is nothing to reuse.
	_, der2, err := mintCert()
	if err != nil {
		t.Fatal(err)
	}
	if string(der) == string(der2) {
		t.Error("two mints produced an identical certificate — a key is being cached")
	}
}
