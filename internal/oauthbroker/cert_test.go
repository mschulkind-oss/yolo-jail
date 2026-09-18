package oauthbroker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// brokerStateDir points BrokerDir() at a fresh temp dir for one test.
func brokerStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YOLO_BROKER_STATE_DIR", dir)
	return dir
}

// readCert reads a PEM certificate file and parses the first block.
func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("%s is not a PEM CERTIFICATE (first block: %+v)", path, block)
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return crt
}

// TestEnsureCAAndLeafMintsAChainAJailWillTrust is the PRIMARY guard, and it
// asserts the verification a jail actually performs rather than the fields the
// minting code happens to set.
//
// Inside a jail, ca.crt is installed as a trust anchor (NODE_EXTRA_CA_CERTS plus
// the generated CA bundle) and Claude Code opens TLS to platform.claude.com,
// which --add-host points at the in-jail terminator. So the question that decides
// whether every jail on the host still works is exactly `leaf.Verify` against a
// pool holding ca.crt, for that DNS name, for ServerAuth — which is what this
// runs. A property-by-property assertion would pass with, say, the SAN dropped
// and CommonName kept, which no modern verifier accepts.
func TestEnsureCAAndLeafMintsAChainAJailWillTrust(t *testing.T) {
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}

	ca := readCert(t, caCrt(dir))
	leaf := readCert(t, serverCrt(dir))

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	// Both names the retired openssl config put in subjectAltName. UpstreamHost is
	// what Claude Code asks for; localhost is what a hand-run curl asks for.
	for _, name := range []string{UpstreamHost, "localhost"} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			DNSName:   name,
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Errorf("the minted leaf does not verify for %q against the minted CA: %v\n"+
				"this is the exact check every jail's node client runs, so a failure here "+
				"is every jail's Claude auth broken", name, err)
		}
	}

	// The anchor half. A verifier that checks basicConstraints on the trust anchor
	// — every OpenSSL-family one, which is what node is — rejects a CA:FALSE
	// anchor, and Go's own verifier would NOT catch that above once the anchor is
	// in the pool.
	if !ca.IsCA || !ca.BasicConstraintsValid {
		t.Errorf("ca.crt IsCA=%v BasicConstraintsValid=%v; an anchor without CA:TRUE is "+
			"rejected by OpenSSL-family verifiers", ca.IsCA, ca.BasicConstraintsValid)
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("ca.crt lacks KeyUsageCertSign, which a CA:TRUE anchor is expected to carry")
	}
	if ca.Subject.CommonName != caCommonName {
		t.Errorf("CA CommonName = %q, want %q (what the openssl -subj produced)",
			ca.Subject.CommonName, caCommonName)
	}
	// Backdated: the verifier is in a container, possibly behind a podman-machine
	// VM clock, and a not-yet-valid cert fails with an error that looks nothing
	// like a clock problem.
	if !leaf.NotBefore.Before(ca.NotAfter) || !leaf.NotBefore.Before(leaf.NotAfter) {
		t.Errorf("leaf NotBefore %v is not inside its own validity window", leaf.NotBefore)
	}
	if leaf.NotAfter.Before(ca.NotBefore) {
		t.Error("the leaf outlives nothing — CA and leaf validity windows do not overlap")
	}
}

// TestEnsureCAAndLeafKeepsTheCAPrivateKeyOffDisk is the half of the original
// defect the image bake did not touch (docs/design/broker-ca-and-nested-hosts.md
// OQ-1): the CA is the trust anchor every jail on this host believes, so its
// private key on disk is a standing authority to impersonate any host to any
// jail.
//
// It sweeps the WHOLE state dir for private-key PEM rather than stat'ing ca.key,
// because the failure it guards against is "a private key ended up on disk", not
// "a file called ca.key exists" — a re-port that wrote the CA key as ca.pem, or
// bundled it into ca.crt, would slip past a filename check.
//
// server.key is the one permitted private key and its presence is asserted, not
// merely tolerated: it is what `yolo-jaild oauth-terminator` hands to
// ListenAndServeTLS inside the jail, so a port that got clever and dropped it
// would break every jail while making this test greener.
func TestEnsureCAAndLeafKeepsTheCAPrivateKeyOffDisk(t *testing.T) {
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var withKeys []string
	for _, e := range entries {
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		for rest := raw; len(rest) > 0; {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if strings.Contains(block.Type, "PRIVATE KEY") {
				withKeys = append(withKeys, e.Name())
			}
		}
	}
	if len(withKeys) != 1 || withKeys[0] != "server.key" {
		t.Errorf("files carrying a PRIVATE KEY block = %v, want exactly [server.key].\n"+
			"The CA's private key must never reach the filesystem: it is the anchor every "+
			"jail trusts, and issue #33 is what happens when it does.", withKeys)
	}

	// The openssl era's leftovers are not merely unwritten, they are gone.
	for _, p := range legacyOpensslArtifacts(dir) {
		if isFile(p) {
			t.Errorf("%s still exists after a crypto/x509 mint", p)
		}
	}
	if !isFile(serverKey(dir)) {
		t.Error("server.key is missing — the in-jail terminator has no TLS key to serve with")
	}
}

