package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserTrustsSystemKeychainCA: A CERTIFICATE AUTHORITY THE MAC'S SYSTEM KEYCHAIN TRUSTS
// FOR TLS IS TRUSTED INSIDE THE SANDBOX, and the launch names it (internal/macosuser/cabundle.go;
// docs/design/provisioner-sets.md PS-D9). It stands in for an MDM-installed corporate CA:
//
//   - a CA made here is added to /Library/Keychains/System.keychain as a trusted root, and a
//     second one is added WITHOUT trust settings, the control the launch must leave out;
//   - this process serves TLS on 127.0.0.1 with a leaf the first CA signed;
//   - a macos-user launch runs the floor's nix curl against it, which trusts only a file a
//     variable names, CURL_CA_BUNDLE ahead of the others, and prints four of the variables;
//   - the launch's output and launch.log must name the first CA and not the second.
//
// Both certificates and the trust setting are removed at cleanup. Adding a trusted root to the
// System keychain non-interactively can need the admin trust-settings right opened first; that
// is a change to the machine's authorization policy, so it is made only on a run that declared
// itself the macos-user job (macosUserDeclareEnv) and restored at cleanup, and anywhere else the
// test skips, saying why.
func TestMacosUserTrustsSystemKeychainCA(t *testing.T) {
	requireMacosUser(t)
	dir := resolvedTempDir(t)
	tag := hex.EncodeToString(randomBytes(t, 4))
	caName, controlName := "yolo-it trusted CA "+tag, "yolo-it untrusted CA "+tag
	ca, caKey := catrustCA(t, caName)
	control, _ := catrustCA(t, controlName)
	caFile, controlFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "control.pem")
	writePEM(t, caFile, ca)
	writePEM(t, controlFile, control)

	catrustInstall(t, dir, caFile, ca, controlFile, control)

	addr := catrustServe(t, ca, caKey)
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, fmt.Sprintf(`echo "CURL=$(command -v curl)"; curl -fsS https://%s/; echo; `+
		`echo "SSL=${SSL_CERT_FILE-}"; echo "NIX=${NIX_SSL_CERT_FILE-}"; echo "CURLCA=${CURL_CA_BUNDLE-}"; `+
		`echo "NODE=${NODE_EXTRA_CA_CERTS-}"`, addr))
	vars := hd10Fields(r.stdout)
	// The floor's nix curl, which reads only a bundle a variable names (CURL_CA_BUNDLE first):
	// Apple's /usr/bin/curl asks the system, so a pass through it would show nothing about the
	// bundle.
	if !strings.HasPrefix(vars["CURL"], "/nix/store/") {
		t.Fatalf("the sandbox's curl is %q, not the floor's nix curl, so this run cannot show that "+
			"the bundle is trusted:\n%s", vars["CURL"], lastLines(r.combined(), 40))
	}
	if r.rc != 0 || !strings.Contains(r.stdout, catrustBody) {
		t.Fatalf("nix curl in the sandbox did not trust the System keychain's CA (rc %d):\n%s",
			r.rc, lastLines(r.combined(), 40))
	}
	for _, k := range []string{"SSL", "NIX", "CURLCA"} {
		if !strings.HasPrefix(vars[k], macosuser.StateDir()+"/env/") || !strings.HasSuffix(vars[k], ".ca-bundle.crt") {
			t.Errorf("%s names %q, not the session's CA bundle", k, vars[k])
		}
	}
	if !strings.HasSuffix(vars["NODE"], ".extra-ca.pem") {
		t.Errorf("NODE_EXTRA_CA_CERTS names %q, not the session's extra-CA file", vars["NODE"])
	}
	logBody, _ := os.ReadFile(filepath.Join(ws, ".yolo", "launch.log"))
	for name, text := range map[string]string{"the launch's output": r.combined(), "launch.log": string(logBody)} {
		if !strings.Contains(text, "from this Mac's System keychain") || !strings.Contains(text, caName) {
			t.Errorf("%s does not name the CA it trusted (%s):\n%s", name, caName, lastLines(text, 40))
		}
		if strings.Contains(text, controlName) {
			t.Errorf("%s names the CA with no trust settings (%s), which must be left out", name, controlName)
		}
	}
}

