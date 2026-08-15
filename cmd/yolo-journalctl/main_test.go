package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
)

// shortSocketDir returns a scratch dir directly under /tmp.
//
// NOT t.TempDir(): a socket bound under a TMPDIR-rooted path overruns darwin's
// sun_path (104 bytes including the NUL) and that shipped as a CI break once
// (internal/cli/run/brokerrelay_test.go carries the same helper and note).
// Nothing here skips on a bind failure — a skip would turn a platform failure
// into invisible non-coverage.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-jctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// frame renders one journal frame the way internal/journald writes it: ">BI"
// header with the bridge's OWN stream ids (1/2/3), not frameproto's 0/1/2.
func frame(stream byte, payload []byte) []byte {
	hdr := make([]byte, 5)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	return append(hdr, payload...)
}

// exitFrame renders the exit frame with a SIGNED int32 rc — a signal death
// arrives as a negative number and must round-trip.
func exitFrame(rc int) []byte {
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], uint32(int32(rc)))
	return frame(frameExit, p[:])
}

// conversation is an in-memory io.ReadWriter: reply is what the "daemon" sends,
// sent records what the client wrote.
type conversation struct {
	reply io.Reader
	sent  bytes.Buffer
}

func (c *conversation) Read(p []byte) (int, error)  { return c.reply.Read(p) }
func (c *conversation) Write(p []byte) (int, error) { return c.sent.Write(p) }

// TestRequestWireShape pins the request header: one newline-terminated JSON
// object carrying the argv under "args", and nothing else. internal/journald
// reads exactly to that newline and validates that key.
func TestRequestWireShape(t *testing.T) {
	c := &conversation{reply: bytes.NewReader(exitFrame(0))}
	var out, errOut bytes.Buffer
	if rc := converse(c, []string{"-u", "nginx", "-n", "50"}, &out, &errOut); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	line := c.sent.String()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("request is not newline-terminated: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("request carries more than one line: %q", line)
	}
	var req struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("request is not JSON: %v (%q)", err, line)
	}
	if strings.Join(req.Args, " ") != "-u nginx -n 50" {
		t.Fatalf("args = %q, want [-u nginx -n 50]", req.Args)
	}
}

// TestNoArgsSendsAnEmptyList, not null: internal/journald accepts a missing
// "args" but a JSON null under the key is a different value, and the daemon's
// list check is where that difference would surface.
func TestNoArgsSendsAnEmptyList(t *testing.T) {
	c := &conversation{reply: bytes.NewReader(exitFrame(0))}
	var out, errOut bytes.Buffer
	converse(c, nil, &out, &errOut)
	if want := `{"args":[]}`; !strings.HasPrefix(c.sent.String(), want) {
		t.Fatalf("request = %q, want it to start %q", c.sent.String(), want)
	}
}

// TestStreamIDsRouteToTheRightFd is the anti-conflation test. The bridge uses
// 1=stdout, 2=stderr, 3=exit where frameproto v1 uses 0/1/2, so a client that
// reused frameproto's constants would put stdout on stderr and never see an
// exit frame at all. Frame 0 is included precisely because it is frameproto's
// stdout: here it must be IGNORED.
func TestStreamIDsRouteToTheRightFd(t *testing.T) {
	var wire bytes.Buffer
	wire.Write(frame(0, []byte("frameproto-stdout")))
	wire.Write(frame(frameStdout, []byte("to-stdout")))
	wire.Write(frame(frameStderr, []byte("to-stderr")))
	wire.Write(exitFrame(0))

	c := &conversation{reply: bytes.NewReader(wire.Bytes())}
	var out, errOut bytes.Buffer
	if rc := converse(c, nil, &out, &errOut); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if out.String() != "to-stdout" {
		t.Errorf("stdout = %q, want %q", out.String(), "to-stdout")
	}
	if errOut.String() != "to-stderr" {
		t.Errorf("stderr = %q, want %q", errOut.String(), "to-stderr")
	}
}

