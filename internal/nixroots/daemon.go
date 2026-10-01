package nixroots

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// daemon.go is the one nix worker-protocol operation yolo speaks: the handshake, then
// AddIndirectRoot (in-jail-nix-roots.md §4, "The protocol client").
//
// THE CLIENT ADVERTISES PROTOCOL 1.37 ON PURPOSE. 1.38 adds a feature negotiation this
// client has no use for; a daemon answers an older client in the older dialect as a matter
// of course, and 1.37 is what the design measured against a 1.38 daemon (§3, M4 and M6). Every
// version-gated step below is gated on the NEGOTIATED version, min(daemon, client), which is
// the rule both of nix's own halves follow, so an older daemon is spoken to in its dialect too.
//
// Sources, nix c621c2b: src/libstore/worker-protocol-connection.cc (the handshake and
// processStderr), src/libstore/daemon.cc (the AddIndirectRoot handler: it records the string
// verbatim and checks no trust), src/libutil/serialise.cc (the error frame).

const (
	workerMagic1 = 0x6e697863
	workerMagic2 = 0x6478696f

	stderrNext          = 0x6f6c6d67
	stderrRead          = 0x64617461
	stderrWrite         = 0x64617416
	stderrLast          = 0x616c7473
	stderrError         = 0x63787470
	stderrStartActivity = 0x53545254
	stderrStopActivity  = 0x53544f50
	stderrResult        = 0x52534c54

	opAddIndirectRoot = 12

	clientProtocol = 1<<8 | 37

	// maxString bounds one string the daemon sends, so a confused peer cannot make the
	// client allocate without limit. A daemon's version string and its log lines are far
	// below it.
	maxString = 1 << 20
)

// DefaultSocket is where a jail finds the host's nix daemon: the launcher binds the host's
// daemon-socket directory at the same path.
const DefaultSocket = "/nix/var/nix/daemon-socket/socket"

// DefaultTimeout bounds the whole exchange with the daemon. The operation is one small
// write on the daemon's side; anything slower is a daemon that is not answering, and a
// launch must not wait on it.
const DefaultTimeout = 10 * time.Second

// RejectedError is the daemon answering with an error, the one failure the design says is
// worth a line (§4, "Failure"): an untrusted client has always been allowed this operation
// (§2.2), so a refusal means the host's nix changed that rule.
type RejectedError struct{ Msg string }

func (e *RejectedError) Error() string { return "the nix daemon refused: " + e.Msg }