// catrustBody is what the test's TLS server answers.
const catrustBody = "hello from the System keychain's CA"

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// catrustCA makes a self-signed CA certificate and its key.
func catrustCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(randomBytes(t, 8)),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func writePEM(t *testing.T, path string, c *x509.Certificate) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
}

// catrustInstall adds the CA as a trusted root and the control with no trust settings to the
// System keychain, and registers their removal. A trusted root needs the admin trust-settings
// right, which is opened only on a declared run and put back at cleanup.
func catrustInstall(t *testing.T, dir, caFile string, ca *x509.Certificate, controlFile string, control *x509.Certificate) {
	t.Helper()
	const keychain = "/Library/Keychains/System.keychain"
	const right = "com.apple.trust-settings.admin"
	sudo := func(args ...string) ([]byte, error) {
		return exec.Command("sudo", append([]string{"-n", "/usr/bin/security"}, args...)...).CombinedOutput()
	}
	// Cleanups run last-registered first. The certificates go LAST, so this one is registered
	// first; the trust setting goes before them and before the right is restored (untrust).
	t.Cleanup(func() {
		for _, c := range []*x509.Certificate{ca, control} {
			sum := sha1.Sum(c.Raw)
			if out, err := sudo("delete-certificate", "-Z", strings.ToUpper(hex.EncodeToString(sum[:])), keychain); err != nil {
				t.Logf("removing %s from the System keychain: %v\n%s", c.Subject.CommonName, err, out)
			}
		}
	})
	// untrust removes the CA's admin trust setting at cleanup, registered once the setting exists
	// and after any opening of the right, so it runs while the right that made it is still open.
	untrust := func() {
		t.Cleanup(func() {
			if out, err := sudo("remove-trusted-cert", "-d", caFile); err != nil {
				t.Logf("removing the trust setting for %s: %v\n%s", ca.Subject.CommonName, err, out)
			}
		})
	}
	if out, err := sudo("add-certificates", "-k", keychain, controlFile); err != nil {
		t.Fatalf("adding the control CA to the System keychain: %v\n%s", err, out)
	}
	out, err := sudo("add-trusted-cert", "-d", "-r", "trustRoot", "-k", keychain, caFile)
	if err == nil {
		untrust()
		return
	}
	if os.Getenv(macosUserDeclareEnv) == "" {
		t.Skipf("could not add a trusted root to the System keychain without opening the admin "+
			"trust-settings right (%v: %s); this test opens it only on a run that sets %s=1, a "+
			"disposable machine", err, strings.TrimSpace(string(out)), macosUserDeclareEnv)
	}
	saved := filepath.Join(dir, "trust-settings-admin.plist")
	// stdout alone: `authorizationdb read` reports its status on stderr, and the plist it prints
	// on stdout is what a write reads back.
	rule, rerr := exec.Command("sudo", "-n", "/usr/bin/security", "authorizationdb", "read", right).Output()
	if rerr != nil {
		t.Fatalf("reading %s to restore it later: %v", right, rerr)
	}
	if err := os.WriteFile(saved, rule, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command("sudo", "-n", "/usr/bin/security", "authorizationdb", "write", right)
		f, err := os.Open(saved)
		if err != nil {
			t.Errorf("restoring %s: %v", right, err)
			return
		}
		defer func() { _ = f.Close() }()
		cmd.Stdin = f
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("restoring %s from %s failed: %v\n%s", right, saved, err, out)
		}
	})
	if out, err := sudo("authorizationdb", "write", right, "allow"); err != nil {
		t.Fatalf("opening %s: %v\n%s", right, err, out)
	}
	if out, err := sudo("add-trusted-cert", "-d", "-r", "trustRoot", "-k", keychain, caFile); err != nil {
		t.Fatalf("adding the CA as a trusted root, with %s open: %v\n%s", right, err, out)
	}
	untrust()
}

// catrustServe serves catrustBody over TLS on 127.0.0.1 with a leaf the CA signed, until the
// test ends, and returns its address.
func catrustServe(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(randomBytes(t, 8)),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(catrustBody))
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}
