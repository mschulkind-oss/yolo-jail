package oauthbroker

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
	"time"
)

// BrokerDir returns the writable state dir for the claude-oauth-broker
// loophole — CA + leaf + refresh lock.
// YOLO_BROKER_STATE_DIR is a test-only override (parity harness) so black-box
// tests don't touch the real ~/.local state.
func BrokerDir() string {
	if v := os.Getenv("YOLO_BROKER_STATE_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local", "share", "yolo-jail", "state", "claude-oauth-broker")
}

// cert paths within BrokerDir.
//
// THESE THREE ARE THE WHOLE ON-DISK SURFACE, and the reason each one is on disk
// is that a DIFFERENT PROCESS IN A DIFFERENT NAMESPACE reads it. All three are
// bind-mounted per-file into every jail by the loophole's `state_files`
// narrowing (packs/claude/loopholes/claude-oauth-broker/manifest.jsonc, issue
// #33): ca.crt becomes a trust anchor through NODE_EXTRA_CA_CERTS and the jail's
// CA bundle, and server.crt/server.key are what `yolo-jaild oauth-terminator`
// hands to ListenAndServeTLS inside the jail. Nothing host-side reads any of
// them — the host daemon serves an AF_UNIX socket, not TLS.
//
// The CA's PRIVATE key is deliberately absent from this list; see mintCAAndLeaf.
func caCrt(dir string) string     { return filepath.Join(dir, "ca.crt") }
func serverCrt(dir string) string { return filepath.Join(dir, "server.crt") }
func serverKey(dir string) string { return filepath.Join(dir, "server.key") }

// legacyOpensslArtifacts are the files the retired `openssl` shell-out left in
// the state dir, in the order they mattered. ca.key is the one that counts: it
// is the PRIVATE KEY OF THE TRUST ANCHOR every jail on this host trusts, so
// anyone who can read it can mint a certificate for any name and be believed by
// every jail — which is what issue #33 was about, and the half of it that baking
// `openssl` into the image (docs/design/broker-ca-and-nested-hosts.md §5) did not
// touch. The other three are bookkeeping openssl needed and Go does not.
//
// They are REMOVED rather than ignored, and a surviving ca.key forces a re-mint
// (see caAndLeafAreCurrent) — otherwise every host that ever ran the old code
// would keep serving a CA whose private key is still sitting in the state dir,
// and the port would retire the defect only for machines that never had it.
func legacyCAKey(dir string) string { return filepath.Join(dir, "ca.key") }
func legacyOpensslArtifacts(dir string) []string {
	return []string{
		legacyCAKey(dir),
		filepath.Join(dir, "ca.srl"),
		filepath.Join(dir, "leaf.cnf"),
		filepath.Join(dir, "server.csr"),
	}
}

// caCommonName is the CA subject, byte-identical to the `-subj` the openssl
// shell-out passed. It is cosmetic — nothing matches on it — but it is what a
// human sees in a browser's certificate viewer or in `openssl x509 -text`, and
// changing it would make an upgraded host's CA unrecognizable to anyone who had
// learned the old one.
const caCommonName = "yolo-jail-claude-oauth-broker"

// certLifetime matches the retired shell-out's `-days 3650`. The pair is local,
// regenerable and reachable only from this machine's own jails, so expiry buys
// nothing here and an expired broker CA costs a debugging session that starts
// nowhere near a clock.
const certLifetime = 10 * 365 * 24 * time.Hour

// certSkewSlack backdates NotBefore, which the openssl path did not do. The
// verifier is in a CONTAINER and may be behind a podman-machine VM clock; a
// not-yet-valid certificate fails the handshake with an error that looks nothing
// like a clock problem. Same reasoning, same value, as internal/svcendpoint.
const certSkewSlack = time.Hour

// EnsureCAAndLeaf creates the CA + leaf cert pair on first run (idempotent).
//
// PORTED to crypto/x509 (docs/design/broker-ca-and-nested-hosts.md §8 item 4,
// ruling OQ-1). It used to shell out to `openssl` five times, which is what
// killed the broker singleton on every launch whose host was itself a jail —
// 2,549 times in one jail, for months, because the image baked no openssl.
//
// THE CA'S PRIVATE KEY NEVER TOUCHES DISK. It is generated, used to sign the
// leaf, and dropped when this function returns. That is the half of the original
// defect the image bake could not fix: a CA key on disk is a standing authority
// to impersonate any host to every jail, and nothing ever read it back — the old
// code wrote it only because `openssl x509 -req -CAkey` needs a file.
//
// THE LEAF'S PRIVATE KEY STILL DOES, NECESSARILY, and this is not an oversight.
// server.key is bind-mounted into the jail because `yolo-jaild oauth-terminator`
// — a different process, in a different namespace — serves TLS with it. Moving
// it out of the filesystem means the jail minting its own leaf, which is a
// redesign of the loophole rather than a port of its cert code, and §6 of the
// design explicitly does not license one.
//
// force regenerates both halves (--force-init-ca). Rotating the CA is safe for
// already-running jails: each of the three files is bind-mounted BY INODE, so a
// running jail keeps the complete old trio it launched with, while the next
// launch binds a complete new one. What must never happen is a MIXED pair, which
// is why the CA and the leaf are always minted and written together.
func EnsureCAAndLeaf(force bool) error {
	dir := BrokerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !force && caAndLeafAreCurrent(dir) {
		return nil
	}
	return mintCAAndLeaf(dir)
}

