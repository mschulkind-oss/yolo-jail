package oauthbroker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
func caCrt(dir string) string     { return filepath.Join(dir, "ca.crt") }
func caKey(dir string) string     { return filepath.Join(dir, "ca.key") }
func serverCrt(dir string) string { return filepath.Join(dir, "server.crt") }
func serverKey(dir string) string { return filepath.Join(dir, "server.key") }

var opensslFallbackPaths = []string{
	"/usr/bin/openssl",
	"/bin/openssl",
	"/usr/local/bin/openssl",
	"/opt/homebrew/bin/openssl",
	"/usr/local/opt/openssl/bin/openssl",
	"/run/current-system/sw/bin/openssl",
}

// resolveOpenssl finds the openssl binary by PATH or known install dirs.
func resolveOpenssl() string {
	if p, err := exec.LookPath("openssl"); err == nil {
		return p
	}
	for _, p := range opensslFallbackPaths {
		if info, err := os.Stat(p); err == nil && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// runOpenssl execs openssl with args.
func runOpenssl(args ...string) error {
	binary := resolveOpenssl()
	if binary == "" {
		return fmt.Errorf("openssl not found; cannot run openssl subcommand")
	}
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("openssl %v failed:\n%s", args, out)
	}
	return nil
}

// EnsureCAAndLeaf creates the CA + leaf cert pair on first run (idempotent).
// A crypto/x509 migration is a LATER flagged change, deliberately deferred.
func EnsureCAAndLeaf(force bool) error {
	dir := BrokerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	haveCA := isFile(caCrt(dir)) && isFile(caKey(dir))
	haveLeaf := isFile(serverCrt(dir)) && isFile(serverKey(dir))
	if haveCA && haveLeaf && !force {
		return nil
	}
	if resolveOpenssl() == "" {
		return fmt.Errorf("yolo-claude-oauth-broker-host: cannot locate openssl " +
			"(install it, or symlink it into a fallback location)")
	}

	if force || !haveCA {
		if err := runOpenssl("genrsa", "-out", caKey(dir), "4096"); err != nil {
			return err
		}
		if err := os.Chmod(caKey(dir), 0o600); err != nil {
			return err
		}
		if err := runOpenssl("req", "-x509", "-new", "-nodes", "-key", caKey(dir),
			"-sha256", "-days", "3650", "-out", caCrt(dir),
			"-subj", "/CN=yolo-jail-claude-oauth-broker/O=yolo-jail/OU=local"); err != nil {
			return err
		}
		haveLeaf = false
	}

	if force || !haveLeaf {
		if err := runOpenssl("genrsa", "-out", serverKey(dir), "2048"); err != nil {
			return err
		}
		if err := os.Chmod(serverKey(dir), 0o600); err != nil {
			return err
		}
		cfg := "[req]\n" +
			"distinguished_name=req_distinguished_name\n" +
			"req_extensions=v3_req\n" +
			"prompt=no\n" +
			"[req_distinguished_name]\n" +
			"CN=" + UpstreamHost + "\n" +
			"[v3_req]\n" +
			"subjectAltName=DNS:" + UpstreamHost + ",DNS:localhost\n"
		cfgPath := filepath.Join(dir, "leaf.cnf")
		if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
			return err
		}
		csrPath := filepath.Join(dir, "server.csr")
		if err := runOpenssl("req", "-new", "-key", serverKey(dir), "-out", csrPath,
			"-config", cfgPath); err != nil {
			return err
		}
		if err := runOpenssl("x509", "-req", "-in", csrPath, "-CA", caCrt(dir),
			"-CAkey", caKey(dir), "-CAcreateserial", "-out", serverCrt(dir),
			"-days", "3650", "-sha256", "-extfile", cfgPath,
			"-extensions", "v3_req"); err != nil {
			return err
		}
		_ = os.Remove(csrPath)
	}
	return nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
