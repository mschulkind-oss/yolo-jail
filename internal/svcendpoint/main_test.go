package svcendpoint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Package-level test CA, installed as this test binary's SYSTEM root pool.
var (
	pkgTestCA    *x509.Certificate
	pkgTestCAKey *ecdsa.PrivateKey
	// pkgSystemPoolTrustsTestCA is true when the platform actually honored
	// SSL_CERT_FILE, i.e. when TestSystemRootsNotTrusted's premise exists.
	pkgSystemPoolTrustsTestCA bool
)

// TestMain points the process's SYSTEM root pool at a CA we control and WARMS THE
// CACHE BEFORE ANY TEST RUNS.
//
// This ordering is the whole reason TestMain exists. x509.SystemCertPool is cached
// behind a sync.Once, so the first caller in the binary wins for its lifetime.
// Nothing in the shipped package calls it — that is the property under test — but
// the regression worth catching is precisely someone MERGING the system roots into
// the pinned pool to make a cert error go away. Under that regression the first
// Dial would warm the cache with the real roots, TestSystemRootsNotTrusted's
// premise would become unbuildable, and it would degrade to a skip: the guard
// disabled by the very change it guards against. Warming here makes that
// impossible.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "svcendpoint-roots-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "svcendpoint tests: mkdtemp:", err)
		os.Exit(1)
	}
	ca, caKey, err := newTestCA()
	if err != nil {
		fmt.Fprintln(os.Stderr, "svcendpoint tests: mint test CA:", err)
		os.Exit(1)
	}
	pkgTestCA, pkgTestCAKey = ca, caKey

	caPEM := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "svcendpoint tests: write CA pem:", err)
		os.Exit(1)
	}
	// Read by crypto/x509's Unix root loader. Darwin uses the keychain and ignores
	// it, which the flag below records rather than hides.
	_ = os.Setenv("SSL_CERT_FILE", caPEM)

	if pool, perr := x509.SystemCertPool(); perr == nil {
		if leaf, lerr := newLeafSignedBy(ca, caKey); lerr == nil {
			parsed, cerr := x509.ParseCertificate(leaf.Certificate[0])
			if cerr == nil {
				if _, verr := parsed.Verify(x509.VerifyOptions{Roots: pool, DNSName: ServerName}); verr == nil {
					pkgSystemPoolTrustsTestCA = true
				}
			}
		}
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// newTestCA mints a short-lived CA for the fixtures. Separate from mintTestCA's
// *testing.T wrapper because TestMain has no T.
func newTestCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "svcendpoint test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	crt, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return crt, key, nil
}
