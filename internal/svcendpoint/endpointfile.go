package svcendpoint

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Typed errors. The ATTRIBUTION is load-bearing, not the text: callers must be
// able to tell "the listener has published nothing" (a transport-layer fault)
// from "the token is wrong" (a configuration fault) from "whatever is behind the
// transport failed" — three different fixes, three different messages.
var (
	// ErrEndpointMissing means no endpoint file exists. An error returned for this
	// reason ALSO satisfies errors.Is(err, syscall.ENOENT): a caller's existing
	// errno gate (the OAuth terminator's, for one) attributes ENOENT to the relay
	// layer, and that attribution must survive the transport change.
	ErrEndpointMissing = errors.New("svcendpoint: endpoint not published")
	// ErrEndpointMalformed means the file exists but is not a complete, usable
	// endpoint — including a truncated write and an older 2-field publication.
	ErrEndpointMalformed = errors.New("svcendpoint: malformed endpoint file")
	// ErrAuthRejected means the listener hung up instead of acking the token.
	ErrAuthRejected = errors.New("svcendpoint: auth rejected")
)

// Endpoint is one published listener: where it is, what certificate it must
// present, and the bearer token that authorizes a connection to it.
//
// Token IS A SECRET. An Endpoint value must never be logged, formatted into a
// diagnostic, or written anywhere but the per-jail file Publish writes 0600.
type Endpoint struct {
	// HostPort is the ADVERTISED host joined to the kernel-assigned local port.
	HostPort string
	// CertDER is the public certificate the client pins, exactly.
	CertDER []byte
	// Token is 64 lowercase hex — A SECRET.
	Token string
}

// Format renders the published line: "<host:port> <base64(cert DER)> <token>\n".
//
// Three whitespace-separated fields on one line. All three are whitespace-free BY
// CONSTRUCTION (a host:port, the base64 standard alphabet, lowercase hex), so
// strings.Fields is unambiguous and there is no escaping question to get wrong.
// JSON would buy nothing here and cost a parser in every client — including the
// baked, dependency-free ones. One line plus one rename is the smallest artifact
// that cannot be read torn.
func (e Endpoint) Format() string {
	return e.HostPort + " " + base64.StdEncoding.EncodeToString(e.CertDER) + " " + e.Token + "\n"
}

// Parse decodes a published line. EXACTLY three fields are required.
//
// Tolerating extra-or-missing fields is what makes a stale or truncated file look
// healthy: a 2-field file (an older publication, or a half-written one) would
// parse as "no token", which either fails later with a confusing error or — if
// anyone ever writes `if token != ""` — authenticates nothing at all. Exactly-3
// makes that a parse error, which is the honest reading. A future fourth field is
// then an explicit, loud format change rather than a silent one.
//
// Parse checks STRUCTURE. Probe checks usability.
func Parse(data string) (Endpoint, error) {
	fields := strings.Fields(data)
	if len(fields) != 3 {
		return Endpoint{}, fmt.Errorf(
			"%w: want exactly 3 whitespace-separated fields (host:port, base64 cert, token), got %d",
			ErrEndpointMalformed, len(fields))
	}
	der, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return Endpoint{}, fmt.Errorf("%w: cert field is not base64", ErrEndpointMalformed)
	}
	return Endpoint{HostPort: fields[0], CertDER: der, Token: fields[2]}, nil
}

// Publish atomically writes ep to path with mode 0600.
//
// THE PUBLISHED ENDPOINT FILE IS A CREDENTIAL. It carries the per-jail bearer
// token alongside the address and the public cert, so its 0600 mode and its
// per-jail directory are load-bearing, not cosmetic. Never log its contents,
// never copy it between jails, never place it in a shared directory.
//
// Temp file in the SAME directory, then rename: a client re-reads this file on
// every dial, and os.WriteFile races that reader — a torn read would hand back a
// truncated token. Publishing again is how ROTATION works: rewrite, and the next
// dial picks it up with no restart and no relaunch.
func Publish(path string, ep Endpoint) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".endpoint-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return e
	}
	if _, err := tmp.WriteString(ep.Format()); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Read reads and parses the published endpoint. Callers read FRESH ON EVERY DIAL
// — see Dial — so this must stay cheap and must never cache.
func Read(path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Two %w: the sentinel AND the *PathError, so errors.Is holds for
			// ErrEndpointMissing and for syscall.ENOENT alike.
			return Endpoint{}, fmt.Errorf("%w: %w", ErrEndpointMissing, err)
		}
		return Endpoint{}, err
	}
	ep, err := Parse(string(data))
	if err != nil {
		return Endpoint{}, fmt.Errorf("%s: %w", path, err)
	}
	return ep, nil
}

// Probe reports whether path holds a COMPLETE, USABLE endpoint. This is the
// health and wait predicate everywhere.
//
// EXISTENCE IS NOT HEALTH. A file that exists but is truncated, or was written by
// an older listener, would otherwise read as healthy forever — so a dead listener
// is never respawned and the jail can never reach it. Probe parses the content:
// three fields, a splittable host:port, a certificate that actually parses, and a
// well-formed token.
func Probe(path string) bool {
	ep, err := Read(path)
	if err != nil {
		return false
	}
	host, port, err := net.SplitHostPort(ep.HostPort)
	if err != nil || host == "" || port == "" {
		return false
	}
	if _, err := x509.ParseCertificate(ep.CertDER); err != nil {
		return false
	}
	return IsToken(ep.Token)
}

// ensurePrivateDir creates dir 0700 and then VERIFIES the result, failing closed
// on any mismatch: it must be a real directory, not a symlink, owned by us, with
// no group or world bits.
//
// The check is not paranoia about our own MkdirAll. The publication directory sits
// at a fully deterministic path under a world-writable /tmp, and it now holds a
// credential — MkdirAll succeeds on an ALREADY-EXISTING attacker-owned directory
// without changing its owner or its mode, and from there they can unlink our file
// and plant their own listener. Failing closed is the difference between "the
// service does not start" and "the service publishes a credential into someone
// else's directory".
//
// Consequence for callers: a pre-existing 0755 publication directory is now an
// ERROR, not a warning. Every site that creates one must create it 0700.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Lstat, not Stat: Stat follows a symlink and would report the target's mode
	// and owner while we publish through the link.
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("svcendpoint: refusing to publish into %s: it is a symlink", dir)
	}
	if !st.IsDir() {
		return fmt.Errorf("svcendpoint: refusing to publish into %s: not a directory", dir)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf(
			"svcendpoint: refusing to publish a credential into %s: mode %#o is group/world-accessible, want 0700",
			dir, perm)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		if uid := os.Getuid(); int(sys.Uid) != uid {
			return fmt.Errorf(
				"svcendpoint: refusing to publish a credential into %s: owned by uid %d, not %d",
				dir, sys.Uid, uid)
		}
	}
	return nil
}
