package approvalqueue

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestIsTerminalOnRealDescriptors(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(w) || isTerminal(r) {
		t.Fatal("a pipe is not a terminal")
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if st, _ := null.Stat(); st.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("test setup: /dev/null should be a character device")
	}
	if isTerminal(null) {
		t.Fatal("/dev/null is a character device but not a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("a regular file is not a terminal")
	}

	// A real pseudo-terminal slave is one.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no /dev/ptmx: " + err.Error())
	}
	defer master.Close()
	const tiocsptlck, tiocgptn = 0x40045431, 0x80045430
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skip("cannot unlock the pty: " + e.Error())
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skip("cannot name the pty: " + e.Error())
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("cannot open the pty slave: " + err.Error())
	}
	defer slave.Close()
	if !isTerminal(slave) {
		t.Fatal("a pty slave is a terminal")
	}
}
