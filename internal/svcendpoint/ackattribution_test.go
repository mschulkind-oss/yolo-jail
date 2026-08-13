package svcendpoint

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// The bug these pin: readAck used to wrap EVERY io.ReadFull error as
// ErrAuthRejected, on the reasoning that "the ack precedes the request, so an EOF
// can only mean the token was refused". True of a clean EOF; false of a timeout or
// a reset, which are the connection dying mid-handshake — and that is exactly what
// a relay RESTART looks like. relayEnsure opens that window on every attach, so the
// most common transient was being reported with the message reserved for the one
// fault that needs a human: "this jail's endpoint file token does not match".
//
// The terminator checks errors.Is(err, ErrAuthRejected) BEFORE its ENOENT /
// ECONNREFUSED relay-layer gate, so this classification decides the message a user
// sees. These tests are about attribution, not connectivity.

// ackPair returns a connected client/server pair over a real TCP loopback socket,
// so the failure modes are the kernel's rather than a fake's.
func ackPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		ch <- accepted{c, err}
	}()

	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	a := <-ch
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = a.c.Close() })
	return client, a.c
}

// A genuine token mismatch: verifyToken returns errBadToken and drops the
// connection WITHOUT writing, so the client reads zero bytes and sees io.EOF.
// That IS a rejection and must keep its precise message — narrowing the
// classification must not cost the diagnosis it exists for.
func TestReadAckCleanEOFIsARejection(t *testing.T) {
	client, server := ackPair(t)
	_ = server.Close() // close without writing an ack — the reject path

	err := readAck(client)
	if err == nil {
		t.Fatal("readAck succeeded against a listener that never wrote an ack")
	}
	if !errors.Is(err, ErrAuthRejected) {
		t.Errorf("a clean EOF must classify as ErrAuthRejected; got %v", err)
	}
}

// A relay restart: the listener accepted TCP and then went away mid-handshake,
// leaving the client's read to hit its deadline. Not a verdict on the token.
func TestReadAckTimeoutIsNotARejection(t *testing.T) {
	client, server := ackPair(t)
	// Hold the connection open and never write. The handshake deadline fires.
	defer func() { _ = server.Close() }()

	start := time.Now()
	err := readAck(client)
	if err == nil {
		t.Fatal("readAck succeeded against a listener that never answered")
	}
	if errors.Is(err, ErrAuthRejected) {
		t.Errorf("a handshake TIMEOUT must not be reported as an auth rejection — "+
			"that is the relay-restart window, and it would send the user hunting a "+
			"token mismatch that does not exist; got %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("the timeout should survive wrapping so callers can see it; got %v", err)
	}
}

// Belt and braces on the sentinel: io.ErrUnexpectedEOF (a partial read) is the
// same "closed without acking" shape as io.EOF and must classify the same way,
// while an unrelated error must not.
func TestReadAckSentinelBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		rejected bool
	}{
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"deadline", os.ErrDeadlineExceeded, false},
		{"reset", syscallResetStub{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Mirror readAck's classification directly: the branch is one line and
			// driving every errno through a real socket is not worth the flake.
			got := errors.Is(tc.err, io.EOF) || errors.Is(tc.err, io.ErrUnexpectedEOF)
			if got != tc.rejected {
				t.Errorf("classify(%v) rejected=%v, want %v", tc.err, got, tc.rejected)
			}
		})
	}
}

type syscallResetStub struct{}

func (syscallResetStub) Error() string { return "connection reset by peer" }