// TestExitCodes: the payload is a SIGNED int32, so a signal death (-15) must
// come back negative rather than as 4294967281.
func TestExitCodes(t *testing.T) {
	for _, rc := range []int{0, 1, 2, 127, -15} {
		var wire bytes.Buffer
		wire.Write(exitFrame(rc))
		c := &conversation{reply: bytes.NewReader(wire.Bytes())}
		var out, errOut bytes.Buffer
		if got := converse(c, nil, &out, &errOut); got != rc {
			t.Errorf("converse exit = %d, want %d", got, rc)
		}
	}
}

// TestStreamEndingWithoutAnExitFrameIsAFailure. Returning 0 there would make a
// killed bridge look like an empty journal — the single most misleading outcome
// this client can produce.
func TestStreamEndingWithoutAnExitFrameIsAFailure(t *testing.T) {
	cases := map[string][]byte{
		"immediate EOF":     nil,
		"truncated header":  {1, 0, 0},
		"truncated payload": append(frame(frameStdout, []byte("partial"))[:8], []byte{}...),
		"output then EOF":   frame(frameStdout, []byte("some logs")),
		"short exit payload": append(
			[]byte{frameExit, 0, 0, 0, 2}, []byte{0, 1}...),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			c := &conversation{reply: bytes.NewReader(wire)}
			var out, errOut bytes.Buffer
			if rc := converse(c, nil, &out, &errOut); rc != 1 {
				t.Fatalf("rc = %d, want 1", rc)
			}
		})
	}
}

// TestHelpIsOursOnlyAsTheFirstArg: `-h` leading prints our doc; anything else
// is a journalctl invocation and is forwarded, which is what keeps
// `journalctl -u foo --help` reachable at all.
func TestHelpIsOursOnlyAsTheFirstArg(t *testing.T) {
	t.Setenv(socketEnv, filepath.Join(shortSocketDir(t), "absent.sock"))

	for _, arg := range []string{"-h", "--help"} {
		var out, errOut bytes.Buffer
		if rc := run([]string{arg}, &out, &errOut); rc != 0 {
			t.Fatalf("run(%s) = %d, want 0 (stderr %q)", arg, rc, errOut.String())
		}
		for _, want := range []string{"yolo-journalctl", "journal bridge", "Socket: "} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("run(%s) help is missing %q:\n%s", arg, want, out.String())
			}
		}
	}

	// Not first: forwarded, so the missing-bridge path is what reports.
	var out, errOut bytes.Buffer
	if rc := run([]string{"-u", "nginx", "--help"}, &out, &errOut); rc != 1 {
		t.Fatalf("rc = %d, want 1 (the bridge is absent, so this must be forwarded)", rc)
	}
	if out.Len() != 0 {
		t.Errorf("a forwarded --help printed our own help: %q", out.String())
	}
}

// TestHelpPassthroughEnvForwardsIt: with the override set, even a leading -h
// goes to the host journalctl.
func TestHelpPassthroughEnvForwardsIt(t *testing.T) {
	t.Setenv(socketEnv, filepath.Join(shortSocketDir(t), "absent.sock"))
	t.Setenv(passthroughHelpEnv, "1")

	var out, errOut bytes.Buffer
	if rc := run([]string{"--help"}, &out, &errOut); rc != 1 {
		t.Fatalf("rc = %d, want 1 (forwarded to an absent bridge)", rc)
	}
	if out.Len() != 0 {
		t.Errorf("passthrough still printed our own help: %q", out.String())
	}
}

// TestMissingBridgeExplainsHowToEnableIt. The bridge is opt-in, so "not
// available" is the EXPECTED state for most jails and the message has to carry
// the config key that turns it on.
func TestMissingBridgeExplainsHowToEnableIt(t *testing.T) {
	socket := filepath.Join(shortSocketDir(t), "absent.sock")
	t.Setenv(socketEnv, socket)

	var out, errOut bytes.Buffer
	if rc := run([]string{"-n", "5"}, &out, &errOut); rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	for _, want := range []string{
		"host journal bridge is not available",
		socket,
		`journal: "user"`,
		"yolo-jail.jsonc",
		"config.jsonc",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr is missing %q:\n%s", want, errOut.String())
		}
	}
}

