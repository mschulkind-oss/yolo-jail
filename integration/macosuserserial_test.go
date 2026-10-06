//go:build darwin

package integration

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox is the serial loophole on macos-user,
// end to end: the launch stages a darwin `yolo-serial` into the sandbox because the session env
// carries the serial loophole's endpoint (macosuser.GuestClients), and the client, run by the
// agent inside the Seatbelt profile, lists, writes to and bridges a host serial device through
// the host daemon.
//
// THE DEVICE IS A HOST PTY PAIR, opened by this test on the Mac: the slave stands for a USB
// serial port (allowed_devices names it alone), and the master is where this test reads what the
// sandbox wrote. A real USB device is the human's check (docs/reference/macos-user-nix-and-features.md).
//
// WHAT ONLY THIS TEST CAN SEE, every item unexecuted before: that a darwin yolo-serial
// cross-compiled on Linux execs from the guest prefix under the profile; that the host serial
// daemon's darwin arm opens a pty slave; that the sandbox account reads the endpoint through the
// ACL grant; and that pty_darwin.go's TIOCPTYGRANT/TIOCPTYUNLK/TIOCPTYGNAME allocate a virtual
// PTY inside the sandbox (`yolo-serial pty` prints "Virtual PTY:").
//
// Darwin-only by build constraint: the pair is opened with darwin's own pty ioctls, and
// requireMacosUser skips everywhere else anyway.
func TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox(t *testing.T) {
	requireMacosUser(t)
	master, slave := hostPtyPair(t)
	got := readMaster(t, master)

	packHome(t, fmt.Sprintf(`{"packs": ["serial"], "loopholes": {"serial": {"enabled": true, `+
		`"settings": {"allowed_devices": [%q]}}}}`, slave))
	ws := macosUserWorkspace(t, `{}`)
	out := fmt.Sprintf("/private/tmp/yolo-it-serial-pty-%d.out", os.Getpid())
	t.Cleanup(func() { _ = runQuiet(time.Minute, "sudo", "-n", "/bin/rm", "-f", out) })
	r := macosUserRunProbe(t, "serial", ws, strings.Join([]string{
		`echo "=== CLIENT ==="`,
		`command -v yolo-serial || echo MISSING`,
		`echo "=== LIST ==="`,
		`yolo-serial list --json; echo "LIST_RC=$?"`,
		`echo "=== WRITE ==="`,
		`yolo-serial write ` + slave + ` ping; echo "WRITE_RC=$?"`,
		`echo "=== PTY ==="`,
		`yolo-serial pty ` + slave + ` --link "$HOME/vpty" >` + out + ` 2>&1 & p=$!`,
		`sleep 5; kill "$p" 2>/dev/null; wait "$p" 2>/dev/null; cat ` + out,
		`echo "=== END ==="`,
	}, "\n"))
	diag := func() string {
		return fmt.Sprintf("\n--- launch stdout:\n%s\n--- launch stderr:\n%s", r.stdout, r.stderr)
	}

	if client := strings.TrimSpace(section(r.stdout, "=== CLIENT ===", "=== LIST ===")); client !=
		macosuser.GuestBinaryPath("yolo-serial", "") {
		t.Errorf("yolo-serial resolves to %q in the sandbox, want the staged guest binary %s%s",
			client, macosuser.GuestBinaryPath("yolo-serial", ""), diag())
	}
	if list := section(r.stdout, "=== LIST ===", "=== WRITE ==="); !strings.Contains(list, "LIST_RC=0") ||
		!strings.Contains(list, slave) {
		t.Errorf("`yolo-serial list --json` does not list the allowed device %s:\n%s%s", slave, list, diag())
	}
	if w := section(r.stdout, "=== WRITE ===", "=== PTY ==="); !strings.Contains(w, "WRITE_RC=0") {
		t.Errorf("`yolo-serial write` failed:\n%s%s", w, diag())
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(got(), "ping") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(got(), "ping") {
		t.Errorf("the host pty's master never read the sandbox's ping (read %q)%s", got(), diag())
	}
	if pty := section(r.stdout, "=== PTY ===", "=== END ==="); !strings.Contains(pty, "Virtual PTY:") {
		t.Errorf("`yolo-serial pty` did not allocate a virtual PTY in the sandbox:\n%s%s", pty, diag())
	}
}

// hostPtyPair opens a pty pair on the Mac, as pty_darwin.go does in the sandbox, and returns the
// master and the slave's path. Both are closed at cleanup.
func hostPtyPair(t *testing.T) (*os.File, string) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatalf("grantpt: %v", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatalf("unlockpt: %v", err)
	}
	var name [128]byte
	//lint:ignore SA1019 TIOCPTYGNAME fills a 128-byte out buffer, and x/sys/unix exports no darwin wrapper for an out-buffer ioctl
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		t.Fatalf("ptsname: %v", e)
	}
	n := bytes.IndexByte(name[:], 0)
	if n < 0 {
		n = len(name)
	}
	master, err := serialMasterFile(fd, "/dev/ptmx")
	if err != nil {
		_ = unix.Close(fd)
		t.Fatalf("make the serial fixture master cancelable: %v", err)
	}
	slave := string(name[:n])
	// Hold the slave open so the line stays up between the daemon's opens.
	hold, err := os.OpenFile(slave, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the slave %s: %v", slave, err)
	}
	t.Cleanup(func() { _ = hold.Close(); _ = master.Close() })
	return master, slave
}

// readMaster reads the master in the background and returns a reader of everything seen so far.
func readMaster(t *testing.T, master *os.File) func() string {
	t.Helper()
	// Fail before starting a reader if this native descriptor cannot be canceled.
	if err := master.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("serial fixture master cannot cancel a blocked read: %v", err)
	}
	var mu sync.Mutex
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 256)
		for {
			n, err := master.Read(b)
			mu.Lock()
			buf.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		t.Log("serial fixture cleanup: close master and join background reader")
		_ = master.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("serial fixture background reader did not stop after master close")
		}
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}
