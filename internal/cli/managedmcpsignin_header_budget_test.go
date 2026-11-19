package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
)

type headerBudgetConn struct {
	input    *bytes.Reader
	output   bytes.Buffer
	deadline time.Time
}

func (c *headerBudgetConn) Read(p []byte) (int, error)         { return c.input.Read(p) }
func (c *headerBudgetConn) Write(p []byte) (int, error)        { return c.output.Write(p) }
func (*headerBudgetConn) Close() error                         { return nil }
func (*headerBudgetConn) LocalAddr() net.Addr                  { return nil }
func (*headerBudgetConn) RemoteAddr() net.Addr                 { return nil }
func (c *headerBudgetConn) SetDeadline(d time.Time) error      { c.deadline = d; return nil }
func (c *headerBudgetConn) SetReadDeadline(d time.Time) error  { return c.SetDeadline(d) }
func (c *headerBudgetConn) SetWriteDeadline(d time.Time) error { return c.SetDeadline(d) }

func TestMCPAuthHeaderCarriesOneDeadlineThroughDialAndFraming(t *testing.T) {
	t.Setenv("YOLO_VERSION", "fixture")
	if mcpAuthCommandTimeout != 8*time.Second {
		t.Fatal("header command budget is not eight seconds")
	}
	oldContext, oldDial := newMCPAuthContext, mcpAuthDial
	t.Cleanup(func() { newMCPAuthContext = oldContext; mcpAuthDial = oldDial })
	var deadline time.Time
	newMCPAuthContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), mcpAuthCommandTimeout)
		deadline, _ = ctx.Deadline()
		return ctx, cancel
	}
	var frames bytes.Buffer
	frameproto.WriteFrame(&frames, frameproto.StreamStdout, []byte("Bearer fixture-access\n"))
	frameproto.WriteExit(&frames, 0)
	conn := &headerBudgetConn{input: bytes.NewReader(frames.Bytes())}
	mcpAuthDial = func(_ string, wait time.Duration) (net.Conn, error) {
		if wait <= 0 || wait > 8*time.Second {
			t.Fatalf("dial wait is outside total budget: %v", wait)
		}
		return conn, nil
	}
	var out, errs bytes.Buffer
	rc := mcpAuthMain([]string{"header", "slack"}, func(string) string { return "fixture.endpoint" }, &out, &errs)
	if rc != 0 || out.String() != "Bearer fixture-access\n" || errs.Len() != 0 {
		t.Fatalf("rc=%d out=%q errs=%q", rc, out.String(), errs.String())
	}
	if !conn.deadline.Equal(deadline) {
		t.Fatal("framing reset the command deadline after dial")
	}
}

func TestMCPAuthHeaderDiscardsPartialStdoutOnBrokenFrame(t *testing.T) {
	var frames bytes.Buffer
	frameproto.WriteFrame(&frames, frameproto.StreamStdout, []byte("Bearer incomplete-secret"))
	var out, errs bytes.Buffer
	if rc := mcpAuthExchange(bytes.NewBuffer(frames.Bytes()), "slack", &out, &errs); rc != 1 || out.Len() != 0 {
		t.Fatal("broken reply exposed partial header")
	}
}

var _ io.ReadWriter = (*headerBudgetConn)(nil)