// TestEndToEndOverAUnixSocket drives the whole client against a real AF_UNIX
// listener speaking the bridge's protocol — the transport the parity step is
// pinned to. Unit-testing converse alone would not catch a wrong socket path,
// a wrong env var, or a dial that never happens.
func TestEndToEndOverAUnixSocket(t *testing.T) {
	socket := filepath.Join(shortSocketDir(t), "journal.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("bind %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan string, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
		_, _ = conn.Write(frame(frameStdout, []byte("Jan 01 boot\n")))
		_, _ = conn.Write(frame(frameStderr, []byte("-- warning --\n")))
		_, _ = conn.Write(exitFrame(3))
	}()

	t.Setenv(socketEnv, socket)
	var out, errOut bytes.Buffer
	if rc := run([]string{"-u", "nginx"}, &out, &errOut); rc != 3 {
		t.Fatalf("rc = %d, want 3 (the daemon's own code)", rc)
	}
	if out.String() != "Jan 01 boot\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.String() != "-- warning --\n" {
		t.Errorf("stderr = %q", errOut.String())
	}
	if req := <-got; !strings.Contains(req, `"args":["-u","nginx"]`) {
		t.Errorf("daemon received %q", req)
	}
}

// TestDefaultSocketIsTheContractPath: with no env var the client falls back to
// the path the run pipeline binds. Producer and consumer are spelled once, in
// internal/paths, because a drift here is silent.
func TestDefaultSocketIsTheContractPath(t *testing.T) {
	if want := "/run/yolo-services/journal.sock"; defaultSocket != want {
		t.Fatalf("defaultSocket = %q, want %q", defaultSocket, want)
	}
	t.Setenv(socketEnv, "")
	var out, errOut bytes.Buffer
	run([]string{"-h"}, &out, &errOut)
	if !strings.Contains(out.String(), "Socket: "+defaultSocket) {
		t.Fatalf("help did not report the default socket:\n%s", out.String())
	}
}

// TestFrameIDsMatchTheDaemon keeps the deliberately duplicated stream IDs
// honest. This client spells them locally so the baked binary stays a pure
// protocol client, and a duplicated constant that is never compared is a
// constant that drifts — silently, since a wrong ID reads as "unknown frame,
// ignore".
func TestFrameIDsMatchTheDaemon(t *testing.T) {
	if frameStdout != journald.FrameStdout || frameStderr != journald.FrameStderr || frameExit != journald.FrameExit {
		t.Fatalf("client frame IDs %d/%d/%d have drifted from the daemon's %d/%d/%d",
			frameStdout, frameStderr, frameExit,
			journald.FrameStdout, journald.FrameStderr, journald.FrameExit)
	}
}

// TestTheDaemonParsesThisClientsRequest closes the loop that a fake daemon
// cannot: the exact bytes this client writes are fed to internal/journald's OWN
// parser, which must accept them and apply the "user"-mode --user prepend. A
// hand-rolled fixture would only prove the test agrees with the test.
func TestTheDaemonParsesThisClientsRequest(t *testing.T) {
	c := &conversation{reply: bytes.NewReader(exitFrame(0))}
	var out, errOut bytes.Buffer
	converse(c, []string{"-u", "nginx", "-n", "50"}, &out, &errOut)

	header := strings.TrimSuffix(c.sent.String(), "\n")
	v := journald.ParseRequest([]byte(header), "user")
	if v.ErrText != "" {
		t.Fatalf("the daemon rejected this client's request: %s (%q)", v.ErrText, header)
	}
	if want := "--user -u nginx -n 50"; strings.Join(v.Args, " ") != want {
		t.Fatalf("daemon parsed args %q, want %q", v.Args, want)
	}
}
