// Package nixrootstest is a fake nix daemon for tests: it speaks the worker-protocol
// handshake and AddIndirectRoot over a unix socket, and records every root it is sent.
// Nothing in a shipped binary imports it.
//
// IT DOES NOT IMPORT nixroots, and that is the point of it: it is the SERVER half of the
// protocol written separately from the client, after nix's own daemon (src/libstore/daemon.cc
// and worker-protocol-connection.cc at c621c2b), so a framing mistake in the client is not
// repeated here and cancelled out.
package nixrootstest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// Daemon is one fake daemon, listening on Socket until the test ends.
type Daemon struct {
	// Socket is the unix socket to dial, under a short directory of its own.
	Socket string

	mu    sync.Mutex
	roots []string
	ops   []string
	conns int
}

// Options shape what the fake answers.
type Options struct {
	// Protocol is the version the daemon advertises; 0 is 1.38, the version the design
	// measured against (in-jail-nix-roots.md §3).
	Protocol uint64
	// Reject, when set, answers AddIndirectRoot with an error frame carrying it.
	Reject string
	// Chatter sends a log line and an activity before each operation's end, as a real
	// daemon may.
	Chatter bool
}

// Start listens on a fresh socket and serves until t ends. The directory is made under /tmp
// rather than t.TempDir(), which on darwin is long enough to pass the 104-byte sun_path.
func Start(t testing.TB, opts Options) *Daemon {
	t.Helper()
	if opts.Protocol == 0 {
		opts.Protocol = 1<<8 | 38
	}
	dir, err := os.MkdirTemp("/tmp", "nrd-")
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{Socket: filepath.Join(dir, "socket")}
	ln, err := net.Listen("unix", d.Socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns++
			id := d.conns
			d.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				d.serve(conn, opts, id)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
		_ = os.RemoveAll(dir)
	})
	return d
}

// Roots is every path the daemon was asked to root, in order.
func (d *Daemon) Roots() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.roots...)
}

// Ops is every operation the daemon served, in order, as "<conn>:temp:<path>" or
// "<conn>:indirect:<path>", <conn> numbering the connections from 1.
func (d *Daemon) Ops() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ops...)
}

// Connections is how many clients connected.
func (d *Daemon) Connections() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns
}

const (
	magic1 = 0x6e697863
	magic2 = 0x6478696f

	stderrNext          = 0x6f6c6d67
	stderrLast          = 0x616c7473
	stderrError         = 0x63787470
	stderrStartActivity = 0x53545254
	stderrStopActivity  = 0x53544f50

	opAddTempRoot     = 11
	opAddIndirectRoot = 12
)

type conn struct {
	r     *bufio.Reader
	w     *bufio.Writer
	proto uint64
}

func (d *Daemon) serve(nc net.Conn, opts Options, id int) {
	c := &conn{r: bufio.NewReader(nc), w: bufio.NewWriter(nc)}
	if c.u64() != magic1 {
		return
	}
	c.put(magic2, opts.Protocol)
	c.w.Flush()
	client := c.u64()
	c.proto = min(client, opts.Protocol)
	minor := c.proto & 0xff
	if minor >= 38 {
		c.put(0) // no features
		c.w.Flush()
		for n := c.u64(); n > 0; n-- {
			c.str()
		}
	}
	if minor >= 14 && c.u64() != 0 {
		c.u64()
	}
	if minor >= 11 {
		c.u64()
	}
	if minor >= 33 {
		c.putStr("2.35.2-fake")
	}
	if minor >= 35 {
		c.put(2) // not trusted, as a jail's client is
	}
	c.put(stderrLast)
	c.w.Flush()
	for {
		op, err := c.u64e()
		if err != nil {
			return
		}
		if op != opAddIndirectRoot && op != opAddTempRoot {
			c.fail("invalid operation")
			c.w.Flush()
			return
		}
		path := c.str()
		if op == opAddTempRoot {
			// daemon.cc: addTempRoot, stopWork, then 1. A temp root is never refused.
			d.mu.Lock()
			d.ops = append(d.ops, strconv.Itoa(id)+":temp:"+path)
			d.mu.Unlock()
			c.put(stderrLast, 1)
			c.w.Flush()
			continue
		}
		if opts.Chatter {
			c.put(stderrNext)
			c.putStr("adding an indirect root")
			if minor >= 20 {
				// id, level, type, text, fields [int 7, string "x"], parent
				c.put(stderrStartActivity, 1, 3, 0)
				c.putStr("rooting")
				c.put(2, 0, 7, 1)
				c.putStr("x")
				c.put(0)
				c.put(stderrStopActivity, 1)
			}
		}
		if opts.Reject != "" {
			c.fail(opts.Reject)
			c.w.Flush()
			continue
		}
		d.mu.Lock()
		d.roots = append(d.roots, path)
		d.ops = append(d.ops, strconv.Itoa(id)+":indirect:"+path)
		d.mu.Unlock()
		c.put(stderrLast, 1)
		c.w.Flush()
	}
}

// fail writes an error frame in the negotiated dialect: structured since 1.26, a message and
// a status before.
func (c *conn) fail(msg string) {
	c.put(stderrError)
	if c.proto&0xff >= 26 {
		c.putStr("Error")
		c.put(0)
		c.putStr("Error")
		c.putStr(msg)
		c.put(0, 1, 0) // no position, one trace with no position
		c.putStr("while adding a root")
		return
	}
	c.putStr(msg)
	c.put(1)
}

func (c *conn) u64() uint64 {
	v, _ := c.u64e()
	return v
}

func (c *conn) u64e() (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func (c *conn) str() string {
	n := c.u64()
	if n > 1<<20 {
		return ""
	}
	b := make([]byte, n+(8-n%8)%8)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return ""
	}
	return string(b[:n])
}

func (c *conn) put(vs ...uint64) {
	for _, v := range vs {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		_, _ = c.w.Write(b[:])
	}
}

func (c *conn) putStr(s string) {
	c.put(uint64(len(s)))
	_, _ = c.w.WriteString(s)
	_, _ = c.w.Write(make([]byte, (8-len(s)%8)%8))
}
