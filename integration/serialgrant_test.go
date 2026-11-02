//go:build linux

package integration

// serialgrant_test.go is the container tier of the serial flip
// (docs/reference/loophole-grants.md F4): the shipped `serial` loophole grants access
// through its valued `devices` grant, not the old `enabled` switch and `allowed_devices` setting
// with its five default globs. Selecting the pack runs the daemon; what `yolo-serial` may list and
// open is the device globs the project requested and a gate approved. The unit tiers pin the
// daemon's decision against a grant file (internal/serialdaemon/grants_test.go) and the legacy
// projection (internal/config/loopholegrantlegacy_test.go); only a launch shows the config, the
// gate, the grant file, the daemon and the in-jail client joined. The macos-user tier is
// macosuserserial_test.go.
//
// Linux-only by build constraint: the stand-in device is a pty pair opened with Linux's own
// ioctls (TIOCSPTLCK, TIOCGPTN).

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A project that selected the pack and holds no grant gets the not_allowed refusal, exit 64:
// `devices` is valued, so it is never requestable by default, and the refusal names the user's
// `yolo allow` before the project's `yolo request`. No device is listed or opened, and the five
// globs `allowed_devices` defaulted to are gone.
func TestSerialWithNoGrantRefusesInTheJail(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = [\"serial\"]\n")
	dir := writeWorkspaceTOML(t, "")

	r := runYolo(t, dir, `yolo-serial list; echo "SER-RC=$?"`)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	t.Logf("the jail's output:\n%s", r.combined())
	if !strings.Contains(r.stdout, "SER-RC=64") {
		t.Fatalf("yolo-serial with no grant must exit 64 (a grant refusal):\n%s", r.combined())
	}
	// combined, not stderr alone: the in-jail stderr reaches this process through the
	// launcher, and only the refusal prints these lines.
	for _, want := range []string{
		"this project holds no grant to open host serial devices",
		"yolo allow serial on_request devices",
		"yolo request serial devices <device globs>",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must name %q:\n%s", want, r.combined())
		}
	}
}

// A user who allows `devices` on request and a project that requests one pty slave, launched
// with the gate accepted (jailRunArgs carries --accept-config-changes), hold the grant:
// `yolo-serial` lists that slave and writes to it, and the host end reads what the jail wrote. A
// device outside the grant is refused, exit 64, naming it as the value to request.
func TestSerialDevicesGrantOpensOnlyTheGrantedDevice(t *testing.T) {
	requireJail(t)
	master, slave := linuxPtyPair(t)
	got := readMaster(t, master)

	writeUserTOML(t, "packs = [\"serial\"]\n\n[allow.serial]\non_request = [\"devices\"]\n")
	dir := writeWorkspaceTOML(t, fmt.Sprintf("[requests.serial]\ndevices = [%q]\n", slave))

	r := runYolo(t, dir, strings.Join([]string{
		`echo "=== LIST ==="`,
		`yolo-serial list --json; echo "LIST_RC=$?"`,
		`echo "=== WRITE ==="`,
		`yolo-serial write ` + slave + ` ping; echo "WRITE_RC=$?"`,
		`echo "=== OUTSIDE ==="`,
		`yolo-serial write /dev/ttyUSB97 ping; echo "OUTSIDE_RC=$?"`,
		`echo "=== END ==="`,
	}, "\n"))
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if list := section(r.stdout, "=== LIST ===", "=== WRITE ==="); !strings.Contains(list, "LIST_RC=0") ||
		!strings.Contains(list, slave) {
		t.Errorf("`yolo-serial list --json` does not list the granted device %s:\n%s", slave, r.combined())
	}
	if w := section(r.stdout, "=== WRITE ===", "=== OUTSIDE ==="); !strings.Contains(w, "WRITE_RC=0") {
		t.Errorf("`yolo-serial write` to the granted device failed:\n%s", r.combined())
	}
	if o := section(r.stdout, "=== OUTSIDE ===", "=== END ==="); !strings.Contains(o, "OUTSIDE_RC=64") ||
		!strings.Contains(r.combined(), "yolo request serial devices /dev/ttyUSB97") {
		t.Errorf("a device outside the grant must be refused with exit 64, naming it:\n%s", r.combined())
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(got(), "ping") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(got(), "ping") {
		t.Errorf("the host pty's master never read the jail's ping (read %q)\n%s", got(), r.combined())
	}
}

// linuxPtyPair opens a pty pair on the host, the slave standing for a USB serial port, and
// returns the master and the slave's path. Both are closed at cleanup.
func linuxPtyPair(t *testing.T) (*os.File, string) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = unix.Close(fd)
		t.Fatalf("ptsname: %v", err)
	}
	master, err := serialMasterFile(fd, "/dev/ptmx")
	if err != nil {
		_ = unix.Close(fd)
		t.Fatalf("make the serial fixture master cancelable: %v", err)
	}
	slave := fmt.Sprintf("/dev/pts/%d", n)
	// Hold the slave open so the line stays up between the daemon's opens.
	hold, err := os.OpenFile(slave, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Fatalf("open the slave %s: %v", slave, err)
	}
	t.Cleanup(func() { _ = hold.Close(); _ = master.Close() })
	return master, slave
}