// TestEnsureCAAndLeafRetiresALegacyOpensslCAKey pins the UPGRADE path, which is
// the only path on which the port actually retires anything: every host that has
// ever run the broker already has a CA key on disk, and a short-circuit that only
// looked for the three certificate files would leave that key — and the CA it
// belongs to — in service forever.
//
// Mutation-checked: dropping the ca.key clause from caAndLeafAreCurrent leaves
// the seeded material untouched and fails here.
func TestEnsureCAAndLeafRetiresALegacyOpensslCAKey(t *testing.T) {
	dir := brokerStateDir(t)
	// A state dir as the openssl path left it: the three mounted files plus the
	// four artifacts only openssl needed.
	seeded := map[string]string{
		"ca.crt":     "old-ca-cert",
		"server.crt": "old-leaf-cert",
		"server.key": "old-leaf-key",
		"ca.key":     "OLD CA PRIVATE KEY",
		"ca.srl":     "01",
		"leaf.cnf":   "[req]",
		"server.csr": "old-csr",
	}
	for name, body := range seeded {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}

	if isFile(legacyCAKey(dir)) {
		t.Error("ca.key survived the upgrade — the on-disk CA private key is exactly what " +
			"this port exists to retire, and a host that ran the old code is the only host " +
			"that has one")
	}
	// Re-minted, not adopted: the old ca.crt is not a certificate at all, so
	// keeping it would leave the jail with an unparseable anchor.
	for _, name := range []string{"ca.crt", "server.crt", "server.key"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) == seeded[name] {
			t.Errorf("%s was kept as-is; the CA and the leaf must be re-minted together "+
				"once the signing key is gone", name)
		}
	}
	// And the replacement is a working chain, not just different bytes.
	roots := x509.NewCertPool()
	roots.AddCert(readCert(t, caCrt(dir)))
	if _, err := readCert(t, serverCrt(dir)).Verify(x509.VerifyOptions{
		DNSName: UpstreamHost, Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("the re-minted pair does not verify: %v", err)
	}
}

// TestEnsureCAAndLeafIsIdempotent pins the short-circuit, which is a safety
// property rather than a performance one: the broker singleton calls
// EnsureCAAndLeaf on every start, and a mint on every start would hand each
// newly-launched jail a CA that the jails already running do not trust.
func TestEnsureCAAndLeafIsIdempotent(t *testing.T) {
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("first EnsureCAAndLeaf: %v", err)
	}
	before := map[string][]byte{}
	for _, p := range []string{caCrt(dir), serverCrt(dir), serverKey(dir)} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = raw
	}

	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("second EnsureCAAndLeaf: %v", err)
	}
	for p, want := range before {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s changed on a second EnsureCAAndLeaf(false); every broker restart "+
				"would then rotate the CA out from under the jails already running", p)
		}
	}
}

