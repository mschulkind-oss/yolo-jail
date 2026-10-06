package integration

import (
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// serialMasterFile wraps a raw descriptor for the background serial-fixture reader.
// A pollable file lets Go cancel a blocked read on Close; a blocking raw descriptor
// does not, so cleanup can wait forever while the PTY's peer remains open.
func serialMasterFile(fd int, name string) (*os.File, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func TestSerialFixtureRawDescriptorReadCanBeCanceled(t *testing.T) {
	requireJail(t)
	var descriptors [2]int
	if err := unix.Pipe(descriptors[:]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(descriptors[1]) }) // Keep the peer open through the read.
	master, err := serialMasterFile(descriptors[0], "serial-fixture-reader")
	if err != nil {
		_ = unix.Close(descriptors[0])
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := master.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("raw serial-fixture descriptor cannot cancel a blocked read: %v", err)
	}
	var b [1]byte
	if _, err := master.Read(b[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("empty open peer read = %v, want deadline exceeded", err)
	}
	if err := master.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = master.Read(b[:]); close(done) }()
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing the serial-fixture file did not release its background reader")
	}
}
