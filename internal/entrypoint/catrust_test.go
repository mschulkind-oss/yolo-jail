package entrypoint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/catrust"
)

// caTestPEM mints one self-signed CA named cn.
func caTestPEM(t *testing.T, cn string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func writeCAFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// caTrustFixture is a jail with the image's public roots (one public root), a host file the
// launcher composed holding that same root and one corporate CA, and one loophole CA named twice
// in the colon list the loophole launcher emits.
type caTrustFixture struct {
	publicRoot, corpCA, loopholeCA string
	roots, host, loophole          string
}

func newCATrustFixture(t *testing.T, e *Env) caTrustFixture {
	t.Helper()
	// setEnvBoth writes the process environment too; restore it after the test.
	for _, k := range append(append([]string{}, CABundleVars...), nodeExtraCAVar) {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	f := caTrustFixture{
		publicRoot: caTestPEM(t, "Public Root"),
		corpCA:     caTestPEM(t, "Example Corp Inspection CA"),
		loopholeCA: caTestPEM(t, "Loophole CA"),
	}
	f.roots = writeCAFixture(t, dir, "roots.pem", f.publicRoot)
	src := catrust.HostBundleSource("/etc/ssl/certs/ca-certificates.crt")
	f.host = writeCAFixture(t, dir, "host.pem", catrust.Render([]catrust.CA{
		{Name: "Public Root", Source: src, PEM: f.publicRoot},
		{Name: "Example Corp Inspection CA", Source: src, PEM: f.corpCA},
	}))
	f.loophole = writeCAFixture(t, dir, "loophole.pem", f.loopholeCA)
	e.Vars["SSL_CERT_FILE"] = f.roots
	e.Vars[HostCAFileEnv] = f.host
	e.Vars[nodeExtraCAVar] = f.loophole + ":" + f.loophole
	return f
}

// runCABootStep runs the boot's generate_ca_bundle step itself, so deleting its call to
// applyCATrust fails every test that uses it.
func runCABootStep(t *testing.T, e *Env) {
	t.Helper()
	for _, s := range bootSteps() {
		if s.name == "generate_ca_bundle" {
			s.run(&bootRun{e: e, target: bootContainer})
			return
		}
	}
	t.Fatal("no generate_ca_bundle boot step")
}

func readCAFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The boot joins the image's roots, the host's CAs the image does not already carry and the
// loophole CAs into one bundle, points all five variables at it, gives Node one extras file, and
// names the host CA it added with its source.
func TestTheBootJoinsTheHostsCAsAndNamesThem(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	f := newCATrustFixture(t, e)
	runCABootStep(t, e)

	bundlePath := filepath.Join(e.Home, caBundleRel)
	bundle := readCAFile(t, bundlePath)
	for _, c := range []string{f.publicRoot, f.corpCA, f.loopholeCA} {
		if strings.Count(bundle, c) != 1 {
			t.Errorf("the bundle holds a CA %d times, want once:\n%s", strings.Count(bundle, c), c)
		}
	}
	extrasPath := filepath.Join(e.Home, ExtraCAFileRel)
	extras := readCAFile(t, extrasPath)
	if strings.Contains(extras, f.publicRoot) || !strings.Contains(extras, f.corpCA) || strings.Count(extras, f.loopholeCA) != 1 {
		t.Errorf("the extras file must hold the host's added CA and the loophole CA once, not the public roots:\n%s", extras)
	}
	for _, k := range CABundleVars {
		if e.Vars[k] != bundlePath || os.Getenv(k) != bundlePath {
			t.Errorf("%s = %q (process %q), want %s", k, e.Vars[k], os.Getenv(k), bundlePath)
		}
	}
	if e.Vars[nodeExtraCAVar] != extrasPath {
		t.Errorf("%s = %q, want the one extras file %s, never a colon list", nodeExtraCAVar, e.Vars[nodeExtraCAVar], extrasPath)
	}
	sh := readCAFile(t, filepath.Join(e.Home, CAEnvFileRel))
	for _, k := range CABundleVars {
		if !strings.Contains(sh, "export "+k+"='"+bundlePath+"'\n") {
			t.Errorf("the shell's copy does not export %s:\n%s", k, sh)
		}
	}
	if !strings.Contains(sh, "export "+nodeExtraCAVar+"='"+extrasPath+"'\n") {
		t.Errorf("the shell's copy does not export %s:\n%s", nodeExtraCAVar, sh)
	}
	mustContain(t, "the boot's disclosure", stderr,
		"Trusting 1 certificate authority from this host's CA bundle (/etc/ssl/certs/ca-certificates.crt): Example Corp Inspection CA.",
		"so Node adds it.")
	if strings.Contains(stderr.String(), "Public Root") {
		t.Errorf("the disclosure names a CA the image's roots already carry:\n%s", stderr)
	}
}

// A bundle the user's env layers name wins for all five: each unset variable follows it, the
// jail does not add the host's CAs to it, and Node still gets the extras.
func TestAUsersSSLCertFileWinsForAllFive(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	newCATrustFixture(t, e)
	userEnv := userEnvFilePath(e)
	if err := os.MkdirAll(filepath.Dir(userEnv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userEnv, []byte("export SSL_CERT_FILE=${SSL_CERT_FILE:-'/workspace/corp.pem'}\n"+
		"export GIT_SSL_CAINFO='/workspace/git.pem'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCABootStep(t, e)

	want := map[string]string{
		"NIX_SSL_CERT_FILE":  "/workspace/corp.pem",
		"SSL_CERT_FILE":      "/workspace/corp.pem",
		"REQUESTS_CA_BUNDLE": "/workspace/corp.pem",
		"CURL_CA_BUNDLE":     "/workspace/corp.pem",
		"GIT_SSL_CAINFO":     "/workspace/git.pem",
		nodeExtraCAVar:       filepath.Join(e.Home, ExtraCAFileRel),
	}
	sh := readCAFile(t, filepath.Join(e.Home, CAEnvFileRel))
	for k, v := range want {
		if e.Vars[k] != v {
			t.Errorf("%s = %q, want %q", k, e.Vars[k], v)
		}
		if !strings.Contains(sh, "export "+k+"='"+v+"'\n") {
			t.Errorf("the shell's copy does not export %s=%s:\n%s", k, v, sh)
		}
	}
	mustContain(t, "the boot's disclosure", stderr,
		"Your configured environment sets SSL_CERT_FILE, so every TLS variable you left unset names /workspace/corp.pem too")
}

// The shell sources the boot's decided variables rather than setting its own, so an interactive
// shell cannot override the user's bundle the way the old bashrc block did.
func TestTheBashrcSourcesTheBootsCAVariables(t *testing.T) {
	e := testEnv(t)
	if err := GenerateBashrc(e); err != nil {
		t.Fatal(err)
	}
	rc := readCAFile(t, filepath.Join(e.Home, ".bashrc"))
	if !strings.Contains(rc, `. "$HOME/`+CAEnvFileRel+`"`) {
		t.Errorf("the bashrc does not source %s", CAEnvFileRel)
	}
	for _, k := range CABundleVars {
		if strings.Contains(rc, "export "+k+"=") {
			t.Errorf("the bashrc sets %s itself, which beats the user's own value", k)
		}
	}
}

// With no host file, the bundle is the image's roots and the loophole CAs, and the boot log
// records that the host added nothing.
func TestNoHostFileTrustsTheImageAndLoopholes(t *testing.T) {
	e, stderr, logOnly := loudEnv(t)
	f := newCATrustFixture(t, e)
	delete(e.Vars, HostCAFileEnv)
	runCABootStep(t, e)
	if strings.Contains(readCAFile(t, filepath.Join(e.Home, caBundleRel)), f.corpCA) {
		t.Error("the bundle holds a host CA with no host file")
	}
	if strings.Contains(stderr.String(), "Trusting") {
		t.Errorf("a disclosure with no host CA: %s", stderr)
	}
	mustContain(t, "the boot log", logOnly, "the host adds no certificate authority", "the launcher composed no host file")
}

// A user's own NODE_EXTRA_CA_CERTS (a Zscaler root behind env_sources, say) is APPENDED to the one
// extras file Node reads, beside the loophole and host CAs: pointing Node at the user's file alone
// would drop the loophole CAs, and with them the oauth broker's interception leaf.
func TestAUsersNodeExtraCACertsKeepsTheLoopholeCAs(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	f := newCATrustFixture(t, e)
	zscaler := caTestPEM(t, "Zscaler Root CA")
	userNode := writeCAFixture(t, t.TempDir(), "zscaler.pem", zscaler)
	userEnv := userEnvFilePath(e)
	if err := os.MkdirAll(filepath.Dir(userEnv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userEnv, []byte("export NODE_EXTRA_CA_CERTS=${NODE_EXTRA_CA_CERTS:-'"+userNode+"'}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCABootStep(t, e)

	extrasPath := filepath.Join(e.Home, ExtraCAFileRel)
	if e.Vars[nodeExtraCAVar] != extrasPath {
		t.Fatalf("%s = %q, want the one extras file %s", nodeExtraCAVar, e.Vars[nodeExtraCAVar], extrasPath)
	}
	extras := readCAFile(t, extrasPath)
	for name, c := range map[string]string{"the loophole CA": f.loopholeCA, "the host's CA": f.corpCA, "the user's CA": zscaler} {
		if strings.Count(extras, c) != 1 {
			t.Errorf("the extras file holds %s %d times, want once", name, strings.Count(extras, c))
		}
	}
	mustContain(t, "the boot's disclosure", stderr, "which also holds the certificates in your "+userNode)
}

// A user NODE_EXTRA_CA_CERTS the jail cannot read is said, with where to look; the loophole CAs
// still reach Node.
func TestAnUnreadableUserNodeExtraCACertsIsSaid(t *testing.T) {
	e, stderr, _ := loudEnv(t)
	f := newCATrustFixture(t, e)
	userEnv := userEnvFilePath(e)
	if err := os.MkdirAll(filepath.Dir(userEnv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userEnv, []byte("export NODE_EXTRA_CA_CERTS='/nowhere/zscaler.pem'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCABootStep(t, e)
	if !strings.Contains(readCAFile(t, filepath.Join(e.Home, ExtraCAFileRel)), f.loopholeCA) {
		t.Error("the loophole CA left the extras file")
	}
	mustContain(t, "the boot's warning", stderr, "/nowhere/zscaler.pem, which the jail could not read", "`env.set`, or a file under `env.files`")
}