// TestForceInitCARotatesBothHalvesTogether pins --force-init-ca, and specifically
// that it rotates the PAIR. A force that regenerated the CA alone (or the leaf
// alone) would leave a jail mounting a leaf its anchor cannot verify — the one
// state the mount narrowing cannot protect against, because all three files cross
// together.
func TestForceInitCARotatesBothHalvesTogether(t *testing.T) {
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}
	firstCA := readCert(t, caCrt(dir))
	firstLeaf := readCert(t, serverCrt(dir))

	if err := EnsureCAAndLeaf(true); err != nil {
		t.Fatalf("EnsureCAAndLeaf(force): %v", err)
	}
	secondCA := readCert(t, caCrt(dir))
	secondLeaf := readCert(t, serverCrt(dir))

	if secondCA.SerialNumber.Cmp(firstCA.SerialNumber) == 0 {
		t.Error("--force-init-ca did not regenerate the CA")
	}
	if secondLeaf.SerialNumber.Cmp(firstLeaf.SerialNumber) == 0 {
		t.Error("--force-init-ca did not regenerate the leaf")
	}
	roots := x509.NewCertPool()
	roots.AddCert(secondCA)
	if _, err := secondLeaf.Verify(x509.VerifyOptions{
		DNSName: UpstreamHost, Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("after --force-init-ca the pair does not verify against itself: %v", err)
	}
	// The old CA must not still validate the new leaf either — that would mean the
	// CA was not really rotated.
	oldRoots := x509.NewCertPool()
	oldRoots.AddCert(firstCA)
	if _, err := secondLeaf.Verify(x509.VerifyOptions{
		DNSName: UpstreamHost, Roots: oldRoots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("the new leaf still verifies under the OLD CA — the rotation did not happen")
	}
}

// TestMintedLeafLoadsThroughTheTerminatorsOwnAPI calls the exact function the
// in-jail consumer calls. `yolo-jaild oauth-terminator` serves with
// http.Server.ListenAndServeTLS(cert, key), which is tls.LoadX509KeyPair under
// the skin — so the PEM types, the key encoding (PKCS#8 here, where openssl
// emitted PKCS#1/SEC1) and the cert/key correspondence are all pinned by one
// call, in the same shape the jail makes it.
//
// It also pins the file modes: server.key is the private half of the pair and
// crosses into the jail, so 0600 is not cosmetic.
func TestMintedLeafLoadsThroughTheTerminatorsOwnAPI(t *testing.T) {
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}

	if _, err := tls.LoadX509KeyPair(serverCrt(dir), serverKey(dir)); err != nil {
		t.Fatalf("tls.LoadX509KeyPair(server.crt, server.key): %v\n"+
			"this is what the in-jail terminator's ListenAndServeTLS does, so a failure "+
			"here is a terminator that cannot start", err)
	}

	for path, want := range map[string]os.FileMode{
		caCrt(dir):     0o644,
		serverCrt(dir): 0o644,
		serverKey(dir): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", path, got, want)
		}
	}
}

// TestNoOpensslInThisPackage is the deletion half of the port: the whole point of
// porting rather than baking is that the binary stops being load-bearing, and
// "nothing execs it any more" is a claim only a guard can keep true.
//
// It has to be a source guard because the dependency was invisible to every
// mechanism that would normally hold one — not an import, not a `requires` entry,
// not a shim — which is exactly how it survived long enough to cost 2,549 silent
// daemon deaths. But it reads the AST rather than the bytes, and that distinction
// is load-bearing in the other direction: this package's comments NAME the
// retired dependency on purpose, and a grep-shaped guard would forbid the
// documentation of the very fix it exists to protect. So: no os/exec import, and
// no string literal naming the binary.
func TestNoOpensslInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		parsed++
		for _, imp := range file.Imports {
			if imp.Path.Value == `"os/exec"` {
				t.Errorf("%s imports os/exec; cert minting is crypto/x509 now "+
					"(docs/design/broker-ca-and-nested-hosts.md §8 item 4) and this "+
					"package no longer shells out to anything", name)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "openssl") {
				t.Errorf("%s carries the string literal %s. Re-introducing the shell-out "+
					"re-introduces a dependency the jail image is not obliged to carry, "+
					"and whose absence is invisible until a daemon dies.", name, lit.Value)
			}
			return true
		})
	}
	if parsed == 0 {
		t.Fatal("parsed no non-test sources — this guard would pass vacuously")
	}
}

// TestOpensslAcceptsTheGoMintedChain is the INTEROP guard, and it is the reason
// the CA carries CA:TRUE and keyCertSign even though Go's verifier is satisfied
// without fussing about them. The verifier that decides whether Claude works is
// node's, which is OpenSSL's — so this runs the real thing against the real
// output when the real thing is available.
//
// Skipped rather than failed when openssl is absent: the point of the port is
// that yolo no longer REQUIRES the binary, and a test that demanded it would put
// the dependency straight back, one layer up.
func TestOpensslAcceptsTheGoMintedChain(t *testing.T) {
	bin, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("no openssl on PATH — the interop cross-check needs one, and yolo " +
			"deliberately no longer does")
	}
	dir := brokerStateDir(t)
	if err := EnsureCAAndLeaf(false); err != nil {
		t.Fatalf("EnsureCAAndLeaf: %v", err)
	}
	out, err := exec.Command(bin, "verify", "-CAfile", caCrt(dir), serverCrt(dir)).CombinedOutput()
	if err != nil {
		t.Errorf("openssl verify rejected the Go-minted chain: %v\n%s", err, out)
	}
	// -verify_hostname is where a missing SAN shows up; `verify` alone does not
	// check names.
	out, err = exec.Command(bin, "verify", "-CAfile", caCrt(dir),
		"-verify_hostname", UpstreamHost, serverCrt(dir)).CombinedOutput()
	if err != nil {
		t.Errorf("openssl verify -verify_hostname %s rejected the leaf: %v\n%s",
			UpstreamHost, err, out)
	}
}