// AddIndirectRoot asks the nix daemon at socket to record path as an indirect GC root. The
// daemon stores the string as given and resolves it on its own filesystem at its next GC
// or root query, so path must be the HOST's spelling of a symlink into the store.
//
// A daemon that answers with an error is a *RejectedError. Every other failure — no socket,
// a closed connection, a deadline, a frame this client does not expect — is returned as a
// plain error, and a caller treats it as "no root", today's behavior in a jail.
func AddIndirectRoot(socket, path string, timeout time.Duration) error {
	if socket == "" {
		socket = DefaultSocket
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conn, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	c := &wire{r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	if err := c.handshake(); err != nil {
		return err
	}
	c.writeU64(opAddIndirectRoot)
	c.writeString(path)
	if err := c.flush(); err != nil {
		return err
	}
	if err := c.processStderr(); err != nil {
		return err
	}
	result, err := c.readU64()
	if err != nil {
		return err
	}
	if result != 1 {
		return fmt.Errorf("nix daemon: AddIndirectRoot answered %d, want 1", result)
	}
	return nil
}

// wire is one connection's framing: little-endian u64s, and strings as a u64 length, the
// bytes, and zero padding to a multiple of eight.
type wire struct {
	r *bufio.Reader
	w *bufio.Writer
	// proto is the negotiated protocol version, set by the handshake.
	proto uint64
	// err is the first write error; writes are buffered, so it surfaces at flush.
	err error
}

func minor(v uint64) uint64 { return v & 0xff }

func (c *wire) handshake() error {
	c.writeU64(workerMagic1)
	if err := c.flush(); err != nil {
		return err
	}
	magic, err := c.readU64()
	if err != nil {
		return err
	}
	if magic != workerMagic2 {
		return errors.New("nix daemon: protocol mismatch (no worker magic)")
	}
	daemon, err := c.readU64()
	if err != nil {
		return err
	}
	if daemon>>8 != 1 || minor(daemon) < 10 {
		return fmt.Errorf("nix daemon: unsupported protocol %d.%d", daemon>>8, minor(daemon))
	}
	c.writeU64(clientProtocol)
	c.proto = min(daemon, clientProtocol)
	if minor(c.proto) >= 14 {
		c.writeU64(0) // obsolete CPU affinity: none
	}
	if minor(c.proto) >= 11 {
		c.writeU64(0) // obsolete reserveSpace: false
	}
	if err := c.flush(); err != nil {
		return err
	}
	if minor(c.proto) >= 33 {
		if _, err := c.readString(); err != nil { // the daemon's nix version
			return err
		}
	}
	if minor(c.proto) >= 35 {
		if _, err := c.readU64(); err != nil { // whether the daemon trusts us
			return err
		}
	}
	return c.processStderr()
}

// processStderr reads the daemon's log frames up to the one that ends an operation, and
// turns an error frame into a *RejectedError.
func (c *wire) processStderr() error {
	for {
		msg, err := c.readU64()
		if err != nil {
			return err
		}
		switch msg {
		case stderrLast:
			return nil
		case stderrError:
			return c.readError()
		case stderrNext:
			if _, err := c.readString(); err != nil {
				return err
			}
		case stderrStartActivity:
			// id, level, type, text, fields, parent
			if err := c.skipU64s(3); err != nil {
				return err
			}
			if _, err := c.readString(); err != nil {
				return err
			}
			if err := c.skipFields(); err != nil {
				return err
			}
			if err := c.skipU64s(1); err != nil {
				return err
			}
		case stderrStopActivity:
			if err := c.skipU64s(1); err != nil {
				return err
			}
		case stderrResult:
			// id, type, fields
			if err := c.skipU64s(2); err != nil {
				return err
			}
			if err := c.skipFields(); err != nil {
				return err
			}
		case stderrRead, stderrWrite:
			return fmt.Errorf("nix daemon: unexpected data frame %#x for an operation that carries none", msg)
		default:
			return fmt.Errorf("nix daemon: unknown log frame %#x", msg)
		}
	}
}

// readError reads an error frame's body. Since protocol 1.26 it is a structured error —
// "Error", level, a vestigial name, the message, a position flag and the traces; before,
// a message and an exit status.
func (c *wire) readError() error {
	if minor(c.proto) < 26 {
		msg, err := c.readString()
		if err != nil {
			return err
		}
		if err := c.skipU64s(1); err != nil {
			return err
		}
		return &RejectedError{Msg: msg}
	}
	if _, err := c.readString(); err != nil { // "Error"
		return err
	}
	if err := c.skipU64s(1); err != nil { // level
		return err
	}
	if _, err := c.readString(); err != nil { // name, no longer used
		return err
	}
	msg, err := c.readString()
	if err != nil {
		return err
	}
	if err := c.skipU64s(1); err != nil { // position: always none
		return err
	}
	traces, err := c.readU64()
	if err != nil {
		return err
	}
	for i := uint64(0); i < traces; i++ {
		if err := c.skipU64s(1); err != nil {
			return err
		}
		if _, err := c.readString(); err != nil {
			return err
		}
	}
	return &RejectedError{Msg: msg}
}

// skipFields reads an activity's field list: a count, then each field's type (0 an
// integer, 1 a string) and value.
func (c *wire) skipFields() error {
	n, err := c.readU64()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		kind, err := c.readU64()
		if err != nil {
			return err
		}
		switch kind {
		case 0:
			err = c.skipU64s(1)
		case 1:
			_, err = c.readString()
		default:
			err = fmt.Errorf("nix daemon: unknown field type %d", kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *wire) skipU64s(n int) error {
	for i := 0; i < n; i++ {
		if _, err := c.readU64(); err != nil {
			return err
		}
	}
	return nil
}

func (c *wire) readU64() (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func (c *wire) readString() (string, error) {
	n, err := c.readU64()
	if err != nil {
		return "", err
	}
	if n > maxString {
		return "", fmt.Errorf("nix daemon: a %d-byte string is past this client's limit", n)
	}
	buf := make([]byte, n+pad(n))
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func (c *wire) writeU64(v uint64) {
	if c.err != nil {
		return
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	_, c.err = c.w.Write(b[:])
}

func (c *wire) writeString(s string) {
	c.writeU64(uint64(len(s)))
	if c.err != nil {
		return
	}
	if _, c.err = c.w.WriteString(s); c.err != nil {
		return
	}
	var zeros [8]byte
	_, c.err = c.w.Write(zeros[:pad(uint64(len(s)))])
}

func (c *wire) flush() error {
	if c.err != nil {
		return c.err
	}
	return c.w.Flush()
}

// pad is the zero padding after an n-byte string.
func pad(n uint64) uint64 { return (8 - n%8) % 8 }
