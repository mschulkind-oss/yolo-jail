package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hostCAFixture is a certificate authority no public root list holds and a localhost server
// certificate it signed, written into dir as ca.pem, leaf.pem and leaf.key.
func hostCAFixture(t *testing.T, dir, cn string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{
		"ca.pem":   {Type: "CERTIFICATE", Bytes: caDER},
		"leaf.pem": {Type: "CERTIFICATE", Bytes: leafDER},
		"leaf.key": {Type: "EC PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// hostCAProbe prints the six TLS variables, verifies the leaf against the bundle and the extras,
// and fetches from a loopback TLS server serving the leaf with curl, which reads the variables.
const hostCAProbe = `echo "=== VARS ==="; ` +
	`echo "NIX=${NIX_SSL_CERT_FILE-}"; echo "SSL=${SSL_CERT_FILE-}"; echo "REQ=${REQUESTS_CA_BUNDLE-}"; ` +
	`echo "CURLCA=${CURL_CA_BUNDLE-}"; echo "GIT=${GIT_SSL_CAINFO-}"; echo "NODE=${NODE_EXTRA_CA_CERTS-}"; ` +
	`echo "=== END ==="; ` +
	`openssl verify -CAfile "$CURL_CA_BUNDLE" /workspace/fixture/leaf.pem && echo VERIFY_BUNDLE_OK; ` +
	`openssl verify -CAfile "$NODE_EXTRA_CA_CERTS" /workspace/fixture/leaf.pem && echo VERIFY_EXTRAS_OK; ` +
	`openssl s_server -quiet -www -accept 18443 -cert /workspace/fixture/leaf.pem -key /workspace/fixture/leaf.key >/dev/null 2>&1 & ` +
	`srv=$!; for _ in $(seq 1 50); do curl -sS -o /dev/null https://localhost:18443/ 2>/dev/null && { echo CURL_OK; break; }; sleep 0.2; done; ` +
	`curl -sS -o /dev/null https://localhost:18443/ || true; kill $srv 2>/dev/null; true`

// TestAContainerJailTrustsTheHostsCAs: A CERTIFICATE AUTHORITY THE HOST TRUSTS IS TRUSTED IN A
// CONTAINER JAIL, and the launch names it with its source (OQ-PS14, OQ-PS15; PS-D16 in
// docs/design/provisioner-sets.md).
//
// The host's bundle is the one its own OpenSSL reads, and SSL_CERT_FILE names it when set: the
// launcher honors that, so the test hands the launch a host bundle holding a CA it minted, with no
// change to this machine's trust. A leaf that CA signed then verifies against the jail's bundle
// and its Node extras, and curl in the jail completes a handshake with a loopback server serving
// it.
func TestAContainerJailTrustsTheHostsCAs(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	fixture := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	const cn = "Example Corp Integration CA"
	hostCAFixture(t, fixture, cn)
	hostBundle := filepath.Join(fixture, "ca.pem")

	r := runYolo(t, dir, hostCAProbe, withEnv("SSL_CERT_FILE="+hostBundle))
	if r.rc != 0 {
		t.Fatalf("the launch failed (rc %d):\n%s", r.rc, lastLines(r.combined(), 40))
	}
	vars := hd10Fields(section(r.stdout, "=== VARS ===", "=== END ==="))
	for _, k := range []string{"NIX", "SSL", "REQ", "CURLCA", "GIT"} {
		if vars[k] != "/home/agent/.yolo-ca-bundle.crt" {
			t.Errorf("%s names %q, not the jail's bundle", k, vars[k])
		}
	}
	if vars["NODE"] != "/home/agent/.config/yolo-extra-ca.pem" {
		t.Errorf("NODE_EXTRA_CA_CERTS names %q, not the one extras file", vars["NODE"])
	}
	for _, want := range []string{"VERIFY_BUNDLE_OK", "VERIFY_EXTRAS_OK", "CURL_OK"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the jail does not trust the host's CA: no %s in\n%s", want, lastLines(r.combined(), 40))
		}
	}
	disclosure := "Trusting 1 certificate authority from the CA bundle at " + hostBundle + ": " + cn + "."
	if !strings.Contains(r.combined(), disclosure) {
		t.Errorf("the launch does not name the host's CA with its source (want %q):\n%s", disclosure, lastLines(r.combined(), 40))
	}
}

// TestAUsersSSLCertFileWinsInAContainerJail: A BUNDLE THE USER'S ENV LAYERS NAME WINS FOR ALL
// FIVE TLS VARIABLES, where the image's own SSL_CERT_FILE used to beat it (PS-D10's container
// trap, fixed by PS-D16). Node still adds the host's CA through its extras file.
func TestAUsersSSLCertFileWinsInAContainerJail(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	fixture := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	hostCAFixture(t, fixture, "Example Corp Integration CA")
	packHome(t, `{"env_sources": [{"SSL_CERT_FILE": "/workspace/fixture/ca.pem"}]}`)

	r := runYolo(t, dir, hostCAProbe, withEnv("SSL_CERT_FILE="+filepath.Join(fixture, "ca.pem")))
	if r.rc != 0 {
		t.Fatalf("the launch failed (rc %d):\n%s", r.rc, lastLines(r.combined(), 40))
	}
	vars := hd10Fields(section(r.stdout, "=== VARS ===", "=== END ==="))
	for _, k := range []string{"NIX", "SSL", "REQ", "CURLCA", "GIT"} {
		if vars[k] != "/workspace/fixture/ca.pem" {
			t.Errorf("%s names %q, not the user's /workspace/fixture/ca.pem", k, vars[k])
		}
	}
	if vars["NODE"] != "/home/agent/.config/yolo-extra-ca.pem" {
		t.Errorf("NODE_EXTRA_CA_CERTS names %q, not the one extras file", vars["NODE"])
	}
	if !strings.Contains(r.combined(), "Your configured environment sets SSL_CERT_FILE") {
		t.Errorf("the launch does not say the user's bundle wins:\n%s", lastLines(r.combined(), 40))
	}
	if !strings.Contains(r.stdout, "CURL_OK") {
		t.Errorf("curl does not trust the user's bundle:\n%s", lastLines(r.combined(), 40))
	}
}
