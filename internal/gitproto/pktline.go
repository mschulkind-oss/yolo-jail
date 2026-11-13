// Package gitproto reads and writes the parts of git's wire protocol the git broker judges:
// pkt-line framing, side-band multiplexing, a push's ref commands and its report-status, and
// the reduction of a remote URL to a repository identity
// (docs/reference/git-broker.md, ID-3 and ID-9; docs/reference/git-broker.md).
//
// It is shared by the jail's remote helper and the host's broker, so it holds no policy, opens
// no connection and imports nothing from either.
//
// The formats are git's own: gitprotocol-common(5) for pkt-line, gitprotocol-pack(5) for the
// receive-pack command list, push options and report-status, and gitprotocol-v2(5) for the
// delim and response-end packets.
package gitproto

import (
	"errors"
	"fmt"
	"io"
)

// ErrProtocol marks input that is not git's protocol. Every parse error in this package wraps
// it, so a caller can tell a malformed stream from a failed read.
var ErrProtocol = errors.New("git protocol error")

// MaxPacket is the largest pkt-line git sends or accepts, its four-byte length header included
// (LARGE_PACKET_MAX in git's pkt-line.h).
const MaxPacket = 65520

// MaxData is the largest payload one pkt-line carries.
const MaxData = MaxPacket - 4

// Kind is a pkt-line's type: a data packet or one of the three special packets.
type Kind int

const (
	// Data is a packet with a payload (possibly empty, length 0004).
	Data Kind = iota
	// Flush is 0000, the end of a section.
	Flush
	// Delim is 0001, protocol v2's section separator.
	Delim
	// ResponseEnd is 0002, protocol v2's end of a stateless response.
	ResponseEnd
)

func (k Kind) String() string {
	switch k {
	case Data:
		return "data"
	case Flush:
		return "flush"
	case Delim:
		return "delim"
	case ResponseEnd:
		return "response-end"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Packet is one pkt-line. Data is nil for the special packets.
type Packet struct {
	Kind Kind
	Data []byte
}

// Reader reads pkt-lines. It reads exactly the bytes of each packet from the underlying reader
// and never buffers ahead, so whatever follows a section (a pack, say) is left unread for the
// caller.
type Reader struct {
	r   io.Reader
	tee io.Writer
	hdr [4]byte
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader { return &Reader{r: r} }

// Tee copies the raw bytes of every packet to w as each is read, from now on; a nil w stops
// the copy. It copies before anything has been judged, so a caller that must not forward an
// unjudged section tees into a buffer and writes that buffer upstream only once the section
// has been accepted. Forwarding a judged push uses these bytes, never WriteCommands, which
// drops shallow lines and re-orders the capabilities.
func (r *Reader) Tee(w io.Writer) { r.tee = w }

// Read returns the next packet. A clean end of input before any header byte is io.EOF; any
// other short read is io.ErrUnexpectedEOF, and a malformed header wraps ErrProtocol.
func (r *Reader) Read() (Packet, error) {
	if _, err := io.ReadFull(r.r, r.hdr[:]); err != nil {
		return Packet{}, err
	}
	n, ok := parseHex4(r.hdr)
	if !ok {
		return Packet{}, fmt.Errorf("%w: pkt-line length %q is not hexadecimal", ErrProtocol, r.hdr[:])
	}
	switch {
	case n == 0:
		return r.special(Flush)
	case n == 1:
		return r.special(Delim)
	case n == 2:
		return r.special(ResponseEnd)
	case n < 4:
		return Packet{}, fmt.Errorf("%w: pkt-line length %d is invalid", ErrProtocol, n)
	case n > MaxPacket:
		return Packet{}, fmt.Errorf("%w: pkt-line length %d exceeds %d", ErrProtocol, n, MaxPacket)
	}
	data := make([]byte, n-4)
	if _, err := io.ReadFull(r.r, data); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Packet{}, err
	}
	if r.tee != nil {
		if _, err := r.tee.Write(r.hdr[:]); err != nil {
			return Packet{}, err
		}
		if _, err := r.tee.Write(data); err != nil {
			return Packet{}, err
		}
	}
	return Packet{Kind: Data, Data: data}, nil
}

func (r *Reader) special(k Kind) (Packet, error) {
	if r.tee != nil {
		if _, err := r.tee.Write(r.hdr[:]); err != nil {
			return Packet{}, err
		}
	}
	return Packet{Kind: k}, nil
}

func parseHex4(b [4]byte) (int, bool) {
	n := 0
	for _, c := range b {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		n = n<<4 | int(v)
	}
	return n, true
}

// Writer writes pkt-lines.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer over w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Data writes one data packet carrying p. A payload over MaxData is refused, not split: a
// caller that streams larger data chunks it (WriteSideband does).
func (w *Writer) Data(p []byte) error {
	if len(p) > MaxData {
		return fmt.Errorf("%w: a %d-byte payload exceeds the pkt-line limit of %d", ErrProtocol, len(p), MaxData)
	}
	const hex = "0123456789abcdef"
	n := len(p) + 4
	buf := make([]byte, 0, n)
	buf = append(buf, hex[n>>12&0xf], hex[n>>8&0xf], hex[n>>4&0xf], hex[n&0xf])
	buf = append(buf, p...)
	_, err := w.w.Write(buf)
	return err
}

// Line writes one data packet carrying s, as given: the caller adds the newline git expects.
func (w *Writer) Line(s string) error { return w.Data([]byte(s)) }

// Flush writes 0000.
func (w *Writer) Flush() error { return w.raw("0000") }

// Delim writes 0001.
func (w *Writer) Delim() error { return w.raw("0001") }

// ResponseEnd writes 0002.
func (w *Writer) ResponseEnd() error { return w.raw("0002") }

func (w *Writer) raw(s string) error {
	_, err := io.WriteString(w.w, s)
	return err
}

// Raw writes p unframed, for the parts of the protocol that are not pkt-lines (a pack, or a
// report sent to a client that negotiated no side-band).
func (w *Writer) Raw(p []byte) error {
	_, err := w.w.Write(p)
	return err
}
