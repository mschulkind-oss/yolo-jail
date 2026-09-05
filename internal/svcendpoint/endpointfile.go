package svcendpoint

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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

// MaxEndpointFileSize bounds what Read is willing to slurp. A published endpoint is
// three whitespace-separated fields — address, base64 DER cert, token — which is
// under 2 KiB for every listener yolo stands up, so a megabyte is orders of magnitude
// of headroom and still a ceiling.
//
// Exported because it is a property of the FORMAT, not of any one reader: the boot
// probe's regression test asserts against the same number, and a second spelling of
// it somewhere else would be a ceiling that drifts.
const MaxEndpointFileSize = 1 << 20

// Read reads and parses the published endpoint. Callers read FRESH ON EVERY DIAL
// — see Dial — so this must stay cheap and must never cache.
func Read(path string) (Endpoint, error) {
	data, err := readEndpointFile(path)
	if err != nil {
		return Endpoint{}, err
	}
	ep, err := Parse(string(data))
	if err != nil {
		return Endpoint{}, fmt.Errorf("%s: %w", path, err)
	}
	return ep, nil
}

// readEndpointFile is the bytes half of Read: STAT FIRST, refuse anything that is
// not a plausible endpoint file, and only then open it.
//
// # Why a stat gate exists at all, when a dozen malformed shapes already end in errors
//
// This used to be a bare os.ReadFile, and os.ReadFile OPENS the path. Opening a FIFO
// that has no writer BLOCKS FOREVER, and no timeout anywhere in this package or its
// callers can reach it: Dial's dialTimeout bounds the dial it has not started yet,
// the boot probe's budget bounds a retry loop it has not entered, and the OAuth
// terminator has no deadline on this call at all. MEASURED 2026-08-18: an os.ReadFile
// of a writer-less fifo does not return in 3s or ever, while an os.Stat of the same
// path answers immediately with p---------.
//
// That is not a caller reporting a fault. It is the caller GONE — the boot probe is
// PID 1 wedged above genFailuresError and below any output, a jail that never starts
// with nothing said about why; the in-jail OAuth terminator is Claude Code simply
// never launching, with no error at all; `yolo check` and the readiness polls hang
// the same way. Every one of those callers reads the SAME per-jail host-services
// directory, which is bind-mounted READ-WRITE into the jail (internal/cli/run's
// hostServicesMountArgs), so anything in the jail can leave a fifo, a device node or
// a unix socket where an endpoint file belongs — a mkfifo, a half-written pack hook,
// a restored backup. No attacker is required, and the gate belongs HERE rather than
// at any one call site precisely because the shape is one file and the readers are
// four packages.
//
// The size cap is the same argument one step along: os.ReadFile has no ceiling, so a
// file something grew to fill the disk is an OOM in the reader rather than an error
// it can report. The cap is enforced twice — the stat refuses the declared size, and
// the read itself is bounded — because the stat's answer is a snapshot and a regular
// file can grow between the two.
//
// # How long such a file lasts, which is not "forever" and is not "one boot" either
//
// The host CLEARS it on the next launch: every respawn path unlinks the stale artifact
// before spawning (internal/cli/run/loopholesruntime.go's os.Remove, retireStaleRelayFiles
// for the relay's pair) and Publish renames a temp file onto the target. unlink(2) and
// rename(2) both work on a fifo, and the host half never opens the path, so it cannot be
// wedged the way a reader can.
//
// What that argument misses — and it was the argument for putting this gate only in the
// boot probe — is that the host's next launch is not the next READ. A jail's OAuth
// terminator reads this directory for the whole life of the session, so the window there
// is not one boot but every read until the container exits. That is the case this gate is
// really for; the boot probe's was merely the one that got noticed first.
//
// # Why the error is ErrEndpointMalformed
//
// Because that is what these shapes are under this package's own definition: "the
// file exists but is not a complete, usable endpoint". The ATTRIBUTION is the part
// that carries weight downstream — internal/entrypoint's classifyReachability maps
// exactly ErrEndpointMissing and ErrEndpointMalformed onto faultUnpublished and
// EVERYTHING ELSE onto faultUnreachable, which is the one class OQ-R2's fatal refuses
// a launch over. An untyped errno here would put "a fifo sits where an endpoint
// belongs" into the refuse-the-launch class, and a local file shape has nothing to do
// with the network. (That is not hypothetical: a DIRECTORY at the endpoint path used
// to land there, via os.ReadFile's untyped EISDIR.)
//
// ErrEndpointMissing would be the wrong sentinel for the same reason it is the right
// one for ENOENT: it carries a second promise — errors.Is(err, syscall.ENOENT), which
// internal/oauthterminator's frozen two-layer attribution gates on — and a fifo that
// exists is not an absent file.
//
// # What it does not close, and why O_NONBLOCK is not the answer
//
// A stat is not an atomic open, so a path that becomes a fifo between the two still
// wedges. An earlier note (internal/entrypoint/reachability.go, now retired) claimed
// the fix for that was O_NONBLOCK plumbed through here. MEASURED 2026-08-18, and it
// is not: os.OpenFile with syscall.O_NONBLOCK returns immediately for a writer-less
// fifo (the read then gives EOF), but with a LIVE, SILENT writer the open succeeds and
// the subsequent Read blocks forever anyway — Go's os package puts the pollable fd in
// the runtime netpoller, which turns EAGAIN back into a wait. Closing the residual
// race properly needs a read deadline on a pollable fd, i.e. a different file object
// for a case that is already a race against a corruption nobody has observed. Spelling
// out the flag that does not work is worth more than adding it: it looks like the fix.
func readEndpointFile(path string) ([]byte, error) {
	// os.Stat, not os.Lstat: os.ReadFile followed symlinks before this gate existed,
	// so refusing them here would be a silent behaviour change dressed up as a safety
	// fix. A DANGLING symlink still reports ENOENT, which is the attribution below.
	fi, err := os.Stat(path)
	if err != nil {
		return nil, readPathError(path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is a %s, not a published endpoint file — refusing to open it",
			ErrEndpointMalformed, path, fileKindName(fi.Mode()))
	}
	if fi.Size() > MaxEndpointFileSize {
		return nil, fmt.Errorf("%w: %s is %d bytes, far larger than any published endpoint — refusing to read it",
			ErrEndpointMalformed, path, fi.Size())
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, readPathError(path, err)
	}
	defer func() { _ = f.Close() }()
	// One byte past the ceiling, so "hit the limit" is distinguishable from "the file
	// is exactly the ceiling" without a second stat.
	data, err := io.ReadAll(io.LimitReader(f, MaxEndpointFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxEndpointFileSize {
		return nil, fmt.Errorf("%w: %s grew past %d bytes while being read — refusing it",
			ErrEndpointMalformed, path, MaxEndpointFileSize)
	}
	return data, nil
}

// readPathError attributes a stat or open failure. Only ENOENT is claimed: it is the
// one errno this package has a meaning for, and it has TWO consumers that must both
// keep working, which is why the wrap is double.
//
// Everything else — EACCES on the parent, EIO — is deliberately passed through
// untouched. That is what os.ReadFile did before this gate existed, and inventing an
// attribution for an errno nobody has reasoned about would change how every caller
// classifies it.
func readPathError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		// Two %w: the sentinel AND the *PathError, so errors.Is holds for
		// ErrEndpointMissing and for syscall.ENOENT alike.
		return fmt.Errorf("%w: %w", ErrEndpointMissing, err)
	}
	return err
}