// caAndLeafAreCurrent reports whether the state dir already holds material this
// code would be willing to keep serving: the three files a jail mounts, and no
// leftover CA private key from the openssl era.
//
// The ca.key clause is what makes the port retire the defect on an UPGRADING
// host rather than only on a fresh one. Without it the short-circuit above fires
// on the very first run after the upgrade, the old RSA CA stays in service
// forever, and its private key stays on disk forever with it.
func caAndLeafAreCurrent(dir string) bool {
	for _, p := range []string{caCrt(dir), serverCrt(dir), serverKey(dir)} {
		if !isFile(p) {
			return false
		}
	}
	return !isFile(legacyCAKey(dir))
}

// mintCAAndLeaf generates the CA in memory, signs a leaf with it, writes the
// three files a jail needs, and sweeps the openssl era's leftovers.
//
// P-256 rather than the old RSA-4096/RSA-2048 pair. Every verifier in the picture
// handles it — node (NODE_EXTRA_CA_CERTS), the OpenSSL-family clients reading the
// jail's CA bundle, and Go's own tls.LoadX509KeyPair in the terminator — and the
// cost it removes is real: RSA-4096 keygen in Go is seconds with a long tail,
// against a broker.BrokerSpawnTimeout of 5s. A slow mint would surface as a
// singleton that "did not bind its socket", which is the same misreported failure
// this whole document is about.
func mintCAAndLeaf(dir string) error {
	now := time.Now()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating the broker CA key: %w", err)
	}
	caSerial, err := randomSerial()
	if err != nil {
		return err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			CommonName:         caCommonName,
			Organization:       []string{"yolo-jail"},
			OrganizationalUnit: []string{"local"},
		},
		NotBefore: now.Add(-certSkewSlack),
		NotAfter:  now.Add(certLifetime),
		// CertSign is what makes this usable as a trust anchor by verifiers that
		// check it (every OpenSSL-family one); CRLSign is conventional alongside it.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("self-signing the broker CA: %w", err)
	}
	// Parse it back: CreateCertificate needs a *Certificate as the parent, and the
	// template is not one until it has been signed (it carries no RawSubject, so
	// the leaf's issuer would come out empty).
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return fmt.Errorf("parsing the freshly minted broker CA: %w", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating the broker leaf key: %w", err)
	}
	leafSerial, err := randomSerial()
	if err != nil {
		return err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: UpstreamHost},
		// The SAN set is the load-bearing part and it is the openssl config's
		// `subjectAltName=DNS:<upstream>,DNS:localhost` verbatim. UpstreamHost is
		// what Claude Code asks for (--add-host routes it to 127.0.0.1, where the
		// terminator listens); localhost is what a hand-run curl asks for. Modern
		// clients ignore CommonName entirely, so dropping either name here breaks
		// the handshake with a hostname-mismatch error and nothing else changes.
		DNSNames:              []string{UpstreamHost, "localhost"},
		NotBefore:             now.Add(-certSkewSlack),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("signing the broker leaf: %w", err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return fmt.Errorf("marshaling the broker leaf key: %w", err)
	}

	// server.crt carries the LEAF ALONE, as the openssl path produced it. The
	// client is expected to hold the CA out of band (it does — ca.crt is mounted
	// and installed as an anchor), so appending the CA here would change what the
	// terminator serves for no gain.
	writes := []struct {
		path string
		data []byte
		perm os.FileMode
	}{
		{caCrt(dir), pemBlock("CERTIFICATE", caDER), 0o644},
		{serverCrt(dir), pemBlock("CERTIFICATE", leafDER), 0o644},
		{serverKey(dir), pemBlock("PRIVATE KEY", leafKeyDER), 0o600},
	}
	for _, w := range writes {
		if err := writeFileAtomic(w.path, w.data, w.perm); err != nil {
			return err
		}
	}

	// Sweep last: a failure above leaves the old material intact, and an old
	// ca.key beside old certs is a consistent (if undesirable) state, while an old
	// ca.key beside NO certs would leave the next run re-minting anyway.
	for _, p := range legacyOpensslArtifacts(dir) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing the retired cert artifact %s: %w", p, err)
		}
	}
	return nil
}

// randomSerial draws a 128-bit positive serial. The openssl path kept a counter
// in ca.srl; a random serial needs no file, which is one fewer thing in the state
// dir and one fewer thing to sweep.
func randomSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("drawing a certificate serial: %w", err)
	}
	return n, nil
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// writeFileAtomic writes via a temp file in the SAME directory and renames.
//
// The reader that matters is `yolo run`, which bind-mounts these three files
// into a starting jail. A rename is atomic, so a launch racing a re-mint binds
// either the old file or the new one and never a half-written one — where the
// openssl path streamed five files over several seconds with no atomicity at all.
// The three renames are still not atomic AS A SET; that window is microseconds
// wide now instead of seconds, and closing it entirely means a generation
// directory, which is a bigger change than this port.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Chmod before the rename so the file is never visible at its final path with
	// CreateTemp's 0600 (harmless for the key, wrong for the two certs).
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
