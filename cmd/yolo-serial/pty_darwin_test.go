//go:build darwin

package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// THE DARWIN openPty GIVES A WORKING PAIR: a slave named /dev/ttysNNN that opens, and bytes
// written on it arrive at the master. Runs on every push's check-macos job (`go test -short
// ./...` on macos-latest), which is the only place pty_darwin.go's three ioctls execute outside
// a macos-user launch (`yolo-serial pty` inside the guest).
func TestTheDarwinPtyPairCarriesBytesFromSlaveToMaster(t *testing.T) {
	master, name, err := openPty()
	if err != nil {
		t.Fatalf("openPty: %v", err)
	}
	defer master.Close()
	if !strings.HasPrefix(name, "/dev/ttys") {
		t.Errorf("the slave is named %q, not /dev/ttysNNN", name)
	}
	slave, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("the slave %s does not open (grant or unlock did not take): %v", name, err)
	}
	defer slave.Close()
	if _, err := slave.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write on the slave: %v", err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := master.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, "ping") {
			t.Errorf("the master read %q, want the slave's ping", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing written on the slave reached the master within 5s")
	}
}