// fileKindName names the shape in the error. "not a regular file" would leave the
// reader to go and stat it themselves, and the specific word is usually the whole
// diagnosis — "named pipe" says somebody ran mkfifo here.
func fileKindName(m os.FileMode) string {
	switch {
	case m.IsDir():
		return "directory"
	case m&os.ModeNamedPipe != 0:
		return "named pipe (fifo)"
	case m&os.ModeSocket != 0:
		return "unix socket"
	case m&os.ModeDevice != 0:
		return "device node"
	default:
		return "special file"
	}
}

// ParsePlain parses a PLAIN endpoint line: exactly one whitespace-separated
// field that splits as host:port.
//
// THE SECOND FORMAT, and why it exists: a service that serves PLAIN HTTP to its
// own jail publishes no credential — there is no cert to pin and, the jail being
// the trust boundary, no token to present (the wire-bridge is the instance;
// wire-bridge.md §2/§4/WB-D4). Its endpoint file is the bare "host:port" line,
// and exactly-one-field is what distinguishes it from the credential triple's
// exactly-three — the same neither-parses-as-the-other discipline Parse
// documents, so a file from either format is unusable as the other and a
// truncated write of one cannot masquerade as the other.
//
// Parse checks STRUCTURE. Probe checks usability.
func ParsePlain(data string) (string, error) {
	fields := strings.Fields(data)
	if len(fields) != 1 {
		return "", fmt.Errorf(
			"%w: a plain endpoint is one whitespace-separated field (host:port), got %d",
			ErrEndpointMalformed, len(fields))
	}
	if _, _, err := net.SplitHostPort(fields[0]); err != nil {
		return "", fmt.Errorf("%w: plain endpoint %q does not split as host:port", ErrEndpointMalformed, fields[0])
	}
	return fields[0], nil
}

// ReadPlain reads and parses a plain endpoint file: the advertised address,
// nothing else. Same stat gate, same size cap, same freshness rule as Read —
// the gate (readEndpointFile) is the part that keeps a fifo planted where an
// endpoint belongs from wedging a reader forever, and a second format must not
// mean a second, ungated reader.
func ReadPlain(path string) (string, error) {
	data, err := readEndpointFile(path)
	if err != nil {
		return "", err
	}
	addr, err := ParsePlain(string(data))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return addr, nil
}

// DialPlain connects to the plain endpoint published at endpointPath — a bare
// TCP connect to the ADVERTISED address, no TLS, no token frame, the connect
// then the caller's close. It is the reachability witness's probe shape for a
// plain-HTTP service, the exact analogue of what Dial + connect-then-close
// already proves for the credential transport: the listener exists and accepts.
//
// The file is re-read fresh on every dial, for the same three reasons Dial's
// doc gives — restarts, republishes, and an already-running container whose
// environment is frozen.
func DialPlain(endpointPath string, dialTimeout time.Duration) (net.Conn, error) {
	addr, err := ReadPlain(endpointPath)
	if err != nil {
		return nil, err
	}
	return net.DialTimeout("tcp", addr, dialTimeout)
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
