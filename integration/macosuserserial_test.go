//go:build darwin

package integration

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox is the serial loophole on macos-user,
// end to end: the launch stages a darwin `yolo-serial` into the sandbox because the session env
// carries the serial loophole's endpoint (macosuser.GuestClients), and the client, run by the
// agent inside the Seatbelt profile, lists, writes to and bridges a host serial device through
// the host daemon.
//
// THE DEVICE IS A HOST PTY PAIR, opened by this test on the Mac: the slave stands for a USB
// serial port (the `devices` grant names it alone), and the master is where this test reads what the
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

	// The flip (docs/reference/loophole-grants.md F4): selecting the pack runs the
	// daemon, and what it may open is the valued `devices` grant. A valued kind is never
	// requestable by default, so the user's ceiling allows it on request; the project requests
	// the slave's path, and the gate accepts it (runMacosUser passes --accept-config-changes).
	writeUserTOML(t, "packs = [\"serial\"]\n\n[allow.serial]\non_request = [\"devices\"]\n")
	ws := macosUserWorkspaceTOML(t, fmt.Sprintf("[requests.serial]\ndevices = [%q]\n", slave))
	out := fmt.Sprintf("/private/tmp/yolo-it-serial-pty-%d.out", os.Getpid())
	// The sandbox account writes it, in sticky /private/tmp, so only that account removes it.
	t.Cleanup(func() { _ = runAsSandboxQuiet(time.Minute, "/bin/rm", "-f", "--", out) })
	r := macosUserRunProbe(t, "serial", ws, strings.Join([]string{
		`echo "=== CLIENT ==="`,
		`command -v yolo-serial || echo MISSING`,
		`echo "=== LIST ==="`,
		`yolo-serial list --json; echo "LIST_RC=$?"`,
		`echo "=== WRITE ==="`,
		`yolo-serial write ` + slave + ` ping; echo "WRITE_RC=$?"`,
		`echo "=== PTY ==="`,
		`yolo-serial pty ` + slave + ` --link "$HOME/vpty" >` + out + ` 2>&1 & p=$!`,
		// Bounded: a pty that survives SIGTERM is reported and SIGKILLed, rather than parking
		// the launch at `wait` until the job's timeout (30 minutes, the first time).
		`sleep 5; kill "$p" 2>/dev/null`,
		`for _ in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$p" 2>/dev/null || break; sleep 1; done`,
		`if kill -0 "$p" 2>/dev/null; then echo PTY_SURVIVED_SIGTERM; kill -9 "$p" 2>/dev/null; fi`,
		`wait "$p" 2>/dev/null; cat ` + out,
		`echo "=== END ==="`,
	}, "\n"))
	diag := func() string {
		return fmt.Sprintf("\n--- launch stdout:\n%s\n--- launch stderr:\n%s", r.stdout, r.stderr)
	}

	requireSessionGuestBinary(t, strings.TrimSpace(section(r.stdout, "=== CLIENT ===", "=== LIST ===")),
		"yolo-serial", diag)
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
	pty := section(r.stdout, "=== PTY ===", "=== END ===")
	if !strings.Contains(pty, "Virtual PTY:") {
		t.Errorf("`yolo-serial pty` did not allocate a virtual PTY in the sandbox:\n%s%s", pty, diag())
	}
	if strings.Contains(pty, "PTY_SURVIVED_SIGTERM") {
		t.Errorf("`yolo-serial pty` was still running 10s after SIGTERM and was SIGKILLed:\n%s%s", pty, diag())
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
